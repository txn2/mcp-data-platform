package stmtlookup

import (
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// What follows is how a statement list is followed past the call it is
// passed to (#2699): through a parameter to what the caller passed, through
// a loop variable to the elements it is drawn from, into a helper the source
// defines to what it returns, and across + and .append. Every statement here
// is a module constant, so the tables a script writes are known before it
// runs, however the list reaches platform.batch.

// Funcs is a Resolver that also names the functions the source defines, so a
// statement list a helper returns is read from its return statements. outer
// is the scope the function's module names are read in: nil for the script's
// own, a library's for a function the script loads.
type Funcs interface {
	Func(name string) (def *syntax.DefStmt, outer Resolver)
}

// Foreign is a Resolver for a module other than the one a statement is read
// in: a library's. Substitute replaces its module names by what they are,
// since the same names in the reading script are other variables.
type Foreign interface {
	// Global is a module name's constant text, or else the expression the
	// module binds it to.
	Global(id *syntax.Ident) (constant string, expr syntax.Expr, ok bool)
}

// opaquePrefix marks a name Substitute could not resolve in the scope it was
// written in. No scope binds it, so it is read as a value only known when the
// script runs, never as a same-named variable of the scope it is read in.
const opaquePrefix = "\x00"

// Opaque is a name standing for a value only known when the script runs.
func Opaque(name string) *syntax.Ident {
	return &syntax.Ident{Name: opaquePrefix + name}
}

// maxSubstitute bounds the nodes one Substitute builds.
const maxSubstitute = 512

// Substitute is e with every local name it holds replaced by what sc binds
// it to, recursively, so the result reads the same in any scope: a module
// name and a builtin stay, a local the source binds becomes its expression,
// and a local it does not becomes Opaque. It is how a caller's argument is
// carried into the function it is passed to (#2699).
func Substitute(sc Resolver, e syntax.Expr) syntax.Expr {
	s := substituter{sc: sc, budget: maxSubstitute}
	return s.expr(e, nil, 0)
}

type substituter struct {
	sc     Resolver
	budget int
}

// isScopeFree reports whether id reads the same in every scope of the module:
// a module name, a builtin, or a name already made opaque.
func isScopeFree(id *syntax.Ident) bool {
	if strings.HasPrefix(id.Name, opaquePrefix) {
		return true
	}
	b, ok := id.Binding.(*resolve.Binding)
	return ok && (b.Scope == resolve.Global || b.Scope == resolve.Predeclared || b.Scope == resolve.Universal)
}

func (s *substituter) expr(e syntax.Expr, shadow map[string]bool, depth int) syntax.Expr {
	s.budget--
	if e == nil || s.budget < 0 || depth > maxLookup {
		return Opaque("")
	}
	switch x := e.(type) {
	case *syntax.Ident:
		return s.ident(x, shadow, depth)
	case *syntax.Literal:
		return x
	case *syntax.ParenExpr:
		return s.expr(x.X, shadow, depth)
	case *syntax.Comprehension:
		return s.comprehension(x, shadow, depth)
	}
	if out, ok := s.container(e, shadow, depth); ok {
		return out
	}
	return s.operation(e, shadow, depth)
}

// container substitutes a list, tuple or dict literal.
func (s *substituter) container(e syntax.Expr, shadow map[string]bool, depth int) (syntax.Expr, bool) {
	switch x := e.(type) {
	case *syntax.ListExpr:
		return &syntax.ListExpr{List: s.list(x.List, shadow, depth)}, true
	case *syntax.TupleExpr:
		return &syntax.TupleExpr{List: s.list(x.List, shadow, depth)}, true
	case *syntax.DictExpr:
		return s.dict(x, shadow, depth), true
	}
	return nil, false
}

// operation substitutes an index, an operator, a field or a call.
func (s *substituter) operation(e syntax.Expr, shadow map[string]bool, depth int) syntax.Expr {
	switch x := e.(type) {
	case *syntax.IndexExpr:
		return &syntax.IndexExpr{X: s.expr(x.X, shadow, depth), Y: s.expr(x.Y, shadow, depth)}
	case *syntax.BinaryExpr:
		return &syntax.BinaryExpr{Op: x.Op, X: s.expr(x.X, shadow, depth), Y: s.expr(x.Y, shadow, depth)}
	case *syntax.UnaryExpr:
		return &syntax.UnaryExpr{Op: x.Op, X: s.expr(x.X, shadow, depth)}
	case *syntax.DotExpr:
		return &syntax.DotExpr{X: s.expr(x.X, shadow, depth), Name: x.Name}
	case *syntax.CallExpr:
		return &syntax.CallExpr{Fn: s.expr(x.Fn, shadow, depth), Args: s.args(x.Args, shadow, depth)}
	}
	return Opaque("")
}

func (s *substituter) ident(x *syntax.Ident, shadow map[string]bool, depth int) syntax.Expr {
	if shadow[x.Name] {
		return x
	}
	if isScopeFree(x) {
		return s.foreign(x, depth)
	}
	if b := s.sc.Bound(x); b != nil {
		return s.expr(b, nil, depth+1)
	}
	if v, ok := s.sc.Known(x); ok {
		return &syntax.Literal{Token: syntax.STRING, Value: v, Raw: v}
	}
	return Opaque(x.Name)
}

// foreign is a module name read in a foreign module's scope: its constant as
// a literal, else the expression the module binds it to, else the name (a
// builtin). In the script's own scope a module name stays as written.
func (s *substituter) foreign(x *syntax.Ident, depth int) syntax.Expr {
	f, ok := s.sc.(Foreign)
	if !ok || strings.HasPrefix(x.Name, opaquePrefix) {
		return x
	}
	constant, expr, ok := f.Global(x)
	switch {
	case !ok:
		return x
	case expr == nil:
		return &syntax.Literal{Token: syntax.STRING, Value: constant, Raw: constant}
	}
	return s.expr(expr, nil, depth+1)
}

func (s *substituter) list(in []syntax.Expr, shadow map[string]bool, depth int) []syntax.Expr {
	out := make([]syntax.Expr, len(in))
	for i, x := range in {
		out[i] = s.expr(x, shadow, depth)
	}
	return out
}

func (s *substituter) dict(d *syntax.DictExpr, shadow map[string]bool, depth int) syntax.Expr {
	out := &syntax.DictExpr{}
	for _, entry := range d.List {
		de, ok := entry.(*syntax.DictEntry)
		if !ok {
			return Opaque("")
		}
		out.List = append(out.List, &syntax.DictEntry{Key: s.expr(de.Key, shadow, depth), Value: s.expr(de.Value, shadow, depth)})
	}
	return out
}

// args substitutes a call's arguments, a keyword's name left as written.
func (s *substituter) args(in []syntax.Expr, shadow map[string]bool, depth int) []syntax.Expr {
	out := make([]syntax.Expr, len(in))
	for i, a := range in {
		if b, ok := a.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			out[i] = &syntax.BinaryExpr{Op: syntax.EQ, X: b.X, Y: s.expr(b.Y, shadow, depth)}
			continue
		}
		out[i] = s.expr(a, shadow, depth)
	}
	return out
}

// comprehension substitutes each clause's iterable and condition and the
// body, leaving the names the comprehension binds as they are.
func (s *substituter) comprehension(c *syntax.Comprehension, shadow map[string]bool, depth int) syntax.Expr {
	inner := map[string]bool{}
	for k := range shadow {
		inner[k] = true
	}
	out := &syntax.Comprehension{Curly: c.Curly}
	for _, clause := range c.Clauses {
		switch cl := clause.(type) {
		case *syntax.ForClause:
			out.Clauses = append(out.Clauses, &syntax.ForClause{Vars: cl.Vars, X: s.expr(cl.X, inner, depth)})
			for _, name := range targetNames(cl.Vars) {
				inner[name] = true
			}
		case *syntax.IfClause:
			out.Clauses = append(out.Clauses, &syntax.IfClause{Cond: s.expr(cl.Cond, inner, depth)})
		}
	}
	out.Body = s.expr(c.Body, inner, depth)
	return out
}

// targetNames is the variables a loop target binds.
func targetNames(e syntax.Expr) []string {
	switch x := e.(type) {
	case *syntax.Ident:
		return []string{x.Name}
	case *syntax.ParenExpr:
		return targetNames(x.X)
	case *syntax.TupleExpr:
		return listTargets(x.List)
	case *syntax.ListExpr:
		return listTargets(x.List)
	}
	return nil
}

func listTargets(list []syntax.Expr) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, targetNames(x)...)
	}
	return out
}

// LoopElements is what each variable of a loop over iterable stands for: an
// element of what it is drawn from, as a lookup with a key known only when
// the script runs, which Candidates reads as every element (#2699). A single
// variable over a list, a tuple or d.values() is one element; the second of
// two over d.items() or enumerate(list) is one value. Any other loop binds
// nothing.
func LoopElements(vars, iterable syntax.Expr) map[string]syntax.Expr {
	out := map[string]syntax.Expr{}
	container, part := iterated(iterable)
	if container == nil {
		return out
	}
	elem := &syntax.IndexExpr{X: container, Y: Opaque("element")}
	names := targetNames(vars)
	switch {
	case part == partElement && len(names) == 1:
		if _, plain := vars.(*syntax.Ident); plain {
			out[names[0]] = elem
		}
	case part == partPair && len(names) == 2:
		out[names[1]] = elem
	}
	return out
}

// What one iteration of a loop yields.
const (
	partElement = iota
	partPair
)

// iterated is what a loop iterates over and what each iteration yields: the
// container of d.values() and d.items(), the list of enumerate(list), or the
// iterable itself. A dict iterated directly yields its keys, which name no
// statement, so a dict literal binds nothing.
func iterated(e syntax.Expr) (container syntax.Expr, part int) {
	switch x := e.(type) {
	case *syntax.ParenExpr:
		return iterated(x.X)
	case *syntax.DictExpr:
		return nil, 0
	case *syntax.CallExpr:
		return iteratedCall(x)
	}
	return e, partElement
}

// iteratedCall is what a loop over a call iterates: d.values(), d.items(),
// enumerate(list), or the value a named function returns, which reads only
// when the source defines it; nothing for a method call.
func iteratedCall(x *syntax.CallExpr) (container syntax.Expr, part int) {
	if dot, ok := x.Fn.(*syntax.DotExpr); ok && len(x.Args) == 0 {
		switch dot.Name.Name {
		case "values":
			return dot.X, partElement
		case "items":
			return dot.X, partPair
		}
		return nil, 0
	}
	fn, ok := x.Fn.(*syntax.Ident)
	switch {
	case !ok:
		return nil, 0
	case fn.Name == "enumerate" && len(x.Args) == 1:
		return x.Args[0], partPair
	}
	return x, partElement
}

// returned is what a call of a function the source defines returns, as a
// statement list: each return statement's value read in the function's
// body, every statement made scope free (Substitute).
func returned(sc Resolver, call *syntax.CallExpr, depth int) ([]syntax.Expr, bool) {
	funcs, ok := sc.(Funcs)
	fn, named := call.Fn.(*syntax.Ident)
	if !ok || !named {
		return nil, false
	}
	def, outer := funcs.Func(fn.Name)
	if def == nil {
		return nil, false
	}
	if outer == nil {
		outer = sc
	}
	body := bodyScope{outer: outer, assigned: assignments(def.Body)}
	lists := ListsBound(def.Body)
	rets := returnsOf(def.Body)
	out := make([]syntax.Expr, 0, len(rets))
	found := false
	for _, r := range rets {
		found = true
		stmts, ok := statementList(body, r, lists, depth+1)
		if !ok {
			return nil, false
		}
		for _, x := range stmts {
			out = append(out, Substitute(body, x))
		}
	}
	return out, found
}

// returnedValues is every value a call of a function the source defines
// returns, each made scope free in the function's body, so it reads the same
// in the caller's scope.
func returnedValues(sc Resolver, call *syntax.CallExpr) ([]syntax.Expr, bool) {
	funcs, ok := sc.(Funcs)
	fn, named := call.Fn.(*syntax.Ident)
	if !ok || !named {
		return nil, false
	}
	def, outer := funcs.Func(fn.Name)
	if def == nil {
		return nil, false
	}
	if outer == nil {
		outer = sc
	}
	body := bodyScope{outer: outer, assigned: assignments(def.Body)}
	var out []syntax.Expr
	for _, r := range returnsOf(def.Body) {
		out = append(out, Substitute(body, r))
	}
	return out, len(out) > 0
}

// returnsOf is every value a function body returns, nested functions left
// out.
func returnsOf(body []syntax.Stmt) []syntax.Expr {
	var out []syntax.Expr
	for _, stmt := range body {
		syntax.Walk(stmt, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.DefStmt, *syntax.LambdaExpr:
				return false
			case *syntax.ReturnStmt:
				if n.Result != nil {
					out = append(out, n.Result)
				}
			}
			return true
		})
	}
	return out
}

// assignments is the expression each name a function body assigns was last
// assigned, nested functions left out.
func assignments(body []syntax.Stmt) map[string]syntax.Expr {
	out := map[string]syntax.Expr{}
	for _, stmt := range body {
		syntax.Walk(stmt, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.DefStmt, *syntax.LambdaExpr:
				return false
			case *syntax.AssignStmt:
				if id, ok := n.LHS.(*syntax.Ident); ok && n.Op == syntax.EQ {
					out[id.Name] = n.RHS
				}
			}
			return true
		})
	}
	return out
}

// bodyScope is the scope a helper's return statements are read in: what
// its body assigns, and the module's names from the scope it was called in.
type bodyScope struct {
	outer    Resolver
	assigned map[string]syntax.Expr
}

// Bound is what the body assigns a name, or for a name that is not the
// body's own (a module name), what the calling scope binds it to. A local the
// body does not assign, such as a parameter, is never looked up in the
// calling scope, where the same name is another variable.
func (b bodyScope) Bound(id *syntax.Ident) syntax.Expr {
	if x, ok := b.assigned[id.Name]; ok {
		return x
	}
	if bind, ok := id.Binding.(*resolve.Binding); ok && !isScopeFree(id) && bind.Scope != resolve.Undefined {
		return nil
	}
	return b.outer.Bound(id)
}

// Known is nothing: a helper's locals have no value the analyzer followed.
func (bodyScope) Known(*syntax.Ident) (string, bool) { return "", false }

// Func is a function the calling scope names.
func (b bodyScope) Func(name string) (*syntax.DefStmt, Resolver) {
	if f, ok := b.outer.(Funcs); ok {
		return f.Func(name)
	}
	return nil, nil
}

// Global is the outer scope's, when it is a foreign module's.
func (b bodyScope) Global(id *syntax.Ident) (string, syntax.Expr, bool) {
	if f, ok := b.outer.(Foreign); ok {
		return f.Global(id)
	}
	return "", nil, false
}
