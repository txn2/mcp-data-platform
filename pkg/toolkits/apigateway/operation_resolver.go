package apigateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// ResolveOperationID maps an inbound (method, runtime path) on a
// connection to the OpenAPI operationId declared in that connection's
// catalog. It returns "" when the connection is unknown, has no catalog
// (no specs), the request matches no spec path, or the matched
// operation has no operationId. Callers map "" to "unknown" for the
// metrics label.
//
// The runtime path is the full path the caller passes to
// api_invoke_endpoint (already includes the connection's effective base
// path), so matching is done against effectiveBasePath + spec rawPath,
// the same full path api_list_endpoints reports. Resolution is
// path-template aware: /v1/users/123 matches a /v1/users/{id} operation.
//
// The per-connection router is built lazily on first call and reused;
// it is discarded when ReloadConnection rebuilds the conn after a
// catalog edit, so resolution always reflects the live spec set.
func (t *Toolkit) ResolveOperationID(_ context.Context, connection, method, path string) string {
	t.mu.RLock()
	c := t.connections[connection]
	t.mu.RUnlock()
	if c == nil {
		return ""
	}

	router := c.opRouter()
	if router == nil {
		return ""
	}

	req := &http.Request{
		Method: strings.ToUpper(method),
		URL:    &url.URL{Path: ensureLeadingSlash(path)},
	}
	route, _, err := router.FindRoute(req)
	if err != nil || route == nil || route.Operation == nil {
		return ""
	}
	return route.Operation.OperationID
}

// opRouter returns the connection's lazily-built path-matching router,
// or nil when the connection has no usable spec paths.
func (c *conn) opRouter() routers.Router {
	c.operationRouterOnce.Do(func() {
		c.operationRouter = buildOperationRouter(c.specs)
	})
	return c.operationRouter
}

// buildOperationRouter assembles a single gorillamux router covering
// every operation across the connection's component specs. Each spec's
// paths are rebased to effectiveBasePath + rawPath (the runtime full
// path) and the server is pinned to "/" so matching is path-only and
// host-independent. Returns nil when no spec contributes any path.
func buildOperationRouter(specs map[string]*specState) routers.Router {
	paths := openapi3.NewPaths()
	count := 0
	for _, st := range specs {
		if st == nil || st.doc == nil || st.doc.Paths == nil {
			continue
		}
		for rawPath, item := range st.doc.Paths.Map() {
			paths.Set(st.effectiveBasePath+rawPath, item)
			count++
		}
	}
	if count == 0 {
		return nil
	}

	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info:    &openapi3.Info{Title: "apigateway-operation-resolver", Version: "0"},
		Servers: openapi3.Servers{{URL: "/"}},
		Paths:   paths,
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil
	}
	return router
}

// ensureLeadingSlash normalizes a runtime path so the router (which
// matches absolute paths) sees a leading slash. An empty path becomes
// "/".
func ensureLeadingSlash(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		return "/" + p
	}
	return p
}
