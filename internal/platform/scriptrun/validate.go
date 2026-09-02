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
// warning means it will run but somebody should look.
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
	// Capabilities is the set of host bindings the source references, and
	// Connections the connection names it names literally, across every call.
	Capabilities []string `json:"capabilities"`
	Connections  []string `json:"connections"`
	// Destinations is where this script's OUTPUTS go: the destination names
	// platform.export writes to, plus the portal for an export that names none
	// and for every platform.publish_data. It is a statement about the output
	// surface, not about every byte the script can move — a script that writes
	// through a tool, say platform.call("s3_object", {"action": "put", ...}), produces no
	// output in this sense and is read in Tools instead.
	Destinations []string `json:"destinations"`
	// Tools is the tool names the source passes to platform.call literally,
	// sorted. It is where a reader learns the reach of the open half of the
	// surface: the persona filter decides what a run MAY call, and this says
	// what this source DOES call (#1419).
	Tools []string `json:"tools"`
	// RefreshTargets is the output names platform.publish_data refreshes, read
	// literally from the calls, so a reader sees WHICH asset's data region a
	// script rewrites.
	RefreshTargets []string `json:"refresh_targets"`
	// DynamicConnections is true when a platform.query call computes its
	// connection instead of naming one literally, DynamicDestinations when
	// a platform.export call computes its destination, and DynamicRefreshTargets
	// when a platform.publish_data call computes the name it refreshes, so the
	// list in question is known to be incomplete. Reporting the gap is the
	// point: a reader shown a list that silently omitted a computed name would
	// be reading a false statement.
	DynamicConnections    bool `json:"dynamic_connections"`
	DynamicDestinations   bool `json:"dynamic_destinations"`
	DynamicRefreshTargets bool `json:"dynamic_refresh_targets"`
	// DynamicTools is true when a platform.call computes the tool it invokes,
	// so the tool list is known to be incomplete. A call that computes its
	// ARGUMENT SET leaves the tool list intact and sets DynamicConnections
	// instead, because the connection is the only claim this report makes
	// about what is inside those arguments.
	DynamicTools bool `json:"dynamic_tools"`
	// StateUse reports whether the source reads run.state and whether it calls
	// platform.save_state (#1537), so a reader learns from the contract whether
	// a run continues from the previous run's save. Both are read from the
	// source: an access written as run.state, and the save_state member in
	// Capabilities.
	script.StateUse
}

// CheckDestinations reports each destination a source names literally that the
// deployment does not declare.
//
// Validate itself is deployment-independent — it parses source and reads what
// the code reaches — so this is a separate pass over its report, applied by the
// surfaces that know the configured set. Splitting it that way keeps a save
// working when configuration changes underneath a stored script, while the
// surface whose job is answering "would this run" answers it (#1415).
//
// It reads report.Destinations, which holds only the destinations named as
// string literals in the source. A call that computes its destination is
// invisible there and is reported by report.DynamicDestinations instead: its
// address is not readable from the source, so there is nothing to check.
//
// The refusal is ResolveDestination's, so validate and the run say the same
// thing about the same script.
func CheckDestinations(report Report, declared []script.Destination) []Finding {
	var findings []Finding
	for _, name := range report.Destinations {
		if _, err := ResolveDestination(name, declared); err != nil {
			findings = append(findings, Finding{
				Severity: SeverityError,
				Message:  err.Error(),
				Hint: "Name a destination this deployment declares, or write to " +
					script.DestinationPortal + ", which is always available. " +
					"A destination is deployment configuration (scripts.destinations), " +
					"not something the script can add.",
			})
		}
	}
	return findings
}

// WithDestinationCheck returns report with CheckDestinations' findings folded
// in and OK recomputed, which is the whole of what a validating surface does
// with them. It exists so the tool arm and the portal editor cannot fold them
// in differently.
func WithDestinationCheck(report Report, declared []script.Destination) Report {
	found := CheckDestinations(report, declared)
	if len(found) == 0 {
		return report
	}
	// Built fresh rather than appended onto the caller's slice: the two share a
	// backing array, and sorting in place would reorder findings the caller
	// still holds.
	merged := make([]Finding, 0, len(report.Findings)+len(found))
	merged = append(merged, report.Findings...)
	merged = append(merged, found...)
	sortFindings(merged)
	report.Findings = merged
	report.OK = !hasErrors(merged)
	return report
}

// hasErrors reports whether any finding blocks execution.
func hasErrors(findings []Finding) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return f.Severity == SeverityError })
}

// Validate parses and resolves a script without executing it, and reports what
// it would reach. It is the fast half of the authoring loop: an author gets
// interpreter-accurate errors, the Python-isms their instincts produce get a
// specific correction, and what the script reaches is extracted for a reader —
// all without a query running or a row moving.
func Validate(source string) Report {
	report := Report{
		Capabilities: []string{}, Connections: []string{}, Tools: []string{},
		Destinations: []string{}, RefreshTargets: []string{},
	}
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
	report.Capabilities, report.Connections = sortedNames(found.capabilities), sortedNames(found.connections)
	report.Tools = sortedNames(found.tools)
	report.Destinations = sortedNames(found.destinations)
	report.RefreshTargets = sortedNames(found.refreshTargets)
	report.DynamicConnections = found.dynamicConnections
	report.DynamicDestinations = found.dynamicDestinations
	report.DynamicRefreshTargets = found.dynamicRefreshTargets
	report.DynamicTools = found.dynamicTools
	report.StateUse = script.StateUse{Reads: found.readsState, Saves: found.capabilities[CapabilitySaveState]}

	if _, resolveErr := starlark.FileProgram(file, isPredeclaredName); resolveErr != nil {
		findings = append(findings, translate(resolveFindings(resolveErr))...)
	}

	sortFindings(findings)
	report.Findings = findings
	report.OK = !hasErrors(findings)
	return report
}

// isPredeclaredName reports whether a name is part of the script environment.
// It answers from PredeclaredNames, which is also what predeclared() binds, so
// a name resolves here exactly when a run can call it.
func isPredeclaredName(name string) bool {
	return slices.Contains(PredeclaredNames, name)
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

// undefinedNameHint lists the environment for an author who reached for a name
// that is not in it. It is composed from PredeclaredNames rather than written
// out, so a global the platform adds or drops cannot leave the hint naming a
// set the resolver disagrees with.
var undefinedNameHint = fmt.Sprintf(
	"Only %s are available, plus the Starlark built-ins. There are no imports and no standard library beyond that.",
	quotedList(PredeclaredNames))

// quotedList renders names as a backticked English list ("`a`, `b`, and `c`").
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "`"+n+"`")
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
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
	{"undefined: ", undefinedNameHint},
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
		hint:     "The platform is the only outside world a script has: read with `platform.query`, write with `platform.export`, and reach anything else — an external API, an object store, another tool — with `platform.call(tool, args)` through a configured connection.",
	},
}

// secretPattern is a credential-shaped string that must never appear in script
// source. Connections are named and their credentials stay in platform
// connection config, so a literal here is either a mistake or an attempt to
// carry authority the script was never given; either way it must be surfaced
// before the source is stored.
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
// would reach. Each set is accumulated as the walk proceeds and rendered in
// sorted order by sortedNames.
type inspection struct {
	capabilities          map[string]bool
	connections           map[string]bool
	destinations          map[string]bool
	refreshTargets        map[string]bool
	tools                 map[string]bool
	dynamicConnections    bool
	dynamicDestinations   bool
	dynamicRefreshTargets bool
	dynamicTools          bool
	// readsState is set by any run.state access. A script that reads the
	// record some other way (getattr(run, "state")) is not seen, which
	// understates its use; the save side is a call and is always seen.
	readsState bool
	findings   []Finding
}

// inspect walks the parsed file for what the script would reach: which members
// of the platform module it names, which tools and connections it calls, and
// where it writes.
func inspect(file *syntax.File) *inspection {
	ins := &inspection{
		capabilities: map[string]bool{}, connections: map[string]bool{},
		destinations: map[string]bool{}, refreshTargets: map[string]bool{},
		tools: map[string]bool{},
	}
	syntax.Walk(file, func(n syntax.Node) bool {
		if call, dot, ok := platformCall(n); ok {
			ins.visit(call, dot)
		}
		if isRunStateRead(n) {
			ins.readsState = true
		}
		return true
	})
	return ins
}

// isRunStateRead recognizes run.state, the one read of the state a script
// carries between runs.
func isRunStateRead(n syntax.Node) bool {
	dot, ok := n.(*syntax.DotExpr)
	if !ok || dot.Name.Name != "state" {
		return false
	}
	ident, ok := dot.X.(*syntax.Ident)
	return ok && ident.Name == "run"
}

// platformCall recognizes a call on the platform module and reports the call
// with the member selection that named it.
func platformCall(n syntax.Node) (*syntax.CallExpr, *syntax.DotExpr, bool) {
	call, ok := n.(*syntax.CallExpr)
	if !ok {
		return nil, nil, false
	}
	dot, ok := call.Fn.(*syntax.DotExpr)
	if !ok {
		return nil, nil, false
	}
	ident, ok := dot.X.(*syntax.Ident)
	if !ok || ident.Name != "platform" {
		return nil, nil, false
	}
	return call, dot, true
}

// visit records what one platform.* call reaches, or refuses a member the
// module does not have.
func (ins *inspection) visit(call *syntax.CallExpr, dot *syntax.DotExpr) {
	name := "platform." + dot.Name.Name
	if !slices.Contains(Capabilities, name) {
		ins.findings = append(ins.findings, Finding{
			Severity: SeverityError, Line: int(dot.NamePos.Line),
			Message: fmt.Sprintf("%s does not exist", name),
			Hint: "The platform module has " + strings.Join(Capabilities, ", ") + ". " +
				"Any other tool is called by name through " + CapabilityCall + "(tool, args).",
		})
		return
	}
	ins.capabilities[name] = true
	if hasStarArg(call) {
		ins.unreadable(name)
		return
	}
	switch name {
	case CapabilityQuery:
		collectKeyword(call, "connection", ins.connections, &ins.dynamicConnections)
	case CapabilityExport:
		ins.visitExport(call, int(dot.NamePos.Line))
	case CapabilityPublishData:
		// A refresh writes to the portal and nowhere else, so the call
		// contributes the portal to the destination list a reader sees.
		ins.destinations[script.DestinationPortal] = true
		collectFirstOrKeyword(call, "name", ins.refreshTargets, &ins.dynamicRefreshTargets)
	case CapabilityCall:
		ins.visitCall(call)
	}
}

// hasStarArg reports whether a call spreads a computed list or dict into its
// arguments (f(*args) or f(**kwargs)), which the Starlark AST represents as a
// unary STAR or STARSTAR.
//
// Every collector below reads arguments by position or by keyword, and a
// spread has neither: the values are in a variable. A call carrying one is
// therefore not readable at all, and reading past it would let
// platform.export(**cfg) be reported as a portal write while cfg names a
// bucket — the same false statement refusePositionalDestination exists to
// prevent.
func hasStarArg(call *syntax.CallExpr) bool {
	for _, arg := range call.Args {
		if un, ok := arg.(*syntax.UnaryExpr); ok && (un.Op == syntax.STAR || un.Op == syntax.STARSTAR) {
			return true
		}
	}
	return false
}

// unreadable records that one platform.* call carried arguments this validator
// cannot read, marking every list that member would otherwise have contributed
// to as incomplete rather than reporting a shorter list as a complete one.
func (ins *inspection) unreadable(name string) {
	switch name {
	case CapabilityQuery:
		ins.dynamicConnections = true
	case CapabilityExport:
		ins.dynamicDestinations = true
	case CapabilityPublishData:
		// A refresh writes to the portal whatever its arguments say, so the
		// destination is still a fact; only the target name is unreadable.
		ins.destinations[script.DestinationPortal] = true
		ins.dynamicRefreshTargets = true
	case CapabilityCall:
		ins.dynamicTools = true
		ins.dynamicConnections = true
	}
}

// visitExport records where one platform.export call writes.
func (ins *inspection) visitExport(call *syntax.CallExpr, line int) {
	if f, ok := refusePositionalDestination(call, line); ok {
		// The engine refuses the call for the same reason, so reporting it here
		// rather than reading past it keeps validate's answer and the run's
		// behavior the same answer.
		ins.findings = append(ins.findings, f)
		return
	}
	collectExportDestination(call, ins.destinations, &ins.dynamicDestinations)
}

// visitCall records the tool one platform.call invokes and, when its argument
// set is a dict the source writes out, the connection that call names.
//
// The connection matters as much as the tool: the picker a caller chooses a
// connection parameter from and the reader deciding whether a script's reach is
// acceptable both read the connection list, and a generic call naming one is
// exactly as much a use of that connection as platform.query naming it.
func (ins *inspection) visitCall(call *syntax.CallExpr) {
	collectFirstOrKeyword(call, "tool", ins.tools, &ins.dynamicTools)
	args, present := callArgsExpr(call)
	if !present {
		// A call with no argument set names no connection. That is a fact about
		// the source, not a gap in this read.
		return
	}
	dict, ok := args.(*syntax.DictExpr)
	if !ok {
		// The arguments were computed, so a connection named inside them is
		// unreadable. The TOOL is unaffected — it may well have been a literal,
		// and reporting the tool list as short because the arguments were not
		// would be a second false statement in place of the one this reports.
		ins.dynamicConnections = true
		return
	}
	collectDictEntry(dict, "connection", ins.connections, &ins.dynamicConnections)
}

// callArgsExpr returns the argument-set expression of a platform.call, whether
// it was passed as args= or as the second positional argument, and reports
// whether the call carried one at all.
func callArgsExpr(call *syntax.CallExpr) (syntax.Expr, bool) {
	positional := 0
	for _, arg := range call.Args {
		if bin, ok := arg.(*syntax.BinaryExpr); ok && bin.Op == syntax.EQ {
			if key, ok := bin.X.(*syntax.Ident); ok && key.Name == "args" {
				return bin.Y, true
			}
			continue
		}
		positional++
		if positional == callArgsPosition {
			return arg, true
		}
	}
	return nil, false
}

// collectDictEntry records the literal string a dict literal holds under one
// key, or marks the read as incomplete when the key is present with a computed
// value. A key the dict does not carry contributes nothing: the call does not
// name one.
func collectDictEntry(dict *syntax.DictExpr, key string, into map[string]bool, dynamic *bool) {
	for _, item := range dict.List {
		entry, ok := item.(*syntax.DictEntry)
		if !ok {
			continue
		}
		lit, ok := entry.Key.(*syntax.Literal)
		if !ok {
			// A COMPUTED key might evaluate to the one being looked for, and
			// this read cannot know that it does not. Reading past it would
			// state positively that the call names no connection while the run
			// reaches one, which is the completeness claim this report exists
			// to keep honest.
			*dynamic = true
			continue
		}
		name, ok := lit.Value.(string)
		if !ok {
			// A non-string literal key is definitively not this key: the keys
			// a tool call takes are strings. Nothing is hidden by it.
			continue
		}
		if name != key {
			continue
		}
		value, ok := entry.Value.(*syntax.Literal)
		if !ok {
			*dynamic = true
			return
		}
		s, ok := value.Value.(string)
		if !ok {
			*dynamic = true
			return
		}
		into[s] = true
		return
	}
}

// collectFirstOrKeyword records the literal string a call passes for a keyword
// whose value may also be given as the call's FIRST positional argument — the
// output name platform.publish_data refreshes, the tool platform.call invokes —
// or marks the call as computing it.
func collectFirstOrKeyword(call *syntax.CallExpr, keyword string, into map[string]bool, dynamic *bool) {
	if collectKeyword(call, keyword, into, dynamic) {
		return
	}
	for _, arg := range call.Args {
		if isKeywordArg(arg) {
			continue
		}
		if lit, ok := arg.(*syntax.Literal); ok {
			if s, ok := lit.Value.(string); ok {
				into[s] = true
				return
			}
		}
		*dynamic = true
		return
	}
	// No value at all: the interpreter refuses the call as a missing argument,
	// so there is nothing here to report.
}

// isKeywordArg reports whether one call argument is a keyword argument, which
// the Starlark AST represents as a BinaryExpr with Op EQ. It is the one
// spelling of that convention for every collector that walks call arguments.
func isKeywordArg(arg syntax.Expr) bool {
	bin, ok := arg.(*syntax.BinaryExpr)
	return ok && bin.Op == syntax.EQ
}

// refusePositionalDestination reports a platform.export call that passes its
// destination or key by position rather than by name.
//
// It is an error rather than a note because of what the alternative costs:
// this validator reads keyword arguments, so a positional destination would be
// invisible to it, and the surface reporting what a script reaches would state
// positively that a script writing to a bucket writes to the portal. A wrong
// statement there is worse than no statement, so the shape that produces one
// is refused — by the engine at run time and here, in the same words.
func refusePositionalDestination(call *syntax.CallExpr, line int) (Finding, bool) {
	positional := 0
	for _, arg := range call.Args {
		if isKeywordArg(arg) {
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
// engine applies, so the reader is shown the same destination the run will
// use rather than an empty list that reads as "writes nowhere".
//
// Whether the call named one is read from the CALL, never inferred from
// whether the set grew: a second export to a destination already in the set
// adds nothing to it, and reading that as "this one defaulted" would report a
// portal write no line of the script performs.
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
