package httpobs

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// DefaultSlowThreshold is how long a request may take before it is logged as
// slow when server.slow_request_threshold is unset. The request that opened
// #1889 took 12 to 30 seconds and left no record of itself.
const DefaultSlowThreshold = 5 * time.Second

// The HTTP semantic convention keys the server span carries. http.route is
// the template's path alone, as the convention wants it; the metric's route
// label keeps the mux pattern whole, method included, so a query on either
// reads one registration.
const (
	spanAttrMethod     = "http.request.method"
	spanAttrRoute      = "http.route"
	spanAttrStatusCode = "http.response.status_code"
	spanAttrErrorType  = "error.type"
)

// Config is what the middleware records with.
type Config struct {
	// Mux is the mux the wrapped handler serves from. It resolves the
	// template of a request no handler reported one for: a CORS preflight
	// the CORS layer answers before the mux runs. Nil for a listener with a
	// single handler, which names its route in Route.
	Mux *http.ServeMux
	// Route is the fixed template of a listener that serves one handler
	// with no mux (the webhook receiver's own listener, "/hooks/").
	Route string
	// Metrics receives the duration and the 429 count. Nil records nothing.
	Metrics *observability.Metrics
	// SlowThreshold is the duration past which a request is logged at WARN;
	// zero means DefaultSlowThreshold. A GET that opened an event stream is
	// never slow: its duration is how long the client listened. A POST
	// answered as an event stream (a streamable MCP message) is a request
	// and is.
	SlowThreshold time.Duration
}

func (c Config) slowThreshold() time.Duration {
	if c.SlowThreshold <= 0 {
		return DefaultSlowThreshold
	}
	return c.SlowThreshold
}

// Middleware is the outermost layer of a listener: it continues the caller's
// W3C trace context, opens the server span, installs the scope the handlers
// below report their template and refusals into, serves, and records what
// happened under the template. It wraps the CORS layer so a preflight is
// counted too.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	tracer := otel.Tracer(observability.InstrumentationScope)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			prop := otel.GetTextMapPropagator()
			ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, observability.HTTPMethodLabel(r.Method), trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			s := &scope{}
			r = r.WithContext(withScope(ctx, s))
			// The MCP transport runs a message's handler on its own context,
			// not the request's, and reads the trace to continue off the
			// request headers (mcpobs.TraceContext). Written here, the
			// server span is what it continues, so a tools/call nests under
			// the HTTP request that carried it; the caller's own traceparent
			// was read above and is this span's parent.
			if span.SpanContext().IsValid() {
				prop.Inject(ctx, propagation.HeaderCarrier(r.Header))
			}
			rw := &responseWriter{ResponseWriter: w}
			start := time.Now()
			next.ServeHTTP(rw, r)
			d := time.Since(start)

			route := s.routeFor(r, cfg)
			status := rw.statusCode()
			finishSpan(span, r.Method, route, status)
			cfg.Metrics.RecordHTTPServerRequest(ctx, route, r.Method, observability.HTTPStatusClass(status), d)
			if status == http.StatusTooManyRequests {
				cfg.Metrics.RecordHTTPRateLimited(ctx, s.limiterOr(LimiterUnknown))
			}
			if d >= cfg.slowThreshold() && !rw.isOpenStream(r.Method) {
				slog.WarnContext(ctx, "slow HTTP request",
					"route", logsan.SanitizeForLog(route),
					"method", observability.HTTPMethodLabel(r.Method),
					"status", status,
					"duration_ms", d.Milliseconds())
			}
		})
	}
}

// routeFor is the template the request is recorded under: what a nested mux
// reported through Routed, else the pattern the mux wrote onto the request
// it served (the innermost mux that saw this request, since a clone keeps
// its own), else the pattern the listener's mux would match (a preflight the
// CORS layer answered), else the listener's fixed route, else unmatched.
func (s *scope) routeFor(r *http.Request, cfg Config) string {
	switch {
	case s.route != "":
		return s.route
	case r.Pattern != "" && r.Method != http.MethodConnect:
		return r.Pattern
	}
	if pattern := resolvePattern(cfg.Mux, r); pattern != "" {
		return pattern
	}
	if cfg.Route != "" {
		return cfg.Route
	}
	return RouteUnmatched
}

func (s *scope) limiterOr(fallback string) string {
	if s.limiter == "" {
		return fallback
	}
	return s.limiter
}

// finishSpan names the span "{method} {route path}" as the HTTP semantic
// conventions say, or the method alone for an unmatched request, and sets
// the response attributes. A server span is in error on a 5xx only: a 4xx
// is the caller's.
func finishSpan(span trace.Span, method, route string, status int) {
	m := observability.HTTPMethodLabel(method)
	path := routePath(route)
	attrs := []attribute.KeyValue{
		attribute.String(spanAttrMethod, m),
		attribute.Int(spanAttrStatusCode, status),
	}
	if path == "" {
		span.SetName(m)
	} else {
		span.SetName(m + " " + path)
		attrs = append(attrs, attribute.String(spanAttrRoute, path))
	}
	if status >= http.StatusInternalServerError {
		attrs = append(attrs, attribute.String(spanAttrErrorType, strconv.Itoa(status)))
	}
	span.SetAttributes(attrs...)
	observability.SetSpanStatus(span, serverStatusCategory(status), nil)
}

// routePath is the path part of a mux pattern: "GET /x/{id}" is "/x/{id}",
// "/x/" is itself, and the unmatched label is nothing.
func routePath(route string) string {
	if route == RouteUnmatched {
		return ""
	}
	if _, path, ok := strings.Cut(route, " "); ok {
		return path
	}
	return route
}

// serverStatusCategory is the status a server span carries: ok below 500,
// server_err from there, which is what the span's status code follows.
func serverStatusCategory(status int) string {
	if status >= http.StatusInternalServerError {
		return observability.StatusServerErr
	}
	return observability.StatusOK
}
