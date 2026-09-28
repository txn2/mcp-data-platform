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

	"github.com/txn2/mcp-data-platform/internal/platform/exportrefs"
	"github.com/txn2/mcp-data-platform/internal/platform/exporttable"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlex"
	"github.com/txn2/mcp-data-platform/internal/scriptconst"
	"github.com/txn2/mcp-data-platform/internal/scriptreserved"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Finding severities. An error means the script cannot run as written; a
// warning means it will run but somebody should look.
const (
	SeverityError   = scriptlex.SeverityError
	SeverityWarning = scriptlex.SeverityWarning
)

// Finding is one thing the validator noticed, addressed to the author.
type Finding struct {
	// Rule names the authoring gate a finding comes from (internal/platform/
	// scriptlint, #1913), empty for the validator's own findings.
	Rule     string `json:"rule,omitempty" example:"cyclomatic-complexity"`
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

	file, parseErr := scriptdialect.Options.Parse("script", source, 0)
	if parseErr != nil {
		findings = append(findings, translate(parseFindings(source, parseErr))...)
		sortFindings(findings)
		report.Findings = findings
		return report
	}

	// Resolved before the walk: which identifiers name a module constant is the
	// resolver's answer (internal/scriptconst), and the walk reads it.
	_, resolveErr := starlark.FileProgram(file, isPredeclaredName)
	found := inspect(file, scriptconst.Collect(file))
	findings = append(findings, found.findings...)
	for _, lit := range exportrefs.InSource(file, strings.TrimPrefix(CapabilityExport, "platform."), strings.TrimPrefix(CapabilityCall, "platform.")) {
		findings = append(findings, Finding{Severity: SeverityWarning, Line: lit.Line, Message: lit.Message(), Hint: lit.Hint()})
	}
	report.Capabilities, report.Connections = sortedNames(found.capabilities), sortedNames(found.connections)
	report.Tools = sortedNames(found.tools)
	report.Destinations = sortedNames(found.destinations)
	report.RefreshTargets = sortedNames(found.refreshTargets)
	report.DynamicConnections = found.dynamicConnections
	report.DynamicDestinations = found.dynamicDestinations
	report.DynamicRefreshTargets = found.dynamicRefreshTargets
	report.DynamicTools = found.dynamicTools
	report.StateUse = script.StateUse{Reads: found.readsState, Saves: found.capabilities[CapabilitySaveState]}

	if resolveErr != nil {
		findings = append(findings, translate(resolveFindings(resolveErr))...)
	}

	sortFindings(findings)
	report.Findings = findings
	report.OK = !hasErrors(findings)
	return report
}

// Parse parses and resolves source under the script dialect, for a reader of
// a script's syntax tree (scriptdialect.Parse).
func Parse(source string) (*syntax.File, error) {
	return scriptdialect.Parse(source, isPredeclaredName) //nolint:wrapcheck // the dialect's own error
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
// A reserved word used as a name is reported by the word, since the parser's
// message names neither the word nor the mistake (#1823).
func parseFindings(source string, err error) []Finding {
	var list syntax.Error
	if errors.As(err, &list) {
		if word, ok := scriptreserved.Misused(source, list); ok && !hasDialectCorrection(list.Msg) {
			return []Finding{reservedNameFinding(word, int(list.Pos.Line))}
		}
		return []Finding{{Severity: SeverityError, Line: int(list.Pos.Line), Message: list.Msg}}
	}
	return []Finding{{Severity: SeverityError, Message: err.Error()}}
}

// reservedNameFinding names a reserved word used as a name, with the rename and
// the whole list, since the author may be about to pick another one.
func reservedNameFinding(word string, line int) Finding {
	return Finding{
		Severity: SeverityError, Line: line,
		Message: fmt.Sprintf("`%s` is a reserved word in Starlark and cannot be used as a name", word),
		Hint: fmt.Sprintf("Rename it, for example to `%s_rows`. No function, parameter, variable or attribute can be named %s.",
			word, strings.Join(scriptreserved.Words(), ", ")),
	}
}

// hasDialectCorrection reports whether a message already has a correction of
// its own: "got class, want primary expression" is a class statement far more
// often than a variable named class, and its hint says what to write instead.
func hasDialectCorrection(msg string) bool {
	return slices.ContainsFunc(dialectPattern, func(re *regexp.Regexp) bool { return re.MatchString(msg) })
}

// resolveFindings turns a resolver failure into findings. The resolver is where
// the dialect's deliberate restrictions surface — while, an undefined name —
// so these are the messages most in need of translation. Recursion is not
// among them: the interpreter refuses a recursive call when it is made.
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
	{`got import\b`, "There is no `import`. Query results come from `platform.query`; JSON is the predeclared `json` module, XML the `xml` module; dates are the predeclared `date` module."},
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

// scanSource runs the lexical checks (internal/platform/scriptlex) and
// reports them as validation findings.
func scanSource(source string) []Finding {
	matches := scriptlex.Scan(source)
	findings := make([]Finding, 0, len(matches))
	for _, m := range matches {
		findings = append(findings, Finding{Severity: m.Severity, Line: m.Line, Message: m.Message, Hint: m.Hint})
	}
	return findings
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
	// consts is the module constants a call may name its connection, tool or
	// destination through, read as the value they hold.
	consts scriptconst.Table
}

// inspect walks the parsed file for what the script would reach: which members
// of the platform module it names, which tools and connections it calls, and
// where it writes. A value named through a module constant is read as the
// constant's value: WAREHOUSE = "warehouse" and connection=WAREHOUSE name the
// warehouse connection as plainly as the literal does (#1906).
func inspect(file *syntax.File, consts scriptconst.Table) *inspection {
	ins := &inspection{
		capabilities: map[string]bool{}, connections: map[string]bool{},
		destinations: map[string]bool{}, refreshTargets: map[string]bool{},
		tools: map[string]bool{}, consts: consts,
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
		ins.collectKeyword(call, "connection", ins.connections, &ins.dynamicConnections)
	case CapabilityExport:
		ins.visitExport(call, int(dot.NamePos.Line))
	case CapabilityPublishData:
		// A refresh writes to the portal and nowhere else, so the call
		// contributes the portal to the destination list a reader sees.
		ins.destinations[script.DestinationPortal] = true
		ins.collectFirstOrKeyword(call, "name", ins.refreshTargets, &ins.dynamicRefreshTargets)
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
	ins.collectExportDestination(call, ins.destinations, &ins.dynamicDestinations)
	if exportrefs.Declares(call) { // a manage_asset call, as register= below is manage_table (#1834)
		ins.tools[exportrefs.Tool] = true
	}
	// register= is a manage_table call on the connection it names (#1820), and
	// the reader of this report is owed it as surely as a platform.call's.
	for _, arg := range call.Args {
		if bin, ok := arg.(*syntax.BinaryExpr); ok && bin.Op == syntax.EQ {
			if key, ok := bin.X.(*syntax.Ident); ok && key.Name == "register" {
				ins.tools[exporttable.Tool] = true
				if dict, ok := bin.Y.(*syntax.DictExpr); ok {
					ins.collectDictEntry(dict, "connection", ins.connections, &ins.dynamicConnections)
				} else {
					ins.dynamicConnections = true
				}
			}
		}
	}
}

// visitCall records the tool one platform.call invokes and, when its argument
// set is a dict the source writes out, the connection that call names.
//
// The connection matters as much as the tool: the picker a caller chooses a
// connection parameter from and the reader deciding whether a script's reach is
// acceptable both read the connection list, and a generic call naming one is
// exactly as much a use of that connection as platform.query naming it.
func (ins *inspection) visitCall(call *syntax.CallExpr) {
	ins.collectFirstOrKeyword(call, "tool", ins.tools, &ins.dynamicTools)
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
	ins.collectDictEntry(dict, "connection", ins.connections, &ins.dynamicConnections)
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
func (ins *inspection) collectDictEntry(dict *syntax.DictExpr, key string, into map[string]bool, dynamic *bool) {
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
		s, ok := ins.consts.String(entry.Value)
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
func (ins *inspection) collectFirstOrKeyword(call *syntax.CallExpr, keyword string, into map[string]bool, dynamic *bool) {
	if ins.collectKeyword(call, keyword, into, dynamic) {
		return
	}
	for _, arg := range call.Args {
		if isKeywordArg(arg) {
			continue
		}
		if s, ok := ins.consts.String(arg); ok {
			into[s] = true
			return
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
func (ins *inspection) collectExportDestination(call *syntax.CallExpr, destSet map[string]bool, dynamic *bool) {
	if !ins.collectKeyword(call, "destination", destSet, dynamic) {
		destSet[script.DestinationPortal] = true
	}
}

// collectKeyword records the literal string a call passes for one keyword
// argument, or marks the call as computing it. It reports whether the call
// carried the keyword at all, which is a different question from whether it
// contributed a new name.
func (ins *inspection) collectKeyword(call *syntax.CallExpr, keyword string, into map[string]bool, dynamic *bool) bool {
	for _, arg := range call.Args {
		bin, ok := arg.(*syntax.BinaryExpr)
		if !ok || bin.Op != syntax.EQ {
			continue
		}
		key, ok := bin.X.(*syntax.Ident)
		if !ok || key.Name != keyword {
			continue
		}
		s, ok := ins.consts.String(bin.Y)
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
