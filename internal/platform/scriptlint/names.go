package scriptlint

import (
	"fmt"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// names applies the rules about what a script names: a local variable or a
// parameter nothing reads, and a name that hides one the platform provides.
func (l *linter) names() {
	binding := l.bindingIdents()
	used := map[*syntax.Ident]bool{}
	syntax.Walk(l.file, func(n syntax.Node) bool {
		if id, ok := n.(*syntax.Ident); ok && !binding[id] {
			if b, ok := id.Binding.(*resolve.Binding); ok && b.First != nil {
				used[b.First] = true
			}
		}
		return true
	})
	for id := range binding {
		if isPredeclared(id.Name) {
			l.add(finding{
				rule: RuleShadowedName, line: int(id.NamePos.Line),
				message: fmt.Sprintf("`%s` hides the predeclared `%s`", id.Name, id.Name),
				hint:    fmt.Sprintf("Choose another name. While this one is in scope, `%s` no longer means what the platform provides.", id.Name),
			})
		}
	}
	syntax.Walk(l.file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.DefStmt:
			l.unused(n.Name.Name, n.Function, used)
		case *syntax.LambdaExpr:
			l.unused("lambda", n.Function, used)
		}
		return true
	})
}

// unused reports each local of one function that nothing reads. A name
// starting with an underscore is one its author has said is unused.
func (l *linter) unused(fn string, function any, used map[*syntax.Ident]bool) {
	f, ok := function.(*resolve.Function)
	if !ok {
		return
	}
	params := paramCount(f.Params)
	for i, b := range f.Locals {
		if b.First == nil || used[b.First] || strings.HasPrefix(b.First.Name, "_") {
			continue
		}
		name := b.First.Name
		if i < params {
			l.add(finding{
				rule: RuleUnusedParameter, line: int(b.First.NamePos.Line),
				message: fmt.Sprintf("parameter `%s` of `%s` is never used", name, fn),
				hint:    "Remove it and the arguments passed for it, or name it with a leading underscore if a caller must still pass it.",
			})
			continue
		}
		l.add(finding{
			rule: RuleUnusedVariable, line: int(b.First.NamePos.Line),
			message: fmt.Sprintf("`%s` in `%s` is assigned and never read", name, fn),
			hint:    "Remove the assignment, or name a loop variable you do not need `_`.",
		})
	}
}

// paramCount is how many of a function's locals are its parameters: every
// parameter but a bare * binds a name, and the resolver lists them first.
func paramCount(params []syntax.Expr) int {
	n := 0
	for _, p := range params {
		if u, ok := p.(*syntax.UnaryExpr); ok && u.X == nil {
			continue
		}
		n++
	}
	return n
}

// bindingIdents is every identifier that binds a name rather than reading one:
// an assignment's targets, loop and comprehension variables, def names,
// parameters and load() names. An augmented assignment (x += 1) reads its
// target, so it is not one.
func (l *linter) bindingIdents() map[*syntax.Ident]bool {
	out := map[*syntax.Ident]bool{}
	syntax.Walk(l.file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.AssignStmt:
			if n.Op == syntax.EQ {
				targets(n.LHS, out)
			}
		case *syntax.ForStmt:
			targets(n.Vars, out)
		case *syntax.ForClause:
			targets(n.Vars, out)
		case *syntax.DefStmt:
			out[n.Name] = true
			params(n.Params, out)
		case *syntax.LambdaExpr:
			params(n.Params, out)
		case *syntax.LoadStmt:
			for _, id := range n.To {
				out[id] = true
			}
		}
		return true
	})
	return out
}

// targets collects the names an assignment target binds.
func targets(e syntax.Expr, out map[*syntax.Ident]bool) {
	switch e := e.(type) {
	case *syntax.Ident:
		out[e] = true
	case *syntax.ParenExpr:
		targets(e.X, out)
	case *syntax.TupleExpr:
		for _, x := range e.List {
			targets(x, out)
		}
	case *syntax.ListExpr:
		for _, x := range e.List {
			targets(x, out)
		}
	}
}

// params collects the names a parameter list binds.
func params(list []syntax.Expr, out map[*syntax.Ident]bool) {
	for _, p := range list {
		switch p := p.(type) {
		case *syntax.Ident:
			out[p] = true
		case *syntax.BinaryExpr: // name=default
			if id, ok := p.X.(*syntax.Ident); ok {
				out[id] = true
			}
		case *syntax.UnaryExpr: // *args, **kwargs
			if id, ok := p.X.(*syntax.Ident); ok {
				out[id] = true
			}
		}
	}
}
