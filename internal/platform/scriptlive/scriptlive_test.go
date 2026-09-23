package scriptlive

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// run executes source with only this package's bindings on platform.
func run(t *testing.T, l *Live, source string) error {
	t.Helper()
	thread := &starlark.Thread{Name: "test", Print: func(_ *starlark.Thread, msg string) { l.Print(msg) }}
	globals := starlark.StringDict{
		"platform": &starlarkstruct.Module{Name: "platform", Members: l.Bindings()},
	}
	opts := &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true}
	if _, err := starlark.ExecFileOptions(opts, thread, "test.star", source, globals); err != nil {
		return fmt.Errorf("running test.star: %w", err)
	}
	return nil
}

func TestProgress_RecordsTheLatestReport(t *testing.T) {
	l := New(1024, 0)
	l.now = func() time.Time { return time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("x", 3600)) }
	require.NoError(t, run(t, l, `
platform.progress("starting")
for i in range(3):
    platform.progress("step", done=i + 1, total=10)
`))
	p := l.Progress()
	require.NotNil(t, p)
	assert.Equal(t, "step", p.Message)
	assert.Equal(t, int64(3), *p.Done)
	assert.Equal(t, int64(10), *p.Total)
	assert.Equal(t, time.UTC, p.At.Location())

	require.NoError(t, run(t, l, `platform.progress("only a message")`))
	p = l.Progress()
	assert.Nil(t, p.Done, "a report without a count clears the count")
	assert.Nil(t, p.Total)
}

func TestProgress_RefusesABadCount(t *testing.T) {
	for _, src := range []string{
		`platform.progress("x", done=-1)`,
		`platform.progress("x", total="ten")`,
		`platform.progress("x", done=1.5)`,
		`platform.progress()`,
	} {
		assert.Error(t, run(t, New(64, 0), src), src)
	}
}

func TestProgress_CutsALongMessageOnARune(t *testing.T) {
	l := New(64, 0)
	require.NoError(t, run(t, l, `platform.progress("`+strings.Repeat("é", 400)+`")`))
	msg := l.Progress().Message
	assert.LessOrEqual(t, len(msg), maxProgressMessage)
	assert.True(t, strings.HasSuffix(msg, "é"), "the cut never splits a character")
}

// TestResult_IsSetOnceAsJSON is #1845's contract: the value comes back as
// JSON, a second call fails the run, and a value JSON cannot hold or one over
// the cap fails it with the reason rather than being cut short.
func TestResult_IsSetOnceAsJSON(t *testing.T) {
	l := New(64, 0)
	require.NoError(t, run(t, l, `platform.result({"total": 42, "ids": [1, 2]})`))
	assert.JSONEq(t, `{"total": 42, "ids": [1, 2]}`, string(l.Result()))

	err := run(t, l, `platform.result(1)`)
	require.ErrorContains(t, err, "already set its result")
	assert.JSONEq(t, `{"total": 42, "ids": [1, 2]}`, string(l.Result()), "the first value stands")

	require.ErrorContains(t, run(t, New(64, 0), `platform.result(lambda: 1)`), "cannot be a JSON result")
	require.ErrorContains(t, run(t, New(64, 16), `platform.result("x" * 100)`), "over the 16-byte cap")
	require.ErrorContains(t, run(t, New(64, 0), `platform.result()`), "missing argument")

	scalar := New(64, 0)
	require.NoError(t, run(t, scalar, `platform.result(None)`))
	assert.Equal(t, "null", string(scalar.Result()), "None is a value, and it counts as the one result")
}

// TestReturnIsNotAName pins why the binding is platform.result: return is a
// Starlark keyword, so platform.return(...) cannot be written at all.
func TestReturnIsNotAName(t *testing.T) {
	err := run(t, New(64, 0), `platform.return(1)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an identifier")
}

func TestLog_KeepsTheHeadAndMarksTheCut(t *testing.T) {
	l := New(10, 0)
	l.Print("abc")
	l.Print("éééé") // 9 bytes with the newline: cut to fit the 6 left
	l.Print("dropped")
	text, truncated := l.Log()
	assert.True(t, truncated)
	assert.True(t, strings.HasPrefix(text, "abc\néé"))
	assert.Contains(t, text, "log truncated")
	assert.NotContains(t, text, "dropped")

	full := New(4, 0)
	full.Print("abcd") // exactly fills the cap with nothing left for the newline
	full.Print("x")
	_, truncated = full.Log()
	assert.True(t, truncated)
}

// TestSnapshot_IsSafeWhileTheRunWrites reads the state from another goroutine
// while the interpreter's goroutine writes it, which is what the worker does.
func TestSnapshot_IsSafeWhileTheRunWrites(t *testing.T) {
	l := New(1<<16, 0)
	var wg sync.WaitGroup
	wg.Go(func() {
		assert.NoError(t, run(t, l, `
for i in range(200):
    print("line", i)
    platform.progress("row", done=i, total=200)
`))
	})
	var last uint64
	for range 50 {
		snap, version := l.Snapshot()
		assert.GreaterOrEqual(t, version, last)
		last = version
		_ = snap.Log
	}
	wg.Wait()
	snap, _ := l.Snapshot()
	assert.Equal(t, int64(199), *snap.Progress.Done)
	assert.Contains(t, snap.Log, "line 199")
}
