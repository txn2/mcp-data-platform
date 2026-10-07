package tracelog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		out = append(out, m)
	}
	return out
}

func spanContext(t *testing.T) (ctx context.Context, traceID, spanID string) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	t.Cleanup(func() { span.End() })
	return ctx, span.SpanContext().TraceID().String(), span.SpanContext().SpanID().String()
}

// TestHandler_StampsTheSpanIds: a record logged with a span's context carries
// its ids; one logged without passes through untouched.
func TestHandler_StampsTheSpanIds(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(New(slog.NewJSONHandler(&buf, nil)))
	ctx, traceID, spanID := spanContext(t)

	logger.InfoContext(ctx, "traced", "k", "v")
	logger.Info("untraced")
	logger.InfoContext(context.Background(), "no span")

	lines := jsonLines(t, &buf)
	require.Len(t, lines, 3)
	assert.Equal(t, traceID, lines[0][KeyTraceID])
	assert.Equal(t, spanID, lines[0][KeySpanID])
	assert.Equal(t, "v", lines[0]["k"])
	for _, line := range lines[1:] {
		_, has := line[KeyTraceID]
		assert.False(t, has, "no span, no id: %v", line)
	}
}

// TestHandler_WithAttrsAndGroupKeepTheStamp: the wrapped handler's derived
// handlers still stamp, and inside the open group.
func TestHandler_WithAttrsAndGroupKeepTheStamp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(New(slog.NewJSONHandler(&buf, nil))).With("svc", "x").WithGroup("req")
	ctx, traceID, _ := spanContext(t)
	logger.WarnContext(ctx, "grouped", "id", 7)

	lines := jsonLines(t, &buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "x", lines[0]["svc"])
	group, ok := lines[0]["req"].(map[string]any)
	require.True(t, ok, "%v", lines[0])
	assert.Equal(t, traceID, group[KeyTraceID], "the ids are record attributes and land in the open group")
	assert.InDelta(t, 7, group["id"], 0)
}

func TestHandler_EnabledDefersToTheSink(t *testing.T) {
	h := New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	assert.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, h.Enabled(context.Background(), slog.LevelError))
}

type failingHandler struct{ slog.Handler }

func (failingHandler) Handle(context.Context, slog.Record) error { return errors.New("sink down") }

// TestBuild_StderrAloneAndWithAnExport: with no export the handler is the
// stamped stderr sink alone; with one, both receive a record at the level,
// stderr with the stamp and the export as the bridge would see it.
func TestBuild_StderrAloneAndWithAnExport(t *testing.T) {
	var stderr bytes.Buffer
	alone := Build(&stderr, slog.LevelWarn, nil)
	if _, ok := alone.(*Handler); !ok {
		t.Fatalf("no export: want the stamped stderr handler alone, got %T", alone)
	}
	slog.New(alone).Info("dropped")
	slog.New(alone).Warn("kept")
	if lines := jsonLines(t, &stderr); len(lines) != 1 || lines[0]["msg"] != "kept" {
		t.Errorf("stderr lines = %v", lines)
	}

	stderr.Reset()
	var export bytes.Buffer
	both := Build(&stderr, slog.LevelInfo, slog.NewJSONHandler(&export, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, traceID, _ := spanContext(t)
	logger := slog.New(both)
	logger.InfoContext(ctx, "traced")
	logger.DebugContext(ctx, "below the level")
	stderrLines, exportLines := jsonLines(t, &stderr), jsonLines(t, &export)
	require.Len(t, stderrLines, 1)
	require.Len(t, exportLines, 1, "the export follows the level too")
	assert.Equal(t, traceID, stderrLines[0][KeyTraceID], "stderr is stamped")
	assert.Equal(t, "traced", exportLines[0]["msg"])
	_, stamped := exportLines[0][KeyTraceID]
	assert.False(t, stamped, "the export handler receives the record as it is; the bridge reads the span itself")
}

// TestTee_DeliversToEveryEnabledHandler: both sinks get the record, a level
// filter on one keeps debug records off it, and one sink's failure does not
// stop the other.
func TestTee_DeliversToEveryEnabledHandler(t *testing.T) {
	var a, b bytes.Buffer
	ha := slog.NewJSONHandler(&a, &slog.HandlerOptions{Level: slog.LevelDebug})
	hb := levelFilter(slog.LevelInfo, slog.NewJSONHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger := slog.New(tee(ha, hb).WithAttrs([]slog.Attr{slog.String("svc", "x")}).WithGroup("g"))

	logger.Debug("only a")
	logger.Info("both", "k", "v")

	la, lb := jsonLines(t, &a), jsonLines(t, &b)
	require.Len(t, la, 2)
	require.Len(t, lb, 1)
	assert.Equal(t, "both", lb[0]["msg"])
	assert.Equal(t, "x", lb[0]["svc"])
	group, ok := lb[0]["g"].(map[string]any)
	require.True(t, ok, "%v", lb[0])
	assert.Equal(t, "v", group["k"])

	failing := tee(failingHandler{ha}, hb)
	var rec slog.Record
	rec = slog.NewRecord(rec.Time, slog.LevelError, "after failure", 0)
	err := failing.Handle(context.Background(), rec)
	require.EqualError(t, err, "sink down")
	assert.Len(t, jsonLines(t, &b), 2, "the second sink still received the record")
	assert.False(t, tee().Enabled(context.Background(), slog.LevelError), "no handlers, nothing enabled")
}

func TestLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	h := levelFilter(slog.LevelWarn, slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	assert.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, h.Enabled(context.Background(), slog.LevelWarn))
	logger := slog.New(h).With("a", 1).WithGroup("g")
	logger.Info("dropped")
	logger.Error("kept", "b", 2)
	lines := jsonLines(t, &buf)
	require.Len(t, lines, 1)
	assert.Equal(t, "kept", lines[0]["msg"])
	assert.InDelta(t, 1, lines[0]["a"], 0)
}
