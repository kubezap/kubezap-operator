package awsmessaging

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubezap/kubezap-operator/internal/certutil"
)

const (
	stdARN  = "arn:aws:sns:us-east-1:123456789012:orders"
	fifoARN = "arn:aws:sns:us-east-1:123456789012:orders.fifo"
	goodTP  = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
)

type fakeSNS struct {
	in  *sns.PublishInput
	n   int
	err error
}

func (f *fakeSNS) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	f.n++
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	return &sns.PublishOutput{MessageId: aws.String("mid-1")}, nil
}

func newPub(f *fakeSNS) *Publisher {
	return NewPublisher(f, "ns1", "aws", logr.Discard())
}

func post(t *testing.T, h http.Handler, body any, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	switch v := body.(type) {
	case string:
		b = []byte(v)
	default:
		b, _ = json.Marshal(v)
	}
	r := httptest.NewRequest(http.MethodPost, "/publish", bytes.NewReader(b))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errMsg(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var f publishFailure
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil || f.Error == "" {
		t.Fatalf("expected JSON error body, got %q", w.Body.String())
	}
	return f.Error
}

func TestPublish_Success(t *testing.T) {
	f := &fakeSNS{}
	w := post(t, newPub(f), PublishRequest{Destination: stdARN, Body: "hello"}, nil)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var s publishSuccess
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	if s.MessageID != "mid-1" {
		t.Errorf("messageId = %q", s.MessageID)
	}
	if aws.ToString(f.in.TopicArn) != stdARN || aws.ToString(f.in.Message) != "hello" {
		t.Errorf("bad input %+v", f.in)
	}
}

func TestPublish_ARNRequired(t *testing.T) {
	for _, dest := range []string{"", "orders", "orders.fifo", "arn:aws:sqs:us-east-1:123456789012:orders",
		"arn:aws:sns:us-east-1::orders", "arn:aws:sns:us-east-1:123456789012:orders:sub-id", "not:an:arn"} {
		f := &fakeSNS{}
		w := post(t, newPub(f), PublishRequest{Destination: dest, Body: "x"}, nil)
		if w.Code != 400 {
			t.Errorf("dest %q: status %d, want 400", dest, w.Code)
		}
		if msg := errMsg(t, w); !strings.Contains(msg, "arn:aws:sns") {
			t.Errorf("dest %q: unhelpful message %q", dest, msg)
		}
		if f.n != 0 {
			t.Errorf("dest %q: SNS was called", dest)
		}
	}
}

func TestPublish_EmptyBodyRejected(t *testing.T) {
	f := &fakeSNS{}
	if w := post(t, newPub(f), PublishRequest{Destination: stdARN}, nil); w.Code != 400 || f.n != 0 {
		t.Errorf("status %d calls %d", w.Code, f.n)
	}
}

func TestPublish_FIFOvsStandardDedup(t *testing.T) {
	f := &fakeSNS{}
	p := newPub(f)

	post(t, p, PublishRequest{Destination: fifoARN, Body: "x", IdempotencyKey: "key-1"}, nil)
	if aws.ToString(f.in.MessageDeduplicationId) != "key-1" {
		t.Errorf("fifo dedup = %v", f.in.MessageDeduplicationId)
	}
	if aws.ToString(f.in.MessageGroupId) != "kubezap-ns1-aws" {
		t.Errorf("fifo group = %v", f.in.MessageGroupId)
	}

	post(t, p, PublishRequest{Destination: fifoARN, Body: "x"}, nil)
	if f.in.MessageDeduplicationId != nil || f.in.MessageGroupId == nil {
		t.Errorf("fifo without key: dedup=%v group=%v", f.in.MessageDeduplicationId, f.in.MessageGroupId)
	}

	w := post(t, p, PublishRequest{Destination: stdARN, Body: "x", IdempotencyKey: "key-1"}, nil)
	if w.Code != 200 || f.in.MessageDeduplicationId != nil || f.in.MessageGroupId != nil {
		t.Errorf("standard topic must ignore idempotencyKey: code=%d in=%+v", w.Code, f.in)
	}

	if w := post(t, p, PublishRequest{Destination: fifoARN, Body: "x", IdempotencyKey: strings.Repeat("a", 129)}, nil); w.Code != 400 {
		t.Errorf("overlong key: status %d", w.Code)
	}
	if w := post(t, p, PublishRequest{Destination: fifoARN, Body: "x", IdempotencyKey: "has space"}, nil); w.Code != 400 {
		t.Errorf("bad-char key: status %d", w.Code)
	}
}

func TestPublish_MessageAttributes(t *testing.T) {
	f := &fakeSNS{}
	p := newPub(f)
	w := post(t, p, PublishRequest{Destination: stdARN, Body: "x",
		Headers: map[string]string{"a": "1", "content-type": "application/json"}},
		map[string]string{"traceparent": goodTP})
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	got := f.in.MessageAttributes
	if len(got) != 3 {
		t.Fatalf("attrs = %v", got)
	}
	for k, want := range map[string]string{"a": "1", "content-type": "application/json", "traceparent": goodTP} {
		v, ok := got[k]
		if !ok || aws.ToString(v.DataType) != "String" || aws.ToString(v.StringValue) != want {
			t.Errorf("attr %q = %+v", k, v)
		}
	}

	// Malformed traceparent is dropped, not forwarded and not fatal.
	post(t, p, PublishRequest{Destination: stdARN, Body: "x"}, map[string]string{"traceparent": "garbage"})
	if _, ok := f.in.MessageAttributes["traceparent"]; ok {
		t.Error("malformed traceparent forwarded")
	}

	// Request header overrides a body header of the same name.
	post(t, p, PublishRequest{Destination: stdARN, Body: "x", Headers: map[string]string{"traceparent": "old"}},
		map[string]string{"traceparent": goodTP})
	if aws.ToString(f.in.MessageAttributes["traceparent"].StringValue) != goodTP {
		t.Error("traceparent header did not win")
	}
}

func TestPublish_InvalidHeaders(t *testing.T) {
	cases := map[string]map[string]string{
		"bad name":    {"bad name": "v"},
		"aws prefix":  {"AWS.x": "v"},
		"empty value": {"k": ""},
	}
	many := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		many[k] = "v"
	}
	cases["too many"] = many
	for name, h := range cases {
		f := &fakeSNS{}
		w := post(t, newPub(f), PublishRequest{Destination: stdARN, Body: "x", Headers: h}, nil)
		if w.Code != 400 || f.n != 0 {
			t.Errorf("%s: status %d calls %d", name, w.Code, f.n)
		}
	}
}

func TestClassifyError(t *testing.T) {
	api := func(code string, fault smithy.ErrorFault) error {
		return &smithy.GenericAPIError{Code: code, Message: "m", Fault: fault}
	}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"authz", &snstypes.AuthorizationErrorException{}, 403},
		{"access denied", api("AccessDenied", smithy.FaultClient), 403},
		{"bad token", api("InvalidClientTokenId", smithy.FaultClient), 403},
		{"expired", api("ExpiredToken", smithy.FaultClient), 403},
		{"not found", &snstypes.NotFoundException{}, 404},
		{"invalid param", &snstypes.InvalidParameterException{}, 400},
		{"invalid param value", &snstypes.InvalidParameterValueException{}, 400},
		{"unknown client fault", api("Weird", smithy.FaultClient), 400},
		{"throttled", &snstypes.ThrottledException{}, 503},
		{"internal", &snstypes.InternalErrorException{}, 502},
		{"unknown server fault", api("Weird", smithy.FaultServer), 502},
		{"network", errors.New("dial tcp: connection refused"), 502},
		{"deadline", context.DeadlineExceeded, 504},
	}
	for _, c := range cases {
		f := &fakeSNS{err: c.err}
		w := post(t, newPub(f), PublishRequest{Destination: stdARN, Body: "x"}, nil)
		if w.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, w.Code, c.want)
			continue
		}
		errMsg(t, w)
	}
}

func TestPublish_Method405(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		f := &fakeSNS{}
		r := httptest.NewRequest(m, "/publish", nil)
		w := httptest.NewRecorder()
		newPub(f).ServeHTTP(w, r)
		if w.Code != 405 || w.Header().Get("Allow") != "POST" || f.n != 0 {
			t.Errorf("%s: code %d allow %q", m, w.Code, w.Header().Get("Allow"))
		}
	}
}

func TestPublish_BodyLimitAndBadJSON(t *testing.T) {
	f := &fakeSNS{}
	big := `{"destination":"` + stdARN + `","body":"` + strings.Repeat("a", MaxRequestBodyBytes) + `"}`
	if w := post(t, newPub(f), big, nil); w.Code != 413 || f.n != 0 {
		t.Errorf("oversize: code %d", w.Code)
	}
	if w := post(t, newPub(f), "{not json", nil); w.Code != 400 {
		t.Errorf("bad json: code %d", w.Code)
	}
	// Unknown fields (future contract additions) are tolerated.
	if w := post(t, newPub(f), `{"destination":"`+stdARN+`","body":"x","future":1}`, nil); w.Code != 200 {
		t.Errorf("unknown field: code %d", w.Code)
	}
}

func TestHealth_PublisherAware(t *testing.T) {
	h := NewHealth()
	h.SetStarted()
	if err := h.Check(); err != nil {
		t.Fatalf("idle with no publisher check must be ready: %v", err)
	}

	var nilPub *Publisher
	h.SetPublisherCheck(nilPub.Ready)
	if err := h.Check(); err == nil || !strings.Contains(err.Error(), "SNS") {
		t.Errorf("want SNS unavailable, got %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 503 {
		t.Errorf("code %d", rec.Code)
	}

	// Publisher-only deployment (no SQS Triggers) with a valid client: ready.
	h.SetPublisherCheck(newPub(&fakeSNS{}).Ready)
	if err := h.Check(); err != nil {
		t.Errorf("publisher-only should be ready: %v", err)
	}

	// An unhealthy SQS session still fails readiness.
	h.SetSession(types.NamespacedName{Namespace: "ns1", Name: "t1"}, false, "boom")
	if err := h.Check(); err == nil {
		t.Error("bad SQS session must fail health")
	}
}

// --- mTLS ---------------------------------------------------------------

type pki struct {
	dir                       string
	caPEM                     []byte
	caCert                    *x509.Certificate
	certFile, keyFile, caFile string
	client                    tls.Certificate
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	now := time.Now()
	ca, caKey, err := certutil.GenerateCA(pkix.Name{CommonName: "test-ca"}, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	server, err := certutil.IssueLeafCert(ca, caKey, pkix.Name{CommonName: "plugin"}, []string{"localhost"},
		x509.ExtKeyUsageServerAuth, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	client, err := certutil.IssueLeafCert(ca, caKey, pkix.Name{CommonName: "controller"}, nil,
		x509.ExtKeyUsageClientAuth, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	p := &pki{dir: t.TempDir(), caCert: ca, client: client, caPEM: certutil.EncodeCertPEM(ca.Raw)}
	p.certFile, p.keyFile, p.caFile = filepath.Join(p.dir, "tls.crt"), filepath.Join(p.dir, "tls.key"), filepath.Join(p.dir, "ca.crt")
	keyPEM, err := certutil.PrivateKeyPEM(server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for f, b := range map[string][]byte{p.certFile: certutil.CertChainPEM(server), p.keyFile: keyPEM, p.caFile: p.caPEM} {
		if err := os.WriteFile(f, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func (p *pki) clientTLS(withCert bool) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(p.caCert)
	c := &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	if withCert {
		c.Certificates = []tls.Certificate{p.client}
	}
	return c
}

func TestMTLS_HealthPortSeparation(t *testing.T) {
	p := newPKI(t)
	cfg := Config{
		PublisherPort: 0, HealthPort: 0, MTLSEnabled: true,
		MTLSCertFile: p.certFile, MTLSKeyFile: p.keyFile, MTLSCAFile: p.caFile,
	}
	f := &fakeSNS{}
	health := NewHealth()
	health.SetStarted()
	health.SetPublisherCheck(newPub(f).Ready)
	srvs, err := NewServers(cfg, newPub(f), health)
	if err != nil {
		t.Fatal(err)
	}
	if !srvs.TLS || srvs.Health == nil {
		t.Fatal("mTLS must yield a TLS publisher server and a separate health server")
	}

	// Publisher handler: /publish only, no /healthz.
	pubTS := httptest.NewUnstartedServer(srvs.Publisher.Handler)
	pubTS.TLS = srvs.Publisher.TLSConfig
	pubTS.StartTLS()
	defer pubTS.Close()
	healthTS := httptest.NewServer(srvs.Health.Handler)
	defer healthTS.Close()

	// Health: plain HTTP, no client cert needed.
	resp, err := http.Get(healthTS.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	resp, _ = http.Get(healthTS.URL + "/publish")
	if resp.StatusCode != 404 {
		t.Errorf("health server must not serve /publish, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Publisher with a valid client cert: works; /healthz absent.
	okClient := &http.Client{Transport: &http.Transport{TLSClientConfig: p.clientTLS(true)}}
	body, _ := json.Marshal(PublishRequest{Destination: stdARN, Body: "x"})
	resp, err = okClient.Post(pubTS.URL+"/publish", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "mid-1") {
		t.Errorf("mTLS publish: %d %s", resp.StatusCode, b)
	}
	resp, _ = okClient.Get(pubTS.URL + "/healthz")
	if resp.StatusCode != 404 {
		t.Errorf("publisher port must not serve /healthz under mTLS, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// No client cert: handshake rejected (TLS 1.3 surfaces it on first read).
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: p.clientTLS(false)}}
	if resp, err := noCert.Post(pubTS.URL+"/publish", "application/json", bytes.NewReader(body)); err == nil {
		_ = resp.Body.Close()
		t.Error("client without cert was accepted")
	}

	// Cert from a different CA: rejected.
	other := newPKI(t)
	wrong := other.clientTLS(true)
	wrong.RootCAs = p.clientTLS(false).RootCAs
	wrongClient := &http.Client{Transport: &http.Transport{TLSClientConfig: wrong}}
	if resp, err := wrongClient.Post(pubTS.URL+"/publish", "application/json", bytes.NewReader(body)); err == nil {
		_ = resp.Body.Close()
		t.Error("client with foreign-CA cert was accepted")
	}
	if f.n != 1 {
		t.Errorf("SNS calls = %d, want exactly the 1 authenticated publish", f.n)
	}
}

func TestServers_PlainSingleListener(t *testing.T) {
	f := &fakeSNS{}
	health := NewHealth()
	health.SetStarted()
	srvs, err := NewServers(Config{PublisherPort: 8090, HealthPort: 8090}, newPub(f), health)
	if err != nil {
		t.Fatal(err)
	}
	if srvs.TLS || srvs.Health != nil {
		t.Fatal("non-mTLS must be a single plain listener")
	}
	ts := httptest.NewServer(srvs.Publisher.Handler)
	defer ts.Close()
	for path, want := range map[string]int{"/healthz": 200, "/nope": 404} {
		resp, err := http.Get(ts.URL + path)
		if err != nil || resp.StatusCode != want {
			t.Errorf("%s: %v %v", path, resp, err)
		}
		_ = resp.Body.Close()
	}
}

func TestServerTLSConfig_ReloadsRotatedCerts(t *testing.T) {
	p := newPKI(t)
	cfg, err := ServerTLSConfig(p.certFile, p.keyFile, p.caFile)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := cfg.GetConfigForClient(nil)

	// Rotate: new PKI written over the same paths with a newer mtime.
	q := newPKI(t)
	for src, dst := range map[string]string{q.certFile: p.certFile, q.keyFile: p.keyFile, q.caFile: p.caFile} {
		b, _ := os.ReadFile(src)
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(time.Minute)
		_ = os.Chtimes(dst, future, future)
	}
	second, _ := cfg.GetConfigForClient(nil)
	if bytes.Equal(first.Certificates[0].Certificate[0], second.Certificates[0].Certificate[0]) {
		t.Error("server cert was not reloaded after rotation")
	}
	if second.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Error("client certs must be required")
	}
}

func TestServerTLSConfig_Errors(t *testing.T) {
	p := newPKI(t)
	if _, err := ServerTLSConfig(p.certFile, p.keyFile, filepath.Join(p.dir, "missing")); err == nil {
		t.Error("missing CA must fail")
	}
	_ = os.WriteFile(p.caFile, []byte("junk"), 0o600)
	if _, err := ServerTLSConfig(p.certFile, p.keyFile, p.caFile); err == nil {
		t.Error("junk CA must fail")
	}
}
