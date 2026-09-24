// Package scriptguard is what stops a managed-script run apart from the
// script's own logic, and what that stop is recorded as: the memory budget a
// run is measured against at every host call (#1861), and the cause a failed
// run is recorded under -- the script's, an upstream that did not answer, or
// the budget -- which decides whether running it again is expected to succeed
// (#1859).
//
// It knows the Starlark value model, and nothing about runs, stores or
// workers: the engine in internal/platform/scriptrun calls into it at its host
// bindings. The waits the host makes on an upstream that asked it to come back
// later are internal/upstreamretry's.
package scriptguard

import (
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// ErrMemoryBudget marks a run stopped for holding more memory than its budget.
var ErrMemoryBudget = errors.New("script exceeded its memory budget")

// UpstreamError is a tool call that failed because the upstream it reached was
// unavailable: it timed out, dropped the connection, or could not be reached,
// or the run's deadline arrived while the host was waiting to retry it. The
// same call made later is expected to succeed, so a run it ends is recorded
// as retryable.
type UpstreamError struct {
	// Tool is the tool the script called.
	Tool string
	err  error
}

// NewUpstreamError wraps the failure of a call to tool as an upstream one.
func NewUpstreamError(tool string, err error) *UpstreamError {
	return &UpstreamError{Tool: tool, err: err}
}

// Error returns the tool's own failure text, which is what the author reads.
func (e *UpstreamError) Error() string { return e.err.Error() }

// Unwrap returns the failure the tool reported.
func (e *UpstreamError) Unwrap() error { return e.err }

// Cause is the cause a run that failed with err is recorded under: memory for
// a budget it exceeded, upstream for an upstream that was unavailable, and the
// script's own for every other failure the interpreter reports, which is what
// reproduces on the same inputs.
func Cause(err error) string {
	var upstream *UpstreamError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrMemoryBudget):
		return runstate.CauseMemory
	case errors.As(err, &upstream):
		return runstate.CauseUpstream
	default:
		return runstate.CauseScript
	}
}

// budgetError is the refusal a run over its budget fails with. Its text names
// the budget, what the run held and where it was measured, which is what the
// author needs to change the script.
type budgetError struct {
	held, budget int64
	at           string
	results      string
}

func (e *budgetError) Error() string {
	msg := fmt.Sprintf("in %s: the run exceeded its %s memory budget, holding about %s of values",
		e.at, FormatBytes(e.budget), FormatBytes(e.held))
	if e.results != "" {
		msg += " after " + e.results
	}
	return msg + ". A page of rows costs several times its size on the wire once decoded: page the work, " +
		"export each page with platform.export(..., append=True), and keep only what the next page needs " +
		"(scripts.worker.max_run_memory sets the budget)"
}

func (*budgetError) Unwrap() error { return ErrMemoryBudget }

// Byte units for FormatBytes.
const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
)

// FormatBytes renders a size the way the budget is configured: "128 MiB".
func FormatBytes(n int64) string {
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%d MiB", n/mib)
	case n >= kib:
		return fmt.Sprintf("%d KiB", n/kib)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
