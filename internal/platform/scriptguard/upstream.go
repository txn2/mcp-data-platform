package scriptguard

import (
	"errors"
	"fmt"
	"net/http"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
)

// LastUpstream keeps whether the upstream answered a run's most recent tool
// call with a failure (#1935). The api gateway hands a script a 5xx as data,
// so a script that checks the status and calls fail() -- the only thing it
// can do -- fails because of that answer, and is recorded as the upstream's
// rather than told the next run fails the same way. The zero value holds none.
type LastUpstream struct {
	tool   string
	status int
}

// Note records the answer to the run's latest tool call: a 5xx or 429 is kept,
// anything else clears what an earlier call left.
func (l *LastUpstream) Note(tool string, out map[string]any) {
	*l = LastUpstream{}
	if status, failed := upstreamretry.Failed(out); failed {
		*l = LastUpstream{tool: tool, status: status}
	}
}

// Clear records a latest call that got no upstream answer at all.
func (l *LastUpstream) Clear() { *l = LastUpstream{} }

// Attribute returns err as the upstream's when the run's latest call was
// answered with a failure and err is otherwise the script's own, handing note
// the line the run's log records why; err unchanged otherwise. A limit a
// caller names in keep, a memory stop and a failure already classified keep
// theirs.
func (l *LastUpstream) Attribute(err error, note func(string), keep ...error) error {
	if l.status == 0 || Cause(err) != runstate.CauseScript {
		return err
	}
	for _, k := range keep {
		if errors.Is(err, k) {
			return err
		}
	}
	note(fmt.Sprintf("the script failed straight after %s answered %d %s; recorded as an upstream failure",
		l.tool, l.status, http.StatusText(l.status)))
	return NewUpstreamError(l.tool, err)
}

// Fail is Starlark's fail(*args, sep=" ") with one more keyword (#1935):
// retryable=True declares the failure temporary, so the run is recorded as
// retryable (cause transient) rather than as the script's own. Everything
// else is the universe's fail, unchanged, so a script that never passes
// retryable= fails exactly as it did.
var Fail = starlark.NewBuiltin("fail", func(
	thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple,
) (starlark.Value, error) {
	retryable := false
	rest := make([]starlark.Tuple, 0, len(kwargs))
	for _, kv := range kwargs {
		if name, _ := starlark.AsString(kv[0]); name != "retryable" {
			rest = append(rest, kv)
			continue
		}
		flag, ok := kv[1].(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("fail: retryable must be True or False, not %s", kv[1].Type())
		}
		retryable = bool(flag)
	}
	_, err := starlark.Call(thread, starlark.Universe["fail"], args, rest)
	// Call reports the universe's failure as an EvalError of its own frame;
	// the failure itself is what this builtin raises, so the backtrace the
	// author reads names one fail() call, as before.
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) && evalErr.Unwrap() != nil {
		err = evalErr.Unwrap()
	}
	if retryable {
		return nil, NewTransientError(err)
	}
	return nil, err //nolint:wrapcheck // fail's own text is the diagnostic
})
