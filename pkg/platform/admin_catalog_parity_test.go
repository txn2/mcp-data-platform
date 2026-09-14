package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

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
	scanRoutesUnder(t, fset, []string{
		filepath.Join(repoRoot, "pkg"),
		filepath.Join(repoRoot, "internal"),
	}, routes)
	if len(routes) == 0 {
		t.Fatal("found zero registered /api/v1 routes; the source scan is broken")
	}
	return routes
}

// scanRoutesUnder parses every non-test .go file under root and records each
// Handle/HandleFunc route pattern into routes.
func scanRoutesUnder(t *testing.T, fset *token.FileSet, roots []string, routes map[routeKey]bool) {
	t.Helper()
	// Parse per package directory: a pattern is routinely assembled from a
	// const declared in a sibling file of the same package, so constants have
	// to be collected across the whole directory before any call in it is
	// resolved.
	byDir := make(map[string][]*ast.File)
	for _, root := range roots {
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
			dir := filepath.Dir(path)
			byDir[dir] = append(byDir[dir], file)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	consts := make(map[string]map[string]string, len(byDir))
	for dir, files := range byDir {
		consts[dir] = collectStringConsts(files)
	}
	// A registrar's mount point is a parameter, so its patterns fold only once
	// the values its call sites pass are known. This has to span the whole
	// tree: the call site is in internal/httpserver and the registrar is in
	// the package it mounts.
	byParam := collectRegistrarPrefixes(byDir, consts)

	for dir, files := range byDir {
		for _, file := range files {
			scanFileRoutes(t, fset, file, scanInputs{consts: consts[dir], byParam: byParam, routes: routes})
		}
	}
}

// scanInputs carries what resolving one file's patterns needs: the package's
// string constants, the prefixes each registrar's parameters are called with,
// and the set the routes found are recorded into.
type scanInputs struct {
	consts  map[string]string
	byParam map[registrarParam][]string
	routes  map[routeKey]bool
}

// scanFileRoutes records every route one file registers, folding each
// enclosing function's string parameters to the values its call sites pass.
func scanFileRoutes(t *testing.T, fset *token.FileSet, file *ast.File, in scanInputs) {
	t.Helper()
	consts, byParam, routes := in.consts, in.byParam, in.routes
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		// Each combination of the enclosing function's prefix parameters is a
		// distinct mount, and every one of them is served.
		for _, bound := range bindings(consts, prefixValuesFor(fn, byParam)) {
			ast.Inspect(fn, func(inner ast.Node) bool {
				call, isCall := inner.(*ast.CallExpr)
				if !isCall || len(call.Args) == 0 {
					return true
				}
				// Parameter bindings apply only to an expression that is
				// already shaped like a route pattern, i.e. one carrying a
				// literal "METHOD " part. Without that discriminator, binding
				// a function's string parameters rewrites unrelated string
				// building in the same body -- SQL fragments assembled from a
				// column parameter, for one -- and the scan records routes
				// that do not exist.
				args := bound
				if !hasMethodLiteral(call.Args[0]) {
					args = consts
				}
				pattern, resolved := staticString(call.Args[0], args)
				if resolved {
					if key, isRoute := parseRoutePattern(pattern); isRoute {
						routes[key] = true
					}
					return true
				}
				// An unresolvable pattern on a real mux registration is the
				// failure mode this gate exists to prevent: the route is
				// served and authorized, the scan cannot see it, and so it is
				// neither checked nor reported. Refusing is the only answer
				// that cannot rot silently.
				if isMuxRegistration(call) && !isPrefixMount(call.Args[0], args) {
					t.Errorf("unresolvable route pattern at %s: the parity gate reads registration "+
						"patterns statically, and this one is built from a value it cannot fold. "+
						"Register the route with a literal \"METHOD /api/v1/...\" pattern, or "+
						"assemble it from package-level string constants or from a parameter whose "+
						"call sites pass literals, so the route cannot be served without being checked.",
						fset.Position(call.Pos()))
				}
				return true
			})
		}
		return true
	})
}

// hasMethodLiteral reports whether an expression contains a string literal
// beginning with an HTTP method and a space, which is what every Go 1.22
// ServeMux route pattern starts with and what distinguishes a route being
// assembled from any other string being assembled.
func hasMethodLiteral(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, m := range []string{"GET ", "POST ", "PUT ", "DELETE ", "PATCH ", "HEAD ", "OPTIONS "} {
			if strings.HasPrefix(v, m) {
				found = true
			}
		}
		return true
	})
	return found
}

// bindings returns one constant map per combination of parameter values, so a
// registrar mounted under two prefixes contributes both sets of routes. With
// no parameters it returns the package constants alone.
func bindings(consts map[string]string, params map[string][]string) []map[string]string {
	out := []map[string]string{consts}
	for name, values := range params {
		next := make([]map[string]string, 0, len(out)*len(values))
		for _, base := range out {
			for _, v := range values {
				bound := make(map[string]string, len(base)+1)
				maps.Copy(bound, base)
				bound[name] = v
				next = append(next, bound)
			}
		}
		out = next
	}
	return out
}

// isMuxRegistration reports whether the call is an http.ServeMux
// Handle/HandleFunc registration, the calls whose first argument must be a
// statically resolvable route pattern.
func isMuxRegistration(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc")
}

// adminPathPrefixDefault is the admin surface's default mount point, the value
// pkg/platform applyAdminDefaults writes when admin.path_prefix is unset.
// admin.path_prefix is operator-configurable, so a registrar mounted there has
// no single compile-time path; the spec documents those routes at the default
// (/admin/scripts and /admin/prompts/{id}/versions are in it today) because an
// override moves the whole surface uniformly and changes no route's shape.
const adminPathPrefixDefault = "/api/v1/admin"

// registrarParam identifies a callee's string parameter by the name the call
// site uses and the argument position, which is what a call site can be read
// for without resolving types.
//
// A registrar takes its mount point as a parameter, and the same one is
// commonly mounted more than once: attachhttp.Register serves the prompt
// attachment routes under both the admin prefix and /api/v1/portal, so a scan
// that resolved its parameter to a single value would see one mount of two and
// report the gate green over a surface it had not looked at. Collecting the
// call sites is what makes both mounts visible.
type registrarParam struct {
	callee string
	index  int
}

// collectRegistrarPrefixes walks every file and records, for each call, the
// path prefixes passed in each argument position. Only /api/v1 values are
// kept: a registrar's mount point is the one string parameter this gate can
// act on, and restricting to that shape keeps an unrelated same-named callee
// from contributing values.
func collectRegistrarPrefixes(all map[string][]*ast.File, consts map[string]map[string]string) map[registrarParam][]string {
	seen := make(map[registrarParam]map[string]bool)
	for dir, files := range all {
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := calleeName(call)
				if name == "" {
					return true
				}
				for i, arg := range call.Args {
					v, ok := staticString(arg, consts[dir])
					if !ok || !strings.HasPrefix(v, "/api/v1") || strings.Contains(v, " ") {
						continue
					}
					key := registrarParam{callee: name, index: i}
					if seen[key] == nil {
						seen[key] = map[string]bool{}
					}
					seen[key][v] = true
				}
				return true
			})
		}
	}
	out := make(map[registrarParam][]string, len(seen))
	for key, values := range seen {
		for v := range values {
			out[key] = append(out[key], v)
		}
		sort.Strings(out[key])
	}
	return out
}

// calleeName returns the identifier a call names, for both pkg.Fn() and
// receiver.Fn() forms, or "" when the callee is not a plain name.
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// prefixValuesFor returns every prefix a function's string parameters are
// called with, keyed by parameter name, plus the admin default for a parameter
// the call sites resolve to nothing (the configured admin prefix is a runtime
// value, not a literal any call site carries).
func prefixValuesFor(fn *ast.FuncDecl, byParam map[registrarParam][]string) map[string][]string {
	if fn == nil || fn.Type.Params == nil {
		return nil
	}
	out := map[string][]string{}
	index := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if isStringType(field.Type) {
				values := byParam[registrarParam{callee: fn.Name.Name, index: index}]
				if len(values) == 0 {
					values = []string{adminPathPrefixDefault}
				}
				out[name.Name] = values
			}
			index++
		}
	}
	return out
}

// isStringType reports whether a parameter is declared as a plain string.
func isStringType(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "string"
}

// isPrefixMount reports whether an unresolvable registration argument is one of
// the shapes that is not a method-prefixed route, and so is outside this gate:
//
//   - a subtree mount, registered as prefix+"/" with no method, which delegates
//     to a handler whose own routes are registered (and checked) elsewhere;
//   - a pattern forwarded through a local registration helper, whose literal
//     patterns are read at the helper's call sites instead.
//
// Anything else that cannot be folded is a real route the gate would otherwise
// skip silently, and is reported.
func isPrefixMount(arg ast.Expr, consts map[string]string) bool {
	if ident, ok := arg.(*ast.Ident); ok {
		// A bare identifier that is a function parameter forwarded from a
		// local helper: the real patterns are literals at its call sites.
		_, known := consts[ident.Name]
		return !known
	}
	if sel, ok := arg.(*ast.SelectorExpr); ok {
		// A constant exported by another package, used as a subtree mount
		// (pkg.PathPrefix). Cross-package constants are outside this scan;
		// the routes under such a mount are registered by the handler it
		// delegates to and are checked there.
		_ = sel
		return true
	}
	bin, ok := arg.(*ast.BinaryExpr)
	if !ok || bin.Op != token.ADD {
		return false
	}
	// prefix+"/" and friends: a trailing-slash subtree mount carries no method.
	tail, ok := staticString(bin.Y, consts)
	return ok && !strings.Contains(tail, " ") && strings.HasSuffix(tail, "/")
}

// collectStringConsts returns every package-level and function-level string
// constant (and string variable initialized from one) declared in the package,
// keyed by name. Route patterns are habitually assembled as base+"/leaf", so
// without this the scan sees a fraction of the real route table.
func collectStringConsts(files []*ast.File) map[string]string {
	consts := make(map[string]string)
	// Two passes: a constant may be defined in terms of another declared
	// later in the package, and declaration order across files is arbitrary.
	for range 2 {
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				for i, name := range spec.Names {
					if i >= len(spec.Values) {
						continue
					}
					if v, ok := staticString(spec.Values[i], consts); ok {
						consts[name.Name] = v
					}
				}
				return true
			})
		}
	}
	return consts
}

// staticString folds an expression to its string value when it is built
// entirely from string literals and known constants joined with +, which is
// how every route pattern in the tree is written. ok=false for anything whose
// value is not fixed at compile time.
func staticString(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.ParenExpr:
		return staticString(v.X, consts)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, ok := staticString(v.X, consts)
		if !ok {
			return "", false
		}
		right, ok := staticString(v.Y, consts)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
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
