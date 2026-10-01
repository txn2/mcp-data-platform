package scriptlint

import (
	"go.starlark.net/syntax"
)

// stateBeforeFail warns where a function calls fail() after
// platform.save_state (#2002).
//
// The platform commits a run's save_state only when the run succeeds, so a
// script that saves its progress and then fails -- one page would not answer,
// so it fails retryably -- saves nothing, and its next run starts where this
// one did. That is right for a run whose work is atomic and wrong for one that
// made durable progress first, which wants platform.checkpoint. The rule is a
// warning, not a refusal: a save_state followed by a fail() on a path that has
// not done its work is correct, and the lint cannot tell the two apart.
func (l *linter) stateBeforeFail() {
	for _, d := range l.defs() {
		var saved *syntax.CallExpr
		syntax.Walk(d, func(n syntax.Node) bool {
			if inner, ok := n.(*syntax.DefStmt); ok && inner != d {
				return false
			}
			c, ok := n.(*syntax.CallExpr)
			if !ok {
				return true
			}
			switch {
			case saved == nil && isPlatformMember(c, memberSaveState):
				saved = c
			case saved != nil && isName(c.Fn, "fail"):
				l.add(finding{
					rule: RuleStateDiscardedOnFail, line: line(c), warn: true,
					message: "fail() is reached after platform.save_state, and the platform discards a failed run's save_state",
					hint: "A failed run keeps its state where it was, so the next run starts from the same point. " +
						"If the work before this fail() landed (rows merged, files written), record how far it got " +
						"with platform.checkpoint, which is committed however the run ends.",
				})
				return false
			}
			return true
		})
	}
}

// isPlatformMember reports whether c calls platform.<member>.
func isPlatformMember(c *syntax.CallExpr, member string) bool {
	dot, ok := c.Fn.(*syntax.DotExpr)
	return ok && isName(dot.X, "platform") && dot.Name.Name == member
}
