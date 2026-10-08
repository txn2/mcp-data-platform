package startup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recordSpans installs an in-memory tracer provider for the test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return rec
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// TestRun_ReportsEveryPhase runs the phases in order and reports one log line
// with each phase's duration, and a platform.startup span with a child each.
func TestRun_ReportsEveryPhase(t *testing.T) {
	rec := recordSpans(t)
	logs := captureLog(t)
	var order []string
	err := Run([]Phase{
		{Name: "data", Run: func() error { order = append(order, "data"); return nil }},
		Do("prompts", func() { order = append(order, "prompts") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "data,prompts" {
		t.Errorf("phases ran as %v", order)
	}
	out := logs.String()
	for _, want := range []string{"platform initialized", "version=", "phase_data_ms=", "phase_prompts_ms="} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %s", want, out)
		}
	}
	spans := rec.Ended()
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3", len(spans))
	}
	root := spans[len(spans)-1]
	if root.Name() != SpanName {
		t.Errorf("root = %q", root.Name())
	}
	for _, s := range spans[:2] {
		if s.Parent().SpanID() != root.SpanContext().SpanID() {
			t.Errorf("%s is not a child of the startup span", s.Name())
		}
		if s.StartTime().Before(root.StartTime()) || s.EndTime().After(root.EndTime()) {
			t.Errorf("%s lies outside the startup span", s.Name())
		}
	}
	if spans[0].Name() != "platform.startup.data" || spans[1].Name() != "platform.startup.prompts" {
		t.Errorf("phase spans = %q, %q", spans[0].Name(), spans[1].Name())
	}
}

// TestRun_StopsAtTheFailingPhase names the phase that failed and runs nothing
// after it.
func TestRun_StopsAtTheFailingPhase(t *testing.T) {
	rec := recordSpans(t)
	logs := captureLog(t)
	boom := errors.New("database refused")
	ranAfter := false
	err := Run([]Phase{
		Do("observability", func() {}),
		{Name: "data", Run: func() error { return boom }},
		Do("auth", func() { ranAfter = true }),
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if ranAfter {
		t.Error("a phase ran after the failure")
	}
	if out := logs.String(); !strings.Contains(out, "platform initialization failed") || !strings.Contains(out, "phase=data") {
		t.Errorf("failure log does not name the phase: %s", out)
	}
	spans := rec.Ended()
	root := spans[len(spans)-1]
	if root.Status().Code != codes.Error || !strings.Contains(root.Status().Description, "data") {
		t.Errorf("root status = %+v", root.Status())
	}
	if spans[1].Status().Code != codes.Error {
		t.Errorf("failing phase status = %+v", spans[1].Status())
	}
}
