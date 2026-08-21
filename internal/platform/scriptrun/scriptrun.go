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
// itself. Neither
// starlark-go nor any comparable embedded interpreter offers a hard MEMORY cap,
// so a pathological script can still grow the process heap. That residual risk
// is recorded in docs/scripts/security.md rather than papered over.
package scriptrun

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

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
	// time spent inside host calls. It matches the ceiling trino_export already
	// applies to a synchronous export.
	RunTimeout = 10 * time.Minute

	// RunMaxRows caps the rows one platform.query may return.
	RunMaxRows = 20_000

	// RunMaxResultBytes caps the serialized size of one query result.
	RunMaxResultBytes = 32 << 20
)

// RunLimits returns the limit set a platform run executes under, so a caller
// configures them by naming the policy rather than by copying four numbers it
// would then have to keep in step.
func RunLimits() Options {
	return Options{
		MaxSteps:       RunMaxSteps,
		Timeout:        RunTimeout,
		MaxRows:        RunMaxRows,
		MaxResultBytes: RunMaxResultBytes,
		MaxLogBytes:    MaxLogBytes,
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
	// message; the script sees it as a Starlark error and the run fails.
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
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

	// Caller issues the script's platform tool calls. A nil Caller leaves the
	// platform module predeclared but every call on it fails, which is what a
	// syntax-only execution wants.
	Caller Caller

	// Destinations is the deployment's configured bucket destinations, the set
	// a platform.export destination name resolves against at run time. The
	// portal is built in and never listed here. A draft run and a platform run
	// carry the same set, so the script an author finishes is the script that
	// runs.
	Destinations []script.Destination

	// Exporter persists what platform.export produces. nil previews instead,
	// which is what a draft run does.
	Exporter Exporter

	// Limits. Zero means the draft default.
	MaxSteps       uint64
	Timeout        time.Duration
	MaxRows        int
	MaxResultBytes int
	MaxLogBytes    int
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
	// this order; see columnOrder.
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
}

// ExportResult is where one output landed. A portal output reports the asset
// version it created; a delivered one reports the object it wrote.
type ExportResult struct {
	AssetID      string
	AssetVersion int
	Bucket       string
	Key          string
	// Bytes is the serialized size actually written.
	Bytes int
}

// ExportRecord is what one platform.export call did, in call order on the run's
// Result. A record with Preview set measured the output and wrote nothing,
// which is what a draft run does; otherwise it names the asset version written
// or the object delivered.
type ExportRecord struct {
	Name string `json:"name"`
	// Destination is the name the script wrote, so a run that sends one result
	// to two places reads as two records rather than as a repeat.
	Destination string `json:"destination"`
	Format      string `json:"format"`
	RowCount    int    `json:"row_count"`
	// Document marks an output written verbatim from a string body, whose
	// RowCount is therefore not a fact about it: without the marker a surface
	// rendering "N rows as html" would describe a dashboard as an empty table.
	Document bool `json:"document,omitempty"`
	// Refresh marks a platform.publish_data call: the run replaced the data
	// region of an existing asset rather than writing a whole output, and
	// Bytes is the payload spliced in, not the document around it.
	Refresh bool `json:"refresh,omitempty"`
	// Bytes is the serialized length of the output in its declared format. A
	// preview serializes to measure rather than estimating, so the number is
	// the same one a real run would report for the same rows.
	Bytes int `json:"bytes"`
	// Preview is true when nothing was persisted.
	Preview      bool   `json:"preview"`
	AssetID      string `json:"asset_id,omitempty"`
	AssetVersion int    `json:"asset_version,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Key          string `json:"key,omitempty"`
}

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
// Failures here are deterministic by construction: the same source on the same
// inputs fails the same way. Callers must not retry them.
func Run(ctx context.Context, opts Options) (*Result, error) {
	opts = opts.withDefaults()

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	log := &logBuffer{limit: opts.MaxLogBytes}
	host := &hostState{opts: opts, ctx: runCtx}
	var overStep atomic.Bool

	thread := &starlark.Thread{
		Name:  opts.Name,
		Print: func(_ *starlark.Thread, msg string) { log.write(msg) },
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
	_, execErr := starlark.ExecFileOptions(fileOptions, thread, opts.Name, opts.Source, predeclared(host))
	result := &Result{
		Log:          log.string(),
		LogTruncated: log.truncated,
		Steps:        thread.ExecutionSteps(),
		Duration:     time.Since(started),
		Queries:      host.queries,
		Exports:      host.exports,
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
	switch {
	case overStep:
		return fmt.Errorf("halted: %w of %d steps; simplify the script or move the work into SQL: %s",
			ErrStepLimit, maxSteps, detail)
	case ctx.Err() != nil:
		return fmt.Errorf("halted: %w: %s", ErrTimeout, detail)
	default:
		return errors.New(detail)
	}
}

// PredeclaredNames are the globals the platform adds on top of the Starlark
// universe, in the order the dialect contract introduces them.
//
// It is the one definition of that set. predeclared() builds the bindings and
// isPredeclaredName answers for them while validating, and a name present in
// one and absent from the other is the defect that let the contract advertise
// a built-in the environment did not have (#1414): validation would resolve a
// name the run cannot bind, or refuse one it can.
var PredeclaredNames = []string{"platform", "json", "date", "run", sumBuiltinName}

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
				"query":        starlark.NewBuiltin(CapabilityQuery, host.query),
				"export":       starlark.NewBuiltin(CapabilityExport, host.export),
				"publish_data": starlark.NewBuiltin(CapabilityPublishData, host.publishData),
				"call":         starlark.NewBuiltin(CapabilityCall, host.call),
			},
		},
		"json":         json.Module,
		"date":         dateModule,
		"run":          host.runValue(),
		sumBuiltinName: sumBuiltin,
	}
}
