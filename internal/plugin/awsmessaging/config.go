// Package awsmessaging implements the KubeZap AWS messaging plugin: an
// Integration{type: plugin} workload that acts as an SQS subscriber (creating
// one FlowRun per message) and, in a later story, an SNS publisher. See
// docs/design/aws-sqs-sns-messaging-plugin.md and docs/api/plugin-contract.md.
//
// The AWS SDK for Go v2 is imported only by this package and
// cmd/aws-messaging-plugin; it must never leak into the operator binaries.
package awsmessaging

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Environment variable names read by the plugin. KUBEZAP_* are injected by the
// operator (docs/api/plugin-contract.md#environment); AWS_* arrive via
// spec.plugin.secretRefs+envVarMappings (credentials) and spec.plugin.env
// (region, endpoint override).
const (
	EnvNamespace       = "KUBEZAP_NAMESPACE"
	EnvIntegrationName = "KUBEZAP_INTEGRATION_NAME"
	EnvPublisherPort   = "KUBEZAP_PUBLISHER_PORT"
	EnvLogLevel        = "KUBEZAP_LOG_LEVEL"
	EnvMTLSEnabled     = "KUBEZAP_MTLS_ENABLED"
	EnvMTLSHealthPort  = "KUBEZAP_MTLS_HEALTH_PORT"

	EnvAWSAccessKeyID     = "AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey = "AWS_SECRET_ACCESS_KEY"
	EnvAWSSessionToken    = "AWS_SESSION_TOKEN"
	EnvAWSRegion          = "AWS_REGION"
	EnvAWSEndpointURL     = "AWS_ENDPOINT_URL"

	// DefaultPublisherPort is the contract default for KUBEZAP_PUBLISHER_PORT.
	DefaultPublisherPort = 8090

	// TriggerConfigQueueURL is the documented key in
	// trigger.spec.plugin.config carrying the SQS queue URL. The operator never
	// interprets it.
	TriggerConfigQueueURL = "queueUrl"
)

// Config holds the plugin's process-level configuration.
type Config struct {
	Namespace       string
	IntegrationName string
	LogLevel        string

	// PublisherPort is KUBEZAP_PUBLISHER_PORT (used by the publisher role in
	// STORY-073; /healthz is served on it too unless mTLS is enabled).
	PublisherPort int
	// HealthPort is the port /healthz is served on: PublisherPort normally,
	// KUBEZAP_MTLS_HEALTH_PORT when mTLS is enabled (plugin-contract.md).
	HealthPort  int
	MTLSEnabled bool

	Region      string
	EndpointURL string

	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// LoadConfig builds a Config from the environment via getenv (os.Getenv in
// production) and validates it. All problems are reported together.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		Namespace:       strings.TrimSpace(getenv(EnvNamespace)),
		IntegrationName: strings.TrimSpace(getenv(EnvIntegrationName)),
		LogLevel:        strings.TrimSpace(getenv(EnvLogLevel)),
		Region:          strings.TrimSpace(getenv(EnvAWSRegion)),
		EndpointURL:     strings.TrimSpace(getenv(EnvAWSEndpointURL)),
		AccessKeyID:     getenv(EnvAWSAccessKeyID),
		SecretAccessKey: getenv(EnvAWSSecretAccessKey),
		SessionToken:    getenv(EnvAWSSessionToken),
		PublisherPort:   DefaultPublisherPort,
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}

	var errs []error
	if c.Namespace == "" {
		errs = append(errs, fmt.Errorf("%s is required", EnvNamespace))
	}
	if c.IntegrationName == "" {
		errs = append(errs, fmt.Errorf("%s is required", EnvIntegrationName))
	}
	if c.Region == "" {
		errs = append(errs, fmt.Errorf("%s is required (set via spec.plugin.env)", EnvAWSRegion))
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		errs = append(errs, fmt.Errorf(
			"%s and %s are required (inject via spec.plugin.secretRefs + envVarMappings)",
			EnvAWSAccessKeyID, EnvAWSSecretAccessKey))
	}
	if c.EndpointURL != "" {
		if u, err := url.Parse(c.EndpointURL); err != nil || u.Host == "" ||
			(u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Errorf("%s %q is not a valid http(s) URL", EnvAWSEndpointURL, c.EndpointURL))
		}
	}

	if v := strings.TrimSpace(getenv(EnvPublisherPort)); v != "" {
		p, err := parsePort(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", EnvPublisherPort, err))
		} else {
			c.PublisherPort = p
		}
	}
	c.HealthPort = c.PublisherPort
	if strings.EqualFold(strings.TrimSpace(getenv(EnvMTLSEnabled)), "true") {
		c.MTLSEnabled = true
		p, err := parsePort(strings.TrimSpace(getenv(EnvMTLSHealthPort)))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", EnvMTLSHealthPort, err))
		} else {
			c.HealthPort = p
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

func parsePort(v string) (int, error) {
	p, err := strconv.Atoi(v)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", v)
	}
	return p, nil
}

// ParseQueueURL does a cheap sanity check of an SQS queue URL taken from
// trigger.spec.plugin.config["queueUrl"]. The operator never validates this
// value (design record Invariants), so a malformed one is reported here and
// surfaces in plugin logs and /healthz.
func ParseQueueURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("plugin.config[%q] is missing or empty", TriggerConfigQueueURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("plugin.config[%q] is not a valid URL: %w", TriggerConfigQueueURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("plugin.config[%q] %q must be an http(s) URL", TriggerConfigQueueURL, raw)
	}
	if strings.Trim(u.Path, "/") == "" {
		return fmt.Errorf("plugin.config[%q] %q has no queue path", TriggerConfigQueueURL, raw)
	}
	return nil
}
