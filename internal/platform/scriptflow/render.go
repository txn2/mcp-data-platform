package scriptflow

import (
	"maps"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow/stmtlookup"

	"github.com/txn2/mcp-data-platform/internal/scriptconst"
)

// scope is what the source says about the names visible at one point, one
// entry per name: the innermost frame that binds a name answers for it. A step
// keeps a copy of the scope it was written in, so rendering it later sees the
// names as they were at the call.
type scope struct {
	vars   map[string]binding
	global map[string]syntax.Expr
	gdicts map[string]*syntax.DictExpr
	// resolve is the function a name refers to in this scope (#2848), and body
	// the statements of the function the scope is in (nil at module scope):
	// what a statement list is followed through (stmtlookup, #2699).
	resolve func(name string) *syntax.DefStmt
	loaded  map[string]stmtlookup.Loaded
	body    []syntax.Stmt
	// lists is stmtlookup.Grown(body), read once when Bound first asks.
	lists map[string][]syntax.Expr
}

// binding is what one frame knows about one variable: its text when the
// source fully determines it, the text of an argument the caller computed, the
// expression it was assigned, and the dict literal it holds.
type binding struct {
	known    string
	isKnown  bool
	shown    string
	hasShown bool
	expr     syntax.Expr
	dict     *syntax.DictExpr
	// arg is what a parameter or a loop variable stands for, scope free
	// (stmtlookup.Substitute): the caller's argument, or an element of what
	// the loop iterates (#2699).
	arg syntax.Expr
}

// scopeOf copies the scope visible from f: its own names, then its lexical
// parents', then the module's expressions for names bound at module scope.
func (a *analyzer) scopeOf(f *frame) *scope {
	fn := ""
	if f != nil {
		fn = f.fn
	}
	sc := &scope{
		vars: map[string]binding{}, global: map[string]syntax.Expr{}, gdicts: map[string]*syntax.DictExpr{}, loaded: a.loaded,
		resolve: func(name string) *syntax.DefStmt {
			_, d := a.resolve(fn, name)
			return d
		},
	}
	if d := a.funcs[fn]; d != nil {
		sc.body = d.Body
	}
	for fr := f; fr != nil && fr != a.top; fr = fr.lexical {
		for name, b := range fr.bindings() {
			if _, ok := sc.vars[name]; !ok {
				sc.vars[name] = b
			}
		}
	}
	if a.top != nil {
		maps.Copy(sc.global, a.top.exprs)
		maps.Copy(sc.gdicts, a.top.dicts)
	}
	return sc
}

// bindings is everything the frame knows, by name.
func (f *frame) bindings() map[string]binding {
	out := map[string]binding{}
	entry := func(name string) binding { return out[name] }
	for name, x := range f.exprs {
		b := entry(name)
		b.expr = x
		out[name] = b
	}
	for name, v := range f.shown {
		b := entry(name)
		b.shown, b.hasShown = v, true
		out[name] = b
	}
	for name, v := range f.known {
		b := entry(name)
		b.known, b.isKnown = v, true
		out[name] = b
	}
	for name, d := range f.dicts {
		b := entry(name)
		b.dict = d
		out[name] = b
	}
	for name, x := range f.args {
		b := entry(name)
		b.arg = x
		out[name] = b
	}
	return out
}

// renderer renders expressions against a scope.
func (a *analyzer) renderer(sc *scope) *scriptconst.Renderer {
	r := &scriptconst.Renderer{Source: a.srcOf}
	r.Name = func(id *syntax.Ident) (string, bool) { return a.name(r, sc, id) }
	return r
}

// name renders one identifier. Which table answers is the resolver's call: a
// name bound at module scope is a module constant or a computed global, and
// any other name is the enclosing function's.
func (a *analyzer) name(r *scriptconst.Renderer, sc *scope, id *syntax.Ident) (string, bool) {
	if scriptconst.IsModuleName(id) {
		if v, ok := a.consts.Value(id); ok {
			return v, true
		}
		if x, ok := sc.global[id.Name]; ok && x != nil {
			v, _ := r.Nested(x)
			return v, false
		}
		return "{" + id.Name + "}", false
	}
	b := sc.vars[id.Name]
	switch {
	case b.isKnown:
		return b.known, true
	case b.hasShown:
		return b.shown, false
	case b.expr != nil:
		v, _ := r.Nested(b.expr)
		return v, false
	}
	if v, ok := sc.loaded[id.Name].Constant(); ok {
		return v, true
	}
	return "{" + id.Name + "}", false
}

// renderIn renders e in the scope f currently sees.
func (a *analyzer) renderIn(f *frame, e syntax.Expr) (string, bool) {
	return a.renderer(a.scopeOf(f)).Render(e)
}

// dictIn resolves an argument to the dict literal it is, or names.
func (a *analyzer) dictIn(f *frame, e syntax.Expr) *syntax.DictExpr {
	return a.scopeOf(f).dict(e)
}

// dict resolves e to a dict literal: the literal itself, or a variable
// assigned one.
func (sc *scope) dict(e syntax.Expr) *syntax.DictExpr {
	switch e := e.(type) {
	case *syntax.DictExpr:
		return e
	case *syntax.ParenExpr:
		return sc.dict(e.X)
	case *syntax.Ident:
		if scriptconst.IsModuleName(e) {
			return sc.gdicts[e.Name]
		}
		return sc.vars[e.Name].dict
	}
	return nil
}

// Bound is the expression a name was last assigned, in the function or at
// module scope (#2615, #2623), or nil when the source assigns it none; Known
// is a local's value when the analyzer knows it. They are how a statement's
// lookup is followed (stmtlookup).
func (sc *scope) Bound(id *syntax.Ident) syntax.Expr {
	if scriptconst.IsModuleName(id) {
		return sc.global[id.Name]
	}
	if grown := sc.grownList(id); grown != nil {
		return grown
	}
	if b := sc.vars[id.Name]; b.expr != nil {
		return b.expr
	}
	if arg := sc.vars[id.Name].arg; arg != nil {
		return arg
	}
	return sc.loaded[id.Name].Expr()
}

// grownList is a list the function builds under id in more than one step
// (assigned, then appended to, extended or added to), as one concatenation of
// every part, so a statement list passed on after it was grown carries each
// statement added to it (#2699). Nil for a name built in one step, which
// Bound reads as it was assigned.
func (sc *scope) grownList(id *syntax.Ident) syntax.Expr {
	if sc.body == nil {
		return nil
	}
	if sc.lists == nil {
		sc.lists = stmtlookup.Grown(sc.body)
	}
	parts := sc.lists[id.Name]
	if len(parts) < 2 {
		return nil
	}
	var out syntax.Expr
	for _, p := range parts {
		if marker, ok := p.(*syntax.Ident); ok && marker.Name == id.Name && marker.Binding == nil {
			// What the name held before the function grew it: its argument.
			if p = sc.vars[id.Name].arg; p == nil {
				p = stmtlookup.Opaque(id.Name)
			}
		}
		if out == nil {
			out = p
			continue
		}
		out = &syntax.BinaryExpr{Op: syntax.PLUS, X: out, Y: p}
	}
	return out
}

// Func is the function the source defines under name, or one it loads from a
// library, read in that library's scope; nil for none.
func (sc *scope) Func(name string) (*syntax.DefStmt, stmtlookup.Resolver) {
	if d := sc.localFunc(name); d != nil {
		return d, nil
	}
	return sc.loaded[name].Def()
}

// localFunc is the function the source defines that name refers to in this
// scope, nil for none.
func (sc *scope) localFunc(name string) *syntax.DefStmt {
	if sc.resolve == nil {
		return nil
	}
	return sc.resolve(name)
}

// Known is the constant string id holds in this scope, when it holds one
// (#2623): what a lookup's candidates resolve through.
func (sc *scope) Known(id *syntax.Ident) (string, bool) {
	b, ok := sc.vars[id.Name]
	return b.known, ok && b.isKnown
}

// text is a node's source text on one line.
func (a *analyzer) text(n syntax.Node) string {
	start, end := n.Span()
	first, last := int(start.Line), min(int(end.Line), len(a.lines))
	if first < 1 || first > last {
		return ""
	}
	span := a.lines[first-1 : last]
	parts := make([]string, 0, len(span))
	for i, l := range span {
		lo, hi := 0, len(l)
		if first+i == int(end.Line) {
			hi = min(int(end.Col)-1, len(l))
		}
		if i == 0 {
			lo = min(int(start.Col)-1, hi)
		}
		parts = append(parts, strings.TrimSpace(l[lo:hi]))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// srcOf is an expression's source text, closing brackets included: the parser
// ends an index or slice span before its "]".
func (a *analyzer) srcOf(e syntax.Expr) string {
	switch e := e.(type) {
	case *syntax.IndexExpr:
		return a.srcOf(e.X) + "[" + a.srcOf(e.Y) + "]"
	case *syntax.SliceExpr:
		lo, hi := "", ""
		if e.Lo != nil {
			lo = a.srcOf(e.Lo)
		}
		if e.Hi != nil {
			hi = a.srcOf(e.Hi)
		}
		return a.srcOf(e.X) + "[" + lo + ":" + hi + "]"
	}
	return a.text(e)
}

// clip shortens s to n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func cloneStrings(m map[string]string) map[string]string {
	return maps.Clone(m)
}

// assignedNames is every variable a block of statements assigns, nested
// blocks included and nested defs excluded.
func assignedNames(stmts []syntax.Stmt) map[string]bool {
	out := map[string]bool{}
	for _, st := range stmts {
		syntax.Walk(st, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.DefStmt:
				return false
			case *syntax.AssignStmt:
				for _, name := range targetNames(n.LHS) {
					out[name] = true
				}
			case *syntax.ForStmt:
				for _, name := range targetNames(n.Vars) {
					out[name] = true
				}
			}
			return true
		})
	}
	return out
}

// targetNames is the variables an assignment target binds.
func targetNames(lhs syntax.Expr) []string {
	switch l := lhs.(type) {
	case *syntax.Ident:
		return []string{l.Name}
	case *syntax.TupleExpr:
		return listNames(l.List)
	case *syntax.ListExpr:
		return listNames(l.List)
	case *syntax.ParenExpr:
		return targetNames(l.X)
	}
	return nil
}

func listNames(list []syntax.Expr) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, targetNames(x)...)
	}
	return out
}
