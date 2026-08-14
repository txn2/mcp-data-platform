package scriptrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Tool names the host bindings call. They are ordinary platform tools invoked
// over the run's MCP session, so a script's query is authorized, rate limited,
// and audited by exactly the middleware an agent's query goes through.
const (
	toolQuery = "trino_query"
)

// Capability names. A capability is one host binding, and the set is closed:
// review means being able to enumerate everything a script can reach, which is
// only possible while the surface stays small enough to list. Nondeterministic
// tools (search, memory, catalog mutation) are deliberately absent — they have
// no place inside an automation whose value proposition is reproducibility.
//
// The names live in the domain (pkg/script) because a grant is written in them:
// the vocabulary a reviewer approves and the vocabulary this engine enforces
// have to be one list, not two that agree by convention.
const (
	CapabilityQuery  = script.CapabilityQuery
	CapabilityExport = script.CapabilityExport
)

// Capabilities is the full host surface, in the order help and validate report
// it.
var Capabilities = script.Capabilities

// exportFormats is the set platform.export accepts, matched to the formats
// trino_export already writes so the contract does not change when preview
// becomes persistence.
var exportFormats = map[string]bool{"csv": true, "json": true, "markdown": true, "text": true}

// defaultExportFormat is what an export takes when the author names none.
const defaultExportFormat = "csv"

// maxExports bounds the outputs one run may produce. A script writing more
// outputs than this is a pipeline, and a pipeline is several scripts.
const maxExports = 16

// exportPositionalArgs is how many of platform.export's arguments may be passed
// by position: name, rows, and format. Everything after them decides where the
// output goes and must be named, so a static read of the source can report it.
const exportPositionalArgs = 3

// Field counts for the dicts the host bindings return, named so the allocation
// hint and the number of SetKey calls below it cannot drift apart.
const (
	queryResultFields  = 3
	exportRecordFields = 10
)

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
	queries int
	exports []ExportRecord
}

// allowCapability refuses a host binding the run's grant does not cover.
//
// This is the first of two independent checks, and the weaker one. It exists
// because it fails fast, inside the interpreter, with a message naming what was
// granted — an author reads why their script stopped instead of an
// authorization denial from three layers down. The authority of record is the
// middleware chain, which enforces the same call under the run's own principal
// whatever this function decides.
//
// A nil grant means no grant layer applies, which is the draft case: a draft
// runs as its author, with the author's own authority, and there is nothing to
// narrow because nothing was widened.
func (h *hostState) allowCapability(name string) error {
	if h.opts.Grants == nil || h.opts.Grants.AllowsCapability(name) {
		return nil
	}
	return fmt.Errorf("the %s binding is not in this script's approved grant (granted: %s); widening what a script may reach requires approving it again",
		name, orNone(h.opts.Grants.Capabilities))
}

// allowConnection refuses a connection the run's grant does not cover.
//
// An unnamed connection is refused rather than defaulted. The platform would
// resolve "" to whichever connection is configured as the default, which is a
// connection the approval never named and which can change underneath an
// approved script; requiring the name keeps the grant checkable and the script
// reproducible.
func (h *hostState) allowConnection(name string) error {
	if h.opts.Grants == nil {
		return nil
	}
	if name == "" {
		return fmt.Errorf("an approved script must name the connection to query (granted: %s); the platform's default connection is not what was approved",
			orNone(h.opts.Grants.Connections))
	}
	if !h.opts.Grants.AllowsConnection(name) {
		return fmt.Errorf("connection %q is not in this script's approved grant (granted: %s); widening what a script may reach requires approving it again",
			name, orNone(h.opts.Grants.Connections))
	}
	return nil
}

// resolveDestination turns the destination a script named into the address its
// approval pinned, refusing one the grant does not cover.
//
// A grant with no destinations is a script approved to compute and not to
// persist, which is a deliberate and useful state, so it gets its own message
// rather than reading as a misconfiguration.
//
// A nil grant is the draft case. A draft has no approval to resolve an address
// from and writes nothing anyway, so it gets the KIND alone: enough for the
// argument checks a draft must answer the same way an approved run will — a key
// aimed at the portal is a mistake in both — and nothing that would let a draft
// address a place nobody has granted.
func (h *hostState) resolveDestination(name string) (script.Destination, error) {
	if h.opts.Grants == nil {
		if name == script.DestinationPortal {
			return script.PortalDestination(), nil
		}
		return script.Destination{Name: name, Kind: script.DestinationKindS3}, nil
	}
	if d, ok := h.opts.Grants.Destination(name); ok {
		return d, nil
	}
	if len(h.opts.Grants.Destinations) == 0 {
		return script.Destination{}, fmt.Errorf("this script was approved with no output destinations, so it may compute but not write; approving it again with the %q destination would let it write", name)
	}
	return script.Destination{}, fmt.Errorf("destination %q is not in this script's approved grant (granted: %s); widening what a script may reach requires approving it again",
		name, orNone(h.opts.Grants.DestinationNames()))
}

// orNone renders a granted list for an error message, naming the empty case
// rather than printing an empty bracket pair.
func orNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// runValue builds the frozen run record — the script's ONLY source of time and
// of caller input, read as run.run_id, run.fire_time, and run.params["name"].
//
// It is frozen so nothing downstream in the script can rewrite the fire time
// and make a run unreproducible from its own record. fire_time is a value
// pinned when the run was created, never a clock read: that is what lets a
// daily report recompute "yesterday" identically when it is re-run months
// later to explain what it said.
func (h *hostState) runValue() starlark.Value {
	params := starlark.NewDict(len(h.opts.Params))
	for _, name := range sortedKeys(h.opts.Params) {
		v, err := toStarlark(h.opts.Params[name])
		if err != nil {
			// Params are bound and type-checked by script.BindParams before a run
			// exists, so an unconvertible value here is a defect in the caller,
			// not author input. Surface it as None rather than failing the run
			// with a message no author can act on.
			v = starlark.None
		}
		_ = params.SetKey(starlark.String(name), v)
	}
	rec := starlarkstruct.FromStringDict(starlark.String("run"), starlark.StringDict{
		"run_id":    starlark.String(h.opts.RunID),
		"fire_time": starlark.String(h.opts.FireTime.UTC().Format(timeLayout)),
		"params":    params,
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
	if err := h.allowCapability(CapabilityQuery); err != nil {
		return nil, argErr(b, err)
	}
	if err := h.allowConnection(connection); err != nil {
		return nil, argErr(b, err)
	}
	bound, err := bindSQL(sql, params)
	if err != nil {
		return nil, argErr(b, err)
	}

	call := map[string]any{"sql": bound, "limit": h.opts.MaxRows}
	if connection != "" {
		call["connection"] = connection
	}
	out, err := h.opts.Caller.CallTool(h.ctx, toolQuery, call)
	if err != nil {
		return nil, argErr(b, err)
	}
	h.queries++
	return h.queryResult(b.Name(), out)
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
		v, err := toStarlark(out[key])
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
// Which of the two happens is decided by the caller, not by the script. A draft
// run is given no Exporter, so it previews: an author iterating on a report
// sees the shape long before an approval exists to make it real, and a draft
// still writes no state. An approved run is given one, and the same call writes
// a new version of the run's output asset, or delivers an object to a granted
// bucket. The arguments are identical either way, so the script an author
// finishes is the script that runs.
//
// One binding covers both, rather than a second one for delivery. The
// difference between refreshing a dashboard and dropping a CSV for another
// system is WHERE the output goes, which is the destination axis of the grant;
// splitting it into two bindings would put the grant check, the audit record,
// and the exactly-once rule in two places to drift apart.
func (h *hostState) export(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	// The capability is checked before anything about this particular output,
	// because it is the coarser question: a script that may not export at all
	// should read that, not a complaint about where it was going to write.
	if err := h.allowCapability(CapabilityExport); err != nil {
		return nil, argErr(b, err)
	}
	req, err := h.exportRequest(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(h.exports) >= maxExports {
		return nil, fmt.Errorf("in %s: a run may produce at most %d outputs", b.Name(), maxExports)
	}
	record, err := h.persistOrPreview(b, req)
	if err != nil {
		return nil, err
	}
	h.exports = append(h.exports, record)
	return exportValue(record), nil
}

// exportRequest unpacks and checks one platform.export call: the output's name
// and format, the destination it names, the key it asks for beneath that
// destination's granted prefix, and the rows themselves.
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
		return ExportRequest{}, fmt.Errorf("in %s: format %q is not one of %s", b.Name(), format, sortedSet(exportFormats))
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
	list, err := exportRows(b, rows)
	if err != nil {
		return ExportRequest{}, err
	}
	return ExportRequest{
		Name: name, Format: format, Columns: columnOrder(rows), Rows: list,
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
// output, so it is checked against the rules that keep it under the granted
// prefix and left exactly as written.
func checkExportKey(b *starlark.Builtin, destination script.Destination, key string) error {
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

// exportRows converts the rows argument to the list of dicts every output
// format is written from.
func exportRows(b *starlark.Builtin, rows starlark.Value) ([]any, error) {
	goRows, err := fromStarlark(rows)
	if err != nil {
		return nil, argErr(b, err)
	}
	list, ok := goRows.([]any)
	if !ok {
		return nil, fmt.Errorf("in %s: rows must be a list of dicts, got %s", b.Name(), rows.Type())
	}
	return list, nil
}

// persistOrPreview writes the output through the run's Exporter, or measures it
// when the run has none.
func (h *hostState) persistOrPreview(b *starlark.Builtin, req ExportRequest) (ExportRecord, error) {
	record := ExportRecord{
		Name: req.Name, Destination: req.Destination.Name, Format: req.Format,
		RowCount: len(req.Rows), Bytes: approxJSONBytes(req.Rows),
		Preview: h.opts.Exporter == nil,
	}
	if h.opts.Exporter == nil {
		return record, nil
	}
	written, err := h.opts.Exporter.Export(h.ctx, req)
	if err != nil {
		return ExportRecord{}, argErr(b, err)
	}
	record.AssetID = written.AssetID
	record.AssetVersion = written.AssetVersion
	record.Bucket = written.Bucket
	record.Key = written.Key
	// The writer's own accounting wins: it knows the serialized size of the
	// format it actually wrote, which the pre-serialization estimate does not.
	if written.Bytes > 0 {
		record.Bytes = written.Bytes
	}
	return record, nil
}

// exportValue renders one export record as the dict the script receives. Where
// the output went decides which half of the record is present: an asset version
// for the portal, an object for a bucket.
func exportValue(record ExportRecord) starlark.Value {
	out := starlark.NewDict(exportRecordFields)
	_ = out.SetKey(starlark.String("preview"), starlark.Bool(record.Preview))
	_ = out.SetKey(starlark.String("name"), starlark.String(record.Name))
	_ = out.SetKey(starlark.String("destination"), starlark.String(record.Destination))
	_ = out.SetKey(starlark.String("format"), starlark.String(record.Format))
	_ = out.SetKey(starlark.String("row_count"), starlark.MakeInt(record.RowCount))
	_ = out.SetKey(starlark.String("bytes"), starlark.MakeInt(record.Bytes))
	if record.AssetID != "" {
		_ = out.SetKey(starlark.String("asset_id"), starlark.String(record.AssetID))
		_ = out.SetKey(starlark.String("asset_version"), starlark.MakeInt(record.AssetVersion))
	}
	if record.Key != "" {
		_ = out.SetKey(starlark.String("bucket"), starlark.String(record.Bucket))
		_ = out.SetKey(starlark.String("key"), starlark.String(record.Key))
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
