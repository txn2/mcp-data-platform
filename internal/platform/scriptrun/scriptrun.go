// Package scriptrun is the managed-script execution engine: an embedded
// Starlark interpreter, the curated host stdlib scripts are allowed to call,
// and the static validator the authoring loop answers with.
//
// Starlark (go.starlark.net) is the engine because determinism is a property of
// the LANGUAGE rather than of a blocklist the platform has to maintain: the
// language has no ambient clock, randomness, filesystem, or network, iteration
// order is specified, and unbounded loops and recursion are off by default. A
// script can only affect the world through bindings this package predeclares,
// and every one of them is one ordinary platform tool call: platform.call names
// the tool, and the three named helpers are that call with a constant and some
// behavior worth a name. What a script may reach is what its author's persona
// authorizes, at every call, at run time; what a READER can enumerate is what
// Validate reads out of the source.
//
// The determinism contract this engine supports, stated exactly:
//
//	same script version + same parameters + same underlying data => same output
//
// It is not "identical forever". The warehouse changes between runs, and that is
// the point of re-running. What the platform eliminates is every source of
// variation it controls: no clock or RNG is predeclared, the fire time arrives
// as a pinned parameter rather than a clock read, and tool results reaching a
// script carry no semantic enrichment (which varies with catalog state).
//
// Resource limits, honestly: starlark-go bounds CPU with an execution-step
// limit and wall-clock through thread cancellation, and this package adds a
// hard byte cap on every host result plus bounded log capture. The ROW cap and
// its push-down into the query belong to platform.query, which is the reason
// that helper exists: a script that calls the query tool through platform.call
// is handed the tool's own result, its truncation flag included, and reads it
// itself. Neither starlark-go nor any comparable embedded interpreter offers a
// hard MEMORY cap, so memory is measured instead (#1861): at every host call
// the values the run can still reach are sized (internal/platform/scriptguard)
// and a run over its budget fails there. A script that grows its heap between
// host calls is measured at the next one, and the worker's lone-run guard
// stops what a budget misses before the kernel does; both are recorded in
// docs/scripts/security.md.
package scriptrun

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/exporttable"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptout"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptout/exportrecord"
	"github.com/txn2/mcp-data-platform/internal/scriptdate"
	"github.com/txn2/mcp-data-platform/internal/scriptsum"
	"github.com/txn2/mcp-data-platform/internal/scriptxml"
	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Draft-execution limits. A draft runs interactively, under its author's own
// identity, while they iterate — so it is bounded more tightly than a platform
// run will be: the author is waiting for the answer, and a runaway draft should
// fail fast with a legible message instead of occupying a serving replica.
const (
	// DraftMaxSteps caps interpreter execution steps for a draft run.
	DraftMaxSteps = 2_000_000

	// DraftTimeout caps wall-clock time for a draft run, including the time
	// spent inside host calls.
	DraftTimeout = 60 * time.Second

	// DraftMaxRows caps the rows one platform.query may return to a draft.
	DraftMaxRows = 5_000

	// DraftMaxResultBytes caps the serialized size of one platform.query result.
	DraftMaxResultBytes = 8 << 20

	// MaxLogBytes caps captured print output. Anything a script needs to emit
	// that is larger than a log is an output asset, not a log line.
	MaxLogBytes = 64 << 10
)

// Platform-run limits. A worker-executed run is looser than a draft on every
// axis, because nobody is waiting at a prompt for it — but it is still bounded
// on all of them, because the one resource no embedded interpreter of this
// class can cap is memory, and every limit here is part of what keeps a
// runaway script from taking the process with it.
const (
	// RunMaxSteps caps interpreter execution steps for a platform run.
	RunMaxSteps = 20_000_000

	// RunTimeout caps wall-clock time for one platform run, including the
	// time spent inside host calls, unless the deployment sets
	// scripts.worker.run_timeout (#1843). A tool a run calls keeps its own
	// ceiling: a trino_export or api_export inside a run is bounded by that
	// tool's timeout as well as by this one.
	RunTimeout = 15 * time.Minute

	// RunMaxRows caps the rows one platform.query may return.
	RunMaxRows = 20_000

	// RunMaxResultBytes caps the serialized size of one query result.
	RunMaxResultBytes = 32 << 20
)

// PlatformLimits are the ceilings of a platform run a deployment may set
// (scripts.worker.run_timeout, max_steps, max_query_rows, #1843). A zero or
// negative field takes the default: RunTimeout, RunMaxSteps, RunMaxRows.
type PlatformLimits struct {
	Timeout  time.Duration
	MaxSteps uint64
	MaxRows  int
	// ResultMaxBytes caps the value platform.result hands back (#1845);
	// zero is scriptlive.DefaultMaxResultBytes.
	ResultMaxBytes int
	// MaxMemoryBytes is the memory one run may hold (#1861); zero sets no
	// budget. It has no default here: the default is a share of the
	// container's limit, which only the composition root can read.
	MaxMemoryBytes int64
}

// WithDefaults fills every unset limit.
func (l PlatformLimits) WithDefaults() PlatformLimits {
	if l.Timeout <= 0 {
		l.Timeout = RunTimeout
	}
	if l.MaxSteps == 0 {
		l.MaxSteps = RunMaxSteps
	}
	if l.MaxRows <= 0 {
		l.MaxRows = RunMaxRows
	}
	if l.ResultMaxBytes <= 0 {
		l.ResultMaxBytes = scriptlive.DefaultMaxResultBytes
	}
	return l
}

// RunLimits returns the limit set a platform run executes under, so a caller
// configures them by naming the policy rather than by copying four numbers it
// would then have to keep in step.
func RunLimits(l PlatformLimits) Options {
	l = l.WithDefaults()
	return Options{
		MaxSteps:       l.MaxSteps,
		Timeout:        l.Timeout,
		MaxRows:        l.MaxRows,
		MaxResultBytes: RunMaxResultBytes,
		MaxLogBytes:    MaxLogBytes,
		MaxMemoryBytes: l.MaxMemoryBytes,
	}
}

// ErrStepLimit marks a run stopped by the execution-step limit, and ErrTimeout
// a run stopped by the wall-clock limit. Both are script-side failures: the
// same script on the same inputs will hit them again, so a caller must never
// retry them.
var (
	ErrStepLimit = errors.New("script exceeded its execution-step limit")
	ErrTimeout   = errors.New("script exceeded its time limit")
)

// Caller issues one platform tool call on behalf of a running script and
// returns the tool's structured result.
//
// Every host binding goes through this one seam, and the production
// implementation drives the fully assembled MCP server over an in-memory
// session — so persona and connection authorization, rate limiting, and audit
// all apply to a script's calls exactly as they apply to an agent's, with no
// second implementation to keep in step. Binding host functions straight onto
// narrow Go interfaces would be faster per call and would mean re-implementing
// authorization for user-authored code, which is the drift this platform's
// single-funnel design exists to prevent.
type Caller interface {
	// CallTool invokes the named tool and returns its structured content. A
	// tool that reports an error returns a non-nil error carrying the tool's
	// message; the script sees it as a Starlark error and the run fails. The
	// one exception is a *RefusalError whose Code is rate_limited: the engine
	// waits its RetryAfter against the run's deadline and issues the call
	// again, and the script sees only the admitted call's result.
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
}

// ReadOnlyDeclarer is the optional half of a Caller that can report what the
// server it is connected to advertises about a tool.
//
// It is optional because it answers a question only a real session can answer,
// and the engine works without it: a Caller that does not implement it leaves
// every unclassified tool a write under the draft's barrier, which is the
// barrier's own default. SessionCaller implements it.
type ReadOnlyDeclarer interface {
	// DeclaresReadOnly reports whether the tool is advertised with MCP's
	// read-only annotation, and whether it is advertised at all.
	DeclaresReadOnly(ctx context.Context, name string) (readOnly, known bool)
}

// Options configures one script execution.
type Options struct {
	// Source is the Starlark source to execute, and Name labels it in tracebacks.
	Source string
	Name   string

	// RunID, FireTime and Params populate the frozen run dict — the script's
	// only source of time and of caller input.
	RunID    string
	FireTime time.Time
	Params   map[string]any

	// State is the script's state as it stood when the run was created,
	// handed to the script as run.state (#1537). Nil reads as {}: a script
	// that has never saved any. It is an input of the run exactly as Params
	// are, pinned by the caller and never read here.
	State map[string]any

	// Caller issues the script's platform tool calls. A nil Caller leaves the
	// platform module predeclared but every call on it fails, which is what a
	// syntax-only execution wants.
	Caller Caller

	// RunURL is this run's own page, the link a platform.notify post carries
	// when the script names none (#1723). Empty omits the link, which is what
	// a deployment that does not know its public address can honestly say.
	RunURL string
	// Live receives what the run prints and reports while it executes, and
	// the value it returns (#1845, #1847). Nil gives the run its own, which
	// is all a draft needs; a platform run passes one its worker snapshots.
	Live *scriptlive.Live

	// Destinations is the deployment's configured bucket destinations, the set
	// a platform.export destination name resolves against at run time. The
	// portal is built in and never listed here. A draft run and a platform run
	// carry the same set, so the script an author finishes is the script that
	// runs.
	Destinations []script.Destination

	// Exporter persists what platform.export produces. nil previews instead,
	// which is what a draft run does.
	Exporter Exporter

	// Writes says what this run does about a platform.call that persists
	// (#1664). A platform run leaves the zero value, which is what a run has
	// always done.
	Writes WriteBarrier

	// Classifier decides which platform.call calls persist. Its zero value
	// classifies from the declared table alone; a composition root holding the
	// live toolkits gives it the lookup that reads an api gateway operation id,
	// so a draft is not refused for addressing a read the way the platform told
	// it to.
	Classifier toolwrite.Classifier

	// Limits. Zero means the draft default.
	MaxSteps       uint64
	Timeout        time.Duration
	MaxRows        int
	MaxResultBytes int
	MaxLogBytes    int
	// MaxMemoryBytes is the memory the run may hold, measured at every host
	// call (#1861). Zero sets no budget; the peak is measured either way.
	MaxMemoryBytes int64
}

// withDefaults fills unset limits with the draft defaults.
func (o Options) withDefaults() Options {
	if o.MaxSteps == 0 {
		o.MaxSteps = DraftMaxSteps
	}
	if o.Timeout <= 0 {
		o.Timeout = DraftTimeout
	}
	if o.MaxRows <= 0 {
		o.MaxRows = DraftMaxRows
	}
	if o.MaxResultBytes <= 0 {
		o.MaxResultBytes = DraftMaxResultBytes
	}
	if o.MaxLogBytes <= 0 {
		o.MaxLogBytes = MaxLogBytes
	}
	if o.Name == "" {
		o.Name = "script"
	}
	return o
}

// WriteBarrier is what a run does about a platform.call that persists something
// outside it.
//
// The distinction exists because platform.call is the open half of the host
// surface. platform.export, publish_data and save_state make themselves a
// rehearsal on their own — a nil Exporter previews, a staged state is reported —
// but every other write a script makes is an ordinary tool call, so a draft of
// an ingestion script created the resources, registrations and assets a real run
// would, with no run record to explain where they came from (#1664).
//
// It is three states rather than two booleans because "made" and "recorded" are
// one decision: a platform run makes every write and nobody reads a list of
// them, while a draft that was allowed to write owes its author exactly that
// list. Accumulating it on a platform run would be memory the engine otherwise
// bounds, spent on a slice nothing reads.
type WriteBarrier int

const (
	// WritesMade is a platform run: every call is issued, and none is recorded.
	WritesMade WriteBarrier = iota
	// WritesRefused is a draft: a call that persists is refused before it is
	// issued, and the run fails there.
	WritesRefused
	// WritesReported is a draft whose caller asked for the writes: every call
	// is issued, and the ones that persist are listed on the Result.
	WritesReported
)

// Exporter persists one script output. The engine holds it behind this
// interface so nothing here knows what an asset, a bucket, or a portal is: the
// interpreter's job ends at "these rows, under this name, in this format".
type Exporter interface {
	// Export writes one output and reports where it landed. An error fails the
	// run: a report whose output did not persist did not happen.
	Export(ctx context.Context, req ExportRequest) (*ExportResult, error)

	// PublishData replaces the data region of the named output asset with the
	// request's payload and reports the version that created. An error fails
	// the run: a dashboard that did not refresh did not refresh.
	PublishData(ctx context.Context, req PublishRequest) (*ExportResult, error)
}

// PublishRequest is one data-region refresh as the script produced it
// (platform.publish_data, #1389).
type PublishRequest struct {
	// Name identifies the target asset through the same identity rule an
	// export's name resolves by: one (script, name) pair is one asset. The
	// asset must already exist — this call refreshes a region of it and can
	// create nothing.
	Name string
	// Data is the payload the asset's data region will hold, already converted
	// to plain Go values. FormatDataPayload is its one serializer, shared by
	// the draft preview and the platform run's write.
	Data any
}

// ExportRequest is one output as the script produced it.
type ExportRequest struct {
	// Name identifies the output across runs. It is the stable half of the
	// output's identity: the same name from the same script maps to one asset
	// whose versions are its run history.
	Name string
	// Format is one of the formats exportFormats admits.
	Format string
	// Columns is the column order the script wrote, read from the rows before
	// they became order-free Go maps. A tabular format writes its columns in
	// this order; see starlarkconv.ColumnOrder.
	Columns []string
	// Rows is the list of row dicts to write. Nil when the script passed a
	// string body instead, which Body then carries.
	Rows []any
	// Body is the document arm: a string the script composed, persisted
	// verbatim. Valid for the document formats (markdown, text, html, jsx) and
	// nil for a tabular output — exactly one of Body and Rows is the content.
	Body *string
	// Destination is the destination the output goes to, already resolved from
	// the name the script wrote to the address configuration declares for it.
	Destination script.Destination
	// Key is the object key the script asked for beneath the destination's
	// configured prefix, empty when it named none and never set for the portal.
	Key string
	// Register is the table the script asked to register over the written
	// file (#1820), nil when it asked for none. The host binding makes the
	// registration once the Exporter has written the file; a writer ignores it.
	Register *exporttable.Spec
	// References is references= (#1834): nil leaves the asset's alone, [] clears
	// them. The host declares them after the write; a writer ignores it.
	References []string
	// Tags and Metadata are tags= and metadata= (#1848): added to the portal
	// asset's tags, and stored on the version it writes.
	Tags     []string
	Metadata map[string]any
	// Workbook is the xlsx arm (#1849): the sheets, checked. Nil otherwise.
	Workbook *tablexlsx.Workbook
	// Append is append=True (#1861): the rows are a page of an output the run
	// builds across calls. Spooled holds the pages serialized so far, and is
	// the content of the request that writes the output when the run ends.
	Append  bool
	Spooled *scriptout.Spool
}

// RowCount is the data rows the output carries: its rows, or every sheet's.
func (r ExportRequest) RowCount() int {
	switch {
	case r.Workbook != nil:
		return r.Workbook.RowCount()
	case r.Spooled != nil:
		return r.Spooled.Rows()
	default:
		return len(r.Rows)
	}
}

// Sheets is a workbook's sheets and their row counts, nil for any other output.
func (r ExportRequest) Sheets() []tablexlsx.SheetShape {
	if r.Workbook == nil {
		return nil
	}
	return r.Workbook.Shape()
}

// ExportResult is where one output landed. A portal output reports the asset
// version it created; a delivered one reports the object it wrote.
type ExportResult struct {
	AssetID      string
	AssetVersion int
	Bucket       string
	Key          string
	// ResourceID, ResourceRef, ResourceURI and ResourceVersion are where a
	// managed-resource output landed (#1663): the file's id, the reference and
	// the mcp:// URI other tools take, and the version this run recorded. The
	// first three are the same every run, which is the point of that
	// destination.
	ResourceID      string
	ResourceRef     string
	ResourceURI     string
	ResourceVersion int
	// Bytes is the serialized size actually written.
	Bytes int
	// TableChanges is what the version did to the tables registered over the
	// output's file (#1536), one sentence per table.
	TableChanges []string
}

// ExportRecord is what one platform.export call did; see exportrecord.Record.
type ExportRecord = exportrecord.Record

// Result reports one completed execution.
type Result struct {
	// Log is the captured print output, truncated at the log cap.
	Log string `json:"log"`
	// LogTruncated is true when output was dropped at the cap.
	LogTruncated bool `json:"log_truncated"`
	// Steps is the interpreter step count the run consumed, and Duration its
	// wall-clock time. Both are the raw material for sizing a platform run's
	// limits against a draft that already works.
	Steps    uint64        `json:"steps"`
	Duration time.Duration `json:"-"`
	// Queries counts the platform.query calls the run issued.
	Queries int `json:"queries"`
	// Exports lists what every platform.export call did, in call order.
	Exports []ExportRecord `json:"exports"`
	// State is the object the script staged with platform.save_state, nil
	// when it called it never. The last call wins: a run's write is one write,
	// and whether it is applied is the caller's decision — a platform run's
	// store applies it when the run succeeds, a draft reports it.
	State *script.StateWrite `json:"state,omitempty"`
	// Writes lists every platform.call the run made that the platform
	// classifies as persisting something, in call order (#1664). It is the
	// account a draft run with the write barrier lifted owes its author: those
	// calls landed, and nothing else in the response says so.
	Writes []WriteRecord `json:"writes,omitempty"`
	// RefusedWrite is the call the write barrier stopped, nil when it stopped
	// none. There is at most one because the refusal fails the run: the author
	// reads which call ended it without parsing the traceback for it.
	RefusedWrite *WriteRecord `json:"refused_write,omitempty"`
	// Return is the value platform.result handed back and Progress the last
	// platform.progress report, each absent when the script made none.
	Return   stdjson.RawMessage  `json:"result,omitempty"`
	Progress *script.RunProgress `json:"progress,omitempty"`
	// PeakMemory is the most the run was measured holding (#1861), which a
	// draft reports so its author sees how close it came to the budget
	// before a scheduled run does.
	PeakMemory int64 `json:"peak_memory_bytes"`
}

// WriteRecord is one persisting platform.call a run made.
type WriteRecord struct {
	// Tool is the tool name the script passed.
	Tool string `json:"tool"`
	// Call names the call the classifier decided on: the tool, plus the action
	// or method that made it a write where the tool has one.
	Call string `json:"call"`
}

// fileOptions is the dialect every managed script is parsed and resolved under.
//
// while and recursion are OFF. Both are unbounded control flow whose cost
// cannot be read off the source, and a script that needs either is doing
// computation that belongs in SQL. This is the deliberate restrictiveness of
// the feature, not an oversight, and it is the only pair of switches here that
// is about safety.
//
// TopLevelControl and GlobalReassign are ON, and both defaults are inverted on
// purpose. Starlark's defaults come from Bazel, where a .bzl file is a
// DECLARATION loaded by other files: top-level control flow and rebinding a
// top-level name would make what a file declares depend on evaluation order. A
// managed script is the opposite — a procedure executed once, top to bottom, by
// one runner, loaded by nobody. Under the Bazel defaults an author could not
// write `total = 0` and then accumulate into it inside a loop without wrapping
// the whole script in a function, which is friction that buys no safety and no
// determinism: neither switch has anything to do with either. `load` stays
// file-local (and there is nothing to load).
var fileOptions = &syntax.FileOptions{
	Set:               true,
	While:             false,
	TopLevelControl:   true,
	GlobalReassign:    true,
	LoadBindsGlobally: false,
	Recursion:         false,
}

// Run executes a script and returns its result. The error is non-nil when the
// script itself failed — a Starlark error, a refused host call, or a limit —
// and the returned Result still carries whatever log and metrics the run
// produced before failing, because that log is exactly what the author needs.
//
// A failure here is the script's unless it wraps one of the causes
// scriptguard.Cause reads: an upstream that did not answer, or a memory budget
// the run exceeded. Only the first is expected to succeed on a retry.
func Run(ctx context.Context, opts Options) (*Result, error) {
	opts = opts.withDefaults()

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	log := opts.Live
	if log == nil {
		log = scriptlive.New(opts.MaxLogBytes, 0)
	}
	host := &hostState{opts: opts, ctx: runCtx, log: log, mem: scriptguard.NewMeter(opts.MaxMemoryBytes)}
	var overStep atomic.Bool

	thread := &starlark.Thread{
		Name:  opts.Name,
		Print: func(_ *starlark.Thread, msg string) { log.Print(msg) },
	}
	thread.SetMaxExecutionSteps(opts.MaxSteps)
	// starlark-go signals both the step limit and an external stop through the
	// same cancellation path, and reports each as a generic EvalError. Recording
	// which one fired is the only way to tell an author "your script is too
	// expensive" apart from "your query took too long".
	thread.OnMaxSteps = func(th *starlark.Thread) {
		overStep.Store(true)
		th.Cancel("too many steps")
	}

	// The interpreter has no notion of a context, so cancellation is bridged:
	// a watchdog cancels the thread when the run context is done, and the
	// deferred close stops the watchdog on the normal path.
	done := make(chan struct{})
	defer close(done)
	go watchCancel(runCtx, thread, done)

	started := time.Now()
	globals, execErr := starlark.ExecFileOptions(fileOptions, thread, opts.Name, opts.Source, predeclared(host))
	host.mem.Settle(globals)
	if execErr == nil {
		// An appended output is written once, and only by a run that got to
		// the end: its pages are the whole file only then (#1861).
		execErr = host.landAppended()
	}
	logText, logTruncated := log.Log()
	result := &Result{
		Log:          logText,
		LogTruncated: logTruncated,
		Return:       log.Result(),
		Progress:     log.Progress(),
		Steps:        thread.ExecutionSteps(),
		Duration:     time.Since(started),
		Queries:      host.queries,
		Exports:      host.exports,
		State:        host.state,
		Writes:       host.writes,
		RefusedWrite: host.refused,
		PeakMemory:   host.mem.Peak(),
	}
	if execErr != nil {
		return result, classifyExecError(runCtx, execErr, overStep.Load(), opts.MaxSteps)
	}
	return result, nil
}

// watchCancel cancels the interpreter thread when ctx ends, unless the run
// finished first (done closed).
func watchCancel(ctx context.Context, thread *starlark.Thread, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		thread.Cancel(ctx.Err().Error())
	case <-done:
	}
}

// classifyExecError turns an interpreter failure into the error a caller can
// act on. A limit is reported as its own sentinel with the corrective action;
// anything else keeps the Starlark backtrace as the message, because that
// backtrace is the whole diagnostic value of a failed run.
//
// The step limit is checked before the deadline: a script that burns its step
// budget may well exceed the wall clock on the way out, and "too expensive" is
// the actionable half of that pair.
func classifyExecError(ctx context.Context, err error, overStep bool, maxSteps uint64) error {
	detail := err.Error()
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		detail = evalErr.Backtrace()
	}
	failure := &execError{detail: detail, cause: err}
	switch {
	case overStep:
		return fmt.Errorf("halted: %w of %d steps; simplify the script or move the work into SQL: %w",
			ErrStepLimit, maxSteps, failure)
	case ctx.Err() != nil:
		return fmt.Errorf("halted: %w: %w", ErrTimeout, failure)
	default:
		return failure
	}
}

// execError is an interpreter failure as the author reads it -- the
// backtrace -- still wrapping the error a host binding returned, so the cause
// a run is recorded under (scriptguard.Cause) is read from the failure itself
// rather than from its text.
type execError struct {
	detail string
	cause  error
}

func (e *execError) Error() string { return e.detail }
func (e *execError) Unwrap() error { return e.cause }

// PredeclaredNames are the globals the platform adds on top of the Starlark
// universe, in the order the dialect contract introduces them.
//
// It is the one definition of that set. predeclared() builds the bindings and
// isPredeclaredName answers for them while validating, and a name present in
// one and absent from the other is the defect that let the contract advertise
// a built-in the environment did not have (#1414): validation would resolve a
// name the run cannot bind, or refuse one it can.
var PredeclaredNames = []string{"platform", "json", "xml", "date", "run", scriptsum.Name}

// predeclared builds the global environment a script sees. Everything absent
// from this dict is absent from the language: no imports, no filesystem, no
// clock, no randomness, no network.
//
// Its keys are PredeclaredNames, which TestPredeclaredMatchesNames pins.
func predeclared(host *hostState) starlark.StringDict {
	return starlark.StringDict{
		"platform": &starlarkstruct.Module{
			Name: "platform",
			Members: starlark.StringDict{
				"query":        starlark.NewBuiltin(CapabilityQuery, host.guarded(host.query)),
				"export":       starlark.NewBuiltin(CapabilityExport, host.guarded(host.export)),
				"publish_data": starlark.NewBuiltin(CapabilityPublishData, host.guarded(host.publishData)),
				"call":         starlark.NewBuiltin(CapabilityCall, host.guarded(host.call)),
				"save_state":   starlark.NewBuiltin(CapabilitySaveState, host.guarded(host.saveState)),
				"notify":       starlark.NewBuiltin(CapabilityNotify, host.guarded(host.notify)),
				"publish":      starlark.NewBuiltin(CapabilityPublish, host.guarded(host.publish)),
				"progress":     host.log.Bindings()["progress"],
				"result":       host.log.Bindings()["result"],
			},
		},
		"json":         json.Module,
		"xml":          scriptxml.Module,
		"date":         scriptdate.Module,
		"run":          host.runValue(),
		scriptsum.Name: scriptsum.Builtin,
	}
}
