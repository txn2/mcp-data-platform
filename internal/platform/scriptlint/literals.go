package scriptlint

import (
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/scriptprintf"
	"github.com/txn2/mcp-data-platform/internal/scriptre"
)

// The rules over text the source already holds: a format string or a
// pattern a run would refuse, refused at save rather than on the first run
// that reaches the line (#2048, #2050).
const (
	RuleInvalidFormat  = "invalid-format-string"
	RuleInvalidPattern = "invalid-pattern"
)

// patternTakers are the re functions whose first argument is a pattern.
var patternTakers = map[string]bool{
	"compile": true, "search": true, "match": true, "fullmatch": true,
	"findall": true, "finditer": true, "sub": true, "split": true,
}

// literals checks every format string and pattern whose text is known from
// the source: a literal, or a module constant.
func (l *linter) literals() {
	syntax.Walk(l.file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.BinaryExpr:
			if n.Op == syntax.PERCENT {
				l.checkFormat(n.X, scriptprintf.CheckPercent, "%")
			}
		case *syntax.CallExpr:
			l.checkCall(n)
		}
		return true
	})
}

// checkCall checks "...".format(...) and re.<fn>(pattern, ...).
func (l *linter) checkCall(c *syntax.CallExpr) {
	dot, ok := c.Fn.(*syntax.DotExpr)
	if !ok {
		return
	}
	switch {
	case dot.Name.Name == "format":
		l.checkFormat(dot.X, scriptprintf.CheckFormat, ".format()")
	case isName(dot.X, scriptre.Name) && patternTakers[dot.Name.Name] && len(c.Args) > 0:
		l.checkPattern(c.Args[0])
	}
}

// checkFormat reports a format string the run would refuse.
func (l *linter) checkFormat(e syntax.Expr, check func(string) error, how string) {
	format, known := l.knownText(e)
	if !known {
		return
	}
	if err := check(format); err != nil {
		l.add(finding{
			rule: RuleInvalidFormat, line: line(e),
			message: "this format string fails when the run reaches it: " + err.Error(),
			hint: "% takes the conversions s r d i o x X e E f F g G c with the flags - 0 + space #, a width and a .precision; " +
				`.format() takes {name!r:spec} with Python's spec, [[fill]align][sign][#][0][width][,][.precision][type]. ` +
				"Fix the " + how + " format, or write a literal % as %% and a literal brace as {{ or }}.",
		})
	}
}

// checkPattern reports a pattern RE2 cannot compile.
func (l *linter) checkPattern(e syntax.Expr) {
	pattern, known := l.knownText(e)
	if !known {
		return
	}
	if err := scriptre.Validate(pattern); err != nil {
		l.add(finding{
			rule: RuleInvalidPattern, line: line(e),
			message: "this pattern fails when the run reaches it: " + err.Error(),
			hint: "re is RE2. Rewrite a lookahead or lookbehind as a capture group around the part to keep, " +
				"and a backreference as a second match or a comparison in the script.",
		})
	}
}

// knownText is the text e is known to be: a string literal or a module
// constant holding one. Text built at run time is the run's to check.
func (l *linter) knownText(e syntax.Expr) (string, bool) {
	switch e := e.(type) {
	case *syntax.Literal:
		s, ok := e.Value.(string)
		return s, ok
	case *syntax.Ident:
		return l.consts.Value(e)
	}
	return "", false
}
