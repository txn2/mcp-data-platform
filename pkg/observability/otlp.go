package observability

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

// OTLPEndpoint is the collector address every OTLP exporter dials, as the
// operator wrote it. One value, parsed once, feeds the trace, metric and log
// exporters so the three cannot read the same variable differently (#1893).
type OTLPEndpoint struct {
	// Endpoint is OTEL_EXPORTER_OTLP_ENDPOINT in either form the OpenTelemetry
	// specification defines: "host:port", or a URL ("http://collector:4317")
	// whose scheme chooses TLS.
	Endpoint string

	// Insecure is OTEL_EXPORTER_OTLP_INSECURE when it was set. Nil leaves the
	// choice to the form: plaintext for "host:port" (the common in-cluster
	// topology), the scheme for a URL. Set, it wins over both.
	Insecure *bool
}

// OTLPEndpointFromEnv reads OTEL_EXPORTER_OTLP_ENDPOINT (default
// DefaultOTLPEndpoint) and OTEL_EXPORTER_OTLP_INSECURE.
func OTLPEndpointFromEnv() OTLPEndpoint {
	ep := OTLPEndpoint{Endpoint: stringEnvOrDefault(envOTLPEndpoint, DefaultOTLPEndpoint)}
	if raw := strings.TrimSpace(os.Getenv(envOTLPInsecure)); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			ep.Insecure = &v
		}
	}
	return ep
}

// otlpTarget is an OTLPEndpoint resolved to what an exporter is given.
type otlpTarget struct {
	// endpoint is the host:port, or the URL with its scheme settled.
	endpoint string
	// isURL says which option the exporter takes: WithEndpointURL for a URL,
	// WithEndpoint (plus WithInsecure when plaintext) for host:port.
	isURL bool
	// insecure is the settled transport choice.
	insecure bool
}

// resolve settles the endpoint's form and transport. A URL's scheme decides
// TLS unless Insecure was set, in which case the scheme is rewritten to
// match, since the exporters read TLS off the scheme and have no option that
// forces it the other way. host:port is plaintext unless Insecure says
// otherwise.
func (ep OTLPEndpoint) resolve() otlpTarget {
	t := otlpTarget{endpoint: ep.Endpoint}
	if u, err := url.Parse(ep.Endpoint); err == nil && u.Scheme != "" && u.Host != "" {
		t.isURL = true
		t.insecure = u.Scheme != "https"
		if ep.Insecure != nil {
			t.insecure = *ep.Insecure
			u.Scheme = "https"
			if t.insecure {
				u.Scheme = "http"
			}
			t.endpoint = u.String()
		}
		return t
	}
	t.insecure = true
	if ep.Insecure != nil {
		t.insecure = *ep.Insecure
	}
	return t
}

// otlpOptions applies ep through one exporter package's option constructors.
// The otlptracegrpc, otlpmetricgrpc and otlploggrpc packages each define their
// own Option type with the same three constructors, so the one resolution is
// handed to each this way rather than written three times.
func otlpOptions[O any](ep OTLPEndpoint, withEndpoint, withEndpointURL func(string) O, withInsecure func() O) []O {
	t := ep.resolve()
	if t.isURL {
		return []O{withEndpointURL(t.endpoint)}
	}
	opts := []O{withEndpoint(t.endpoint)}
	if t.insecure {
		opts = append(opts, withInsecure())
	}
	return opts
}
