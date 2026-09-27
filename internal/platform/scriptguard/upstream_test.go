package scriptguard

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/runstate"
)

var errLimit = errors.New("step limit")

// #1935: the run's latest call decides. A 5xx or 429 answer makes a script
// failure after it the upstream's; any later answer, or a call with no
// upstream answer, clears it.
func TestLastUpstream_Attribute(t *testing.T) {
	scriptErr := errors.New("fail: NWS returned 500")
	for name, tc := range map[string]struct {
		calls func(*LastUpstream)
		err   error
		cause string
		note  string
	}{
		"nothing noted": {func(*LastUpstream) {}, scriptErr, runstate.CauseScript, ""},
		"a 500 last": {
			func(l *LastUpstream) { l.Note("api_invoke_endpoint", map[string]any{"status": float64(500)}) },
			scriptErr, runstate.CauseUpstream, "the script failed straight after api_invoke_endpoint answered 500 Internal Server Error; recorded as an upstream failure",
		},
		"a 500 then a 200": {func(l *LastUpstream) {
			l.Note("api_invoke_endpoint", map[string]any{"status": float64(500)})
			l.Note("api_invoke_endpoint", map[string]any{"status": float64(200)})
		}, scriptErr, runstate.CauseScript, ""},
		"a 500 then a refused call": {func(l *LastUpstream) {
			l.Note("api_export", map[string]any{"upstream_status": float64(502)})
			l.Clear()
		}, scriptErr, runstate.CauseScript, ""},
		"a limit keeps its cause": {
			func(l *LastUpstream) { l.Note("x", map[string]any{"status": float64(503)}) },
			fmt.Errorf("halted: %w", errLimit), runstate.CauseScript, "",
		},
		"a memory stop keeps its cause": {
			func(l *LastUpstream) { l.Note("x", map[string]any{"status": float64(503)}) },
			ErrMemoryBudget, runstate.CauseMemory, "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var l LastUpstream
			tc.calls(&l)
			var noted string
			err := l.Attribute(tc.err, func(s string) { noted = s }, errLimit)
			assert.Equal(t, tc.cause, Cause(err))
			assert.Equal(t, tc.note, noted)
			assert.Equal(t, tc.err.Error(), err.Error(), "the author reads the same failure")
		})
	}
}

// Fail is the universe's fail with retryable= added.
func TestFail(t *testing.T) {
	run := func(src string) error {
		_, err := starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t", src, starlark.StringDict{"fail": Fail})
		return err //nolint:wrapcheck // the interpreter's error is what the test reads
	}
	err := run(`fail("feed not published", "yet", retryable=True)`)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseTransient, Cause(err))
	assert.Contains(t, err.Error(), "fail: feed not published yet")

	err = run(`fail("a", 1, sep="-", retryable=False)`)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseScript, Cause(err))
	assert.Contains(t, err.Error(), "fail: a-1")

	err = run(`fail("x", retryable="yes")`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retryable must be True or False, not string")
}
