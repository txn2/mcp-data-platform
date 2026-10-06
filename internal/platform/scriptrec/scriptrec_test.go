package scriptrec

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// writer is an Exporter that answers every write with where it landed.
type writer struct{ err error }

func (w writer) Export(_ context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	return &scriptrun.ExportResult{AssetID: "a-" + req.Name, AssetVersion: 3}, w.err
}

func (w writer) PublishData(_ context.Context, req scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	return &scriptrun.ExportResult{AssetID: "p-" + req.Name}, w.err
}

func finish(t *testing.T, r *Recorder) *Recording {
	t.Helper()
	data, reason := r.Finish()
	require.Empty(t, reason)
	rec, err := Decode(data)
	require.NoError(t, err)
	return rec
}

func TestARecordingReplaysEveryCallInOrder(t *testing.T) {
	fire := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	r := NewRecorder(Header{RunID: "run_1", FireTime: fire, Params: map[string]any{"day": "x"}, MaxRows: 5})
	r.OnCall("trino_query", map[string]any{"sql": "select 1"}, map[string]any{"rows": []any{1.0}}, nil)
	r.OnCall("trino_query", map[string]any{"sql": "select 1"}, map[string]any{"rows": []any{2.0}}, nil)
	r.OnCall("s3_object", map[string]any{"action": "put"}, nil, errors.New("denied"))
	exp := r.Exporter(writer{})
	_, err := exp.Export(context.Background(), scriptrun.ExportRequest{Name: "w", Format: "csv", Destination: script.Destination{Name: "portal"}})
	require.NoError(t, err)
	_, err = exp.PublishData(context.Background(), scriptrun.PublishRequest{Name: "d"})
	require.NoError(t, err)
	assert.Nil(t, r.Exporter(nil), "a run that previews has no writes to record")

	rec := finish(t, r)
	assert.Equal(t, "run_1", rec.RunID)
	assert.True(t, rec.FireTime.Equal(fire))
	assert.Equal(t, 5, rec.MaxRows)
	require.Len(t, rec.Calls, 5)

	replay := NewReplay(rec)
	assert.Equal(t, "run_1", replay.Header().RunID)
	first, err := replay.CallTool(context.Background(), "trino_query", map[string]any{"sql": "select 1"})
	require.NoError(t, err)
	second, err := replay.CallTool(context.Background(), "trino_query", map[string]any{"sql": "select 1"})
	require.NoError(t, err)
	assert.Equal(t, []any{1.0}, first["rows"])
	assert.Equal(t, []any{2.0}, second["rows"], "repeated calls are answered in recorded order")

	_, err = replay.CallTool(context.Background(), "trino_query", map[string]any{"sql": "select 1"})
	assert.EqualError(t, err, `the recording holds 2 answer(s) for platform.query("select 1"), and it was called again`)

	_, err = replay.CallTool(context.Background(), "s3_object", map[string]any{"action": "put"})
	assert.EqualError(t, err, "denied", "a recorded failure is replayed")

	_, err = replay.CallTool(context.Background(), "trino_query", map[string]any{"sql": "select 2"})
	var missing *MissingError
	require.ErrorAs(t, err, &missing)
	assert.Contains(t, err.Error(), `the recording holds no answer for platform.query("select 2")`)

	out, err := replay.Exporter().Export(context.Background(), scriptrun.ExportRequest{Name: "w", Format: "csv", Destination: script.Destination{Name: "portal"}})
	require.NoError(t, err)
	assert.Equal(t, "a-w", out.AssetID)
	pub, err := replay.Exporter().PublishData(context.Background(), scriptrun.PublishRequest{Name: "d"})
	require.NoError(t, err)
	assert.Equal(t, "p-d", pub.AssetID)
	_, err = replay.Exporter().Export(context.Background(), scriptrun.ExportRequest{Name: "other"})
	assert.Contains(t, err.Error(), `platform.export("other")`)

	assert.Len(t, replay.Made(), 5)
}

func TestAReplayedAnswerCannotBeChangedByTheScript(t *testing.T) {
	r := NewRecorder(Header{})
	r.OnCall("t", nil, map[string]any{"n": 1.0}, nil)
	r.OnCall("t", nil, map[string]any{"n": 1.0}, nil)
	replay := NewReplay(finish(t, r))
	got, err := replay.CallTool(context.Background(), "t", nil)
	require.NoError(t, err)
	got["n"] = 9.0
	again, err := replay.CallTool(context.Background(), "t", nil)
	require.NoError(t, err)
	assert.Equal(t, 1.0, again["n"])
}

func TestALenientReplayAnswersAMovedCallByItsTool(t *testing.T) {
	r := NewRecorder(Header{})
	r.OnCall("notify", map[string]any{"body": "3"}, map[string]any{"ok": true}, nil)
	rec := finish(t, r)
	_, err := NewReplay(rec).CallTool(context.Background(), "notify", map[string]any{"body": "2"})
	assert.Error(t, err)
	out, err := NewReplay(rec).Lenient().CallTool(context.Background(), "notify", map[string]any{"body": "2"})
	require.NoError(t, err)
	assert.Equal(t, true, out["ok"])
	_, err = NewReplay(rec).Lenient().CallTool(context.Background(), "other", nil)
	assert.Error(t, err, "a tool the recording never called has nothing to answer with")
}

func TestARecordingOverItsCapKeepsNothingAndSaysWhy(t *testing.T) {
	r := NewRecorder(Header{})
	// Random bytes do not compress, so the cap is reached after a few calls.
	for range 12 {
		b := make([]byte, 1<<20)
		_, _ = rand.Read(b)
		r.OnCall("t", nil, map[string]any{"blob": hex.EncodeToString(b)}, nil)
	}
	data, reason := r.Finish()
	assert.Nil(t, data)
	assert.Contains(t, reason, "more than the 8 MiB a recording may hold")

	st := &memStore{}
	assert.False(t, r.SaveTo(context.Background(), st, Meta{RunID: "r"}))
	require.Len(t, st.saved, 1)
	assert.False(t, st.saved[0].Replayable())
}

func TestAnUnencodableCallFailsTheRecording(t *testing.T) {
	r := NewRecorder(Header{})
	r.OnCall("t", nil, map[string]any{"f": func() {}}, nil)
	data, reason := r.Finish()
	assert.Nil(t, data)
	assert.Contains(t, reason, "recording a call")
}

func TestSaveToReportsAReplayableRecording(t *testing.T) {
	var nilRec *Recorder
	assert.False(t, nilRec.SaveTo(context.Background(), &memStore{}, Meta{}))
	assert.False(t, NewRecorder(Header{}).SaveTo(context.Background(), nil, Meta{}))

	st := &memStore{}
	assert.True(t, NewRecorder(Header{}).SaveTo(context.Background(), st, Meta{RunID: "r"}))
	assert.NotEmpty(t, st.saved[0].Data)

	failing := &memStore{err: errors.New("down")}
	assert.False(t, NewRecorder(Header{}).SaveTo(context.Background(), failing, Meta{RunID: "r"}))
}

func TestDecodeRefusesWhatIsNotARecording(t *testing.T) {
	_, err := Decode([]byte("plain"))
	assert.ErrorContains(t, err, "reading the recording")

	gz := func(s string) []byte {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return b.Bytes()
	}
	_, err = Decode(gz(""))
	assert.ErrorContains(t, err, "empty")
	_, err = Decode(gz("{bad\n"))
	assert.ErrorContains(t, err, "header")
	_, err = Decode(gz(`{"v":99}` + "\n"))
	assert.ErrorContains(t, err, "layout 99")
	_, err = Decode(gz(`{"v":1}` + "\n{bad\n"))
	assert.ErrorContains(t, err, "call 1")
}

func TestDescribeCallNamesTheCallAsTheScriptWroteIt(t *testing.T) {
	assert.Equal(t, `platform.query("select 1")`, DescribeCall(scriptrun.ToolQuery, map[string]any{"sql": " select 1\n"}))
	assert.Equal(t, `platform.call("x", {"a":1})`, DescribeCall("x", map[string]any{"a": 1}))
	assert.Equal(t, `platform.call("x")`, DescribeCall("x", map[string]any{"f": func() {}}))
	assert.Equal(t, "", toolOf("export\x00w"))
	assert.Equal(t, 64, len(SourceHash("x")))
}

type memStore struct {
	saved []Stored
	err   error
}

func (m *memStore) Save(_ context.Context, rec Stored) error {
	if m.err != nil {
		return m.err
	}
	m.saved = append(m.saved, rec)
	return nil
}

func (*memStore) Get(context.Context, string) (*Stored, error) { return nil, ErrNotFound }
func (*memStore) Recent(context.Context, string, int) ([]Stored, error) {
	return nil, nil
}
func (*memStore) Keep(context.Context, string, string, []string) error { return nil }
func (*memStore) Purge(context.Context, time.Duration) (int64, error)  { return 0, nil }

// A write that failed is replayed as the failure, one that answered nothing
// as an empty answer, and a draft's previewed writes have no writer.
func TestReplayedWritesAnswerAsTheRunsWriterDid(t *testing.T) {
	r := NewRecorder(Header{})
	r.output("export\x00a", nil, errors.New("disk full"))
	r.output("export\x00b", nil, nil)
	rec := finish(t, r)
	assert.Contains(t, ToolKey("t", map[string]any{"f": func() {}}), "tool\x00t\x00", "arguments JSON cannot hold still key the call")
	replay := NewReplay(rec)
	_, err := replay.output("export\x00a", "a")
	assert.EqualError(t, err, "disk full")
	out, err := replay.output("export\x00b", "b")
	require.NoError(t, err)
	assert.Equal(t, &scriptrun.ExportResult{}, out)
	assert.Nil(t, NewReplay(&Recording{Header: Header{Preview: true}}).Exporter())
	assert.Nil(t, cloneMap(nil))
	assert.NotNil(t, cloneMap(map[string]any{"f": func() {}}), "a value JSON cannot copy is handed over as it is")

	failing := NewRecorder(Header{}).Exporter(writer{err: errors.New("no")})
	_, err = failing.Export(context.Background(), scriptrun.ExportRequest{Name: "x"})
	assert.Error(t, err)
}

// toolWriter is a writer that also lists the outputs a tool wrote.
type toolWriter struct {
	writer
	listed []script.RunOutput
}

func (w *toolWriter) RecordToolOutput(_ context.Context, out script.RunOutput) {
	w.listed = append(w.listed, out)
}

// Recording a run leaves the outputs a tool wrote listed on it (#1854).
func TestRecordingKeepsToolOutputsListed(t *testing.T) {
	inner := &toolWriter{}
	wrapped := NewRecorder(Header{}).Exporter(inner)
	rec, ok := wrapped.(scriptrun.ToolOutputRecorder)
	require.True(t, ok, "the recording writer still lists tool outputs")
	rec.RecordToolOutput(context.Background(), script.RunOutput{Tool: "trino_export"})
	assert.Len(t, inner.listed, 1)
	plain, ok := NewRecorder(Header{}).Exporter(writer{}).(scriptrun.ToolOutputRecorder)
	require.True(t, ok)
	plain.RecordToolOutput(context.Background(), script.RunOutput{}) // a writer that lists none is passed nothing
}

// answers is a declared-answer source holding one answer per tool.
type answers map[string]struct {
	out     map[string]any
	errText string
}

func (a answers) Answer(tool string, _ map[string]any) (Answered, bool) {
	got, ok := a[tool]
	if got.errText != "" {
		return Answered{Fail: errors.New(got.errText)}, ok
	}
	return Answered{Out: got.out}, ok
}

// A declared answer is consulted before the recording, and says so in what
// the execution made (#1953).
func TestADeclaredAnswerIsConsultedBeforeTheRecording(t *testing.T) {
	r := NewReplay(&Recording{Calls: []Call{{Key: ToolKey("q", nil), Tool: "q", Out: map[string]any{"from": "recording"}}}}).
		WithAnswers(answers{
			"write": {out: map[string]any{"id": "r1"}},
			"fails": {errText: "refused"},
		})
	out, err := r.CallTool(context.Background(), "write", map[string]any{"a": 1})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": "r1"}, out)
	_, err = r.CallTool(context.Background(), "fails", nil)
	require.EqualError(t, err, "refused")
	out, err = r.CallTool(context.Background(), "q", nil)
	require.NoError(t, err)
	assert.Equal(t, "recording", out["from"])

	made := r.Made()
	require.Len(t, made, 3)
	assert.True(t, made[0].Declared)
	assert.True(t, made[1].Declared)
	assert.Equal(t, "refused", made[1].Error)
	assert.False(t, made[2].Declared)

	_, err = r.CallTool(context.Background(), "missing", nil)
	assert.ErrorContains(t, err, "declare the answer it gets with testing.answer")
}

// TestAReplayAnswersHostValuesInTheOrderTheRunReadThem holds #2004's replay
// half: the time left is read back value by value, and it is not a call the
// run made.
func TestAReplayAnswersHostValuesInTheOrderTheRunReadThem(t *testing.T) {
	key := ToolKey("platform.remaining_ms", map[string]any{})
	replay := NewReplay(&Recording{Calls: []Call{
		{Key: key, Tool: "platform.remaining_ms", Out: map[string]any{"remaining_ms": float64(900)}},
		{Key: key, Tool: "platform.remaining_ms", Out: map[string]any{"remaining_ms": float64(10)}},
	}})
	first, err := replay.HostValue("platform.remaining_ms")
	require.NoError(t, err)
	assert.Equal(t, float64(900), first["remaining_ms"])
	second, err := replay.HostValue("platform.remaining_ms")
	require.NoError(t, err)
	assert.Equal(t, float64(10), second["remaining_ms"])
	_, err = replay.HostValue("platform.remaining_ms")
	assert.Error(t, err, "the run read it twice")
	assert.Empty(t, replay.Made(), "a host value is not a call the run made")
}

// A failure the tool classified is recorded with its envelope and replays as
// the same refusal, so a replay of the run fails as the run did -- the same
// text and the same class (#2032). One recorded before envelopes were kept
// replays as the platform's generic tool failure, its text unchanged.
func TestAClassifiedFailureReplaysAsTheSameRefusal(t *testing.T) {
	env := map[string]any{
		"code": "trino_query_failed", "category": "upstream_unavailable", "retryable": true,
		"message": "EXTERNAL: The connection attempt failed.", "trino": map[string]any{"sql_state": "08001"},
	}
	r := NewRecorder(Header{})
	r.OnCall("trino_execute", map[string]any{"sql": "MERGE"}, nil, scriptsession.NewRefusal("Execution failed: EXTERNAL", env))
	rec := finish(t, r)
	rec.Calls = append(rec.Calls, Call{Key: ToolKey("old", nil), Tool: "old", Error: "refused long ago"})

	replay := NewReplay(rec)
	_, err := replay.CallTool(context.Background(), "trino_execute", map[string]any{"sql": "MERGE"})
	var refusal *scriptsession.RefusalError
	require.True(t, errors.As(err, &refusal))
	assert.Equal(t, "Execution failed: EXTERNAL", err.Error())
	assert.True(t, refusal.Retryable)
	trino, _ := refusal.Envelope["trino"].(map[string]any)
	assert.Equal(t, "08001", trino["sql_state"])

	_, err = replay.CallTool(context.Background(), "old", nil)
	require.True(t, errors.As(err, &refusal))
	assert.Equal(t, "refused long ago", err.Error())
	assert.Equal(t, "tool_error", refusal.Code)
	assert.False(t, refusal.Retryable)
}
