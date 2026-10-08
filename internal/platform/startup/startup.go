// Package startup runs the platform's initialization phases and reports them
// (#1898): one log line naming the version and every phase's duration, and a
// platform.startup span with a child per phase. Before it a slow boot was a
// gap between two timestamps nobody had correlated, and a boot that failed
// named the error but not the phase.
package startup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/buildinfo"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// SpanName is the root span every boot's phases are children of.
const SpanName = "platform.startup"

// Phase is one named step of initialization.
type Phase struct {
	Name string
	Run  func() error
}

// Do is a Phase for a step that cannot fail.
func Do(name string, run func()) Phase {
	return Phase{Name: name, Run: func() error { run(); return nil }}
}

// timing is one phase's measured span.
type timing struct {
	name       string
	start, end time.Time
	err        error
}

// Run runs phases in order, stopping at the first that fails, and reports
// what ran. The span is written after the phases finish, with the times they
// were measured at, because the first phase is the one that installs the
// tracer.
func Run(phases []Phase) error {
	start := time.Now()
	timings := make([]timing, 0, len(phases))
	var failed error
	for _, ph := range phases {
		t := timing{name: ph.Name, start: time.Now()}
		t.err = ph.Run()
		t.end = time.Now()
		timings = append(timings, t)
		if t.err != nil {
			failed = t.err
			break
		}
	}
	report(start, time.Now(), timings, failed)
	return failed
}

// report logs the boot and writes its span.
func report(start, end time.Time, timings []timing, failed error) {
	ctx := context.Background()
	ctx, root := otel.Tracer(observability.InstrumentationScope).Start(ctx, SpanName,
		trace.WithTimestamp(start), trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("service.version", buildinfo.Version)))
	args := []any{"version", buildinfo.Version, "duration_ms", end.Sub(start).Milliseconds()}
	for _, t := range timings {
		_, span := otel.Tracer(observability.InstrumentationScope).Start(ctx, "platform.startup."+t.name,
			trace.WithTimestamp(t.start))
		if t.err != nil {
			span.RecordError(observability.RedactError(t.err))
			span.SetStatus(codes.Error, "phase failed")
		}
		span.End(trace.WithTimestamp(t.end))
		args = append(args, "phase_"+t.name+"_ms", t.end.Sub(t.start).Milliseconds())
	}
	if failed != nil {
		last := timings[len(timings)-1].name
		root.RecordError(observability.RedactError(failed))
		root.SetStatus(codes.Error, fmt.Sprintf("phase %s failed", last))
		root.End(trace.WithTimestamp(end))
		slog.ErrorContext(ctx, "platform initialization failed",
			append(args, "phase", last, "error", logsan.SanitizeForLog(failed.Error()))...)
		return
	}
	root.End(trace.WithTimestamp(end))
	slog.InfoContext(ctx, "platform initialized", args...)
}
