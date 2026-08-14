package scriptrun

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Finding severities. An error means the script cannot run as written; a
// warning means it will run but a reviewer should look.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Finding is one thing the validator noticed, addressed to the author.
type Finding struct {
	Severity string `json:"severity" example:"error"`
	Line     int    `json:"line,omitempty" example:"12"`
	Message  string `json:"message"`
	// Hint is the corrective action. It carries most of the value of this
	// validator: an author who writes Python at a Starlark interpreter needs to
	// be told what to write instead, not merely that the parser disagreed.
	Hint string `json:"hint,omitempty"`
}

// Report is the result of validating one script's source: whether it can run,
// what it would reach if it did, and everything the author or a reviewer should
// know first.
type Report struct {
	OK       bool      `json:"ok"`
	Findings []Finding `json:"findings"`
	// Capabilities is the set of host bindings the source references,
	// Connections the connection names it names literally, and Destinations
	// the output destinations it names literally. Together they are the raw
	// material for the grants diff a reviewer will be shown: what the code
	// reaches, next to what it was granted.
	Capabilities []string `json:"capabilities"`
	Connections  []string `json:"connections"`
	Destinations []string `json:"destinations"`
	// DynamicConnections is true when a platform.query call computes its
	// connection instead of naming one literally, and DynamicDestinations when
	// a platform.export call computes its destination, so the list in question
	// is known to be incomplete. Reporting the gap is the point: a reviewer
	// reading a list that silently omitted a computed name would be reading a
	// false statement.
	DynamicConnections  bool `json:"dynamic_connections"`
	DynamicDestinations bool `json:"dynamic_destinations"`
}

// hasErrors reports whether any finding blocks execution.
func hasErrors(findings []Finding) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return f.Severity == SeverityError })
}

// Validate parses and resolves a script without executing it, and reports what
// it would reach. It is the fast half of the authoring loop: an author gets
// interpreter-accurate errors, the Python-isms their instincts produce get a
// specific correction, and the capability set is extracted for review — all
// without a query running or a row moving.
func Validate(source string) Report {
	report := Report{Capabilities: []string{}, Connections: []string{}, Destinations: []string{}}
	findings := scanSource(source)

	file, parseErr := fileOptions.Parse("script", source, 0)
	if parseErr != nil {
		findings = append(findings, translate(parseFindings(parseErr))...)
		sortFindings(findings)
		report.Findings = findings
		return report
	}

	found := inspect(file)
	findings = append(findings, found.findings...)
	report.Capabilities, report.Connections = found.capabilities, found.connections
	report.Destinations = found.destinations
	report.DynamicConnections = found.dynamicConnections
	report.DynamicDestinations = found.dynamicDestinations

	if _, resolveErr := starlark.FileProgram(file, isPredeclaredName); resolveErr != nil {
		findings = append(findings, translate(resolveFindings(resolveErr))...)
	}

	sortFindings(findings)
	report.Findings = findings
	report.OK = !hasErrors(findings)
	return report
}

// isPredeclaredName reports whether a name is part of the script environment.
// It is the one definition of that environment, shared by validation and by
// execution through predeclared().
func isPredeclaredName(name string) bool {
	switch name {
	case "platform", "json", "date", "run":
		return true
	default:
		return false
	}
}

// sortFindings orders findings by line so a report reads top to bottom.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
}

// parseFindings turns a parse failure into a finding. The Starlark parser stops
// at the first syntax error, so there is exactly one to report; the slice return
// keeps the shape uniform with resolveFindings, which genuinely reports many.
func parseFindings(err error) []Finding {
	var list syntax.Error
	if errors.As(err, &list) {
		return []Finding{{Severity: SeverityError, Line: int(list.Pos.Line), Message: list.Msg}}
	}
	return []Finding{{Severity: SeverityError, Message: err.Error()}}
}

// resolveFindings turns a resolver failure into findings. The resolver is where
// the dialect's deliberate restrictions surface — while, recursion, an
// undefined name — so these are the messages most in need of translation.
func resolveFindings(err error) []Finding {
	var list resolve.ErrorList
	if errors.As(err, &list) {
		out := make([]Finding, 0, len(list))
		for _, e := range list {
			out = append(out, Finding{Severity: SeverityError, Line: int(e.Pos.Line), Message: e.Msg})
		}
		return out
	}
	return []Finding{{Severity: SeverityError, Message: err.Error()}}
}

// dialectCorrection maps a fragment of an interpreter message to the hint that
// tells an author what to write instead. Keyed on a fragment rather than on
// the whole message so a wording change upstream degrades to a bare error
// rather than to a wrong hint.
var dialectCorrections = []struct {
	fragment string
	hint     string
}{
	{"does not support while loops", "Unbounded loops are disabled so a script's cost is predictable from its source. Iterate over a list with `for`, or express the repetition in SQL."},
	{"called recursively", "Recursion is disabled for the same reason as `while`. Flatten the work into a loop over a list, or do it in SQL."},
	{"undefined: ", "Only `platform`, `json`, `date`, and `run` are available, plus the Starlark built-ins. There are no imports and no standard library beyond that."},
	{`got import\b`, "There is no `import`. Query results come from `platform.query`; JSON is the predeclared `json` module; dates are the predeclared `date` module."},
	{`got (?:try|except|finally)\b`, "There is no `try`/`except`. An error fails the run by design, so the failure is visible in the run record instead of being swallowed."},
	{`got class\b`, "There are no classes. Use dicts for structured values and functions for behavior."},
	{`got with\b`, "There is no `with`. Nothing a script touches needs to be opened or closed."},
	{`got raise\b`, "There is no `raise`. `fail(\"message\")` stops the run with a message."},
	{`got yield\b`, "There are no generators. Build and return a list."},
}

// dialectPattern compiles one correction fragment as a regexp so a message with
// a variable middle ("function f called recursively") still matches.
var dialectPattern = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(dialectCorrections))
	for i, c := range dialectCorrections {
		out[i] = regexp.MustCompile(c.fragment)
	}
	return out
}()

// translate attaches a dialect correction to any finding whose message matches
// one, leaving the interpreter's own text as the message.
func translate(findings []Finding) []Finding {
	for i := range findings {
		if findings[i].Hint != "" {
			continue
		}
		for j, re := range dialectPattern {
			if re.MatchString(findings[i].Message) {
				findings[i].Hint = dialectCorrections[j].hint
				break
			}
		}
	}
	return findings
}

// sourcePattern is a lexical check run over the raw source, before parsing.
// Some of these parse cleanly and are still wrong (an f-string is a name
// followed by a string; datetime is merely an undefined name), and some make
// the parser produce a message that names a token rather than the mistake — so
// catching them on the text gives the author a specific answer either way.
type sourcePattern struct {
	re       *regexp.Regexp
	severity string
	message  string
	hint     string
}

// sourcePatterns are the author-facing lexical checks: the Python instincts an
// agent brings to a Starlark file, and the secret-shaped strings that must
// never be pasted into one.
var sourcePatterns = []sourcePattern{
	{
		re:       regexp.MustCompile(`(?m)^\s*(?:import\s+\w|from\s+\w+\s+import\b)`),
		severity: SeverityError,
		message:  "`import` is not available",
		hint:     "There is no module system. Data comes from `platform.query`; `json` and `date` are already predeclared.",
	},
	{
		re:       regexp.MustCompile(`(?m)^\s*(?:try|except|finally)\s*:`),
		severity: SeverityError,
		message:  "`try`/`except` does not exist",
		hint:     "Errors fail the run by design, so a failure is recorded rather than hidden. Check a value before using it, or stop deliberately with `fail(\"message\")`.",
	},
	{
		re:       regexp.MustCompile(`\bf"|\bf'`),
		severity: SeverityWarning,
		message:  "f-strings are not supported",
		hint:     "Use `\"total: {}\".format(n)` or `\"total: %d\" % n`.",
	},
	{
		re:       regexp.MustCompile(`\b(?:datetime|time\.time|date\.today|now\(\))`),
		severity: SeverityWarning,
		message:  "there is no clock in a script",
		hint:     "Reading a clock would make the run unreproducible. The fire time is pinned on `run.fire_time`; derive dates from it with `date.of(run.fire_time)` and the `date` helpers.",
	},
	{
		re:       regexp.MustCompile(`\brandom\.\w`),
		severity: SeverityWarning,
		message:  "there is no randomness in a script",
		hint:     "A random value would make the run unreproducible. Derive what you need from `run.run_id` or from the data itself.",
	},
	{
		re:       regexp.MustCompile(`\b(?:open|__import__)\s*\(|\bos\.\w|\brequests\.\w`),
		severity: SeverityWarning,
		message:  "there is no filesystem or network access in a script",
		hint:     "The platform is the only outside world a script has: read with `platform.query`, write with `platform.export`.",
	},
}

// secretPattern is a credential-shaped string that must never appear in script
// source. Connections are named and their credentials stay in platform
// connection config, so a literal here is either a mistake or an attempt to
// carry authority the script was never granted; either way a reviewer must see
// it before approval.
//
// Severity follows confidence. A pattern that matches a specific credential
// FORMAT (a private-key header, an AWS key id, a provider token) is an error
// and blocks the save, because nothing else looks like that. A pattern that
// matches a NAMING convention is a warning: `password = 'hunter22'` is a
// credential in Go source and an ordinary predicate inside a SQL string, and
// this scanner cannot tell those apart, so blocking would refuse to store a
// legitimate query with no way around it.
type secretPattern struct {
	re       *regexp.Regexp
	message  string
	severity string
}

// secretPatterns are matched against the raw source. They are shaped to catch
// the credential forms that actually get pasted, and each match is reported as
// an ERROR: a script carrying an inline credential does not get to run while
// someone decides whether it was serious.
var secretPatterns = []secretPattern{
	{regexp.MustCompile(`-{5}BEGIN [A-Z ]*PRIVATE KEY-{5}`), "a private key is embedded in the source", SeverityError},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "an AWS access key id is embedded in the source", SeverityError},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), "a GitHub token is embedded in the source", SeverityError},
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), "a Slack token is embedded in the source", SeverityError},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "a JSON Web Token is embedded in the source", SeverityError},
	{regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s:/@"']+:[^\s:/@"']+@`), "a URL with embedded credentials is in the source", SeverityError},
	{regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|access[_-]?token)\s*=\s*["'][^"']{6,}["']`), "a credential-shaped assignment is in the source; if this is a SQL predicate rather than a credential, it is fine", SeverityWarning},
}

// secretHint is the one corrective action for every secret finding.
const secretHint = "Credentials never belong in a script. Name a connection instead; the platform holds its credentials and authorizes the call."

// scanSource runs the lexical checks over the raw source.
func scanSource(source string) []Finding {
	var findings []Finding
	for _, p := range sourcePatterns {
		if loc := p.re.FindStringIndex(source); loc != nil {
			findings = append(findings, Finding{
				Severity: p.severity, Line: lineOf(source, loc[0]),
				Message: p.message, Hint: p.hint,
			})
		}
	}
	for _, p := range secretPatterns {
		if loc := p.re.FindStringIndex(source); loc != nil {
			findings = append(findings, Finding{
				Severity: p.severity, Line: lineOf(source, loc[0]),
				Message: p.message, Hint: secretHint,
			})
		}
	}
	return findings
}

// lineOf reports the 1-based line containing byte offset off.
func lineOf(source string, off int) int {
	if off > len(source) {
		off = len(source)
	}
	return 1 + strings.Count(source[:off], "\n")
}

// inspection is what one walk of a parsed file learns about what the script
// would reach.
type inspection struct {
	capabilities        []string
	connections         []string
	destinations        []string
	dynamicConnections  bool
	dynamicDestinations bool
	findings            []Finding
}

// inspect walks the parsed file for what the script would reach: which host
// bindings it names, which connections it queries, and where it writes.
func inspect(file *syntax.File) inspection {
	var findings []Finding
	var dynamicConnections, dynamicDestinations bool
	capSet := map[string]bool{}
	connSet := map[string]bool{}
	destSet := map[string]bool{}

	syntax.Walk(file, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		dot, ok := call.Fn.(*syntax.DotExpr)
		if !ok {
			return true
		}
		ident, ok := dot.X.(*syntax.Ident)
		if !ok || ident.Name != "platform" {
			return true
		}
		name := "platform." + dot.Name.Name
		if !slices.Contains(Capabilities, name) {
			findings = append(findings, Finding{
				Severity: SeverityError, Line: int(dot.NamePos.Line),
				Message: fmt.Sprintf("%s does not exist", name),
				Hint:    "The platform module provides exactly " + strings.Join(Capabilities, " and ") + ".",
			})
			return true
		}
		capSet[name] = true
		switch name {
		case CapabilityQuery:
			collectKeyword(call, "connection", connSet, &dynamicConnections)
		case CapabilityExport:
			if f, ok := refusePositionalDestination(call, int(dot.NamePos.Line)); ok {
				// The engine refuses the call for the same reason, so reporting
				// it here rather than reading past it keeps validate's answer
				// and the run's behavior the same answer.
				findings = append(findings, f)
				break
			}
			collectExportDestination(call, destSet, &dynamicDestinations)
		}
		return true
	})

	return inspection{
		capabilities:        sortedNames(capSet),
		connections:         sortedNames(connSet),
		destinations:        sortedNames(destSet),
		dynamicConnections:  dynamicConnections,
		dynamicDestinations: dynamicDestinations,
		findings:            findings,
	}
}

// refusePositionalDestination reports a platform.export call that passes its
// destination or key by position rather than by name.
//
// It is an error rather than a note because of what the alternative costs: this
// validator reads keyword arguments, so a positional destination would be
// invisible to it, and the review surface would state positively that a script
// writing to a bucket writes to the portal. A wrong statement in a capability
// diff is worse than no statement, so the shape that produces one is refused —
// by the engine at run time and here, in the same words.
func refusePositionalDestination(call *syntax.CallExpr, line int) (Finding, bool) {
	positional := 0
	for _, arg := range call.Args {
		if bin, ok := arg.(*syntax.BinaryExpr); ok && bin.Op == syntax.EQ {
			continue
		}
		positional++
	}
	if positional <= exportPositionalArgs {
		return Finding{}, false
	}
	return Finding{
		Severity: SeverityError, Line: line,
		Message: "platform.export takes at most three positional arguments",
		Hint: "Pass destination and key by name: platform.export(name, rows, format=\"csv\", destination=\"acme-drop\", key=\"2026/08/sales.csv\"). " +
			"Where a script writes has to be readable from its source, and a positional destination is not.",
	}, true
}

// collectExportDestination records where one platform.export call writes. A
// call naming no destination writes to the portal, which is the default the
// engine applies, so the reviewer is shown the same destination the run will
// use rather than an empty list that reads as "writes nowhere".
//
// Whether the call named one is read from the CALL, never inferred from whether
// the set grew: a second export to a destination already in the set adds
// nothing to it, and reading that as "this one defaulted" would report a portal
// write no line of the script performs — and then refuse the approval for not
// granting it.
func collectExportDestination(call *syntax.CallExpr, destSet map[string]bool, dynamic *bool) {
	if !collectKeyword(call, "destination", destSet, dynamic) {
		destSet[script.DestinationPortal] = true
	}
}

// collectKeyword records the literal string a call passes for one keyword
// argument, or marks the call as computing it. It reports whether the call
// carried the keyword at all, which is a different question from whether it
// contributed a new name.
func collectKeyword(call *syntax.CallExpr, keyword string, into map[string]bool, dynamic *bool) bool {
	for _, arg := range call.Args {
		bin, ok := arg.(*syntax.BinaryExpr)
		if !ok || bin.Op != syntax.EQ {
			continue
		}
		key, ok := bin.X.(*syntax.Ident)
		if !ok || key.Name != keyword {
			continue
		}
		lit, ok := bin.Y.(*syntax.Literal)
		if !ok {
			*dynamic = true
			return true
		}
		s, ok := lit.Value.(string)
		if !ok {
			*dynamic = true
			return true
		}
		into[s] = true
		return true
	}
	return false
}

// sortedNames returns a set's members in sorted order, never nil, so the report
// serializes as a list rather than as null.
func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
