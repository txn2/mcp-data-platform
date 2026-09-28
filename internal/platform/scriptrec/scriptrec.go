// Package scriptrec records the host calls a managed script's run makes and
// the answers they were given, and answers a later execution from that record
// (#1939).
//
// A run cannot read a clock or a random number, its fire time and run id are
// pinned, and every effect goes through a host binding. A run is therefore a
// function of its parameters, its state and the answers its host calls
// returned, and replaying a recording reproduces it exactly: the script's tests
// run against one (internal/platform/scripttest), and a save replays a
// script's recent runs through its new source to show what changed (#1942).
//
// What is recorded is the answer a binding was finally given: a call the host
// paced or retried is recorded once, with the answer the script saw. A
// recording is gzipped JSON lines, the header first and one call per line,
// written as the run goes so it never holds more than its compressed bytes,
// and capped at MaxBytes: a run whose recording outgrows the cap keeps its
// history and has no recording, and says so.
package scriptrec

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// MaxBytes is the most one run's recording may hold, compressed.
const MaxBytes = 8 << 20

// maxDecodedBytes bounds what reading a recording back may decompress to, so a
// stored row cannot be made to expand without limit.
const maxDecodedBytes = 256 << 20

// formatVersion is the recording layout this package writes and reads.
const formatVersion = 1

// Header is what a run started from: everything a replay pins so that the
// calls it makes are the calls the run made.
type Header struct {
	V        int            `json:"v"`
	RunID    string         `json:"run_id"`
	FireTime time.Time      `json:"fire_time"`
	Params   map[string]any `json:"params"`
	State    map[string]any `json:"state"`
	RunURL   string         `json:"run_url,omitempty"`
	// MaxRows is the row cap the run's queries carried, which is part of each
	// query's arguments.
	MaxRows int `json:"max_rows"`
	// Preview is true when the run's outputs were previewed rather than
	// written: a draft. A replay of it previews them too.
	Preview bool `json:"preview"`
}

// Call is one host call and its answer: a tool call, or an output the run
// wrote.
type Call struct {
	// Key is what a replay matches a call by: the tool and its arguments, or
	// the output's identity.
	Key  string         `json:"key"`
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`
	Out  map[string]any `json:"out,omitempty"`
	// Output is set for a written output: what the writer answered.
	Output *scriptrun.ExportResult `json:"output,omitempty"`
	// Error is the failure the call answered with instead, if it failed.
	Error string `json:"error,omitempty"`
}

// Recording is one run's header and its calls, in the order they were made.
type Recording struct {
	Header
	Calls []Call
}

// Recorder writes one run's recording as the run makes its calls. Hand OnCall
// to scriptrun.Options.OnCall and wrap the run's Exporter with Exporter.
type Recorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	gz     *gzip.Writer
	over   bool
	failed error
}

// NewRecorder starts a recording of a run that starts from h.
func NewRecorder(h Header) *Recorder {
	r := &Recorder{}
	r.gz = gzip.NewWriter(&r.buf)
	h.V = formatVersion
	r.write(h)
	return r
}

// OnCall records one tool call and the answer it was finally given.
func (r *Recorder) OnCall(tool string, args, out map[string]any, err error) {
	c := Call{Key: ToolKey(tool, args), Tool: tool, Args: args, Out: out}
	if err != nil {
		c.Out, c.Error = nil, err.Error()
	}
	r.write(c)
}

// write appends one line, and gives the recording up once it is over the cap.
func (r *Recorder) write(v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.over || r.failed != nil {
		return
	}
	line, err := json.Marshal(v)
	if err != nil {
		r.failed = fmt.Errorf("recording a call: %w", err)
		return
	}
	if _, err := r.gz.Write(append(line, '\n')); err != nil {
		r.failed = fmt.Errorf("recording a call: %w", err)
		return
	}
	if r.buf.Len() > MaxBytes {
		r.over = true
		r.buf = bytes.Buffer{}
	}
}

// Finish ends the recording. data is the gzipped recording, nil with the
// reason when there is none to keep.
func (r *Recorder) Finish() (data []byte, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return nil, r.failed.Error()
	}
	if !r.over {
		if err := r.gz.Close(); err != nil {
			return nil, "closing the recording: " + err.Error()
		}
		r.over = r.buf.Len() > MaxBytes
	}
	if r.over {
		return nil, fmt.Sprintf("the run's host calls and their answers came to more than the %d MiB a recording may hold", MaxBytes>>20)
	}
	return r.buf.Bytes(), ""
}

// Exporter wraps the run's writer so that what it answered is recorded. A nil
// writer stays nil: the run previews, and there is nothing to record.
func (r *Recorder) Exporter(inner scriptrun.Exporter) scriptrun.Exporter {
	if inner == nil {
		return nil
	}
	return &recordingExporter{inner: inner, rec: r}
}

// The recording writer keeps every optional capability of the writer it
// wraps: one it hid would be a behavior recording took away (#1854).
var _ scriptrun.ToolOutputRecorder = (*recordingExporter)(nil)

type recordingExporter struct {
	inner scriptrun.Exporter
	rec   *Recorder
}

// Export writes through the run's writer and records what it answered.
func (e *recordingExporter) Export(ctx context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	res, err := e.inner.Export(ctx, req)
	e.rec.output(ExportKey(req), res, err)
	return res, err //nolint:wrapcheck // the writer's answer, passed through unchanged
}

// PublishData refreshes through the run's writer and records what it answered.
func (e *recordingExporter) PublishData(ctx context.Context, req scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	res, err := e.inner.PublishData(ctx, req)
	e.rec.output(PublishKey(req), res, err)
	return res, err //nolint:wrapcheck // the writer's answer, passed through unchanged
}

// RecordToolOutput passes on an output a tool wrote for the run (#1854) to
// the run's writer, which is where the run lists it; recording a run must
// not hide it.
func (e *recordingExporter) RecordToolOutput(ctx context.Context, out script.RunOutput) {
	if rec, ok := e.inner.(scriptrun.ToolOutputRecorder); ok {
		rec.RecordToolOutput(ctx, out)
	}
}

func (r *Recorder) output(key string, res *scriptrun.ExportResult, err error) {
	c := Call{Key: key, Output: res}
	if err != nil {
		c.Output, c.Error = nil, err.Error()
	}
	r.write(c)
}

// keySep separates the parts of a call's key; no name or argument text a
// script writes carries it.
const keySep = "\x00"

// ToolKey is what a tool call is matched by: its tool and its arguments.
func ToolKey(tool string, args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		b = fmt.Append(nil, args)
	}
	return "tool" + keySep + tool + keySep + string(b)
}

// ExportKey is what an output write is matched by: its name, where it goes,
// its format and its key.
func ExportKey(req scriptrun.ExportRequest) string {
	return strings.Join([]string{"export", req.Name, req.Destination.Name, req.Format, req.Key}, keySep)
}

// PublishKey is what a data-region refresh is matched by: the asset's name.
func PublishKey(req scriptrun.PublishRequest) string {
	return "publish_data" + keySep + req.Name
}

// Decode reads a recording back.
func Decode(data []byte) (*Recording, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the recording: %w", err)
	}
	defer func() { _ = gz.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(gz, maxDecodedBytes))
	scanner.Buffer(make([]byte, 0, 64<<10), maxDecodedBytes)
	rec := &Recording{}
	first := true
	for scanner.Scan() {
		if first {
			first = false
			if err := json.Unmarshal(scanner.Bytes(), &rec.Header); err != nil {
				return nil, fmt.Errorf("reading the recording's header: %w", err)
			}
			if rec.V != formatVersion {
				return nil, fmt.Errorf("the recording is in layout %d, and this platform reads %d", rec.V, formatVersion)
			}
			continue
		}
		var c Call
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("reading call %d of the recording: %w", len(rec.Calls)+1, err)
		}
		rec.Calls = append(rec.Calls, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the recording: %w", err)
	}
	if first {
		return nil, errors.New("the recording is empty")
	}
	return rec, nil
}

// SaveTo finishes the recording and stores it under m, with the reason when
// there is none to keep, and reports whether a replayable recording was kept.
// A failure to store it is logged and not returned: a run whose recording
// could not be kept still ran.
func (r *Recorder) SaveTo(ctx context.Context, store Store, m Meta) bool {
	if r == nil || store == nil {
		return false
	}
	data, reason := r.Finish()
	m.Reason = reason
	if err := store.Save(ctx, Stored{Meta: m, Data: data}); err != nil {
		slog.Warn("scripts: keeping a run's recording failed", "run_id", m.RunID, "error", err)
		return false
	}
	return reason == ""
}

// SourceHash is the hex digest a recording names the source it ran by.
func SourceHash(source string) string { return hex.EncodeToString(script.SourceDigest(source)) }
