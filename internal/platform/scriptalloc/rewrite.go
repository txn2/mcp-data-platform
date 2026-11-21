package scriptalloc

import (
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/scriptprintf"
)

// Rewrite routes, in place, every operation in file that can build a large
// value in one step through the globals Bind binds:
//
//   - `x * y` and `x + y`;
//   - `name *= y` and `name += y` (the in-place add keeps extending a list
//     in place);
//   - `x[k] *= y` and `x[k] += y`, as one call taking x and k once, so a
//     target with a side effect is evaluated once as it is natively;
//   - `x.replace(...)`, `x.join(...)`, `x.extend(...)`, `x.split(...)`,
//     `x.rsplit(...)` and `x.splitlines(...)`.
//
// Positions are kept: each call is placed at the operator or the method
// name, so an error is reported where the script wrote it. An augmented
// assignment to a field (s.f += y) is left alone: no Starlark value a script
// holds has a field that can be assigned.
func Rewrite(file *syntax.File) {
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.File:
			n.Stmts = statements(n.Stmts)
		case *syntax.DefStmt:
			n.Body = statements(n.Body)
		case *syntax.IfStmt:
			n.True, n.False = statements(n.True), statements(n.False)
		case *syntax.ForStmt:
			n.Body = statements(n.Body)
		case *syntax.WhileStmt:
			n.Body = statements(n.Body)
		}
		return true
	})
	scriptprintf.ReplaceExprs(file, rewritten)
}

// statements rewrites the augmented assignments in a statement list.
func statements(stmts []syntax.Stmt) []syntax.Stmt {
	for i, s := range stmts {
		a, ok := s.(*syntax.AssignStmt)
		if !ok || (a.Op != syntax.PLUS_EQ && a.Op != syntax.STAR_EQ) {
			continue
		}
		switch lhs := a.LHS.(type) {
		case *syntax.Ident:
			name := mulName
			if a.Op == syntax.PLUS_EQ {
				name = addAssignName
			}
			a.Op = syntax.EQ
			a.RHS = call(name, a.OpPos, &syntax.Ident{NamePos: lhs.NamePos, Name: lhs.Name}, a.RHS)
		case *syntax.IndexExpr:
			name := indexMulName
			if a.Op == syntax.PLUS_EQ {
				name = indexAddName
			}
			stmts[i] = &syntax.ExprStmt{X: call(name, a.OpPos, lhs.X, lhs.Y, a.RHS)}
		}
	}
	return stmts
}

// rewritten is the call e becomes, or nil when it stays.
func rewritten(e syntax.Expr) syntax.Expr {
	switch e := e.(type) {
	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.STAR:
			return call(mulName, e.OpPos, e.X, e.Y)
		case syntax.PLUS:
			return call(addName, e.OpPos, e.X, e.Y)
		}
	case *syntax.CallExpr:
		if dot, ok := e.Fn.(*syntax.DotExpr); ok {
			if name, routed := methods[dot.Name.Name]; routed {
				args := append([]syntax.Expr{dot.X}, e.Args...)
				return &syntax.CallExpr{Fn: &syntax.Ident{NamePos: dot.Name.NamePos, Name: name}, Lparen: e.Lparen, Args: args, Rparen: e.Rparen}
			}
		}
	}
	return nil
}

// methods are the methods routed, by name.
//
//nolint:gochecknoglobals // a read-only lookup table.
var methods = map[string]string{
	"replace": replaceName, "join": joinName, "extend": extendName,
	"split": splitName, "rsplit": rsplitName, "splitlines": splitlinesName,
}

// call is a call of the global name at pos with args.
func call(name string, pos syntax.Position, args ...syntax.Expr) *syntax.CallExpr {
	return &syntax.CallExpr{Fn: &syntax.Ident{NamePos: pos, Name: name}, Lparen: pos, Args: args, Rparen: pos}
}
