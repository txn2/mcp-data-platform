// Package scriptlint holds the authoring gates a managed script's source is put
// through on every save (#1913): the formatter's output is what is stored
// (#1937), a script created since #1944 keeps its work in main(), and the
// structural lint and limits below apply to every script (#1938).
//
// The gates are for the agent writing the script. A finding carries a rule, a
// line and a hint in the shape the validator already returns, so a refused save
// is fixed in the same loop the script is written in. There is no
// configuration: the limits are the platform's, the same for every deployment
// and every script.
//
// The gates run on a save and never on a run: a script saved before them keeps
// running as it is, and its next version is held to every rule.
package scriptlint

import (
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptfmt"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/scriptconst"
)

// The rules. Each is the Rule of the findings it produces, which is what an
// agent reads to know which of its habits a finding is about.
const (
	RuleEntryPoint       = "entry-point"
	RuleTopLevelWork     = "top-level-work"
	RuleCyclomatic       = "cyclomatic-complexity"
	RuleCognitive        = "cognitive-complexity"
	RuleFunctionLength   = "function-length"
	RuleNestingDepth     = "nesting-depth"
	RuleUnusedVariable   = "unused-variable"
	RuleUnusedParameter  = "unused-parameter"
	RuleShadowedName     = "shadowed-name"
	RuleMissingDocstring = "missing-docstring"
	RuleSQLFromValues    = "sql-built-from-values"
	RuleCallInLoop       = "call-in-loop"
	RuleStateWithoutRead = "save-state-without-read"
	// RuleStateDiscardedOnFail warns, without refusing the save, that a path
	// reaches fail() after platform.save_state, whose state the platform then
	// discards (#2002).
	RuleStateDiscardedOnFail = "save-state-before-fail"
	RuleLibraryEffect        = "library-effect"
)

// The limits, the ones this repository holds its own Go to where the two have
// a counterpart.
const (
	MaxCyclomatic = 10
	MaxCognitive  = 15
	MaxStatements = 40
	MaxNesting    = 4
)

// finding is one thing a rule noticed.
type finding struct {
	rule    string
	line    int
	message string
	hint    string
	// warn makes the finding a warning: reported with the save, never a
	// reason to refuse it.
	warn bool
}

// Result is what the gates made of a source.
type Result struct {
	// Source is the formatted source, which is what a save stores.
	Source string
	// Findings is every finding on Source: the errors, and the warnings.
	Findings []scriptrun.Finding
	// Refused is the findings that refuse the save: every error. Empty means
	// it goes through.
	Refused []scriptrun.Finding
	// Warnings is the findings a save reports and goes through with.
	Warnings []scriptrun.Finding
}

// Check formats source and holds it to the gates. A source that does not
// parse or resolve yields no findings here: the validator reports that, and a
// save is refused for it before this matters.
func Check(source string) Result {
	formatted := scriptfmt.Format(source)
	found := lint(formatted)
	res := Result{Source: formatted, Findings: make([]scriptrun.Finding, 0, len(found)), Refused: make([]scriptrun.Finding, 0, len(found))}
	res.Warnings = make([]scriptrun.Finding, 0)
	for _, f := range found {
		out := scriptrun.Finding{Rule: f.rule, Severity: scriptrun.SeverityError, Line: f.line, Message: f.message, Hint: f.hint}
		if f.warn {
			out.Severity = scriptrun.SeverityWarning
			res.Findings = append(res.Findings, out)
			res.Warnings = append(res.Warnings, out)
			continue
		}
		res.Findings = append(res.Findings, out)
		res.Refused = append(res.Refused, out)
	}
	return res
}

// lint runs every rule over source.
func lint(source string) []finding {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	file, err := scriptdialect.Parse(source, isPredeclared)
	if err != nil {
		return nil
	}
	l := &linter{file: file, consts: scriptconst.Collect(file)}
	l.entryPoint()
	l.functions()
	l.names()
	l.hostCalls()
	l.stateBeforeFail()
	l.tests()
	slices.SortFunc(l.found, func(a, b finding) int {
		if a.line != b.line {
			return a.line - b.line
		}
		return strings.Compare(a.rule+a.message, b.rule+b.message)
	})
	return l.found
}

// linter carries one file through the rules.
type linter struct {
	file   *syntax.File
	consts scriptconst.Table
	found  []finding
}

func (l *linter) add(f finding) { l.found = append(l.found, f) }

// isPredeclared answers from the names the run binds, so the lint resolves the
// file the run executes.
func isPredeclared(name string) bool { return slices.Contains(scriptrun.PredeclaredNames, name) }

// line is the line a node starts on.
func line(n syntax.Node) int {
	start, _ := n.Span()
	return int(start.Line)
}

// defs returns every def in the file, nested ones included, outermost first.
func (l *linter) defs() []*syntax.DefStmt {
	var out []*syntax.DefStmt
	syntax.Walk(l.file, func(n syntax.Node) bool {
		if d, ok := n.(*syntax.DefStmt); ok {
			out = append(out, d)
		}
		return true
	})
	return out
}

// walkBody visits every node of a function body, not entering a nested def,
// which is linted as its own function.
func walkBody(body []syntax.Stmt, visit func(syntax.Node) bool) {
	for _, s := range body {
		syntax.Walk(s, func(n syntax.Node) bool {
			if _, ok := n.(*syntax.DefStmt); ok {
				return false
			}
			return visit(n)
		})
	}
}

// Merge folds the gates' findings into a validation report, sorted by line,
// with OK recomputed as whether a save of the source would go through: every
// surface that validates reports the gates the same way.
func Merge(report scriptrun.Report, res Result) scriptrun.Report {
	merged := make([]scriptrun.Finding, 0, len(report.Findings))
	merged = append(merged, report.Findings...)
	merged = append(merged, res.Findings...)
	slices.SortStableFunc(merged, byLine)
	report.Findings = merged
	report.OK = report.OK && len(res.Refused) == 0
	return report
}

// Detail renders the refused findings as one message, for a surface that
// shows a refusal as text: each with its line, rule and hint, since the person
// fixing the script needs all of them.
func Detail(refused []scriptrun.Finding) string {
	parts := make([]string, 0, len(refused))
	for _, f := range refused {
		parts = append(parts, fmt.Sprintf("line %d: %s (%s). %s", f.Line, f.Message, f.Rule, f.Hint))
	}
	return "the source does not pass the authoring gates, so it was not saved: " + strings.Join(parts, " | ")
}
