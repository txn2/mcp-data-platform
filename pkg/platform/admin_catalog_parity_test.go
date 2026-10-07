package platform

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/routescan"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// TestAdminCatalogRouteParity guards issue #697: every authenticated admin /
// portal REST route registered on the running server must be discoverable
// through the OpenAPI catalog that the platform-admin self-connection seeds and
// that api_discover serves. A served-but-undocumented endpoint makes an
// agent conclude a capability does not exist when it is present and authorized.
//
// The test enumerates the real route table by parsing every call in the source
// tree whose first argument folds to a "METHOD /api/v1/..." pattern -- folding
// covers the literal registrations and the base+"/leaf" ones alike, and reaches
// registrations made through a local helper rather than the mux directly. It
// then asserts each appears in the catalog content produced by
// adminSelfSpecContent (the exact OpenAPI document the self-connection upserts),
// enumerated through the same production parser api_discover uses. The two
// directions of drift it cannot tolerate:
//
//   - A new authenticated route added without swaggo annotations: it is
//     registered, served, and authorized, yet invisible to discovery.
//   - A stale entry in catalogParityExclusions: a route that was removed or
//     later documented but whose exclusion lingers.
//   - A registration whose pattern the scan cannot resolve: the route is
//     served and authorized but invisible to this gate, so it is neither
//     checked nor reported. That is how 39 routes reached a release
//     undocumented (#1741); it now fails instead of being skipped.
func TestAdminCatalogRouteParity(t *testing.T) {
	catalog := catalogOperationSet(t)
	registered := registeredAPIRoutes(t)

	// Every registered route, minus the documented exclusions, must be in the
	// catalog.
	var undocumented []string
	for route := range registered {
		if _, excluded := catalogParityExclusions[route]; excluded {
			continue
		}
		if !catalog[normalizeRouteParams(route)] {
			undocumented = append(undocumented, route.method+" "+route.path)
		}
	}
	if len(undocumented) > 0 {
		sort.Strings(undocumented)
		t.Errorf("served-but-undocumented admin routes (#697): %d endpoint(s) are registered "+
			"but absent from the OpenAPI catalog api_discover serves. Add swaggo "+
			"annotations and run `make swagger`, or, if the route is genuinely not part of "+
			"the operable admin surface, add it to catalogParityExclusions with a rationale:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}

	// Keep the exclusion list honest: an exclusion that no longer matches a
	// registered route is stale and must be removed so the gate cannot silently
	// rot into hiding a real route behind a dead entry.
	for route := range catalogParityExclusions {
		if !registered[route] {
			t.Errorf("stale catalogParityExclusions entry %q %q: no such route is registered; "+
				"remove the exclusion", route.method, route.path)
		}
	}
}

// routeKey is a (METHOD, path) pair with the /api/v1 base path stripped, matching
// how catalog operations are keyed (basePath becomes a server URL on the v2->v3
// conversion, leaving paths like /portal/knowledge-pages).
type routeKey struct {
	method string
	path   string
}

// catalogParityExclusions lists routes that are registered on an HTTP mux but
// deliberately absent from the admin OpenAPI catalog, each with the reason it is
// not part of the operable, authenticated admin surface api_discover
// exposes. Keep this set as small as the truth allows.
var catalogParityExclusions = map[routeKey]string{
	{method: "GET", path: "/admin/public/branding"}: "served on the unauthenticated publicMux to brand " +
		"the login screen before sign-in; not part of the authenticated, identity-passthrough admin surface.",
	{method: "GET", path: "/observability/query"}: "raw Prometheus PromQL proxy passthrough; its request/response " +
		"contract is defined by upstream Prometheus and it backs the observability dashboards, not a first-class admin REST operation.",
	{method: "GET", path: "/observability/query_range"}: "raw Prometheus PromQL range-query proxy passthrough; " +
		"contract defined by upstream Prometheus, backs the observability dashboards, not a first-class admin REST operation.",
}

// catalogOperationSet parses the catalog content the self-connection seeds and
// returns the set of (METHOD, path) operations with path parameters normalized.
// It enumerates through the production parser (apigatewaycatalog.ParseSpec +
// PathItem.Operations) so the test measures exactly the operation surface
// api_discover walks, not a parallel hand-rolled reading of the JSON.
func catalogOperationSet(t *testing.T) map[routeKey]bool {
	t.Helper()
	content, err := adminSelfSpecContent()
	if err != nil {
		t.Fatalf("building admin spec content: %v", err)
	}
	doc, err := apigatewaycatalog.ParseSpec(content)
	if err != nil {
		t.Fatalf("parsing catalog content: %v", err)
	}
	if doc.Paths == nil {
		t.Fatal("catalog produced no paths; the embedded OpenAPI document is empty or unparsable")
	}
	ops := make(map[routeKey]bool)
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			ops[routeKey{method: strings.ToUpper(method), path: normalizePath(path)}] = true
		}
	}
	if len(ops) == 0 {
		t.Fatal("catalog produced zero operations; the embedded OpenAPI document is empty or unparsable")
	}
	return ops
}

// registeredAPIRoutes reads the route table out of the source tree
// (internal/routescan) and returns every method-prefixed /api/v1 pattern as a
// routeKey with the base path stripped. Both Handle and HandleFunc
// registrations are read, because per-route middleware (e.g. the gateway's
// withMetrics wrapper) is wired with mux.Handle, and such a route is just as
// served and authorized as a HandleFunc one. A registration the scan cannot
// fold fails here: the route is served and authorized but invisible to this
// gate, so it would be neither checked nor reported (#1741).
func registeredAPIRoutes(t *testing.T) map[routeKey]bool {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	// Scan every directory that can register HTTP routes, not just pkg/, so a
	// route added under internal/ cannot escape the gate by location.
	res, err := routescan.Scan(filepath.Join(repoRoot, "pkg"), filepath.Join(repoRoot, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pos := range res.Unresolved {
		t.Errorf("unresolvable route pattern at %s: the parity gate reads registration "+
			"patterns statically, and this one is built from a value it cannot fold. "+
			"Register the route with a literal \"METHOD /api/v1/...\" pattern, or "+
			"assemble it from package-level string constants or from a parameter whose "+
			"call sites pass literals, so the route cannot be served without being checked.", pos)
	}
	routes := make(map[routeKey]bool)
	for _, pattern := range res.Patterns() {
		if key, isRoute := parseRoutePattern(pattern); isRoute {
			routes[key] = true
		}
	}
	if len(routes) == 0 {
		t.Fatal("found zero registered /api/v1 routes; the source scan is broken")
	}
	return routes
}

// parseRoutePattern splits a Go 1.22 ServeMux pattern ("METHOD /path") into a
// routeKey, returning ok=false unless it is a method-prefixed pattern under the
// /api/v1 base path. Patterns without a method, or outside /api/v1, are not part
// of the catalog surface.
func parseRoutePattern(pattern string) (routeKey, bool) {
	method, path, found := strings.Cut(pattern, " ")
	if !found {
		return routeKey{}, false
	}
	method = strings.TrimSpace(method)
	path = strings.TrimSpace(path)
	if method == "" || !strings.HasPrefix(path, "/api/v1/") {
		return routeKey{}, false
	}
	return routeKey{method: strings.ToUpper(method), path: strings.TrimPrefix(path, "/api/v1")}, true
}

// normalizeRouteParams returns a routeKey with path parameters normalized so a
// registered {id} matches the catalog's {id} regardless of the parameter name.
func normalizeRouteParams(r routeKey) routeKey {
	return routeKey{method: r.method, path: normalizePath(r.path)}
}

// normalizePath replaces every {param} segment with a placeholder so paths
// compare on shape, not on the (arbitrary) parameter name.
func normalizePath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			segments[i] = "{}"
		}
	}
	return strings.Join(segments, "/")
}
