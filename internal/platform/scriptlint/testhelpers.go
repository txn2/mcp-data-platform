package scriptlint

import (
	"fmt"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// mainName is the function a run calls.
const mainName = "main"

// testOnlyHelpers names the top-level functions a run can never reach, which
// may use testing and assert as a test does (#2116): a shared fake_login()
// that declares a flow's answers, called by several tests. A run reaches
// main, every top-level statement that is not a function, and every function
// either of them names, transitively; a function is reached when its name
// appears at all, called or passed, which keeps a function a run might call
// through a value out of the set.
func (l *linter) testOnlyHelpers() map[string]bool {
	defs, roots := l.partition()
	reached := map[string]bool{}
	if d, ok := defs[mainName]; ok {
		reached[mainName] = true
		reachFrom(d, defs, reached)
	}
	for _, r := range roots {
		reachFrom(r, defs, reached)
	}
	helpers := map[string]bool{}
	for name := range defs {
		if !reached[name] {
			helpers[name] = true
		}
	}
	return helpers
}

// partition splits the file's statements into the functions that are not
// tests, by name, and the top-level statements that are not definitions.
func (l *linter) partition() (defs map[string]*syntax.DefStmt, roots []syntax.Node) {
	defs = map[string]*syntax.DefStmt{}
	for _, s := range l.file.Stmts {
		d, ok := s.(*syntax.DefStmt)
		switch {
		case ok && scriptdialect.IsTest(s):
		case ok:
			defs[d.Name.Name] = d
		default:
			roots = append(roots, s)
		}
	}
	return defs, roots
}

// reachFrom marks every function of defs that n names, and every function
// those name in turn.
func reachFrom(n syntax.Node, defs map[string]*syntax.DefStmt, reached map[string]bool) {
	syntax.Walk(n, func(n syntax.Node) bool {
		id, ok := n.(*syntax.Ident)
		if !ok || reached[id.Name] {
			return true
		}
		if d, isDef := defs[id.Name]; isDef {
			reached[id.Name] = true
			reachFrom(d, defs, reached)
		}
		return true
	})
}

// inTestHelper reports what a test-only helper does that only a test may: a
// recording is named by the test that replays it, where a save reads it and
// keeps it from being swept.
func (l *linter) inTestHelper(d *syntax.DefStmt) {
	syntax.Walk(d, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			if id, isIdent := call.Fn.(*syntax.Ident); isIdent && strings.HasPrefix(id.Name, scriptdialect.TestPrefix) {
				l.outsideTests(call)
			}
		}
		dot, ok := n.(*syntax.DotExpr)
		if !ok || dot.Name.Name != "replay" {
			return true
		}
		if id, isIdent := dot.X.(*syntax.Ident); isIdent && id.Name == scriptrun.TestingName {
			l.add(finding{
				rule: RuleTestModuleOutsideTest, line: int(dot.Name.NamePos.Line),
				message: fmt.Sprintf("`testing.replay` is called in %s, which is not a test", d.Name.Name),
				hint: "A test names the recording it replays itself, as testing.replay(\"<run id>\"), so a save " +
					"knows which recordings to keep. Call testing.replay in the test and the helper after it.",
			})
		}
		return true
	})
}
