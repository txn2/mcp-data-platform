package sitegw

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// What a request the gateway answered is counted as (#2597).
const (
	kindSite  = "site"
	kindPages = "pages"

	attrSiteKind = "site.kind"
	attrSiteName = "site.name"
	attrSiteEnv  = "site.environment"
)

// observedWriter counts one request answered for a resolved route: its status
// and how long it took, under the route's kind, name and environment, never
// anything the request carried.
type observedWriter struct {
	http.ResponseWriter
	g               *Gateway
	r               *http.Request
	kind, name, env string
	start           time.Time
	status          int
}

// observe names the request on its server span and returns the writer that
// counts it; done records it.
func (g *Gateway) observe(w http.ResponseWriter, r *http.Request, route *site.Route) *observedWriter {
	o := &observedWriter{ResponseWriter: w, g: g, r: r, kind: kindSite, name: route.Site, env: route.Environment, start: time.Now()}
	if route.Application != "" {
		httpobs.SetRoute(r, httpobs.RoutePages)
		o.kind, o.name, o.env = kindPages, route.Application, ""
	}
	trace.SpanFromContext(r.Context()).SetAttributes(
		attribute.String(attrSiteKind, o.kind), attribute.String(attrSiteName, o.name), attribute.String(attrSiteEnv, o.env))
	return o
}

// WriteHeader records the status before passing it on.
func (o *observedWriter) WriteHeader(code int) {
	if o.status == 0 {
		o.status = code
	}
	o.ResponseWriter.WriteHeader(code)
}

// Write records an implicit 200 before passing the body on.
func (o *observedWriter) Write(b []byte) (int, error) {
	if o.status == 0 {
		o.status = http.StatusOK
	}
	return o.ResponseWriter.Write(b) //nolint:wrapcheck // the writer's own error, passed through
}

// Flush passes through, so a page origin's streamed answer still streams.
func (o *observedWriter) Flush() {
	_ = http.NewResponseController(o.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (o *observedWriter) Unwrap() http.ResponseWriter { return o.ResponseWriter }

func (o *observedWriter) done() {
	status := o.status
	if status == 0 {
		status = http.StatusOK
	}
	o.g.deps.Metrics.RecordSiteRequest(o.r.Context(), o.kind, o.name, o.env,
		observability.HTTPStatusClass(status), time.Since(o.start))
}
