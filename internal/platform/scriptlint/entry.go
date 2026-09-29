package scriptlint

import (
	"slices"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
)

// hostNames are the predeclared names that reach the run: a module constant
// defined from one is work, not a declaration.
var hostNames = []string{"platform", "run"}

// entryPoint holds a script created since #1944 to its shape: a main() taking
// no parameters, which the platform calls, and a top level of definitions,
// constants, load() and docstrings. Work at the top level cannot be called in
// isolation, which is what a test of the script needs.
func (l *linter) entryPoint() {
	var main *syntax.DefStmt
	for _, s := range l.file.Stmts {
		if d, ok := s.(*syntax.DefStmt); ok && d.Name.Name == scriptdialect.EntryPointName {
			main = d
		}
	}
	switch {
	case main == nil:
		l.library()
	case len(main.Params) > 0:
		l.add(finding{
			rule: RuleEntryPoint, line: line(main),
			message: "main() takes parameters",
			hint:    "The platform calls main() with no arguments. Read the script's parameters from `run.params` inside it.",
		})
	}
	for _, s := range l.file.Stmts {
		if !declares(s) {
			l.add(finding{
				rule: RuleTopLevelWork, line: line(s),
				message: "this statement does work at the top level",
				hint: "Move it into main() (or a function main() calls), indented one level. " +
					"The top level may only define functions, bind constants written as literals, and load().",
			})
		}
	}
}

// library holds a source with no main() to what a library is (#1941): pure
// code another script loads, which names neither platform nor run. Each use is
// a finding on its line; the first of each name is enough to fix the rest.
func (l *linter) library() {
	seen := map[string]bool{}
	syntax.Walk(l.file, func(n syntax.Node) bool {
		id, ok := n.(*syntax.Ident)
		if !ok || !slices.Contains(hostNames, id.Name) || seen[id.Name] {
			return true
		}
		seen[id.Name] = true
		l.add(finding{
			rule: RuleLibraryEffect, line: line(id),
			message: "the source defines no main(), so it is a library, and a library may not name " + id.Name,
			hint: "A library is pure code another script loads by version, as load(\"lib:<name>@<version>\", \"fn\"); " +
				"the script that loads it makes the calls. Pass what the function needs as arguments. " +
				"If this is meant to run on its own, put its work in `def main():`.",
		})
		return true
	})
}

// declares reports whether a top-level statement only declares something.
func declares(s syntax.Stmt) bool {
	switch s := s.(type) {
	case *syntax.DefStmt, *syntax.LoadStmt:
		return true
	case *syntax.ExprStmt:
		lit, ok := s.X.(*syntax.Literal)
		return ok && lit.Token == syntax.STRING
	case *syntax.AssignStmt:
		return s.Op == syntax.EQ && targetsOnly(s.LHS) && constant(s.RHS)
	}
	return false
}

// targetsOnly reports whether an assignment's left side only names.
func targetsOnly(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.Ident:
		return true
	case *syntax.ParenExpr:
		return targetsOnly(e.X)
	case *syntax.TupleExpr:
		return allOf(e.List, targetsOnly)
	case *syntax.ListExpr:
		return allOf(e.List, targetsOnly)
	}
	return false
}

// constant reports whether an expression is a value written in the source:
// literals and the operators over them, collections of them, names, and
// lambdas. A call, an attribute read (run.params, platform.query) and a
// comprehension compute at run time, so they belong in main().
func constant(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.Literal, *syntax.LambdaExpr:
		return true
	case *syntax.Ident:
		return !slices.Contains(hostNames, e.Name)
	}
	return constantOperation(e) || constantCollection(e)
}

// constantOperation reports whether an operator expression works only on
// constants.
func constantOperation(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return constant(e.X)
	case *syntax.UnaryExpr:
		return constant(e.X)
	case *syntax.BinaryExpr:
		return constant(e.X) && constant(e.Y)
	case *syntax.CondExpr:
		return constant(e.Cond) && constant(e.True) && constant(e.False)
	case *syntax.IndexExpr:
		return constant(e.X) && constant(e.Y)
	}
	return false
}

// constantCollection reports whether a collection literal holds only
// constants.
func constantCollection(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.ListExpr:
		return allOf(e.List, constant)
	case *syntax.TupleExpr:
		return allOf(e.List, constant)
	case *syntax.DictExpr:
		return allOf(e.List, constant)
	case *syntax.DictEntry:
		return constant(e.Key) && constant(e.Value)
	}
	return false
}

func allOf(list []syntax.Expr, pred func(syntax.Expr) bool) bool {
	return !slices.ContainsFunc(list, func(e syntax.Expr) bool { return !pred(e) })
}
