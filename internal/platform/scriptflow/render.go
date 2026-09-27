package scriptflow

import (
	"maps"
	"strings"

	"go.starlark.net/syntax"

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
}

// scopeOf copies the scope visible from f: its own names, then its lexical
// parents', then the module's expressions for names bound at module scope.
func (a *analyzer) scopeOf(f *frame) *scope {
	sc := &scope{vars: map[string]binding{}, global: map[string]syntax.Expr{}, gdicts: map[string]*syntax.DictExpr{}}
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
