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

// envEnabled toggles the metrics subsystem. When unset or false, New
// returns a no-op Metrics value where every Record method is a fast
// no-op and no listener is started.
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
func ConfigFromEnv() Config {
	return Config{
		Enabled:    parseBoolEnv(envEnabled, false),
		ListenAddr: stringEnvOrDefault(envListenAddr, DefaultListenAddr),
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
