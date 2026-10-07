// Package routescan reads the platform's HTTP route table out of its source:
// every pattern a Go 1.22 ServeMux registration (Handle, HandleFunc) is given,
// folded from string literals, package constants and the mount prefixes a
// registrar's call sites pass. Two gates read it: the admin catalog parity
// test (every authenticated route is discoverable through the OpenAPI
// catalog, #697) and the inbound request observer's gate (every route reports
// its own template, #1889). The two must enumerate the same table, which is
// why the scan is one package rather than two test files.
//
// A registration whose pattern the scan cannot fold is reported, not skipped:
// a route the scan cannot see is served and authorized without being checked,
// which is how 39 routes reached a release undocumented (#1741).
package routescan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Route is one registered pattern and where it is registered.
type Route struct {
	// Pattern is the ServeMux pattern as registered: "GET /api/v1/x/{id}",
	// or "/api/v1/admin/" for a subtree mount with no method.
	Pattern string
	// Pos is the file:line of the registration, for a message.
	Pos string
}

// Result is what a scan found.
type Result struct {
	// Routes are the resolved patterns, sorted and deduplicated.
	Routes []Route
	// Unresolved are the positions of mux registrations whose pattern is
	// built from a value the scan cannot fold. A caller fails on them.
	Unresolved []string
}

// Patterns are the resolved patterns alone.
func (r Result) Patterns() []string {
	out := make([]string, 0, len(r.Routes))
	for _, rt := range r.Routes {
		out = append(out, rt.Pattern)
	}
	return out
}

// Scan parses every non-test .go file under roots and returns the route table
// they register.
func Scan(roots ...string) (Result, error) {
	fset := token.NewFileSet()
	byDir, err := parseTree(fset, roots)
	if err != nil {
		return Result{}, err
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

	found := map[string]Route{}
	var res Result
	for dir, files := range byDir {
		for _, file := range files {
			scanFileRoutes(fset, file, scanInputs{consts: consts[dir], byParam: byParam}, found, &res.Unresolved)
		}
	}
	for _, rt := range found {
		res.Routes = append(res.Routes, rt)
	}
	sort.Slice(res.Routes, func(i, j int) bool { return res.Routes[i].Pattern < res.Routes[j].Pattern })
	sort.Strings(res.Unresolved)
	return res, nil
}

// parseTree parses every non-test .go file under roots, grouped by package
// directory: a pattern is routinely assembled from a const declared in a
// sibling file of the same package, so constants have to be collected across
// the whole directory before any call in it is resolved.
func parseTree(fset *token.FileSet, roots []string) (map[string][]*ast.File, error) {
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
				return fmt.Errorf("parsing %s: %w", path, perr)
			}
			dir := filepath.Dir(path)
			byDir[dir] = append(byDir[dir], file)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", root, err)
		}
	}
	return byDir, nil
}

// scanInputs carries what resolving one file's patterns needs: the package's
// string constants and the prefixes each registrar's parameters are called with.
type scanInputs struct {
	consts  map[string]string
	byParam map[registrarParam][]string
}

// scanFileRoutes records every route one file registers, folding each
// enclosing function's string parameters to the values its call sites pass.
func scanFileRoutes(fset *token.FileSet, file *ast.File, in scanInputs, found map[string]Route, unresolved *[]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		// Each combination of the enclosing function's prefix parameters is a
		// distinct mount, and every one of them is served.
		for _, bound := range bindings(in.consts, prefixValuesFor(fn, in.byParam)) {
			ast.Inspect(fn, func(inner ast.Node) bool {
				call, isCall := inner.(*ast.CallExpr)
				if !isCall || len(call.Args) == 0 {
					return true
				}
				recordRegistration(fset, call, bound, in.consts, sinks{found: found, unresolved: unresolved})
				return true
			})
		}
		return true
	})
}

// recordRegistration folds one call's first argument as a route pattern. A
// method pattern is read off any call, because a registration helper's call
// sites carry the literal the helper forwards to the mux; a pattern with no
// method is read off a mux registration alone, so a path compared or logged
// elsewhere is not mistaken for a route. Parameter bindings apply only to an
// expression that is already shaped like a route pattern, i.e. one carrying a
// literal "METHOD " part, or that is a subtree mount built from a parameter:
// without that discriminator, binding a function's string parameters
// rewrites unrelated string building in the same body.
// sinks are where a registration lands: the resolved routes, and the
// positions of the ones that could not be folded.
type sinks struct {
	found      map[string]Route
	unresolved *[]string
}

func recordRegistration(fset *token.FileSet, call *ast.CallExpr, bound, consts map[string]string, to sinks) {
	arg := call.Args[0]
	args := bound
	if !hasMethodLiteral(arg) && !isPrefixMount(arg, consts) {
		args = consts
	}
	pattern, resolved := staticString(arg, args)
	if resolved {
		if isMethodPattern(pattern) || (isMuxRegistration(call) && isRoutePattern(pattern)) {
			to.found[pattern] = Route{Pattern: pattern, Pos: fset.Position(call.Pos()).String()}
		}
		return
	}
	if !isMuxRegistration(call) {
		return
	}
	// A pattern forwarded through a local registration helper (a bare
	// parameter the helper's call sites fill with literals) is read at those
	// call sites; a cross-package constant used as a subtree mount is the
	// mounted handler's to register. Anything else that cannot be folded is a
	// real route the gates would otherwise skip silently.
	if isForwardedPattern(arg, consts) {
		return
	}
	*to.unresolved = append(*to.unresolved, fset.Position(call.Pos()).String())
}

// isRoutePattern reports whether a folded string is a ServeMux pattern: a
// path, or a method and a path.
func isRoutePattern(s string) bool {
	return strings.HasPrefix(s, "/") || isMethodPattern(s)
}

// isMethodPattern reports whether a folded string is a method pattern,
// "METHOD /path", which only a route is ever written as.
func isMethodPattern(s string) bool {
	method, path, ok := strings.Cut(s, " ")
	if !ok || !strings.HasPrefix(path, "/") {
		return false
	}
	return slices.Contains([]string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}, method)
}

// hasMethodLiteral reports whether an expression contains a string literal
// beginning with an HTTP method and a space, which is what every Go 1.22
// ServeMux method pattern starts with and what distinguishes a route being
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
		next := make([]map[string]string, 0, len(out))
		for _, base := range out {
			for _, v := range values {
				bound := make(map[string]string, len(base))
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

// AdminPathPrefixDefault is the admin surface's default mount point, the value
// pkg/platform writes when admin.path_prefix is unset. admin.path_prefix is
// operator-configurable, so a registrar mounted there has no single
// compile-time path; the scan reads those routes at the default because an
// override moves the whole surface uniformly and changes no route's shape.
const AdminPathPrefixDefault = "/api/v1/admin"

// registrarParam identifies a callee's string parameter by the name the call
// site uses and the argument position, which is what a call site can be read
// for without resolving types.
//
// A registrar takes its mount point as a parameter, and the same one is
// commonly mounted more than once: attachhttp.Register serves the prompt
// attachment routes under both the admin prefix and /api/v1/portal, so a scan
// that resolved its parameter to a single value would see one mount of two.
type registrarParam struct {
	callee string
	index  int
}

// collectRegistrarPrefixes walks every file and records, for each call, the
// path prefixes passed in each argument position. Only /api/v1 values are
// kept: a registrar's mount point is the one string parameter this scan can
// act on, and restricting to that shape keeps an unrelated same-named callee
// from contributing values.
func collectRegistrarPrefixes(all map[string][]*ast.File, consts map[string]map[string]string) map[registrarParam][]string {
	seen := make(map[registrarParam]map[string]bool)
	for dir, files := range all {
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					recordPrefixArgs(call, consts[dir], seen)
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

// recordPrefixArgs records each /api/v1 prefix one call passes, by callee
// name and argument position.
func recordPrefixArgs(call *ast.CallExpr, consts map[string]string, seen map[registrarParam]map[string]bool) {
	name := calleeName(call)
	if name == "" {
		return
	}
	for i, arg := range call.Args {
		v, ok := staticString(arg, consts)
		if !ok || !strings.HasPrefix(v, "/api/v1") || strings.Contains(v, " ") {
			continue
		}
		key := registrarParam{callee: name, index: i}
		if seen[key] == nil {
			seen[key] = map[string]bool{}
		}
		seen[key][v] = true
	}
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
					values = []string{AdminPathPrefixDefault}
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

// isPrefixMount reports whether a registration argument is a subtree mount
// built from a parameter or constant, prefix+"/" with no method, which
// delegates to a handler whose own routes are registered (and read) elsewhere.
func isPrefixMount(arg ast.Expr, consts map[string]string) bool {
	bin, ok := arg.(*ast.BinaryExpr)
	if !ok || bin.Op != token.ADD {
		return false
	}
	tail, ok := staticString(bin.Y, consts)
	return ok && !strings.Contains(tail, " ") && strings.HasSuffix(tail, "/")
}

// isForwardedPattern reports whether an unresolvable registration argument is
// one of the shapes that is read somewhere else and so is not a failure:
//
//   - a bare identifier that is a function parameter forwarded from a local
//     registration helper: the real patterns are literals at its call sites;
//   - a value from another package (pkg.PathPrefix as a subtree mount, a
//     route table's field): the routes under such a mount are registered by
//     the handler it delegates to, and a route table is the owning package's
//     to list.
func isForwardedPattern(arg ast.Expr, consts map[string]string) bool {
	switch v := arg.(type) {
	case *ast.Ident:
		_, known := consts[v.Name]
		return !known
	case *ast.SelectorExpr:
		return true
	case *ast.BinaryExpr:
		return isPrefixMount(arg, consts)
	}
	return false
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
				if spec, ok := n.(*ast.ValueSpec); ok {
					recordStringSpec(spec, consts)
				}
				return true
			})
		}
	}
	return consts
}

// recordStringSpec records every name in one declaration whose value folds
// to a string.
func recordStringSpec(spec *ast.ValueSpec, consts map[string]string) {
	for i, name := range spec.Names {
		if i >= len(spec.Values) {
			continue
		}
		if v, ok := staticString(spec.Values[i], consts); ok {
			consts[name.Name] = v
		}
	}
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
