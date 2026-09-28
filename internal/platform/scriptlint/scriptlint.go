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
// A script saved before the gates (script.Script.Legacy) keeps running as it
// did. Its top level may do work, and a new version of it is refused only for a
// finding the version before it did not have, so the older set can be brought
// up over time.
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
	"github.com/txn2/mcp-data-platform/pkg/script"
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
	RuleLibraryEffect    = "library-effect"
)

// The limits, the ones this repository holds its own Go to where the two have
// a counterpart.
const (
	MaxCyclomatic = 10
	MaxCognitive  = 15
	MaxStatements = 40
	MaxNesting    = 4
)

// finding is one thing a rule noticed. subject is what the finding is about (a
// function, a variable) without its line or its measured value, so the same
// finding in two versions of a script is recognized as the same one after the
// lines around it moved or the number changed.
type finding struct {
	rule    string
	subject string
	line    int
	message string
	hint    string
}

func (f finding) key() string { return f.rule + "\x00" + f.subject }

// Save is what a save knows about the script it is saving into: whether it is
// a script saved before the gates, and the source of the version it replaces
// ("" for a new script).
type Save struct {
	Legacy   bool
	Previous string
}

// Result is what the gates made of a source.
type Result struct {
	// Source is the formatted source, which is what a save stores.
	Source string
	// Findings is every finding on Source. A refused one is an error; one a
	// legacy script already carried is a warning, reported and not refused.
	Findings []scriptrun.Finding
	// Refused is the findings that refuse the save. Empty means it goes
	// through.
	Refused []scriptrun.Finding
}

// Check formats source and holds it to the gates. A source that does not
// parse or resolve yields no findings here: the validator reports that, and a
// save is refused for it before this matters.
func Check(source string, s Save) Result {
	formatted := scriptfmt.Format(source)
	found := lint(formatted, !s.Legacy)
	refused := found
	if s.Legacy {
		refused = added(lint(scriptfmt.Format(s.Previous), false), found)
	}
	res := Result{Source: formatted, Findings: make([]scriptrun.Finding, 0, len(found)), Refused: []scriptrun.Finding{}}
	for _, f := range found {
		out := scriptrun.Finding{Rule: f.rule, Severity: scriptrun.SeverityWarning, Line: f.line, Message: f.message, Hint: f.hint}
		if slices.Contains(refused, f) {
			out.Severity = scriptrun.SeverityError
			res.Refused = append(res.Refused, out)
		}
		res.Findings = append(res.Findings, out)
	}
	return res
}

// added returns the findings in next that previous did not have: for each
// subject, the ones past the number previous carried, latest first taken as
// the new ones.
func added(previous, next []finding) []finding {
	had := map[string]int{}
	for _, f := range previous {
		had[f.key()]++
	}
	seen := map[string]int{}
	var out []finding
	for _, f := range next {
		seen[f.key()]++
		if seen[f.key()] > had[f.key()] {
			out = append(out, f)
		}
	}
	return out
}

// lint runs every rule over source. entry is whether the entry-point rules
// apply, which they do to a script created since #1944 and not to one saved
// before it.
func lint(source string, entry bool) []finding {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	file, err := scriptdialect.Parse(source, isPredeclared)
	if err != nil {
		return nil
	}
	l := &linter{file: file, consts: scriptconst.Collect(file)}
	if entry {
		l.entryPoint()
	}
	l.functions()
	l.names()
	l.hostCalls()
	l.tests()
	slices.SortFunc(l.found, func(a, b finding) int {
		if a.line != b.line {
			return a.line - b.line
		}
		return strings.Compare(a.rule+a.subject+a.message, b.rule+b.subject+b.message)
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

// For is the Save of new source into existing, or of a new script when
// existing is nil.
func For(existing *script.Script) Save {
	if existing == nil {
		return Save{}
	}
	return Save{Legacy: existing.Legacy, Previous: existing.Source}
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
