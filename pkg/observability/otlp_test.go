package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOTLPEndpoint_Resolve(t *testing.T) {
	tests := []struct {
		name     string
		ep       OTLPEndpoint
		endpoint string
		isURL    bool
		insecure bool
	}{
		{"host:port is plaintext by default", OTLPEndpoint{Endpoint: "collector:4317"}, "collector:4317", false, true},
		{"host:port honors an explicit secure", OTLPEndpoint{Endpoint: "collector:4317", Insecure: new(false)}, "collector:4317", false, false},
		{"an IP host:port is not a URL", OTLPEndpoint{Endpoint: "127.0.0.1:4317"}, "127.0.0.1:4317", false, true},
		{"http URL is plaintext from its scheme", OTLPEndpoint{Endpoint: "http://collector:4317"}, "http://collector:4317", true, true},
		{"https URL is TLS from its scheme", OTLPEndpoint{Endpoint: "https://collector:4317"}, "https://collector:4317", true, false},
		{"an explicit insecure rewrites an https scheme", OTLPEndpoint{Endpoint: "https://collector:4317", Insecure: new(true)}, "http://collector:4317", true, true},
		{"an explicit secure rewrites an http scheme", OTLPEndpoint{Endpoint: "http://collector:4317/v1", Insecure: new(false)}, "https://collector:4317/v1", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ep.resolve()
			assert.Equal(t, tt.endpoint, got.endpoint)
			assert.Equal(t, tt.isURL, got.isURL)
			assert.Equal(t, tt.insecure, got.insecure)
		})
	}
}

func TestOTLPOptions_PicksTheConstructorForTheForm(t *testing.T) {
	type opt string
	withEndpoint := func(s string) opt { return opt("endpoint:" + s) }
	withURL := func(s string) opt { return opt("url:" + s) }
	withInsecure := func() opt { return "insecure" }

	assert.Equal(t, []opt{"endpoint:c:4317", "insecure"},
		otlpOptions(OTLPEndpoint{Endpoint: "c:4317"}, withEndpoint, withURL, withInsecure))
	assert.Equal(t, []opt{"endpoint:c:4317"},
		otlpOptions(OTLPEndpoint{Endpoint: "c:4317", Insecure: new(false)}, withEndpoint, withURL, withInsecure))
	assert.Equal(t, []opt{"url:https://c:4317"},
		otlpOptions(OTLPEndpoint{Endpoint: "https://c:4317"}, withEndpoint, withURL, withInsecure),
		"a URL carries TLS in its scheme; the exporter reads it there")
}

func TestOTLPEndpointFromEnv(t *testing.T) {
	t.Setenv(envOTLPEndpoint, " http://collector:4317 ")
	t.Setenv(envOTLPInsecure, "false")
	ep := OTLPEndpointFromEnv()
	assert.Equal(t, "http://collector:4317", ep.Endpoint)
	require.NotNil(t, ep.Insecure)
	assert.False(t, *ep.Insecure)

	t.Setenv(envOTLPEndpoint, "")
	t.Setenv(envOTLPInsecure, "not-a-bool")
	ep = OTLPEndpointFromEnv()
	assert.Equal(t, DefaultOTLPEndpoint, ep.Endpoint)
	assert.Nil(t, ep.Insecure, "an unparsable value is as if unset")
}
