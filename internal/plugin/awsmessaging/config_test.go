package awsmessaging

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		EnvNamespace:          "team-a",
		EnvIntegrationName:    "aws",
		EnvAWSRegion:          "eu-west-1",
		EnvAWSAccessKeyID:     "AKIA",
		EnvAWSSecretAccessKey: "secret",
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	c, err := LoadConfig(envMap(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" || c.PublisherPort != 8090 || c.HealthPort != 8090 || c.MTLSEnabled {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.SessionToken != "" || c.EndpointURL != "" {
		t.Errorf("optional values should be empty: %+v", c)
	}
}

func TestLoadConfig_OptionalAndPorts(t *testing.T) {
	e := validEnv()
	e[EnvAWSSessionToken] = "tok"
	e[EnvAWSEndpointURL] = "http://localstack:4566"
	e[EnvPublisherPort] = "9000"
	e[EnvLogLevel] = "debug"
	c, err := LoadConfig(envMap(e))
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionToken != "tok" || c.EndpointURL != "http://localstack:4566" ||
		c.PublisherPort != 9000 || c.HealthPort != 9000 || c.LogLevel != "debug" {
		t.Errorf("unexpected config: %+v", c)
	}
}

func TestLoadConfig_MTLSHealthPort(t *testing.T) {
	e := validEnv()
	e[EnvMTLSEnabled] = "true"
	e[EnvMTLSHealthPort] = "8091"
	c, err := LoadConfig(envMap(e))
	if err != nil {
		t.Fatal(err)
	}
	if !c.MTLSEnabled || c.HealthPort != 8091 || c.PublisherPort != 8090 {
		t.Errorf("unexpected config: %+v", c)
	}
	delete(e, EnvMTLSHealthPort)
	if _, err := LoadConfig(envMap(e)); err == nil {
		t.Error("expected error when mTLS enabled without health port")
	}
}

func TestLoadConfig_Errors(t *testing.T) {
	_, err := LoadConfig(envMap(map[string]string{EnvAWSEndpointURL: "ftp://x", EnvPublisherPort: "abc"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{EnvNamespace, EnvIntegrationName, EnvAWSRegion, EnvAWSAccessKeyID,
		EnvAWSEndpointURL, EnvPublisherPort} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "secret") && strings.Contains(err.Error(), "AKIA") {
		t.Error("error must not echo credentials")
	}
}

func TestParseQueueURL(t *testing.T) {
	good := []string{
		"https://sqs.eu-west-1.amazonaws.com/123456789012/orders",
		"http://localstack:4566/000000000000/orders.fifo",
	}
	for _, g := range good {
		if err := ParseQueueURL(g); err != nil {
			t.Errorf("ParseQueueURL(%q) = %v", g, err)
		}
	}
	for _, b := range []string{"", "  ", "orders", "sqs://x/y", "https://host", "https://host/", "http://%zz"} {
		if err := ParseQueueURL(b); err == nil {
			t.Errorf("ParseQueueURL(%q) expected error", b)
		}
	}
}
