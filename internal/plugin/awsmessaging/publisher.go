package awsmessaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"
)

const (
	// MaxRequestBodyBytes caps the /publish request body. SNS itself accepts
	// at most 256 KiB per message; 1 MiB leaves headroom for JSON escaping and
	// headers while still bounding memory per request.
	MaxRequestBodyBytes = 1 << 20

	// publishTimeout bounds one SNS Publish call. It is below the controller's
	// 30s per-attempt timeout so the plugin answers (with a 5xx) before the
	// controller gives up on the connection.
	publishTimeout = 25 * time.Second

	// maxMessageAttributes is the SNS limit on MessageAttributes per message.
	maxMessageAttributes = 10
	// maxFIFOIDLen is the SNS limit for MessageGroupId / MessageDeduplicationId.
	maxFIFOIDLen = 128
)

var (
	// attrNameRE is the SNS MessageAttribute name rule (alphanumerics,
	// underscore, hyphen, period; max 256 chars).
	attrNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,256}$`)
	// fifoIDRE is the SNS rule for MessageGroupId / MessageDeduplicationId:
	// alphanumerics and !"#$%&'()*+,-./:;<=>?@[\]^_`{|}~.
	fifoIDRE = regexp.MustCompile("^[A-Za-z0-9!\"#$%&'()*+,\\-./:;<=>?@\\[\\\\\\]^_`{|}~]+$")
	// traceparentRE is the W3C trace-context traceparent format (version 00).
	traceparentRE = regexp.MustCompile(`^00-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

// PublishRequest is the JSON body of POST /publish (docs/api/plugin-contract.md).
// Unknown fields are tolerated so the contract can grow.
type PublishRequest struct {
	Integration string            `json:"integration"`
	Namespace   string            `json:"namespace"`
	Destination string            `json:"destination"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	// IdempotencyKey is optional; forwarded as MessageDeduplicationId on FIFO
	// topics only.
	IdempotencyKey string `json:"idempotencyKey"`
}

type publishSuccess struct {
	MessageID string `json:"messageId"`
}

type publishFailure struct {
	Error string `json:"error"`
}

// Publisher implements the plugin contract's publisher role against SNS.
type Publisher struct {
	sns             SNSAPI
	log             logr.Logger
	namespace       string
	integrationName string
}

// NewPublisher returns a Publisher. namespace/integrationName come from the
// operator-injected environment (not the request) and seed the default FIFO
// MessageGroupId.
func NewPublisher(api SNSAPI, namespace, integrationName string, log logr.Logger) *Publisher {
	return &Publisher{sns: api, namespace: namespace, integrationName: integrationName, log: log}
}

// Ready reports whether the SNS client is usable (used by /healthz). The
// client is constructed at startup from validated config; this guards against
// a missing client.
func (p *Publisher) Ready() error {
	if p == nil || p.sns == nil {
		return errors.New("SNS client not initialised")
	}
	return nil
}

// Mux returns a mux serving only POST /publish.
func (p *Publisher) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/publish", p)
	return mux
}

// ServeHTTP implements POST /publish.
func (p *Publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, publishFailure{Error: "method not allowed; use POST"})
		return
	}

	tp := r.Header.Get("traceparent")
	if tp != "" && !traceparentRE.MatchString(tp) {
		tp = "" // malformed; ignore rather than fail the publish
	}
	log := p.log
	if tp != "" {
		log = log.WithValues("traceparent", tp, "traceId", traceparentRE.FindStringSubmatch(tp)[1])
	}

	var req PublishRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes))
	if err := dec.Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusRequestEntityTooLarge, publishFailure{
				Error: fmt.Sprintf("request body exceeds %d bytes", MaxRequestBodyBytes)})
			return
		}
		writeJSON(w, http.StatusBadRequest, publishFailure{Error: "invalid JSON request body: " + err.Error()})
		return
	}

	in, status, verr := p.buildInput(&req, tp)
	if verr != nil {
		log.Info("publish rejected", "status", status, "reason", verr.Error(), "destination", req.Destination)
		writeJSON(w, status, publishFailure{Error: verr.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), publishTimeout)
	defer cancel()
	out, err := p.sns.Publish(ctx, in)
	if err != nil {
		status, msg := classifyError(err)
		// Message bodies are never logged; the AWS error text does not echo them.
		log.Error(err, "SNS publish failed", "status", status, "destination", req.Destination,
			"durationMs", time.Since(start).Milliseconds())
		writeJSON(w, status, publishFailure{Error: msg})
		return
	}
	log.Info("published to SNS", "destination", req.Destination, "messageId", aws.ToString(out.MessageId),
		"durationMs", time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, publishSuccess{MessageID: aws.ToString(out.MessageId)})
}

// ValidateTopicARN requires a full SNS topic ARN. Bare topic names are
// rejected; the plugin never resolves names (design record: no
// ListTopics/GetTopicAttributes).
func ValidateTopicARN(dest string) error {
	if strings.TrimSpace(dest) == "" {
		return errors.New("destination is required and must be the full SNS topic ARN " +
			"(arn:aws:sns:<region>:<account-id>:<topic-name>)")
	}
	a, err := arn.Parse(dest)
	if err != nil {
		return fmt.Errorf("destination %q is not a full SNS topic ARN "+
			"(expected arn:aws:sns:<region>:<account-id>:<topic-name>); topic names are not resolved", dest)
	}
	if a.Service != "sns" || a.Region == "" || a.AccountID == "" ||
		a.Resource == "" || strings.Contains(a.Resource, ":") {
		return fmt.Errorf("destination %q is not an SNS topic ARN "+
			"(expected arn:aws:sns:<region>:<account-id>:<topic-name>)", dest)
	}
	return nil
}

// buildInput validates the request and maps it to an SNS PublishInput. The
// returned status is the HTTP code to use when err != nil.
func (p *Publisher) buildInput(req *PublishRequest, traceparent string) (*sns.PublishInput, int, error) {
	if err := ValidateTopicARN(req.Destination); err != nil {
		return nil, http.StatusBadRequest, err
	}
	if req.Body == "" {
		return nil, http.StatusBadRequest, errors.New("body is required (SNS rejects empty messages)")
	}

	attrs := make(map[string]snstypes.MessageAttributeValue, len(req.Headers)+1)
	for k, v := range req.Headers {
		if !attrNameRE.MatchString(k) || strings.HasPrefix(k, ".") || strings.HasSuffix(k, ".") ||
			strings.Contains(k, "..") ||
			strings.HasPrefix(strings.ToLower(k), "aws.") || strings.HasPrefix(strings.ToLower(k), "amazon.") {
			return nil, http.StatusBadRequest, fmt.Errorf("header %q is not a valid SNS message attribute name", k)
		}
		if v == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("header %q has an empty value (SNS requires non-empty String attributes)", k)
		}
		attrs[k] = snstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	if traceparent != "" {
		// The request's traceparent header wins over a same-named body header.
		attrs[traceparentKey] = snstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(traceparent)}
	}
	if len(attrs) > maxMessageAttributes {
		return nil, http.StatusBadRequest, fmt.Errorf("%d message attributes (headers%s) exceed the SNS limit of %d",
			len(attrs), map[bool]string{true: " + traceparent", false: ""}[traceparent != ""], maxMessageAttributes)
	}

	in := &sns.PublishInput{
		TopicArn: aws.String(req.Destination),
		Message:  aws.String(req.Body),
	}
	if len(attrs) > 0 {
		in.MessageAttributes = attrs
	}

	if strings.HasSuffix(req.Destination, ".fifo") {
		// SNS FIFO topics require a MessageGroupId. Default is deterministic
		// per Integration so ordering is preserved within one Integration.
		in.MessageGroupId = aws.String(p.defaultGroupID())
		if req.IdempotencyKey != "" {
			if len(req.IdempotencyKey) > maxFIFOIDLen || !fifoIDRE.MatchString(req.IdempotencyKey) {
				return nil, http.StatusBadRequest, fmt.Errorf(
					"idempotencyKey must be 1-%d chars of alphanumerics and punctuation to be used as an SNS FIFO MessageDeduplicationId", maxFIFOIDLen)
			}
			in.MessageDeduplicationId = aws.String(req.IdempotencyKey)
		}
		// No key: leave MessageDeduplicationId unset; the topic must have
		// ContentBasedDeduplication enabled or SNS answers InvalidParameter (4xx).
	}
	// Standard topics: idempotencyKey is silently ignored (no such parameter).
	return in, 0, nil
}

// defaultGroupID is the FIFO MessageGroupId: "kubezap-<namespace>-<integration>".
func (p *Publisher) defaultGroupID() string {
	id := fmt.Sprintf("kubezap-%s-%s", p.namespace, p.integrationName)
	if len(id) > maxFIFOIDLen {
		id = id[:maxFIFOIDLen]
	}
	return id
}

// classifyError maps an SNS/SDK error to an HTTP status and message following
// the contract: 4xx = permanent (retrying cannot help), 5xx = transient.
func classifyError(err error) (int, string) {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		code, msg := ae.ErrorCode(), fmt.Sprintf("SNS error %s: %s", ae.ErrorCode(), ae.ErrorMessage())
		switch code {
		case "AuthorizationError", "AccessDenied", "AccessDeniedException", "InvalidClientTokenId",
			"UnrecognizedClientException", "SignatureDoesNotMatch", "ExpiredToken", "ExpiredTokenException",
			"InvalidSecurity", "KMSAccessDenied", "OptInRequired", "KMSOptInRequired":
			return http.StatusForbidden, msg
		case "NotFound", "NotFoundException", "NoSuchEntity", "KMSNotFound":
			return http.StatusNotFound, msg
		case "InvalidParameter", "InvalidParameterValue", "ValidationError", "ValidationException",
			"EndpointDisabled", "PlatformApplicationDisabled", "KMSDisabled", "KMSInvalidState",
			"InvalidState", "MissingParameter":
			return http.StatusBadRequest, msg
		case "Throttled", "ThrottledException", "Throttling", "ThrottlingException", "RequestLimitExceeded",
			"KMSThrottling", "SlowDown", "TooManyRequestsException":
			return http.StatusServiceUnavailable, msg
		case "InternalError", "InternalFailure", "ServiceUnavailable", "ServiceUnavailableException",
			"RequestTimeout", "RequestTimeoutException":
			return http.StatusBadGateway, msg
		}
		// Unknown code: trust the fault classification.
		if ae.ErrorFault() == smithy.FaultClient {
			return http.StatusBadRequest, msg
		}
		return http.StatusBadGateway, msg
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "timed out publishing to SNS"
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusServiceUnavailable, "publish cancelled"
	}
	// Network / DNS / TLS / SDK-retry-exhausted errors: transient.
	return http.StatusBadGateway, "failed to reach SNS: " + err.Error()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
