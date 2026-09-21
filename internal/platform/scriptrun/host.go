package scriptrun

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/scriptdate"
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/script"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// toolQuery is the tool platform.query names. Every host binding issues an
// ordinary platform tool call over the run's MCP session — the named helpers
// with a constant here, platform.call with the name the author wrote — so a
// script's query is authorized, rate limited, and audited by exactly the
// middleware an agent's query goes through.
const toolQuery = "trino_query"

// Member names of the platform module.
//
// Three of them are named helpers over one tool call each, kept because they
// carry behavior worth a name: the typed parameter binding on query, the format
// and destination resolution on export, the structural splice on publish_data.
// CapabilityCall is the same mechanism with the tool name left to the author,
// and it is what makes the surface open.
//
// The set is not access control. What a script may reach is what its author's
// persona authorizes, decided by the persona filter at every call, at run time
// (#1419).
const (
	CapabilityQuery  = "platform.query"
	CapabilityExport = "platform.export"
	// CapabilityPublishData replaces the data region of a portal document this
	// script already publishes, and touches nothing else in it. It is separate
	// from CapabilityExport, whose document arm composes whole documents,
	// because the two describe different behavior a reader should be able to
	// tell apart.
	CapabilityPublishData = "platform.publish_data"
	// CapabilityCall invokes any platform tool by name. It resolves to the same
	// Caller the three helpers resolve to, so a tool the run's persona may not
	// call is refused by the middleware in the middleware's own words, exactly
	// as it refuses an agent.
	CapabilityCall = "platform.call"
	// CapabilitySaveState replaces the script's one object of state (#1537).
	// It is not a tool call: the object is staged on the run and applied by
	// the platform when the run succeeds, in the write that marks it so, with
	// a compare-and-set on the revision the run read.
	CapabilitySaveState = "platform.save_state"
	// CapabilityNotify posts a message to a channel an administrator
	// configured (#1723). It is a named helper over one notify action, kept
	// because a monitor that posts what it found is what most scripts that
	// post are, and because the link back to the run is supplied here rather
	// than composed by every script that wants one.
	CapabilityNotify = "platform.notify"
	// CapabilityPublish posts a portal asset this script wrote to a channel,
	// naming the asset the way platform.export named it.
	CapabilityPublish = "platform.publish"
)

// Capabilities is the full member set of the platform module, in the order help
// and validate report it.
//
// It is a report ordering and the spelling check behind validate's "no such
// member" refusal. It is not a boundary: platform.call reaches every tool the
// run's persona authorizes, and what a script reaches is read from the source
// by Validate, which reports the tool names it names.
var Capabilities = []string{CapabilityQuery, CapabilityExport, CapabilityPublishData, CapabilityCall, CapabilitySaveState, CapabilityNotify, CapabilityPublish}

// The formats platform.export accepts, split by what serializes them. A format
// may appear in both sets: markdown and text are sometimes a table computed
// from rows and sometimes a document the script composed itself.
var (
	// rowFormats serialize a list of row dicts, matched to the formats
	// trino_export already writes so the contract does not change when preview
	// becomes persistence. csv and json are ONLY here: a data feed another
	// system parses stays well-formed by construction.
	rowFormats = map[string]bool{"csv": true, "json": true, "markdown": true, "text": true}
	// documentFormats accept a string body written verbatim. html and jsx are
	// ONLY here: they have no tabular serialization, and they map to the
	// content types the portal already stores and renders for saved assets, so
	// a script-published dashboard is patchable like any other document asset.
	documentFormats = map[string]bool{"markdown": true, "text": true, "html": true, "jsx": true}
	// exportFormats is their union, for the unknown-format refusal, merged
	// from the two sets the arms are checked against so it cannot drift.
	exportFormats = func() map[string]bool {
		out := maps.Clone(rowFormats)
		maps.Copy(out, documentFormats)
		return out
	}()
)

// defaultExportFormat is what an export takes when the author names none.
const defaultExportFormat = "csv"

// maxExports bounds the outputs one run may produce. A script writing more
// outputs than this is a pipeline, and a pipeline is several scripts.
const maxExports = 16

// exportPositionalArgs is how many of platform.export's arguments may be passed
// by position: name, rows, and format. Everything after them decides where the
// output goes and must be named, so a static read of the source can report it.
const exportPositionalArgs = 3

// minStateEntryBytes is the fewest bytes one state entry costs serialized: two
// quotes, a colon and a separator around an empty key and a one-byte value.
const minStateEntryBytes = 5

// callArgsPosition is which positional argument of platform.call carries the
// tool's argument set: the tool name is first, the arguments second. Named so
// the binding's signature and the static read of it cannot drift apart.
const callArgsPosition = 2

// Field counts for the dicts the host bindings return, named so the allocation
// hint and the number of SetKey calls below it cannot drift apart.
const (
	queryResultFields  = 3
	exportRecordFields = 17
)

// TextResultKey is the single field a tool result arrives under when the tool
// returned no structured object: the text it produced, verbatim (SessionCaller.
// CallTool). It is one key rather than a shape per tool so the rule an author
// has to remember is one sentence, and it sits with the other result-shape
// constants because that is what it is — the shape a host call hands back.
// Exported because the dialect contract states it.
const TextResultKey = "text"

// PublishFormat is the one format a data-region payload has. The region is a
// JSON data island by contract, so unlike an export there is no format axis for
// the script to choose on. Exported because the writer records the same fact on
// the run row, and the two spellings must be one.
const PublishFormat = "json"

// argErr wraps an argument-unpacking failure with the binding it came from, so
// an author reads which call they got wrong rather than a bare argument name.
func argErr(b *starlark.Builtin, err error) error {
	return fmt.Errorf("in %s: %w", b.Name(), err)
}

// hostState carries everything the host bindings need for one run and records
// what the run did.
type hostState struct {
	opts    Options
	ctx     context.Context //nolint:containedctx // one run's context, bound for the life of that run and used only by its host bindings
	log     *logBuffer
	queries int
	exports []ExportRecord
	// writes records the persisting platform.call calls the run made, in call
	// order. It stays empty under the write barrier, which refuses them.
	writes []WriteRecord
	// refused is the call the write barrier stopped. There is at most one:
	// the refusal fails the run.
	refused *WriteRecord
	// declared caches what the server advertises about a tool no rule names,
	// keyed by tool name. The answer cannot change inside a run, and the lookup
	// walks a tool listing.
	declared map[string]bool
	// state is what platform.save_state staged, nil until it is called. A
	// second call replaces the first: the run's write is one write.
	state *script.StateWrite
}

// saveState implements platform.save_state: stage the whole state object the
// next run of this script will read.
//
// Nothing is written here. The object is checked now — every value must be
// JSON-representable and the whole must fit the bound — because the author
// reads the refusal inside a run that has already queried, and a refusal at
// the write would arrive after the outputs. Whether it is applied is decided
// by whoever runs the script: a platform run's store applies it when the run
// succeeds, in the same transaction that marks it so, and a draft reports it
// beside the outputs it would have written.
func (h *hostState) saveState(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value *starlark.Dict
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "state", &value); err != nil {
		return nil, argErr(b, err)
	}
	object, err := stateObject(value)
	if err != nil {
		return nil, argErr(b, err)
	}
	if err := script.ValidateState(object); err != nil {
		return nil, argErr(b, err)
	}
	h.state = &script.StateWrite{Value: object}
	return starlark.None, nil
}

// stateObject converts the dict a script passed to save_state into the plain
// object the platform stores. A value that cannot cross into JSON — a function,
// a set — is refused naming its key, so the author is told which entry to fix.
func stateObject(value *starlark.Dict) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	// Every key costs at least its quotes, a colon and a separator once
	// serialized, so a dict with more keys than the bound can hold is refused
	// before it is walked rather than converted whole and then measured.
	if value.Len()*minStateEntryBytes > script.MaxStateBytes {
		return nil, fmt.Errorf("the state has %d keys, more than the %d-byte limit can hold; state is a cursor or a summary, so keep a table as a resource instead",
			value.Len(), script.MaxStateBytes)
	}
	out := make(map[string]any, value.Len())
	for _, item := range value.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("state keys must be strings, got %s", item[0].Type())
		}
		converted, err := starlarkconv.FromStarlark(item[1])
		if err != nil {
			return nil, fmt.Errorf("state key %q: %w", string(key), err)
		}
		out[string(key)] = converted
	}
	return out, nil
}

// resolveDestination turns the destination a script named into the address the
// deployment's configuration declares for it, refusing a name nothing
// declares. The portal is built in; every other destination comes from the
// scripts.destinations configuration, resolved here at run time so repointing
// one takes effect on the next run. A draft resolves through the same set, so
// a destination a real run would refuse fails while the author is iterating.
func (h *hostState) resolveDestination(name string) (script.Destination, error) {
	return ResolveDestination(name, h.opts.Destinations)
}

// ResolveDestination turns a destination name into the address it stands for,
// against the set a deployment declares. The portal is built in; every other
// name comes from the scripts.destinations configuration.
//
// It is exported because the run is no longer the only caller: validate
// reports a script that names a destination this deployment does not declare
// (#1415), and the two must refuse in the same words for the same reason. A
// script whose export was accepted by validate and then refused at run time
// had already executed its queries by the time it learned.
func ResolveDestination(name string, declared []script.Destination) (script.Destination, error) {
	switch name {
	case script.DestinationPortal:
		return script.PortalDestination(), nil
	case script.DestinationResources:
		return script.ResourcesDestination(), nil
	}
	for _, d := range declared {
		if d.Name == name {
			return d, nil
		}
	}
	if len(declared) == 0 {
		return script.Destination{}, fmt.Errorf("destination %q is not configured: this deployment declares no bucket destinations, so %q and %q are the only places a script can write",
			name, script.DestinationPortal, script.DestinationResources)
	}
	return script.Destination{}, fmt.Errorf("destination %q is not configured; this deployment declares %s, and %q and %q are always available",
		name, strings.Join(destinationNames(declared), ", "), script.DestinationPortal, script.DestinationResources)
}

// destinationNames lists the configured destinations for a refusal.
func destinationNames(destinations []script.Destination) []string {
	out := make([]string, 0, len(destinations))
	for _, d := range destinations {
		out = append(out, d.Name)
	}
	return out
}

// runValue builds the frozen run record — the script's ONLY source of time and
// of caller input, read as run.run_id, run.fire_time, run.params["name"], and
// run.state, the state as it stood when the run was created.
//
// It is frozen so nothing downstream in the script can rewrite the fire time
// and make a run unreproducible from its own record. fire_time is a value
// pinned when the run was created, never a clock read: that is what lets a
// daily report recompute "yesterday" identically when it is re-run months
// later to explain what it said.
func (h *hostState) runValue() starlark.Value {
	params := starlark.NewDict(len(h.opts.Params))
	for _, name := range starlarkconv.SortedKeys(h.opts.Params) {
		v, err := starlarkconv.ToStarlark(h.opts.Params[name])
		if err != nil {
			// Params are bound and type-checked by script.BindParams before a run
			// exists, so an unconvertible value here is a defect in the caller,
			// not author input. Surface it as None rather than failing the run
			// with a message no author can act on.
			v = starlark.None
		}
		_ = params.SetKey(starlark.String(name), v)
	}
	state := starlark.NewDict(len(h.opts.State))
	for _, name := range starlarkconv.SortedKeys(h.opts.State) {
		v, err := starlarkconv.ToStarlark(h.opts.State[name])
		if err != nil {
			// State was validated as JSON-representable when it was saved, so
			// an unconvertible value here is a defect in the store, not author
			// input; None keeps the run readable rather than failing it with
			// a message no author can act on.
			v = starlark.None
		}
		_ = state.SetKey(starlark.String(name), v)
	}
	rec := starlarkstruct.FromStringDict(starlark.String("run"), starlark.StringDict{
		"run_id":    starlark.String(h.opts.RunID),
		"fire_time": starlark.String(h.opts.FireTime.UTC().Format(scriptdate.TimeLayout)),
		"params":    params,
		"state":     state,
	})
	rec.Freeze()
	return rec
}

// query implements platform.query: bind the parameters, issue one tool call
// over the run's MCP session, cap the result, and hand back rows as a list of
// dicts.
func (h *hostState) query(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		connection string
		sql        string
		params     *starlark.Dict
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"sql", &sql, "connection?", &connection, "params?", &params); err != nil {
		return nil, argErr(b, err)
	}
	if h.opts.Caller == nil {
		return nil, fmt.Errorf("host binding %s is not available in this context", b.Name())
	}
	bound, err := bindSQL(sql, params)
	if err != nil {
		return nil, argErr(b, err)
	}

	call := map[string]any{"sql": bound, "limit": h.opts.MaxRows}
	if connection != "" {
		call["connection"] = connection
	}
	out, err := h.callTool(toolQuery, call)
	if err != nil {
		return nil, argErr(b, err)
	}
	h.queries++
	return h.queryResult(b.Name(), out)
}

// call implements platform.call: invoke any platform tool by name and hand the
// script its structured result.
//
// This is the whole of what the three named helpers do, with the tool name left
// to the author. It resolves to the same Caller they resolve to, so the call
// crosses the same authentication, persona and connection authorization, rate
// limiting and audit an agent's call crosses, presenting the roles the run's
// version captured from its author. There is no allowlist in front of it and no
// second authorization path behind it: a tool the run's persona may not call is
// refused by the middleware, in the middleware's own words.
//
// What a script reaches is therefore what its author reaches, which is what
// automation is. What a READER can enumerate comes from Validate, which reads
// the tool names out of the source and reports a call that computes one.
func (h *hostState) call(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		tool     string
		toolArgs *starlark.Dict
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "tool", &tool, "args?", &toolArgs); err != nil {
		return nil, argErr(b, err)
	}
	if tool == "" {
		return nil, fmt.Errorf("in %s: tool is empty; name the tool to call, as %s(\"trino_execute\", {\"connection\": \"warehouse\", \"sql\": \"...\"})", b.Name(), b.Name())
	}
	if h.opts.Caller == nil {
		return nil, fmt.Errorf("host binding %s is not available in this context", b.Name())
	}
	payload, err := callArguments(toolArgs)
	if err != nil {
		return nil, argErr(b, err)
	}
	decision, err := h.admitCall(b, tool, payload)
	if err != nil {
		return nil, err
	}
	out, err := h.callTool(tool, payload)
	if err != nil {
		return nil, argErr(b, err)
	}
	// Recorded AFTER the call, and only for a call that returned. A tool that
	// refused the arguments persisted nothing, and a response listing it under
	// "these calls persisted for real" would be a false statement about the
	// deployment the author is looking at.
	if decision.Writes && h.opts.Writes == WritesReported {
		h.writes = append(h.writes, WriteRecord{Tool: tool, Call: decision.Call})
	}
	// A tool that wrote a file reports what the write did to the tables
	// registered over it, and the run log carries that the way it carries an
	// export's (#1536): the run that put a table behind its file says so.
	h.noteTables(tool, tableSentences(out))
	// The byte cap is the query binding's, applied here for the same reason: a
	// heap the interpreter cannot bound is the one resource no limit in this
	// engine covers, and a tool asked for more than a run can hold must fail
	// with a message rather than by growing the process.
	if n := approxJSONBytes(out); n > h.opts.MaxResultBytes {
		return nil, fmt.Errorf("result of %s(%q) is %d bytes, over the %d-byte cap; narrow what the tool is asked for",
			b.Name(), tool, n, h.opts.MaxResultBytes)
	}
	value, err := starlarkconv.ToStarlark(out)
	if err != nil {
		return nil, fmt.Errorf("converting the result of %s(%q): %w", b.Name(), tool, err)
	}
	return value, nil
}

// admitCall applies the run's write barrier and records what the run persists.
//
// A draft run is a rehearsal, and the three named helpers make themselves one
// on their own — a nil Exporter previews, save_state reports. platform.call
// cannot: it is an ordinary tool call, so a draft of an ingestion script
// created the resources, registrations and assets a platform run would, with no
// run record to explain where they came from (#1664).
//
// The barrier refuses rather than faking a result. A synthesized answer would
// be read by the next line of the script — the new resource's uri, the
// registration's id — and the run would fail somewhere downstream, describing
// the wrong problem. Failing at the call names the call.
//
// Under WritesReported the same classification still runs, and the caller
// records every write it recognizes once the call has returned: a draft that
// was allowed to persist owes its author the list of what it persisted.
func (h *hostState) admitCall(b *starlark.Builtin, tool string, args map[string]any) (toolwrite.Decision, error) {
	// A platform run makes every call and records none, so it asks no question
	// here. Classifying anyway would put a tools/list behind every call to an
	// unclassified tool on the path that runs unattended.
	if h.opts.Writes == WritesMade {
		return toolwrite.Decision{}, nil
	}
	decision := h.declare(tool, args)
	if !decision.Writes || h.opts.Writes != WritesRefused {
		return decision, nil
	}
	h.refused = &WriteRecord{Tool: tool, Call: decision.Call}
	return decision, fmt.Errorf("in %s: %s", b.Name(), refusedWriteMessage(decision))
}

// declare classifies one call, falling back to what the tool says about itself
// when no rule names it.
//
// The platform does not define every tool it serves: an MCP gateway connection
// proxies whatever its upstream serves, under names chosen upstream, and the
// upstream's own read-only annotation travels with the tool onto this server's
// listing. Taking that statement is what keeps the barrier from refusing an
// entire class of tools nobody could have listed in advance, and it is still
// deny-by-default — a tool that declares nothing stays a write.
func (h *hostState) declare(tool string, args map[string]any) toolwrite.Decision {
	decision := h.opts.Classifier.Classify(tool, args)
	if !decision.Writes || decision.Declared {
		return decision
	}
	// Only for a tool NO rule names. A rule that ran and could not read its
	// argument has already said what it knows, and an annotation is not
	// permitted to overrule it.
	declarer, ok := h.opts.Caller.(ReadOnlyDeclarer)
	if !ok || toolwrite.Classified(tool) {
		return decision
	}
	if h.declaresRead(declarer, tool) {
		decision.Writes = false
		decision.Declared = true
	}
	return decision
}

// declaresRead answers whether the server advertises the tool as read-only,
// once per tool per run.
//
// The lookup walks a tool listing, so a script calling one unclassified tool in
// a loop would walk it once per iteration. The answer cannot change inside a
// run: the listing is the session's, and the session is the run.
func (h *hostState) declaresRead(declarer ReadOnlyDeclarer, tool string) bool {
	if cached, seen := h.declared[tool]; seen {
		return cached
	}
	readOnly, known := declarer.DeclaresReadOnly(h.ctx, tool)
	answer := known && readOnly
	if h.declared == nil {
		h.declared = map[string]bool{}
	}
	h.declared[tool] = answer
	return answer
}

// refusedWriteMessage states why one call was not made, in the words the author
// needs to act on it.
//
// A tool the platform classifies and a tool it does not get different sentences,
// because the author's next move differs: the first is a decision about whether
// this draft should persist, the second is the platform saying it cannot tell,
// which a reader must not mistake for a judgment about the tool.
func refusedWriteMessage(decision toolwrite.Decision) string {
	if decision.Declared {
		return fmt.Sprintf(
			"%s persists outside this run, and a draft run does not write. "+
				"Run the draft with allow_writes to let it write for real, and it will report what it wrote.",
			decision.Call)
	}
	return fmt.Sprintf(
		"the platform cannot tell whether %s persists, so a draft run does not make it. "+
			"Run the draft with allow_writes to let it write for real, and it will report what it wrote.",
		decision.Call)
}

// tableSentences reads the table report a file-writing tool's result carries:
// manage_resource replace_content and manage_asset's content edits answer with
// a "table_changes" list of sentences (#1666). Any other result has none.
func tableSentences(out map[string]any) []string {
	raw, ok := out["table_changes"].([]any)
	if !ok {
		return nil
	}
	lines := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			lines = append(lines, s)
		}
	}
	return lines
}

// callArguments converts the argument dict a script passed into the plain Go
// map a tool call takes. A missing dict is an empty argument set, which is what
// a tool taking no arguments wants.
func callArguments(args *starlark.Dict) (map[string]any, error) {
	if args == nil {
		return map[string]any{}, nil
	}
	converted, err := starlarkconv.DictFromStarlark(args, 0)
	if err != nil {
		return nil, err
	}
	out, ok := converted.(map[string]any)
	if !ok {
		// starlarkconv.DictFromStarlark returns a map[string]any or an error, so this is
		// unreachable; it is here so a future change to that contract fails
		// loudly rather than panicking inside a script.
		return nil, fmt.Errorf("args converted to %T rather than to an argument set", converted)
	}
	return out, nil
}

// queryResult caps and converts one query tool result.
func (h *hostState) queryResult(name string, out map[string]any) (starlark.Value, error) {
	raw, present := out["rows"]
	rows, ok := raw.([]any)
	if !present || !ok {
		// Without this the script would receive None for res["rows"] and fail on
		// the next line with a type error that says nothing about the real
		// problem, which is that the tool answered in a shape this binding does
		// not understand.
		return nil, fmt.Errorf("result of %s has no rows field; the query tool answered in an unexpected shape", name)
	}
	// Truncation is checked FIRST and from the tool's own stats, not inferred
	// from the row count. The row cap is pushed down as the query's limit, so
	// the engine stops at exactly that many rows and a length comparison can
	// never fire: a script summing a 40,000-row day would receive the first
	// MaxRows of them and export a total that is silently wrong. Silently wrong
	// is the one outcome the determinism contract exists to exclude, so a
	// truncated result fails the run.
	if truncated(out) {
		return nil, fmt.Errorf("result of %s was truncated at %d rows; aggregate in SQL or narrow the query, because a partial result would silently change what this script computes",
			name, len(rows))
	}
	if len(rows) > h.opts.MaxRows {
		return nil, fmt.Errorf("result of %s is %d rows, over the %d-row cap; add a LIMIT or aggregate in SQL",
			name, len(rows), h.opts.MaxRows)
	}
	if n := approxJSONBytes(out); n > h.opts.MaxResultBytes {
		return nil, fmt.Errorf("result of %s is %d bytes, over the %d-byte cap; select fewer columns or aggregate in SQL",
			name, n, h.opts.MaxResultBytes)
	}
	result := starlark.NewDict(queryResultFields)
	for _, key := range []string{"columns", "rows"} {
		v, err := starlarkconv.ToStarlark(out[key])
		if err != nil {
			return nil, fmt.Errorf("converting the %s field of the %s result: %w", key, name, err)
		}
		_ = result.SetKey(starlark.String(key), v)
	}
	_ = result.SetKey(starlark.String("row_count"), starlark.MakeInt(len(rows)))
	return result, nil
}

// export implements platform.export: validate the output contract, then either
// persist the output or report the shape it would have.
//
// Which of the two happens is decided by the caller, not by the script. A
// draft run is given no Exporter, so it previews: an author iterating on a
// report sees the shape before anything persists, and a draft still writes no
// state. A platform run is given one, and the same call writes a new version
// of the run's output asset, or delivers an object to a configured bucket
// destination. The arguments are identical either way, so the script an
// author finishes is the script that runs.
//
// One binding covers both, rather than a second one for delivery. The
// difference between refreshing a dashboard and dropping a CSV for another
// system is WHERE the output goes, which is the destination axis; splitting it
// into two bindings would put the resolution, the audit record, and the
// exactly-once rule in two places to drift apart.
func (h *hostState) export(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	req, err := h.exportRequest(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	if err := h.admitOutput(b, req.Name, req.Destination.Name); err != nil {
		return nil, err
	}
	record, err := h.persistOrPreview(b, req)
	if err != nil {
		return nil, err
	}
	h.exports = append(h.exports, record)
	return exportValue(record), nil
}

// admitOutput enforces the two rules every output-producing binding shares:
// the per-run output budget, and the once-per-destination rule.
//
// The once-per-destination rule is checked here, not only by the writer, so a
// draft run refuses exactly what a platform run refuses: a script that writes
// one name to one place twice must fail while the author is iterating, not at
// the first scheduled fire.
func (h *hostState) admitOutput(b *starlark.Builtin, name, destination string) error {
	if len(h.exports) >= maxExports {
		return fmt.Errorf("in %s: a run may produce at most %d outputs", b.Name(), maxExports)
	}
	for _, prior := range h.exports {
		if prior.Name == name && prior.Destination == destination {
			return fmt.Errorf("in %s: output %q was already written to %q by this run; each output name may be written once per destination, so give the second one its own name",
				b.Name(), name, destination)
		}
	}
	return nil
}

// publishData implements platform.publish_data: refresh the data region of an
// output asset this script already publishes, leaving the rest of the document
// untouched (#1389).
//
// The presentation is not creatable through this call, on purpose: the split
// this binding exists for keeps the template in the asset and the data in the
// script, so a layout edit made in the portal survives the next scheduled fire
// instead of being overwritten by a whole-document re-emit, and changing the
// presentation never requires a script edit. Composing the whole document is
// platform.export's document arm.
func (h *hostState) publishData(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		name string
		data starlark.Value
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name, "data", &data); err != nil {
		return nil, argErr(b, err)
	}
	// Trimmed exactly as an export's name is: the name is the output's identity
	// across runs, and it must resolve to the same asset the export wrote.
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("in %s: name is required and identifies the output asset whose data region to refresh", b.Name())
	}
	payload, err := publishPayload(b, data)
	if err != nil {
		return nil, err
	}
	// A data region is a property of a portal document, so the write is always
	// against the portal destination, which is built in.
	if err := h.admitOutput(b, name, script.DestinationPortal); err != nil {
		return nil, err
	}
	record, err := h.persistOrPreviewPublish(b, PublishRequest{Name: name, Data: payload})
	if err != nil {
		return nil, err
	}
	h.exports = append(h.exports, record)
	return exportValue(record), nil
}

// publishPayload converts the data argument to the Go value the payload
// serializer consumes, refusing a scalar: the region holds the data structure a
// dashboard renders from, and a bare string or number there is a mistake worth
// naming rather than a one-character island.
func publishPayload(b *starlark.Builtin, data starlark.Value) (any, error) {
	goData, err := starlarkconv.FromStarlark(data)
	if err != nil {
		return nil, argErr(b, err)
	}
	switch goData.(type) {
	case map[string]any, []any:
		return goData, nil
	default:
		return nil, fmt.Errorf("in %s: data must be a dict or a list — the structure the dashboard renders from — got %s",
			b.Name(), data.Type())
	}
}

// persistOrPreviewPublish writes the refresh through the run's Exporter, or
// measures it when the run has none. The preview serializes through the same
// FormatDataPayload the writer uses, so the size a draft reports is the size a
// platform run splices.
func (h *hostState) persistOrPreviewPublish(b *starlark.Builtin, req PublishRequest) (ExportRecord, error) {
	record := ExportRecord{
		Name: req.Name, Destination: script.DestinationPortal, Format: PublishFormat,
		RowCount: PublishRowCount(req.Data), Refresh: true,
	}
	return h.finishRecord(b, record,
		func() ([]byte, error) { return FormatDataPayload(req.Name, req.Data) },
		func() (*ExportResult, error) { return h.opts.Exporter.PublishData(h.ctx, req) })
}

// finishRecord completes one output record by the preview-or-persist rule every
// output-producing binding shares: with no Exporter the content is serialized
// only to be measured, and with one the write's own answer fills in where the
// output landed. One implementation, so the preview contract cannot drift
// between the bindings.
func (h *hostState) finishRecord(
	b *starlark.Builtin, record ExportRecord,
	measure func() ([]byte, error), persist func() (*ExportResult, error),
) (ExportRecord, error) {
	if h.opts.Exporter == nil {
		record.Preview = true
		data, err := measure()
		if err != nil {
			return ExportRecord{}, argErr(b, err)
		}
		record.Bytes = len(data)
		return record, nil
	}
	written, err := persist()
	if err != nil {
		return ExportRecord{}, argErr(b, err)
	}
	record.AssetID = written.AssetID
	record.AssetVersion = written.AssetVersion
	record.Bucket = written.Bucket
	record.Key = written.Key
	record.ResourceID = written.ResourceID
	record.ResourceRef = written.ResourceRef
	record.ResourceURI = written.ResourceURI
	record.ResourceVersion = written.ResourceVersion
	record.Bytes = written.Bytes
	record.TableChanges = written.TableChanges
	h.noteTables(record.Name, written.TableChanges)
	return record, nil
}

// noteTables prints what a write did to the tables registered over the file
// it wrote into the run log, one line per table (#1536). The run's log is its
// history, and a scheduled run that put a table behind its file has to say so
// there, whether or not the script prints the result it was handed.
func (h *hostState) noteTables(subject string, changes []string) {
	for _, line := range changes {
		h.log.write("table_changes: " + subject + ": " + line)
	}
}

// noteChannel prints what a post went to into the run log, so a scheduled
// run's history says where its message landed whether or not the script
// printed the result it was handed (#1723). It follows noteTables, and for
// the same reason: the run log is the run's history.
func (h *hostState) noteChannel(payload, out map[string]any) {
	channel, _ := payload["channel"].(string)
	if channel == "" {
		return
	}
	action, _ := payload["action"].(string)
	if detail, ok := out["detail"].(string); ok {
		h.log.write("notify: " + action + " to " + channel + ": " + detail)
		return
	}
	h.log.write("notify: " + action + " to " + channel)
}

// PublishRowCount reports the honest row count of a payload: the length of a
// list, and zero for a dict, whose size is not a row count.
func PublishRowCount(data any) int {
	if list, ok := data.([]any); ok {
		return len(list)
	}
	return 0
}

// exportRequest unpacks and checks one platform.export call: the output's name
// and format, the destination it names, the key it asks for beneath that
// destination's configured prefix, and the rows themselves.
func (h *hostState) exportRequest(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (ExportRequest, error) {
	var (
		name        string
		format      string
		destination string
		key         string
		rows        starlark.Value
	)
	// destination and key must be NAMED. The static validator reads keyword
	// arguments, so a destination passed by position would be invisible to it —
	// and the review surface would then state, positively, that a script writing
	// to a bucket writes to the portal. A reviewer is better served by an author
	// having to name the argument than by a diff that is quietly wrong.
	if len(args) > exportPositionalArgs {
		return ExportRequest{}, fmt.Errorf("in %s: pass destination and key by name (destination=\"...\", key=\"...\"); only name, rows and format may be positional, because a reviewer has to be able to read where this script writes",
			b.Name())
	}
	if err := starlark.UnpackArgs(b.Name(), args, kwargs,
		"name", &name, "rows", &rows, "format?", &format,
		"destination?", &destination, "key?", &key); err != nil {
		return ExportRequest{}, argErr(b, err)
	}
	if format == "" {
		format = defaultExportFormat
	}
	if !exportFormats[format] {
		return ExportRequest{}, fmt.Errorf("in %s: format %q is not one of %s", b.Name(), format, starlarkconv.SortedSet(exportFormats))
	}
	// Trimmed here rather than checked here: the name is the output's identity
	// across runs, so "daily" and "daily " must not become two assets.
	name = strings.TrimSpace(name)
	if name == "" {
		return ExportRequest{}, fmt.Errorf("in %s: name is required and identifies the output asset across runs", b.Name())
	}
	if destination == "" {
		destination = script.DestinationPortal
	}
	resolved, err := h.resolveDestination(strings.TrimSpace(destination))
	if err != nil {
		return ExportRequest{}, argErr(b, err)
	}
	key = strings.TrimSpace(key)
	if err := checkExportKey(b, resolved, key); err != nil {
		return ExportRequest{}, err
	}
	body, list, err := exportContent(b, format, rows)
	if err != nil {
		return ExportRequest{}, err
	}
	return ExportRequest{
		Name: name, Format: format, Columns: starlarkconv.ColumnOrder(rows), Rows: list, Body: body,
		Destination: resolved, Key: key,
	}, nil
}

// checkExportKey validates the key a script asks for, and refuses one where it
// would mean nothing.
//
// The portal names its own objects — an asset's storage layout is the
// platform's, and its identity across runs is the output name — so a key aimed
// at it is a misunderstanding worth reporting rather than ignoring. Everywhere
// else the key is the contract between this script and whatever consumes its
// output, so it is checked against the rules that keep it under the
// destination's prefix and left exactly as written.
func checkExportKey(b *starlark.Builtin, destination script.Destination, key string) error {
	if destination.IsResource() {
		return checkLibraryKey(b, key)
	}
	if key == "" {
		return nil
	}
	if destination.IsPortal() {
		return fmt.Errorf("in %s: the %q destination stores its own objects and takes no key; the output's name is its identity there",
			b.Name(), destination.Name)
	}
	if err := script.ValidateObjectKey(key); err != nil {
		return fmt.Errorf("in %s: key %q cannot be used: %w", b.Name(), key, err)
	}
	return nil
}

// checkLibraryKey validates the key a managed-resource output is addressed by.
//
// Here the key is REQUIRED, which is the opposite of every other destination: the
// library file's identity across runs IS its path, so a resource output with no
// key has no address to be the same file at. The output name still labels the
// file, and the path is what the next run, a person, and a table registration
// find it by.
func checkLibraryKey(b *starlark.Builtin, key string) error {
	if key == "" {
		return fmt.Errorf("in %s: the %q destination needs a key, which is the path the file is filed at in the library, for example key=\"datasets/orders.csv\". It is the file's identity across runs: the same key next run writes the next version of that same file",
			b.Name(), script.DestinationResources)
	}
	if _, _, err := script.SplitLibraryKey(key); err != nil {
		return fmt.Errorf("in %s: key %q cannot be used: %w", b.Name(), key, err)
	}
	return nil
}

// exportContent reads the rows argument into whichever of the two content arms
// the declared format serializes: a string body written verbatim for a
// document, or a list of row dicts for a tabular write. Exactly one of the two
// returns is set.
func exportContent(b *starlark.Builtin, format string, rows starlark.Value) (body *string, list []any, err error) {
	if s, ok := rows.(starlark.String); ok {
		if !documentFormats[format] {
			return nil, nil, fmt.Errorf("in %s: format %q is serialized from rows, a list of dicts; a string body is written verbatim and is valid for the document formats %s",
				b.Name(), format, starlarkconv.SortedSet(documentFormats))
		}
		content := string(s)
		// A blank document is refused rather than published: a body assembled
		// conditionally that ends up empty is a script bug, and the failure
		// must be loud rather than a blank version silently replacing the
		// current one on a shared dashboard. A run with nothing to say should
		// say so in the document, or fail("why").
		if strings.TrimSpace(content) == "" {
			return nil, nil, fmt.Errorf("in %s: the string body is empty; write the document's content, state \"no data\" in it, or stop the run with fail(...)", b.Name())
		}
		return &content, nil, nil
	}
	if !rowFormats[format] {
		return nil, nil, fmt.Errorf("in %s: format %q is a document written verbatim from a string body, not serialized from rows",
			b.Name(), format)
	}
	list, err = exportRows(b, rows)
	if err != nil {
		return nil, nil, err
	}
	return nil, list, nil
}

// exportRows converts the rows argument to the list of dicts a tabular output
// format is written from.
func exportRows(b *starlark.Builtin, rows starlark.Value) ([]any, error) {
	goRows, err := starlarkconv.FromStarlark(rows)
	if err != nil {
		return nil, argErr(b, err)
	}
	list, ok := goRows.([]any)
	if !ok {
		return nil, fmt.Errorf("in %s: rows must be a list of dicts, or a string body for a document format (%s), got %s",
			b.Name(), strings.Join(starlarkconv.SortedSet(documentFormats), ", "), rows.Type())
	}
	return list, nil
}

// persistOrPreview writes the output through the run's Exporter, or measures it
// when the run has none.
//
// A preview measures by serializing: it runs the same formatter the writer
// would, and reports the length of what that produced. The documented loop is
// create, validate, run_draft, then save, and sizing an output against a cap
// is one of the few things the number is for — so the number a draft reports
// has to be the number a real run would write, not a format-independent
// estimate of it.
func (h *hostState) persistOrPreview(b *starlark.Builtin, req ExportRequest) (ExportRecord, error) {
	record := ExportRecord{
		Name: req.Name, Destination: req.Destination.Name, Format: req.Format,
		RowCount: len(req.Rows), Document: req.Body != nil,
	}
	return h.finishRecord(b, record,
		func() ([]byte, error) { data, _, err := FormatOutput(req); return data, err },
		func() (*ExportResult, error) { return h.opts.Exporter.Export(h.ctx, req) })
}

// exportValue renders one export record as the dict the script receives. Where
// the output went decides which part of the record is present: an asset version
// for the portal, an object for a bucket, the file's reference and version for
// the library.
func exportValue(record ExportRecord) starlark.Value {
	out := starlark.NewDict(exportRecordFields)
	_ = out.SetKey(starlark.String("preview"), starlark.Bool(record.Preview))
	_ = out.SetKey(starlark.String("name"), starlark.String(record.Name))
	_ = out.SetKey(starlark.String("destination"), starlark.String(record.Destination))
	_ = out.SetKey(starlark.String("format"), starlark.String(record.Format))
	_ = out.SetKey(starlark.String("row_count"), starlark.MakeInt(record.RowCount))
	_ = out.SetKey(starlark.String("document"), starlark.Bool(record.Document))
	_ = out.SetKey(starlark.String("refresh"), starlark.Bool(record.Refresh))
	_ = out.SetKey(starlark.String("bytes"), starlark.MakeInt(record.Bytes))
	if record.AssetID != "" {
		_ = out.SetKey(starlark.String("asset_id"), starlark.String(record.AssetID))
		_ = out.SetKey(starlark.String("asset_version"), starlark.MakeInt(record.AssetVersion))
	}
	if record.Bucket != "" {
		_ = out.SetKey(starlark.String("bucket"), starlark.String(record.Bucket))
	}
	if record.Key != "" {
		_ = out.SetKey(starlark.String("key"), starlark.String(record.Key))
	}
	if record.ResourceID != "" {
		_ = out.SetKey(starlark.String("resource_id"), starlark.String(record.ResourceID))
		_ = out.SetKey(starlark.String("reference"), starlark.String(record.ResourceRef))
		_ = out.SetKey(starlark.String("uri"), starlark.String(record.ResourceURI))
		_ = out.SetKey(starlark.String("version"), starlark.MakeInt(record.ResourceVersion))
	}
	if len(record.TableChanges) > 0 {
		changes := make([]starlark.Value, 0, len(record.TableChanges))
		for _, line := range record.TableChanges {
			changes = append(changes, starlark.String(line))
		}
		_ = out.SetKey(starlark.String("table_changes"), starlark.NewList(changes))
	}
	return out
}

// truncated reports whether the query tool says it stopped short of the full
// result. A tool that reports no stats at all is treated as complete: this is a
// positive signal, and inventing truncation from its absence would fail every
// call against a tool that answers in a different shape.
func truncated(out map[string]any) bool {
	stats, ok := out["stats"].(map[string]any)
	if !ok {
		return false
	}
	flag, _ := stats["truncated"].(bool)
	return flag
}

// approxJSONBytes measures a value the way it will be serialized. It is the cap
// the byte limits are enforced against, so it must measure the shape that
// actually crosses into the script rather than a Go in-memory size.
func approxJSONBytes(v any) int {
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(data)
}

// logBuffer captures print output up to a byte cap. Past the cap it keeps the
// HEAD and drops the tail: the first lines of a failing run explain how it got
// there, while the last lines of a runaway loop are the same line repeated.
type logBuffer struct {
	limit     int
	buf       strings.Builder
	truncated bool
}

// write appends one print line, stopping at the cap.
func (l *logBuffer) write(msg string) {
	if l.truncated {
		return
	}
	remaining := l.limit - l.buf.Len()
	if remaining <= 0 {
		l.truncated = true
		return
	}
	line := msg + "\n"
	if len(line) > remaining {
		// Cut on a rune boundary: a byte-offset cut through a multi-byte
		// character leaves an invalid byte that json.Marshal silently rewrites
		// to U+FFFD in the response.
		l.buf.WriteString(truncateRunes(line, remaining))
		l.truncated = true
		return
	}
	l.buf.WriteString(line)
}

// truncateRunes cuts s to at most n bytes without splitting a rune.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// string returns the captured log, marked when output was dropped.
func (l *logBuffer) string() string {
	if l.truncated {
		return l.buf.String() + "\n... log truncated at the size cap; write large output as an export instead\n"
	}
	return l.buf.String()
}

// MaxOutputBytes caps one serialized output. It matches the ceiling the portal
// export path applies, so a script cannot write an asset a human could not have
// exported by hand.
const MaxOutputBytes = 100 << 20

// OutputIdentity is how one output is stored: the media type it carries and
// the file extension its object keys take. It is a value rather than the
// formatter that produced the bytes, because the bytes are already serialized
// when FormatOutput returns — nothing downstream may re-serialize, and a
// document has no serializer to hand back.
type OutputIdentity struct {
	ContentType string
	Extension   string
}

// FormatOutput serializes one export request in its declared format, checks it
// against the output ceiling, and returns the identity the bytes are stored
// under.
//
// It is the single serializer for a script's output, and the ceiling is
// applied here rather than by the writer so that the two runs of a script
// agree: a platform run persists exactly these bytes, and a draft run measures
// them, so an output too large to write is refused while the author is still
// iterating rather than at the first scheduled fire.
func FormatOutput(req ExportRequest) ([]byte, OutputIdentity, error) {
	if req.Body != nil {
		return formatDocument(req)
	}
	formatter, err := trinokit.NewFormatter(req.Format)
	if err != nil {
		return nil, OutputIdentity{}, fmt.Errorf("output %q: %w", req.Name, err)
	}
	data, err := formatter.Format(req.Columns, tabular(req.Columns, req.Rows))
	if err != nil {
		return nil, OutputIdentity{}, fmt.Errorf("formatting output %q: %w", req.Name, err)
	}
	if len(data) > MaxOutputBytes {
		return nil, OutputIdentity{}, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; aggregate in SQL or write fewer columns",
			req.Name, len(data), MaxOutputBytes)
	}
	return data, OutputIdentity{ContentType: formatter.ContentType(), Extension: formatter.FileExtension()}, nil
}

// FormatDataPayload serializes one publish_data payload as the JSON the data
// region will hold, checked against the same output ceiling every export is.
//
// It is the single serializer for the payload — a draft measures exactly the
// bytes a platform run splices — and it keeps encoding/json's default
// escaping, which writes <, > and & as \u escapes, so no string in the payload
// can ever terminate the <script> element it lands inside. Go serializes map
// keys in sorted order, so the bytes are deterministic for a given payload.
// The indentation is for the reader of a version diff: a refreshed dashboard's
// history should read field by field, not as one replaced line.
func FormatDataPayload(name string, data any) ([]byte, error) {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("output %q: the data cannot be serialized as JSON: %w", name, err)
	}
	if len(out) > MaxOutputBytes {
		return nil, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; aggregate in SQL or publish less data",
			name, len(out), MaxOutputBytes)
	}
	return out, nil
}

// documentTypes maps each document format to the canonical media type it is
// stored under — the same types the portal stores and renders for saved
// assets, so a script-published document is patchable like any other. The
// extension an object key carries follows from the type through
// contenttype.Extension, the one authority every other write path derives
// keys from, so a jsx document lands on the same key spelling a
// save_asset-written text/jsx object does.
var documentTypes = map[string]string{
	"markdown": contenttype.Markdown,
	"text":     contenttype.PlainText,
	"html":     contenttype.HTML,
	"jsx":      contenttype.JSX,
}

// formatDocument passes a string body through as the output's bytes, checked
// against the same ceiling a tabular output is. Verbatim is the contract: what
// the script composed is what the portal stores or the bucket receives, byte
// for byte, so a draft's measurement and a real run's write cannot differ.
//
// The format is checked against documentFormats here as well as at the
// argument edge: FormatOutput is the serializer both runs share, and a
// request some other constructor built with a body under csv or json must be
// refused rather than written verbatim as a "well-formed by construction"
// feed.
func formatDocument(req ExportRequest) ([]byte, OutputIdentity, error) {
	if !documentFormats[req.Format] {
		return nil, OutputIdentity{}, fmt.Errorf("output %q: format %q is serialized from rows, a list of dicts; a string body is valid for the document formats %s",
			req.Name, req.Format, starlarkconv.SortedSet(documentFormats))
	}
	if len(*req.Body) > MaxOutputBytes {
		return nil, OutputIdentity{}, fmt.Errorf("output %q is %d bytes, over the %d-byte limit; write a smaller document",
			req.Name, len(*req.Body), MaxOutputBytes)
	}
	ct := documentTypes[req.Format]
	return []byte(*req.Body), OutputIdentity{ContentType: ct, Extension: contenttype.Extension(ct)}, nil
}

// tabular projects row dicts onto the column order the script wrote. A row
// missing a column contributes an empty cell rather than shifting the row,
// which is what keeps a ragged result readable instead of misaligned.
func tabular(columns []string, rows []any) [][]any {
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		dict, ok := row.(map[string]any)
		if !ok {
			// A non-dict row has no columns to project. Rendering it as an empty
			// row keeps the row count honest; the alternative, dropping it, would
			// make the output disagree with the row count the script was told.
			out = append(out, make([]any, len(columns)))
			continue
		}
		cells := make([]any, len(columns))
		for i, column := range columns {
			cells[i] = dict[column]
		}
		out = append(out, cells)
	}
	return out
}
