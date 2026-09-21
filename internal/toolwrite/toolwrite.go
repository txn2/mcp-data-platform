// Package toolwrite answers one question about one platform tool call: does it
// persist something that outlives the call?
//
// The question has one caller today — the write barrier a managed script's
// draft run puts in front of platform.call (#1664) — and one property that
// makes it worth its own package: it is decided by a declared table rather than
// inferred, so what a draft will and will not do is readable in one file.
//
// # Deny by default
//
// A tool no rule here names is reported as writing. The alternative fails in
// the one direction that matters: a classifier that guesses "read" on a tool it
// has never seen lets a draft land data, which is the defect this exists to
// close. Guessing "write" costs an author a refusal that names the tool and
// tells them how to proceed.
//
// # Why a table and not the MCP annotations
//
// MCP carries ReadOnlyHint, and where a toolkit sets it the value here agrees
// with it. It cannot be the source, for two reasons. Most of the platform's
// tools set no annotation at all, so reading them would classify most of the
// surface as unknown. And the platform's management surface is action-based —
// one tool name covering manage_resource create and manage_table list — so a
// hint attached to a tool cannot separate the half that writes from the half
// that does not. The unit of classification is the call, not the tool.
//
// TestEveryRegisteredToolIsClassified (test/structure) refuses a tool the
// platform registers that no rule here names, so the table cannot quietly go
// stale behind a new toolkit.
//
// # What "writes" means
//
// A call writes when it changes what a later reader of the platform sees: a
// stored record, a registration, a file, an upstream resource. The machinery
// every call produces — an audit row, a session's discovery state, a metric —
// is not a write in this sense. It is produced by refused calls too, and a
// deployment that counted it would have no read-only surface at all.
package toolwrite

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
)

// Decision is what the classifier concluded about one call.
type Decision struct {
	// Writes reports whether the call may persist something that outlives it.
	Writes bool
	// Call names the call the way a refusal names it: the tool, plus the
	// action or command that decided it where the tool has one.
	Call string
	// Declared reports whether this call's class was actually decided, rather
	// than fallen back to. A caller distinguishes them because the two
	// refusals ask for different things: a declared write asks whether the
	// author meant to persist, an undeclared one says the platform cannot tell.
	// It is false both for a tool no rule names and for a call whose rule could
	// not read the argument it decides on.
	Declared bool
}

// MethodResolver reports the HTTP method an api_invoke_endpoint operation_id is
// invoked with. The api gateway holds the parsed specs that answer it, so the
// composition root supplies this; a caller with none leaves the operation_id
// form unresolved, and it is then classified as a write like anything else the
// table cannot read.
type MethodResolver func(connection, spec, operationID string) (method string, ok bool)

// Classifier decides one call's class. The zero value works: it classifies the
// operation_id form of an api gateway call, and every tool no rule here names,
// as a write. A MethodResolver is how a caller holding the live api connections
// narrows the first of those.
type Classifier struct {
	// ResolveMethod resolves an api_invoke_endpoint operation_id to its method.
	ResolveMethod MethodResolver
}

// Classify reports whether calling tool with args persists anything.
func (c Classifier) Classify(tool string, args map[string]any) Decision {
	name := strings.TrimSpace(tool)
	if readOnlyTools[name] {
		return Decision{Call: name, Declared: true}
	}
	if writeTools[name] {
		return Decision{Writes: true, Call: name, Declared: true}
	}
	if rule, ok := actionTools[name]; ok {
		return rule.classify(name, args)
	}
	switch name {
	case ToolInvokeEndpoint:
		return c.classifyInvoke(name, args)
	case ToolGraphQLQuery:
		return classifyGraphQL(name, args)
	}
	return Decision{Writes: true, Call: name}
}

// Tool names this package rules on by something other than a fixed action set.
// They are literals rather than imports because a classifier that pulled in a
// toolkit to read one constant would depend on the whole toolkit, and the
// structural gate already fails when a registered name has no rule.
const (
	// ToolInvokeEndpoint is the api gateway's HTTP call, classified by the
	// method it sends.
	ToolInvokeEndpoint = "api_invoke_endpoint"
	// ToolGraphQLQuery is the graphql kind's document call, classified by
	// whether the operation that will execute is a mutation.
	ToolGraphQLQuery = "graphql_query"
)

// readOnlyTools is every tool whose every call persists nothing.
//
// The list is deliberately literal. Each entry is a promise about a specific
// tool, and a pattern would extend that promise to names nobody has read.
var readOnlyTools = map[string]bool{
	// The platform's own orientation surface.
	"platform_info":       true,
	"list_connections":    true,
	"platform_find_tools": true,
	"show_prompts":        true,
	"show_scripts":        true,
	// Discovery.
	"search": true,
	"fetch":  true,
	// Trino. trino_execute and trino_export are absent: the first is the DDL
	// and DML tool, the second lands an asset.
	"trino_query":          true,
	"trino_explain":        true,
	"trino_browse":         true,
	"trino_describe_table": true,
	// Object storage. s3_object is action-based and ruled on below.
	"s3_list": true,
	// The metadata catalog. datahub_create, datahub_update and datahub_delete
	// are absent by their names.
	"datahub_browse":      true,
	"datahub_get_lineage": true,
	// The two catalog reads. Their siblings api_invoke_endpoint and
	// graphql_query are ruled on by what they send.
	"api_discover":     true,
	"graphql_discover": true,
}

// writeTools is every tool whose every call persists something. They are named
// rather than left to the fallback so a refusal can say the platform knows what
// this call does, which is a different sentence from the one an unrecognized
// tool gets.
var writeTools = map[string]bool{
	// The asset library's write half.
	"save_asset": true,
	// The knowledge loop's two writes: an insight applied to a sink, and a
	// record captured into memory.
	"apply_knowledge": true,
	"memory_capture":  true,
	// Executing a saved script is the write everything else here guards: it
	// runs the platform version, with its own outputs and its own state.
	"run_script": true,
	// Trino's DDL and DML tool, and the three export tools, each of which
	// lands an asset or an object.
	"trino_execute":  true,
	"trino_export":   true,
	"api_export":     true,
	"graphql_export": true,
	// The metadata catalog's writes.
	"datahub_create": true,
	"datahub_update": true,
	"datahub_delete": true,
}

// actionRule classifies one tool by the value of one argument.
type actionRule struct {
	// arg is the argument carrying the action or command.
	arg string
	// reads is the value set that persists nothing. Every other value —
	// including an absent, empty or non-string one — writes, which is the
	// deny-by-default rule applied within a tool.
	reads map[string]bool
}

// classify applies one action rule.
func (r actionRule) classify(tool string, args map[string]any) Decision {
	action, _ := args[r.arg].(string)
	action = strings.TrimSpace(action)
	call := tool
	if action != "" {
		call = tool + " " + r.arg + "=" + action
	}
	return Decision{Writes: !r.reads[action], Call: call, Declared: true}
}

// actionTools is the platform's action-based surface: one tool name covering a
// set of verbs, of which some read.
//
// Each entry lists the READ half, so a verb added to one of these tools is
// classified as a write until somebody reads it and says otherwise. That is the
// same default the package as a whole takes, applied one level down, and it is
// why a new manage_asset action cannot silently start persisting inside drafts.
var actionTools = map[string]actionRule{
	// manage_asset: the asset library's content, versions, shares and
	// collections. Its content verbs (locate, get_content, outline, stats,
	// diff) read; patch writes.
	"manage_asset": {arg: "action", reads: set(
		"list", "get", "list_versions", "search", "provenance",
		"locate", "get_content", "outline", "stats", "diff",
		"list_shares", "list_collections", "get_collection",
	)},
	// manage_table: the tables registered over a managed file.
	"manage_table": {arg: "action", reads: set("list")},
	// manage_resource: both its actions write. It is listed rather than left to
	// the fallback so the refusal names the action, and so the day a read
	// action is added the rule is already here to hold it.
	"manage_resource": {arg: "action", reads: nil},
	// manage_feedback: reply, resolve and the two validation verbs write.
	"manage_feedback": {arg: "action", reads: set("list", "get")},
	// memory_manage: update, forget and consolidate write; the three listings
	// read. An empty command renders the tool's help, which persists nothing.
	"memory_manage": {arg: "command", reads: set(
		"list", "review_stale", "review_duplicates", "",
	)},
	// manage_prompt: create, update, delete, patch and the two script
	// attachments write. use renders a stored prompt and writes nothing.
	"manage_prompt": {arg: "command", reads: set(
		"list", "get", "use", "locate", "get_content", "outline", "stats", "diff",
	)},
	// manage_script: authoring, scheduling and the state verb write. run_draft
	// is absent because a run may not start another run at all, which
	// scriptlayer refuses on its own before this rule is consulted.
	"manage_script": {arg: "command", reads: set(
		"get", "list", "validate", "help",
		"locate", "get_content", "outline", "stats", "diff",
		"versions", "runs", "get_run", "schedule_list",
	)},
	// s3_object: put, copy and delete write. presign mints a URL against the
	// object store and stores nothing.
	"s3_object": {arg: "action", reads: set("get", "metadata", "presign")},
	// notify: list reads. send and publish leave a message in somebody else's
	// chat client or inbox, which nothing can take back -- the most literal
	// write on this list, whatever it does to the platform's own tables.
	"notify": {arg: "action", reads: set("list")},
}

// readMethods are the HTTP methods that read. PROPFIND is WebDAV's read, and
// belongs here for the same reason GET does; MKCOL, MOVE and COPY are its
// writes and are absent.
var readMethods = map[string]bool{"GET": true, "HEAD": true, "PROPFIND": true}

// classifyInvoke classifies an api gateway call by the method it sends.
//
// A call addressed by method+path carries its own answer. One addressed by
// operation_id carries an id whose method lives in the connection's parsed
// spec, which is why a resolver exists: without one the call is a write, and an
// author drafting a read-only pull would be refused for addressing it the way
// api_discover told them to.
func (c Classifier) classifyInvoke(tool string, args map[string]any) Decision {
	method := strings.ToUpper(strings.TrimSpace(str(args["method"])))
	if method == "" && c.ResolveMethod != nil {
		if id := strings.TrimSpace(str(args["operation_id"])); id != "" {
			if resolved, ok := c.ResolveMethod(str(args["connection"]), str(args["spec"]), id); ok {
				method = strings.ToUpper(strings.TrimSpace(resolved))
			}
		}
	}
	if method == "" {
		// The addressing named an operation nothing here could resolve to a
		// method. That is not a judgment about the call, so it is reported the
		// way an unrecognized tool is: the platform cannot tell.
		return Decision{Writes: true, Call: tool}
	}
	return Decision{Writes: !readMethods[method], Call: tool + " " + method, Declared: true}
}

// classifyGraphQL classifies a graphql call by the operation that will execute.
//
// A document the platform cannot parse is a write: graphql_query would refuse
// it too, and a barrier that let an unparseable document through on the grounds
// that it could not read it would be classifying by its own failure.
func classifyGraphQL(tool string, args map[string]any) Decision {
	doc, err := gqlschema.Parse(str(args["query"]), strings.TrimSpace(str(args["operation_name"])))
	if err != nil {
		return Decision{Writes: true, Call: tool, Declared: true}
	}
	kind := doc.Kind()
	return Decision{
		Writes:   kind == gqlschema.OperationMutation,
		Call:     tool + " " + string(kind),
		Declared: true,
	}
}

// str reads a string argument, yielding "" for an absent or differently typed
// one. A tool call's arguments arrive as JSON, so a caller can put anything
// under a key; a non-string where a string belongs is not this package's
// refusal to write, it is the tool's.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// set builds a lookup from its arguments.
func set(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

// ReadOnly reports whether EVERY call to a tool persists nothing, which is
// what an MCP client reads readOnlyHint to learn. It is the per-tool half of
// this package's per-call question: a tool whose actions split (manage_asset,
// s3_object) is not read-only however the one call in hand is classified,
// because the annotation describes the tool and not the call (#1692).
//
// The registration sites advertise the hint and a structural gate holds the two
// together, so a tool classified here and annotated otherwise fails the build
// rather than telling a client the opposite of what a draft run believes.
func ReadOnly(tool string) bool {
	return readOnlyTools[strings.TrimSpace(tool)]
}

// Classified reports whether any rule here names the tool, whatever it decides
// about a given call. The structural gate asks this of every tool the platform
// registers; a caller deciding one call asks Classify.
func Classified(tool string) bool {
	name := strings.TrimSpace(tool)
	if readOnlyTools[name] {
		return true
	}
	if writeTools[name] {
		return true
	}
	if _, ok := actionTools[name]; ok {
		return true
	}
	return name == ToolInvokeEndpoint || name == ToolGraphQLQuery
}
