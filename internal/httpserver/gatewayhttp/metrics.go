package gatewayhttp

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// OperationResolver maps an inbound (connection, method, path) to the
// OpenAPI operationId for metric labeling, and reports whether a
// connection is registered. Implemented by the apigateway toolkit.
// ResolveOperationID returns "" when unresolved; the middleware maps
// "" to "unknown". HasConnection lets the middleware clamp the
// connection label to the registered set so an arbitrary {connection}
// URL segment cannot mint unbounded label values.
type OperationResolver interface {
	ResolveOperationID(ctx context.Context, connection, method, path string) string
	HasConnection(connection string) bool
}

// IdentityResolver maps a request's auth context to a display identity
// (API key name or OIDC subject) for metric labeling. Returns
// "unknown" when no identity can be resolved. Implemented in the
// platform package over the existing authenticator so this package
// imports no auth code.
type IdentityResolver interface {
	ResolveIdentity(ctx context.Context) string
}

const metricLabelUnknown = "unknown"

// invokeMeta carries the method and path parsed inside the invoke
// handler back up to the metrics middleware. A handler mutation of
// r.Context() is not visible to the caller, so the middleware installs
// a pointer the handler fills in. nil when the metrics middleware is
// not wrapping the handler (e.g. unit tests that mount the bare
// handler), in which case the handler's fill is a no-op.
type invokeMeta struct {
	method string
	path   string
}

type invokeMetaKey struct{}

func withInvokeMeta(ctx context.Context, m *invokeMeta) context.Context {
	return context.WithValue(ctx, invokeMetaKey{}, m)
}

func getInvokeMeta(ctx context.Context) *invokeMeta {
	m, _ := ctx.Value(invokeMetaKey{}).(*invokeMeta)
	return m
}

// statusRecorder captures the HTTP status code written by the wrapped
// handler. net/http defaults an unwritten status to 200, so status()
// returns 200 when WriteHeader was never called.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the first status code written and forwards it to
// the wrapped ResponseWriter.
func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) statusCode() int {
	if s.wroteHeader {
		return s.status
	}
	return http.StatusOK
}

// withMetrics wraps the invoke handler to record one inbound
// observation per request. When deps.Metrics is nil it returns next
// unwrapped so the disabled-metrics path has zero overhead. The
// connection is read from the path value inside the wrapper (the route
// pattern is registered on the wrapped handler so the value is present).
func withMetrics(next http.Handler, deps Deps) http.Handler {
	if deps.Metrics == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := &invokeMeta{}
		r = r.WithContext(withInvokeMeta(r.Context(), meta))
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()

		next.ServeHTTP(rec, r)

		deps.Metrics.RecordAPIGatewayInbound(r.Context(), observability.APIGatewayInboundAttrs{
			Connection:  connectionLabel(r, deps),
			OperationID: resolveOperation(r, deps, meta),
			Method:      methodLabel(meta),
			StatusClass: observability.HTTPStatusClass(rec.statusCode()),
			Identity:    resolveIdentity(r, deps),
		}, time.Since(start))
	})
}

// resolveOperation returns the operationId for the request, or
// "unknown" when no resolver is configured or the path matches no
// catalog operation. meta.method/path are empty for requests that
// failed to decode (400s), which correctly yields "unknown".
func resolveOperation(r *http.Request, deps Deps, meta *invokeMeta) string {
	if deps.Resolver == nil || meta.path == "" {
		return metricLabelUnknown
	}
	op := deps.Resolver.ResolveOperationID(r.Context(), r.PathValue("connection"), meta.method, meta.path)
	if op == "" {
		return metricLabelUnknown
	}
	return op
}

// resolveIdentity returns the caller's display identity, or "unknown".
// Identity is not on r.Context() at this layer (the handler only stashes
// the raw token), so the token context is rebuilt the same way the
// handler does before delegating to the resolver.
func resolveIdentity(r *http.Request, deps Deps) string {
	if deps.Identity == nil {
		return metricLabelUnknown
	}
	ctx := r.Context()
	if token := readRequestToken(r); token != "" {
		ctx = middleware.WithToken(ctx, token)
	}
	id := deps.Identity.ResolveIdentity(ctx)
	if id == "" {
		return metricLabelUnknown
	}
	return id
}

// connectionLabel clamps the {connection} URL segment to the registered
// connection set. An unregistered or unmatched segment (a 404, or a
// probe at an arbitrary path) records as "unknown" so a caller cannot
// mint unbounded connection label values. Falls back to "unknown" when
// no resolver is wired (the label would otherwise be the raw segment).
func connectionLabel(r *http.Request, deps Deps) string {
	name := r.PathValue("connection")
	if deps.Resolver == nil || !deps.Resolver.HasConnection(name) {
		return metricLabelUnknown
	}
	return name
}

// methodLabel clamps the caller-supplied method to the gateway's
// supported-method set (see apigateway.NormalizeMethodLabel); anything
// else, including an empty method from a request that failed to decode,
// records as "unknown".
func methodLabel(meta *invokeMeta) string {
	return apigatewaykit.NormalizeMethodLabel(meta.method)
}
