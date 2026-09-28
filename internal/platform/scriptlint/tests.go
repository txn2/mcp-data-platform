package scriptlint

import (
	"fmt"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// The rules that keep a script's tests apart from what it runs (#1939).
const (
	RuleTestModuleOutsideTest = "test-module-outside-test"
	RuleTestCalled            = "test-called"
	RuleConstantAssertion     = "constant-assertion"
)

// tests applies them: testing and assert are used only inside a test_*
// function, and nothing calls a test_* function, which only the test runner
// does. A run never calls a test, and a test the script called would run on
// every schedule.
func (l *linter) tests() {
	for _, s := range l.file.Stmts {
		if scriptdialect.IsTest(s) {
			l.constantAssertions(s)
			continue
		}
		syntax.Walk(s, func(n syntax.Node) bool {
			l.outsideTests(n)
			return true
		})
	}
}

// outsideTests reports what one node outside the tests does with them.
func (l *linter) outsideTests(n syntax.Node) {
	switch n := n.(type) {
	case *syntax.Ident:
		if n.Name == scriptrun.TestingName || n.Name == scriptrun.AssertName {
			l.testModuleOutsideTest(n)
		}
	case *syntax.CallExpr:
		if id, ok := n.Fn.(*syntax.Ident); ok && strings.HasPrefix(id.Name, scriptdialect.TestPrefix) {
			l.add(finding{
				rule: RuleTestCalled, subject: id.Name, line: int(id.NamePos.Line),
				message: fmt.Sprintf("`%s` is a test, and the script calls it", id.Name),
				hint:    "A test_* function is run by manage_script command=test and on save, never by the script. Rename the function if it is not a test.",
			})
		}
	}
}

// testModuleOutsideTest reports a use of testing or assert outside a test,
// unless the name is the script's own binding of it (shadowed-name reports
// that).
func (l *linter) testModuleOutsideTest(id *syntax.Ident) {
	if b, ok := id.Binding.(*resolve.Binding); !ok || b.Scope != resolve.Predeclared {
		return
	}
	l.add(finding{
		rule: RuleTestModuleOutsideTest, subject: id.Name, line: int(id.NamePos.Line),
		message: fmt.Sprintf("`%s` is used outside a test", id.Name),
		hint:    fmt.Sprintf("`%s` exists only inside a %s* function; a run fails where it is used.", id.Name, scriptdialect.TestPrefix),
	})
}

// constantAssertions reports an assertion inside a test whose every compared
// value is written into the test: it holds whatever the script does.
func (l *linter) constantAssertions(test syntax.Stmt) {
	syntax.Walk(test, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		dot, ok := call.Fn.(*syntax.DotExpr)
		if !ok {
			return true
		}
		if id, ok := dot.X.(*syntax.Ident); !ok || id.Name != scriptrun.AssertName {
			return true
		}
		compared := map[string]int{"eq": 2, "ne": 2, "contains": 2, "true": 1}[dot.Name.Name]
		if compared == 0 || len(call.Args) < compared {
			return true
		}
		for _, a := range call.Args[:compared] {
			if !writtenOut(a) {
				return true
			}
		}
		l.add(finding{
			rule: RuleConstantAssertion, subject: "assert." + dot.Name.Name, line: line(call),
			message: fmt.Sprintf("assert.%s compares only values written into the test, so it holds whatever the script does", dot.Name.Name),
			hint:    "Assert on what the script produced: testing.outputs().exports, .state, .notifies, .calls or .result.",
		})
		return true
	})
}

// writtenOut reports whether an expression is written out in full: a literal,
// True, False or None, or a list, tuple, dict or operation of them.
func writtenOut(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.Literal:
		return true
	case *syntax.Ident:
		return e.Name == "True" || e.Name == "False" || e.Name == "None"
	}
	list, ok := parts(e)
	return ok && allWrittenOut(list)
}

// parts is what a composite expression is made of, and whether e is one
// writtenOut looks inside.
func parts(e syntax.Expr) ([]syntax.Expr, bool) {
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return []syntax.Expr{e.X}, true
	case *syntax.UnaryExpr:
		return []syntax.Expr{e.X}, e.X != nil
	case *syntax.BinaryExpr:
		return []syntax.Expr{e.X, e.Y}, true
	case *syntax.DictEntry:
		return []syntax.Expr{e.Key, e.Value}, true
	case *syntax.ListExpr:
		return e.List, true
	case *syntax.TupleExpr:
		return e.List, true
	case *syntax.DictExpr:
		return e.List, true
	}
	return nil, false
}

// allWrittenOut reports whether every expression of list is written out.
func allWrittenOut(list []syntax.Expr) bool {
	for _, e := range list {
		if !writtenOut(e) {
			return false
		}
	}
	return true
}
