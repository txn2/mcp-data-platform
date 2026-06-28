package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// TestAdminCatalogRouteParity guards issue #697: every authenticated admin /
// portal REST route registered on the running server must be discoverable
// through the OpenAPI catalog that the platform-admin self-connection seeds and
// that api_list_endpoints serves. A served-but-undocumented endpoint makes an
// agent conclude a capability does not exist when it is present and authorized.
//
// The test enumerates the real route table by parsing every
// `*.Handle`/`*.HandleFunc("METHOD /api/v1/...")` registration in the source
// tree, then asserts each appears in the catalog content produced by
// adminSelfSpecContent (the exact OpenAPI document the self-connection upserts),
// enumerated through the same production parser api_list_endpoints uses. The two
// directions of drift it cannot tolerate:
//
//   - A new authenticated route added without swaggo annotations: it is
//     registered, served, and authorized, yet invisible to discovery.
//   - A stale entry in catalogParityExclusions: a route that was removed or
//     later documented but whose exclusion lingers.
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
			"but absent from the OpenAPI catalog api_list_endpoints serves. Add swaggo "+
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
// not part of the operable, authenticated admin surface api_list_endpoints
// exposes. Keep this set as small as the truth allows.
var catalogParityExclusions = map[routeKey]string{
	{method: "GET", path: "/admin/public/branding"}: "served on the unauthenticated publicMux to brand " +
		"the login screen before sign-in; not part of the authenticated, identity-passthrough admin surface.",
	{method: "GET", path: "/observability/query"}: "raw Prometheus PromQL proxy passthrough; its request/response " +
		"contract is defined by upstream Prometheus and it backs the observability dashboards, not a first-class admin REST operation.",
	{method: "GET", path: "/observability/query_range"}: "raw Prometheus PromQL range-query proxy passthrough; " +
		"contract defined by upstream Prometheus, backs the observability dashboards, not a first-class admin REST operation.",
	{method: "POST", path: "/gateway/{connection}/invoke"}: "generic API-gateway data-plane proxy: forwards an " +
		"arbitrary request to a configured upstream connection. Its contract is the upstream's OpenAPI spec (surfaced " +
		"per connection via api_list_endpoints), not the platform's own admin control-plane catalog.",
	{method: "POST", path: "/gateway/{connection}/invoke-raw"}: "generic API-gateway data-plane proxy that streams " +
		"the upstream body unbuffered (#535); same rationale as /gateway/{connection}/invoke.",
}

// catalogOperationSet parses the catalog content the self-connection seeds and
// returns the set of (METHOD, path) operations with path parameters normalized.
// It enumerates through the production parser (apigatewaycatalog.ParseSpec +
// PathItem.Operations) so the test measures exactly the operation surface
// api_list_endpoints walks, not a parallel hand-rolled reading of the JSON.
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

// registeredAPIRoutes walks the source tree and returns every
// `*.Handle`/`*.HandleFunc("METHOD /api/v1/...")` registration as a routeKey
// with the /api/v1 base path stripped. Both registration styles are scanned
// because per-route middleware (e.g. the gateway's withMetrics wrapper) is wired
// with mux.Handle, and such a route is just as served and authorized as a
// HandleFunc one. Using the AST (not a text scan) means commented-out or
// string-fragment matches cannot leak in.
func registeredAPIRoutes(t *testing.T) map[routeKey]bool {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	routes := make(map[routeKey]bool)
	fset := token.NewFileSet()
	// Scan every directory that can register HTTP routes, not just pkg/, so a
	// route added under internal/ cannot escape the gate by location.
	for _, dir := range []string{"pkg", "internal"} {
		scanRoutesUnder(t, fset, filepath.Join(repoRoot, dir), routes)
	}
	if len(routes) == 0 {
		t.Fatal("found zero registered /api/v1 routes; the source scan is broken")
	}
	return routes
}

// scanRoutesUnder parses every non-test .go file under root and records each
// Handle/HandleFunc route pattern into routes.
func scanRoutesUnder(t *testing.T, fset *token.FileSet, root string, routes map[routeKey]bool) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if key, ok := routeFromCall(n); ok {
				routes[key] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

// routeFromCall extracts a routeKey from an AST node when it is a
// Handle/HandleFunc call whose first argument is a "METHOD /api/v1/..." string
// literal; ok=false otherwise.
func routeFromCall(n ast.Node) (routeKey, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return routeKey{}, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") || len(call.Args) == 0 {
		return routeKey{}, false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return routeKey{}, false
	}
	return parseRoutePattern(strings.Trim(lit.Value, "`\""))
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
