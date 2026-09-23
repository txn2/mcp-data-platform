// Package scriptlive holds what a managed-script run reports while it executes
// and what it hands back when it ends: the log it prints, its latest
// platform.progress, and the value it returns with platform.result (#1845,
// #1847).
//
// One Live serves the interpreter and the reader at once. The interpreter
// writes to it from the run's goroutine; the worker snapshots it every few
// seconds from another and writes the snapshot to the run row, which is how a
// running run shows "120 of 500" and the log so far instead of only "running".
// It is the run's log itself, not a copy of it: the log a finished run records
// is read from the same buffer, so the live view and the final record cannot
// disagree about what was printed.
package scriptlive

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Binding names, as the dialect contract and Validate's capability list name
// them.
const (
	CapabilityProgress = "platform.progress"
	CapabilityResult   = "platform.result"
)

// DefaultMaxResultBytes caps the value platform.result hands back. A result
// is an answer for a caller waiting on the run, not a dataset; anything larger
// is an output.
const DefaultMaxResultBytes = 1 << 20

// maxProgressMessage caps one progress message. It is a status line for a
// person watching the run; a longer one is cut on a rune boundary.
const maxProgressMessage = 512

// truncatedMarker ends a log cut at its cap.
const truncatedMarker = "\n... log truncated at the size cap; write large output as an export instead\n"

// Live is one run's reporting state. Safe for concurrent use.
type Live struct {
	mu        sync.Mutex
	logLimit  int
	log       strings.Builder
	truncated bool
	progress  *script.RunProgress
	// version moves on every change, so a reader that already wrote a
	// snapshot can tell there is nothing new to write.
	version   uint64
	result    json.RawMessage
	maxResult int
	now       func() time.Time
}

// New returns a Live whose log holds at most maxLogBytes and whose result is
// at most maxResultBytes (DefaultMaxResultBytes when not positive).
func New(maxLogBytes, maxResultBytes int) *Live {
	if maxResultBytes <= 0 {
		maxResultBytes = DefaultMaxResultBytes
	}
	return &Live{logLimit: maxLogBytes, maxResult: maxResultBytes, now: time.Now}
}

// Print appends one line to the log, stopping at the cap. The head of the log
// is kept: the first lines say what the run set out to do, which is what the
// reader of a truncated log needs.
func (l *Live) Print(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.truncated {
		return
	}
	remaining := l.logLimit - l.log.Len()
	if remaining <= 0 {
		l.truncated = true
		l.version++
		return
	}
	line := msg + "\n"
	if len(line) > remaining {
		// Cut on a rune boundary: a byte-offset cut through a multi-byte
		// character leaves an invalid byte that json.Marshal silently rewrites
		// to U+FFFD in the response.
		_, _ = l.log.WriteString(truncateRunes(line, remaining))
		l.truncated = true
	} else {
		_, _ = l.log.WriteString(line)
	}
	l.version++
}

// Log returns the captured log, marked when output was dropped, and whether it
// was.
func (l *Live) Log() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.logLocked(), l.truncated
}

func (l *Live) logLocked() string {
	if l.truncated {
		return l.log.String() + truncatedMarker
	}
	return l.log.String()
}

// Snapshot is what the run has reported so far, with the version it was
// taken at.
func (l *Live) Snapshot() (live script.RunLive, version uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return script.RunLive{Progress: l.progressLocked(), Log: l.logLocked(), LogTruncated: l.truncated}, l.version
}

// Progress is the latest progress report, nil before the first.
func (l *Live) Progress() *script.RunProgress {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.progressLocked()
}

func (l *Live) progressLocked() *script.RunProgress {
	if l.progress == nil {
		return nil
	}
	p := *l.progress
	return &p
}

// Result is the value the run returned, nil when it set none.
func (l *Live) Result() json.RawMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.result
}

// Bindings are the platform members this package provides, keyed by the
// member name a script calls.
func (l *Live) Bindings() starlark.StringDict {
	return starlark.StringDict{
		"progress": starlark.NewBuiltin(CapabilityProgress, l.reportProgress),
		"result":   starlark.NewBuiltin(CapabilityResult, l.setResult),
	}
}

// reportProgress implements platform.progress(message, done=None, total=None).
// It only records the report; the worker writes the latest one to the run row
// on its own interval, so a script calling it on every iteration of a tight
// loop costs a lock, not a database write.
func (l *Live) reportProgress(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		message     string
		done, total starlark.Value = starlark.None, starlark.None
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "message", &message, "done?", &done, "total?", &total); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	p := &script.RunProgress{Message: truncateRunes(message, maxProgressMessage)}
	var err error
	if p.Done, err = count(b, "done", done); err != nil {
		return nil, err
	}
	if p.Total, err = count(b, "total", total); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p.At = l.now().UTC()
	l.progress = p
	l.version++
	return starlark.None, nil
}

// count reads an optional non-negative whole number.
func count(b *starlark.Builtin, name string, v starlark.Value) (*int64, error) {
	if v == starlark.None {
		return nil, nil //nolint:nilnil // an absent count is not an error
	}
	i, ok := v.(starlark.Int)
	if !ok {
		return nil, fmt.Errorf("in %s: %s must be a whole number or None, got %s", b.Name(), name, v.Type())
	}
	n, ok := i.Int64()
	if !ok || n < 0 {
		return nil, fmt.Errorf("in %s: %s must be a whole number of zero or more, got %s", b.Name(), name, i)
	}
	return &n, nil
}

// setResult implements platform.result(value): the one value a run hands back
// to whoever is waiting on it. It is set once, and a value that cannot be
// JSON or is over the cap fails the run here, where the author can see which
// call it was, rather than being cut short.
func (l *Live) setResult(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value); err != nil {
		return nil, fmt.Errorf("in %s: %w", b.Name(), err)
	}
	converted, err := starlarkconv.FromStarlark(value)
	if err != nil {
		return nil, fmt.Errorf("in %s: the value cannot be a JSON result: %w", b.Name(), err)
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("in %s: the value cannot be a JSON result: %w", b.Name(), err)
	}
	if len(encoded) > l.maxResult {
		return nil, fmt.Errorf("in %s: the result is %d bytes, over the %d-byte cap; a result is a small answer for the caller, and a larger one is an output (platform.export)",
			b.Name(), len(encoded), l.maxResult)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.result != nil {
		return nil, fmt.Errorf("in %s: this run already set its result; a run hands back one value, so build it whole and set it once", b.Name())
	}
	l.result = encoded
	l.version++
	return starlark.None, nil
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
