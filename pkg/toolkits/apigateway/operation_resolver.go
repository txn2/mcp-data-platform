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
// the same full path api_discover reports. Resolution is
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

	upper := strings.ToUpper(strings.TrimSpace(method))
	normPath := ensureLeadingSlash(stripQueryAndFragment(path))

	// Standard resolution: the gorillamux router covers the
	// GET/POST/PUT/DELETE/PATCH/HEAD verbs on single-segment paths and
	// does most-specific-wins between literal and templated routes.
	if id := c.resolveViaRouter(upper, normPath); id != "" {
		return id
	}
	// WebDAV + nested-path fallback (issue #876). A router miss above can
	// mean the caller sent a real WebDAV verb (PROPFIND/MKCOL/MOVE/COPY),
	// which the catalog documents under a carrier method via
	// x-webdav-method and the router therefore has no route for, or a
	// nested resource path whose slash-bearing tail exceeds the router's
	// single-segment path variable. Both resolve here from the WebDAV
	// route index. Returns "" for connections with no WebDAV operations,
	// so a genuine miss still records "unknown".
	return c.resolveWebDAVOperation(upper, normPath)
}

// resolveViaRouter runs the gorillamux operationRouter for an
// already-normalized (upper-cased method, leading-slash, query-stripped
// path) request and returns the matched operationId, or "" on any miss.
// upper is the caller's real HTTP method; normPath is the runtime full
// path. Split out from ResolveOperationID so the WebDAV fallback path is
// a clean sibling rather than nested inside the router branch.
func (c *conn) resolveViaRouter(upper, normPath string) string {
	router := c.opRouter()
	if router == nil {
		return ""
	}
	req := &http.Request{
		Method: upper,
		URL:    &url.URL{Path: normPath},
	}
	route, _, err := router.FindRoute(req)
	if err != nil || route == nil || route.Operation == nil {
		return ""
	}
	if id := route.Operation.OperationID; id != "" {
		return id
	}
	// The matched operation declares no operationId. Synthesize the same
	// id api_discover advertises for it (appendItemOperations) so
	// the metric label agrees with the listed, invokable id instead of
	// falling through to "unknown". Only methods the catalog lists qualify:
	// the router also matches OPTIONS/TRACE/CONNECT, which pathItemMethods
	// omits, so synthesizing for them would invent a label no catalog entry
	// carries. rawPath is spec-relative, NOT route.Path (the
	// effectiveBasePath-prefixed router key), because that is what the list
	// side synthesizes from.
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
		c.operationWebDAVRoutes = buildWebDAVRoutes(c.specs)
	})
	return c.operationRouter
}

// webdavRoutes returns the connection's lazily-built WebDAV route index,
// building it (and the router) on first use. Empty for connections whose
// catalog declares no x-webdav-method operation. Discarded with the conn
// when ReloadConnection rebuilds it after a catalog edit, so no separate
// invalidation path is needed (issue #876).
func (c *conn) webdavRoutes() []webdavRoute {
	c.opRouter() // ensure the shared operationRouterOnce has populated the index
	return c.operationWebDAVRoutes
}

// rawPathForRoute maps a matched route's effectiveBasePath-prefixed
// Path back to the spec-relative raw path it was registered under, or
// "" when unknown. Used to synthesize the operationId for operations
// with no declared id, matching what api_discover advertises.
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
// declare no operationId, mirroring api_discover.
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

// ResolveOperationRequest rebuilds the concrete (method, path) a call
// addressed when it named an operation by id and passed its path
// template values separately. It is the inverse of ResolveOperationID:
// that one names an operation from a request, this one rebuilds the
// request from a recorded operation.
//
// It runs the same resolution api_invoke_endpoint performs, so the path
// a record reads back is the path the call took, base path and
// substitution included. ok is false when the connection is unknown,
// carries no catalog, resolves the id to nothing or to more than one
// operation, or has a placeholder with no value: the caller then keeps
// the operation id the call was addressed by.
//
// It is read by an asset's provenance, which holds the operation and
// the values a call passed but not the path template they went into
// (issue #1423).
func (t *Toolkit) ResolveOperationRequest(
	_ context.Context, connection, operationID, spec string, pathParams map[string]string,
) (method, path string, ok bool) {
	if operationID == "" {
		return "", "", false
	}
	t.mu.RLock()
	c := t.connections[connection]
	t.mu.RUnlock()
	if c == nil {
		return "", "", false
	}

	m, p, err := resolveOperationTarget(c, operationAddressing{
		OperationID: operationID,
		Spec:        spec,
		PathParams:  pathParams,
	})
	if err != nil {
		return "", "", false
	}
	return m, p, true
}
