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
		URL:    &url.URL{Path: ensureLeadingSlash(stripQueryAndFragment(path))},
	}
	route, _, err := router.FindRoute(req)
	if err != nil || route == nil || route.Operation == nil {
		return ""
	}
	if id := route.Operation.OperationID; id != "" {
		return id
	}
	// The matched operation declares no operationId. Synthesize the same
	// id api_list_endpoints advertises for it (appendItemOperations) so
	// the metric label agrees with the listed, invokable id instead of
	// falling through to "unknown". Only methods the catalog lists qualify:
	// the router also matches OPTIONS/TRACE/CONNECT, which pathItemMethods
	// omits, so synthesizing for them would invent a label no catalog entry
	// carries. rawPath is spec-relative, NOT route.Path (the
	// effectiveBasePath-prefixed router key), because that is what the list
	// side synthesizes from.
	upper := strings.ToUpper(method)
	if !listableMethod(upper) {
		return ""
	}
	if raw := c.rawPathForRoute(route.Path); raw != "" {
		return synthesizedOperationID(upper, raw)
	}
	return ""
}

// stripQueryAndFragment removes a "?query" and/or "#fragment" suffix
// from a runtime path, leaving only the path component the router
// matches on. A collection endpoint invoked with query parameters
// (e.g. /v1/orders?limit=100) must still resolve to its operation;
// leaving the query string in url.URL.Path makes the router try to
// match it as part of the path and fall through to "" (#519).
func stripQueryAndFragment(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		return p[:i]
	}
	return p
}

// opRouter returns the connection's lazily-built path-matching router,
// or nil when the connection has no usable spec paths. The companion
// rawByKey map (effectiveBasePath-prefixed router key -> spec-relative
// raw path) is built in the same pass so operationId synthesis can
// recover the raw path a matched route came from.
func (c *conn) opRouter() routers.Router {
	c.operationRouterOnce.Do(func() {
		c.operationRouter, c.operationRawPaths = buildOperationRouter(c.specs)
	})
	return c.operationRouter
}

// rawPathForRoute maps a matched route's effectiveBasePath-prefixed
// Path back to the spec-relative raw path it was registered under, or
// "" when unknown. Used to synthesize the operationId for operations
// with no declared id, matching what api_list_endpoints advertises.
func (c *conn) rawPathForRoute(routePath string) string {
	return c.operationRawPaths[routePath]
}

// buildOperationRouter assembles a single gorillamux router covering
// every operation across the connection's component specs. Each spec's
// paths are rebased to effectiveBasePath + rawPath (the runtime full
// path) and the server is pinned to "/" so matching is path-only and
// host-independent. Returns nil when no spec contributes any path.
//
// The second return value maps each router path key
// (effectiveBasePath+rawPath) back to its spec-relative rawPath so the
// resolver can synthesize "<METHOD> <rawPath>" ids for operations that
// declare no operationId, mirroring api_list_endpoints.
func buildOperationRouter(specs map[string]*specState) (router routers.Router, rawByKey map[string]string) {
	paths := openapi3.NewPaths()
	rawByKey = make(map[string]string)
	count := 0
	for _, st := range specs {
		if st == nil || st.doc == nil || st.doc.Paths == nil {
			continue
		}
		for rawPath, item := range st.doc.Paths.Map() {
			key := st.effectiveBasePath + rawPath
			paths.Set(key, item)
			rawByKey[key] = rawPath
			count++
		}
	}
	if count == 0 {
		return nil, nil
	}

	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info:    &openapi3.Info{Title: "apigateway-operation-resolver", Version: "0"},
		Servers: openapi3.Servers{{URL: "/"}},
		Paths:   paths,
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, nil
	}
	return router, rawByKey
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
