package scriptrun

import (
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptfail"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// callOpts are what one binding asks of the call funnel beyond the call.
type callOpts struct {
	// noRetry turns off the host's re-issue of a read its tool refused as
	// temporary (platform.query(..., retry = False), #2032).
	noRetry bool
}

// invokeOpts are what platform.call and platform.execute ask of invoke.
type invokeOpts struct {
	call callOpts
	// returnFailure hands a failed call back to the script as data
	// (on_error = "return") instead of ending the run.
	returnFailure bool
}

// failed is what a binding answers a failed call with: the failure as data
// when the script asked for it with on_error = "return" and it is a failure of
// the call, and otherwise the error that ends the run.
func (h *hostState) failed(b *starlark.Builtin, err error, returnFailure bool) (starlark.Value, error) {
	if returnFailure {
		if value, ok := h.returned(err); ok {
			line, _, _ := strings.Cut(err.Error(), "\n")
			h.log.Print(fmt.Sprintf("%s failed and returned the failure to the script: %s", b.Name(), line))
			return value, nil
		}
	}
	return nil, argErr(b, err)
}

// returned is {"error": <the envelope>} for a failed call, and false when err
// is not a failure of the call -- the run's deadline, its memory budget --
// which ends the run however the script asked. Only the error is returned, so
// a script that reads a result field without checking error fails at that
// read rather than proceeding on a result that does not exist.
func (h *hostState) returned(err error) (starlark.Value, bool) {
	if h.ctx.Err() != nil || errors.Is(err, scriptguard.ErrMemoryBudget) {
		return nil, false
	}
	env, ok := scriptfail.Envelope(err)
	if !ok {
		return nil, false
	}
	value, convErr := starlarkconv.ResultToStarlark(map[string]any{"error": env})
	return value, convErr == nil
}
