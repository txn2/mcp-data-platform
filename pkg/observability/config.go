// Package observability provides OpenTelemetry-based metrics for the
// mcp-data-platform server.
//
// Phase 1 instruments two chokepoints: the MCP tool-call middleware and
// the apigateway outbound HTTP path. Metrics are exported in Prometheus
// format on a separate HTTP listener so scrape traffic is isolated from
// the main MCP/HTTP listener.
//
// Configuration is environment-only in this phase to keep the surface
// small. See ConfigFromEnv for the recognized variables.
package observability

import (
	"os"
	"strconv"
	"strings"
)

// envEnabled toggles the metrics subsystem. Defaults to true; set to
// "false" (or "0") to disable. When disabled, New returns a no-op
// Metrics value where every Record method is a fast no-op and no
// listener is started.
const envEnabled = "OTEL_METRICS_ENABLED"

// envListenAddr selects the listen address for the /metrics endpoint.
// The metrics listener is intentionally separate from the main MCP
// listener so scrape traffic and tool traffic do not share an auth
// path.
const envListenAddr = "OTEL_METRICS_ADDR"

// DefaultListenAddr is the address the /metrics listener binds to when
// OTEL_METRICS_ADDR is unset. Port 9090 is the conventional Prometheus
// scrape port and does not collide with the platform's main HTTP port
// (8080 by default).
const DefaultListenAddr = ":9090"

// Config holds the operator-configurable knobs for the metrics
// subsystem. Phase 1 keeps this minimal; tracing and per-toolkit
// instrumentation in later phases may add fields.
type Config struct {
	// Enabled gates the entire subsystem. When false, New returns a
	// Metrics value whose Record methods are no-ops, the listener is
	// not started, and no OTel MeterProvider is constructed.
	Enabled bool

	// ListenAddr is the bind address for the /metrics HTTP listener,
	// e.g. ":9090" or "127.0.0.1:9090". Ignored when Enabled is false.
	ListenAddr string
}

// ConfigFromEnv reads the observability configuration from environment
// variables. Unset or unparsable values fall back to the defaults so
// the platform can boot even with a partial configuration.
//
// Metrics are enabled by default. The /metrics listener binds to
// DefaultListenAddr on a separate port from the main MCP/HTTP listener
// so operators can isolate scrape traffic with a NetworkPolicy. Set
// OTEL_METRICS_ENABLED=false to disable.
func ConfigFromEnv() Config {
	return Config{
		Enabled:    parseBoolEnv(envEnabled, true),
		ListenAddr: stringEnvOrDefault(envListenAddr, DefaultListenAddr),
	}
}

// Tracing environment variables. Tracing is OFF by default: unlike
// metrics (an always-available scrape endpoint), traces require an OTLP
// collector to receive them, so enabling without a collector would be
// pointless. Operators opt in explicitly.
const (
	// envTracesEnabled toggles the tracing subsystem. Defaults to false.
	envTracesEnabled = "OTEL_TRACES_ENABLED"

	// envOTLPEndpoint is the OTLP/gRPC collector endpoint, e.g.
	// "otel-collector:4317". This is the standard OpenTelemetry variable
	// name so existing collector deployments work unchanged.
	envOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

	// envOTLPInsecure disables transport TLS to the collector. Defaults
	// to true: the common topology is an in-cluster collector reached
	// over the pod network, not the public internet. Set to false to use
	// TLS to a remote collector.
	envOTLPInsecure = "OTEL_EXPORTER_OTLP_INSECURE"

	// envTracesSamplerArg is the head-based sampling ratio in [0,1]
	// applied to ROOT spans (a parent's sampling decision is always
	// honored). Defaults to 0.1 (10%). Tail-based sampling — keeping
	// 100% of error/slow traces — is configured in the collector, not
	// here, so it can be tuned without redeploying the platform.
	envTracesSamplerArg = "OTEL_TRACES_SAMPLER_ARG"

	// envServiceName sets the service.name resource attribute on every
	// span. Standard OpenTelemetry variable. Defaults to DefaultServiceName.
	envServiceName = "OTEL_SERVICE_NAME"
)

// Tracing defaults.
const (
	// DefaultOTLPEndpoint is used when tracing is enabled but
	// OTEL_EXPORTER_OTLP_ENDPOINT is unset.
	DefaultOTLPEndpoint = "localhost:4317"

	// DefaultServiceName is the service.name resource value when
	// OTEL_SERVICE_NAME is unset.
	DefaultServiceName = "mcp-data-platform"

	// DefaultSamplerArg is the head-based sampling ratio when
	// OTEL_TRACES_SAMPLER_ARG is unset or unparsable.
	DefaultSamplerArg = 0.1
)

// TracingConfig holds the operator-configurable knobs for the tracing
// subsystem. Environment-only, mirroring the metrics Config.
type TracingConfig struct {
	// Enabled gates the entire subsystem. When false, NewTracer returns
	// a nil *Tracer whose methods are no-ops and no exporter, provider,
	// or global TracerProvider override is constructed.
	Enabled bool

	// Endpoint is the OTLP/gRPC collector address ("host:port").
	Endpoint string

	// Insecure disables transport TLS to the collector.
	Insecure bool

	// SamplerArg is the head-based sampling ratio for root spans, [0,1].
	SamplerArg float64

	// ServiceName is the service.name resource attribute on every span.
	ServiceName string
}

// TracingConfigFromEnv reads the tracing configuration from environment
// variables. Tracing is disabled unless OTEL_TRACES_ENABLED is truthy.
// Unset or unparsable values fall back to defaults so a partial
// configuration still boots.
func TracingConfigFromEnv() TracingConfig {
	return TracingConfig{
		Enabled:     parseBoolEnv(envTracesEnabled, false),
		Endpoint:    stringEnvOrDefault(envOTLPEndpoint, DefaultOTLPEndpoint),
		Insecure:    parseBoolEnv(envOTLPInsecure, true),
		SamplerArg:  parseFloatEnv(envTracesSamplerArg, DefaultSamplerArg),
		ServiceName: stringEnvOrDefault(envServiceName, DefaultServiceName),
	}
}

// parseFloatEnv parses a float environment variable, clamping the result
// to [0,1] (the valid range for a sampling ratio). Returns def when the
// variable is unset, empty, or unparsable.
func parseFloatEnv(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	const floatBitSize = 64
	v, err := strconv.ParseFloat(raw, floatBitSize)
	if err != nil {
		return def
	}
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// parseBoolEnv parses a boolean environment variable. Returns def when
// the variable is unset, empty, or unparsable. Accepted truthy values
// match strconv.ParseBool: "1", "t", "T", "true", "TRUE", "True".
func parseBoolEnv(key string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

// stringEnvOrDefault returns the trimmed value of the named env var,
// or def when the variable is unset or empty.
func stringEnvOrDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}
