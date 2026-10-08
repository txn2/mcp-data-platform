// Package outbound is the one transport chain every HTTP client the platform
// builds is made from (#1895). A request sent through it carries the
// platform's User-Agent, a client span that continues the caller's trace and
// the W3C traceparent that lets the upstream continue it, and is counted and
// timed under http_client_requests_total{kind, connection, status_class}; a
// destination the egress guard refused is counted under
// egress_blocked_total{reason} and logged once with the host sanitized.
//
// A client is built with NewClient, or an existing RoundTripper is wrapped
// with Transport when the client is someone else's to build (the oauth2
// library's token client, a websocket dialer). Nothing else in the tree
// constructs an http.Client or reaches http.DefaultClient; the Semgrep rule
// go-outbound-client refuses the shapes that would.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/useragent"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Kind is what an outbound call is for: the kind label, a closed set.
type Kind string

// The kinds. A new client names one of these; the metric's cardinality is
// their count.
const (
	KindAPI          Kind = "api"          // an API gateway connection's upstream
	KindGraphQL      Kind = "graphql"      // a GraphQL connection's endpoint
	KindMCP          Kind = "mcp"          // an MCP gateway connection's upstream server
	KindUtil         Kind = "util"         // the util connection's public fetch
	KindOAuth        Kind = "oauth"        // a token endpoint: client credentials, code exchange, refresh
	KindOIDC         Kind = "oidc"         // OIDC discovery and JWKS
	KindEmbedding    Kind = "embedding"    // the embedding provider
	KindNotification Kind = "notification" // a notification channel's webhook
	KindSpecFetch    Kind = "spec_fetch"   // an OpenAPI document fetched for a catalog
	KindPromQL       Kind = "promql"       // the PromQL proxy's Prometheus
	KindRenderer     Kind = "renderer"     // the headless renderer's DevTools endpoint
	KindDataHub      Kind = "datahub"      // the knowledge layer's DataHub REST writes
	KindBranding     Kind = "branding"     // the portal's logo fetch
)

// The span attribute keys a client span carries beside the HTTP semantic
// conventions otelhttp emits: what the call was for, and the connection.
const (
	spanAttrKind       = "upstream.kind"
	spanAttrConnection = "mcp.connection"
)

// Options is what a client is built with.
type Options struct {
	// Kind is what the calls are for. Required.
	Kind Kind
	// Connection is the operator's connection name, or empty for a kind
	// that has none.
	Connection string
	// Base is the transport the chain is built over: a connection's TLS and
	// dial settings, an egress guard, an in-process handler. Nil is a fresh
	// http.Transport with Go's defaults, never the process-wide one, so a
	// client has its own connection pool.
	Base http.RoundTripper
	// Timeout is the client's whole-request bound; zero leaves the request
	// to its context.
	Timeout time.Duration
	// CheckRedirect is the client's redirect policy; nil refuses redirects,
	// which is what a credential-bearing or SSRF-guarded call wants.
	CheckRedirect func(req *http.Request, via []*http.Request) error
	// NoPropagation leaves the traceparent and tracestate headers off the
	// request: a connection's trace_propagation key, for an upstream that
	// rejects unknown headers or must not see the deployment's trace ids.
	NoPropagation bool
	// Metrics overrides the recorder the observability layer installed
	// (SetDefaultMetrics). Nil records through the default.
	Metrics *observability.Metrics
}

// defaultMetrics is the recorder every chain records through when its
// options name none: the platform's one Metrics, installed by the
// observability layer before any client is built. A package default rather
// than a handle threaded through fifteen construction sites, some of them
// package-level clients with no deps at all; nil records nothing.
//
//nolint:gochecknoglobals // the one process-wide recorder, like the global tracer provider the span uses
var defaultMetrics atomic.Pointer[observability.Metrics]

// SetDefaultMetrics installs the recorder every chain records through
// unless its Options name another. Nil-safe; a disabled recorder is a nil
// *Metrics and records nothing.
func SetDefaultMetrics(m *observability.Metrics) {
	defaultMetrics.Store(m)
}

// Metrics is the recorder the chain records through by default, for a
// retry loop that counts beside the requests it issues (upstream_retries_total)
// and holds no handle of its own. Nil, and nil-safe, until the observability
// layer installs one.
func Metrics() *observability.Metrics {
	return defaultMetrics.Load()
}

// NewClient builds a client over the chain.
func NewClient(o Options) *http.Client {
	check := o.CheckRedirect
	if check == nil {
		check = refuseRedirects
	}
	return &http.Client{
		Transport:     Transport(o.Base, o),
		Timeout:       o.Timeout,
		CheckRedirect: check,
	}
}

func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// maxRedirects is how many redirects FollowRedirects follows, net/http's own
// default.
const maxRedirects = 10

// The fresh transport a client with no base is built over: Go's defaults for
// the handshake and the idle pool, a pool of its own rather than the
// process-wide one.
const (
	tlsHandshakeTimeout   = 10 * time.Second
	idleConnectionTimeout = 90 * time.Second
	maxIdleConnections    = 10
)

// FollowRedirects is the redirect policy of a client that follows them, as a
// client with no policy would: for a discovery document, an identity
// provider, a logo, a renderer. A client that carries a credential or a
// guarded destination keeps the default and refuses.
func FollowRedirects(_ *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	return nil
}

// Transport is the chain over base: the recorder outermost, the User-Agent,
// and the otelhttp transport that opens the client span and injects the
// trace context, over base. o.Base is ignored; base is what is given.
func Transport(base http.RoundTripper, o Options) http.RoundTripper {
	if base == nil {
		base = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       idleConnectionTimeout,
			MaxIdleConns:          maxIdleConnections,
		}
	}
	otelOpts := []otelhttp.Option{
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return string(o.Kind) + " " + observability.HTTPMethodLabel(r.Method)
		}),
		otelhttp.WithSpanOptions(trace.WithAttributes(
			attribute.String(spanAttrKind, string(o.Kind)),
			attribute.String(spanAttrConnection, o.Connection),
		)),
	}
	if o.NoPropagation {
		otelOpts = append(otelOpts, otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()))
	}
	return &transport{
		next:       useragent.Transport(otelhttp.NewTransport(base, otelOpts...)),
		base:       base,
		kind:       o.Kind,
		connection: o.Connection,
		metrics:    o.Metrics,
	}
}

// transport is the chain's outermost layer: it records what the layers below
// it did.
type transport struct {
	next       http.RoundTripper
	base       http.RoundTripper
	kind       Kind
	connection string
	metrics    *observability.Metrics
}

func (t *transport) recorder() *observability.Metrics {
	if t.metrics != nil {
		return t.metrics
	}
	return defaultMetrics.Load()
}

// connectionKinds are the kinds whose connection is a toolkit connection, so
// what its upstream answered is that connection's state (#1898).
//
//nolint:gochecknoglobals // a read-only lookup set.
var connectionKinds = map[Kind]bool{KindAPI: true, KindGraphQL: true, KindMCP: true}

// RoundTrip sends the request through the chain and records it. The error
// is returned as the layer below produced it, so a caller's errors.As on a
// *url.Error, a *BlockedError or a timeout keeps working.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	d := time.Since(start)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	ctx := req.Context()
	m := t.recorder()
	m.RecordHTTPClientRequest(ctx, string(t.kind), t.connection, observability.HTTPStatusClass(status), d)
	if connectionKinds[t.kind] {
		connstate.Observe(string(t.kind), t.connection, connstate.FromHTTP(status, err))
	}
	var blocked *egressguard.BlockedError
	if errors.As(err, &blocked) {
		m.RecordEgressBlocked(ctx, blocked.Class)
		slog.WarnContext(ctx, "egress blocked",
			"kind", string(t.kind),
			"host", logsan.SanitizeForLog(blocked.Host),
			"reason", blocked.Class)
	}
	return resp, err //nolint:wrapcheck // the layer below's error, as it is
}

// CloseIdleConnections forwards to the base transport, so a connection whose
// client is on the chain still releases its idle sockets when it is removed.
func (t *transport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Unwrap is the transport the chain was built over, for a test that asserts
// on it (a dial timeout, a TLS config, an egress guard).
func (t *transport) Unwrap() http.RoundTripper { return t.base }

// Chain is what Wraps reports of a client's transport: the transport the
// chain was built over and the kind and connection it records under.
type Chain struct {
	Base       http.RoundTripper
	Kind       Kind
	Connection string
}

// Wraps reports whether rt is this package's chain, and what it carries, for
// a test that asserts on the client a package built.
func Wraps(rt http.RoundTripper) (Chain, bool) {
	t, isChain := rt.(*transport)
	if !isChain {
		return Chain{}, false
	}
	return Chain{Base: t.base, Kind: t.kind, Connection: t.connection}, true
}

// RecordBlocked counts and logs an egress refusal a caller found before any
// request was sent: a URL whose host a preflight check refused. The chain
// records the ones its dial refuses itself.
func RecordBlocked(ctx context.Context, kind Kind, host, class string) {
	defaultMetrics.Load().RecordEgressBlocked(ctx, class)
	slog.WarnContext(ctx, "egress blocked",
		"kind", string(kind),
		"host", logsan.SanitizeForLog(host),
		"reason", class)
}
