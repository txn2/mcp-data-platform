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

// envMetricsExporter selects where metrics go (#1893): "prometheus" (the
// default: the /metrics listener only), "otlp" (pushed to the collector at
// OTEL_EXPORTER_OTLP_ENDPOINT, no listener), or "both". An unrecognized
// value is the default, so a typo cannot silently switch the scrape off.
const envMetricsExporter = "OTEL_METRICS_EXPORTER"

// MetricsExporter is the OTEL_METRICS_EXPORTER choice.
type MetricsExporter string

// The MetricsExporter values.
const (
	MetricsExporterPrometheus MetricsExporter = "prometheus"
	MetricsExporterOTLP       MetricsExporter = "otlp"
	MetricsExporterBoth       MetricsExporter = "both"
)

// Prometheus reports whether the /metrics listener is served.
func (e MetricsExporter) Prometheus() bool {
	return e == MetricsExporterPrometheus || e == MetricsExporterBoth || e == ""
}

// OTLP reports whether metrics are pushed over OTLP.
func (e MetricsExporter) OTLP() bool {
	return e == MetricsExporterOTLP || e == MetricsExporterBoth
}

// parseMetricsExporter reads the choice, defaulting on anything it does not
// recognize.
func parseMetricsExporter(raw string) MetricsExporter {
	switch e := MetricsExporter(strings.ToLower(strings.TrimSpace(raw))); e {
	case MetricsExporterPrometheus, MetricsExporterOTLP, MetricsExporterBoth:
		return e
	default:
		return MetricsExporterPrometheus
	}
}

// Config holds the operator-configurable knobs for the metrics subsystem.
type Config struct {
	// Enabled gates the entire subsystem. When false, New returns a
	// Metrics value whose Record methods are no-ops, the listener is
	// not started, and no OTel MeterProvider is constructed.
	Enabled bool

	// ListenAddr is the bind address for the /metrics HTTP listener,
	// e.g. ":9090" or "127.0.0.1:9090". Ignored when Enabled is false or
	// Exporter leaves Prometheus out.
	ListenAddr string

	// Exporter is where metrics go: the Prometheus listener, an OTLP push to
	// the collector, or both (#1893).
	Exporter MetricsExporter

	// OTLP is the collector the OTLP reader pushes to; shared with the
	// tracer and the log provider.
	OTLP OTLPEndpoint
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
		Exporter:   parseMetricsExporter(os.Getenv(envMetricsExporter)),
		OTLP:       OTLPEndpointFromEnv(),
	}
}

// Tracing environment variables. Tracing is OFF by default: unlike
// metrics (an always-available scrape endpoint), traces require an OTLP
// collector to receive them, so enabling without a collector would be
// pointless. Operators opt in explicitly.
const (
	// envTracesEnabled toggles the tracing subsystem. Defaults to false.
	envTracesEnabled = "OTEL_TRACES_ENABLED"

	// envOTLPEndpoint is the OTLP/gRPC collector endpoint, in either form
	// the OpenTelemetry specification defines: "otel-collector:4317", or a
	// URL whose scheme chooses TLS (OTLPEndpoint). The standard variable
	// name, so existing collector deployments work unchanged; one value for
	// the trace, metric and log exporters.
	envOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

	// envOTLPInsecure disables transport TLS to the collector. Unset, a
	// host:port endpoint is plaintext (the common topology is an in-cluster
	// collector reached over the pod network) and a URL follows its scheme.
	// Set, it decides for both forms.
	envOTLPInsecure = "OTEL_EXPORTER_OTLP_INSECURE"

	// envTracesSamplerArg is the head-based sampling ratio in [0,1]
	// applied to ROOT spans (a parent's sampling decision is always
	// honored). Defaults to 0.1 (10%). The decision is made when the span
	// starts, before its outcome is known, so at the default about 90% of
	// root traces are never exported, errors and slow calls included. A
	// collector's tail sampling can only keep what it receives: a
	// deployment that wants every error or slow trace kept there sets this
	// to 1.0 and lets the collector drop the rest.
	envTracesSamplerArg = "OTEL_TRACES_SAMPLER_ARG"

	// envTracesIncludeUserEmail lets the tool-call span carry the caller's
	// email address. Defaults to false: the user id is on every span, the
	// address is personal data, and a trace backend is outside the
	// platform (#1892).
	envTracesIncludeUserEmail = "OTEL_TRACES_INCLUDE_USER_EMAIL"
)

// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES are read by the SDK's
// environment detector inside Resource; the platform does not read them
// itself.

// Tracing defaults.
const (
	// DefaultOTLPEndpoint is used when tracing is enabled but
	// OTEL_EXPORTER_OTLP_ENDPOINT is unset.
	DefaultOTLPEndpoint = "localhost:4317"

	// DefaultServiceName is the service.name resource value when neither
	// OTEL_SERVICE_NAME nor OTEL_RESOURCE_ATTRIBUTES names one.
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

	// OTLP is the collector the spans are exported to; shared with the
	// metrics OTLP reader and the log provider.
	OTLP OTLPEndpoint

	// SamplerArg is the head-based sampling ratio for root spans, [0,1].
	SamplerArg float64

	// IncludeUserEmail lets the tool-call span carry mcp.user_email.
	IncludeUserEmail bool
}

// TracingConfigFromEnv reads the tracing configuration from environment
// variables. Tracing is disabled unless OTEL_TRACES_ENABLED is truthy.
// Unset or unparsable values fall back to defaults so a partial
// configuration still boots.
func TracingConfigFromEnv() TracingConfig {
	return TracingConfig{
		Enabled:    parseBoolEnv(envTracesEnabled, false),
		OTLP:       OTLPEndpointFromEnv(),
		SamplerArg: parseFloatEnv(envTracesSamplerArg, DefaultSamplerArg),

		IncludeUserEmail: parseBoolEnv(envTracesIncludeUserEmail, false),
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
