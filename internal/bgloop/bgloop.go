// Package bgloop is the one way the platform runs work in the background
// (#1897). Every periodic sweep, poll/wake worker, heartbeat and event consumer
// goes through Run or Consume, which is what makes a loop observable without
// its author remembering to: each iteration is counted by result
// (background_loop_iterations_total{loop,result}), timed
// (background_loop_duration_seconds{loop}) and, when it succeeds, stamped
// (background_loop_last_success_timestamp_seconds{loop}), so a loop that has
// stopped reads as a timestamp that stopped advancing rather than as silence.
//
// Each iteration also gets a span: a root span by default, since a background
// iteration has no caller to continue, or a child of the caller's span for a
// loop that runs inside a unit of work (a lease heartbeat). The iteration's
// context carries that span, so a log record the body writes with it is
// correlated with the iteration's trace. A loop whose iterations are mostly
// idle polls (a queue worker) sets SpanPerUnit and opens a span per unit of
// work it finds with Unit instead, keeping a trace per claimed job rather than
// one per empty poll.
//
// .semgrep/go-background-loop.yml refuses time.NewTicker, time.Tick and a
// for/select loop anywhere else, so a new loop cannot ship without these
// signals.
package bgloop

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// ErrSkipped is returned by an iteration that found its work held elsewhere
// (another replica's advisory lock) and did nothing. It is counted as result
// "skipped", is not an error, and does not advance the last-success time.
var ErrSkipped = errors.New("bgloop: work held elsewhere")

// ErrStop is returned by an iteration to end its loop: a heartbeat whose
// lease went to another worker. The iteration is counted as ok.
var ErrStop = errors.New("bgloop: stop")

// spanAttrLoop is the span attribute naming the loop an iteration belongs to.
const spanAttrLoop = "mcp_platform.loop"

// defaultMetrics is the recorder every loop records through. It is installed
// once by the observability layer (internal/platform/obs) at startup, so the
// thirty-odd loops the platform runs do not each take a metrics parameter.
var defaultMetrics atomic.Pointer[observability.Metrics]

// SetDefaultMetrics installs the recorder loops record through. Nil turns
// recording off.
func SetDefaultMetrics(m *observability.Metrics) { defaultMetrics.Store(m) }

// Metrics returns the installed recorder (nil when none is), for a loop body
// recording its own series beside the loop's.
func Metrics() *observability.Metrics { return defaultMetrics.Load() }

// Loop describes one background loop.
type Loop struct {
	// Name labels every series and span the loop produces. It is one of the
	// Name* constants in names.go.
	Name string
	// Every is the wait between iterations. Ignored when Next is set.
	Every time.Duration
	// Next, when set, returns the wait before the next iteration, read after
	// each one: a loop that found work runs again at once, one that found a
	// lock held retries sooner.
	Next func() time.Duration
	// Immediate runs the first iteration at start instead of one wait in.
	Immediate bool
	// Wake starts an iteration early: a LISTEN notification, an enqueue on
	// this replica, an administrator's request.
	Wake <-chan struct{}
	// Stop ends the loop, as ctx ending does. Optional.
	Stop <-chan struct{}
	// Child makes each iteration's span a child of ctx's span instead of a
	// root: a loop running inside a unit of work, such as a lease heartbeat.
	Child bool
	// SpanPerUnit opens no span for the iteration; the body opens one per
	// unit of work it finds with Unit.
	SpanPerUnit bool
	// Body is one iteration. It returns ErrSkipped when the work was held
	// elsewhere and an error when it failed; the loop continues either way.
	Body func(ctx context.Context) error
	// Final runs once after the loop stops, with a context that is not
	// canceled: a buffer writing what it still holds.
	Final func(ctx context.Context)
}

// iterationKey marks a context as carrying a loop iteration, so Unit nests
// under it and Woken can report how the iteration started.
type iterationKey struct{}

type iteration struct {
	woken bool
}

// Run runs l until ctx ends or l.Stop is closed. It blocks; callers start it
// on their own goroutine.
func Run(ctx context.Context, l Loop) {
	if l.Final != nil {
		defer l.Final(context.WithoutCancel(ctx))
	}
	if l.Immediate && errors.Is(iterate(ctx, l, false), ErrStop) {
		return
	}
	timer := time.NewTimer(l.wait())
	defer timer.Stop()
	for {
		woken, ok := waitNext(ctx, l, timer)
		if !ok {
			return
		}
		if errors.Is(iterate(ctx, l, woken), ErrStop) {
			return
		}
		timer.Reset(l.wait())
	}
}

// waitNext blocks until the timer fires or a wake arrives, reporting which.
// ok is false when the loop should stop.
func waitNext(ctx context.Context, l Loop, timer *time.Timer) (woken, ok bool) {
	select {
	case <-ctx.Done():
		return false, false
	case <-l.Stop:
		return false, false
	case <-l.Wake:
		if !timer.Stop() {
			drain(timer)
		}
		return true, true
	case <-timer.C:
		return false, true
	}
}

// drain empties a stopped timer's channel when it had already fired, so the
// following Reset starts a clean wait.
func drain(t *time.Timer) {
	select {
	case <-t.C:
	default:
	}
}

func (l Loop) wait() time.Duration {
	if l.Next != nil {
		return max(l.Next(), 0)
	}
	if l.Every <= 0 {
		return forever
	}
	return l.Every
}

// forever is the wait of a loop that runs only when woken.
const forever = time.Duration(1<<63 - 1)

// iterate runs one iteration under its span, records it, and returns what the
// body returned.
func iterate(ctx context.Context, l Loop, woken bool) error {
	ctx = context.WithValue(ctx, iterationKey{}, iteration{woken: woken})
	start := time.Now()
	var span trace.Span
	if !l.SpanPerUnit {
		ctx, span = startSpan(ctx, l.Name, l.Child)
	}
	err := l.Body(ctx)
	finish(ctx, span, l.Name, err, time.Since(start))
	return err
}

// Unit runs one unit of background work -- a claimed job, a delivered
// notification, one of several sweeps in a pass -- under its own span and
// records it under the loop name given. The span is a child of the enclosing
// iteration's span when there is one, and a root span otherwise.
func Unit(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	start := time.Now()
	child := trace.SpanContextFromContext(ctx).IsValid()
	ctx, span := startSpan(ctx, name, child)
	err := fn(ctx)
	finish(ctx, span, name, err, time.Since(start))
	return err
}

// Woken reports whether the iteration ctx belongs to was started by the loop's
// Wake channel rather than its timer or start.
func Woken(ctx context.Context) bool {
	it, _ := ctx.Value(iterationKey{}).(iteration)
	return it.woken
}

// Purged records rows a retention sweep deleted under the loop name given.
func Purged(ctx context.Context, name string, n int64) {
	Metrics().RecordRowsPurged(ctx, name, n)
}

func startSpan(ctx context.Context, name string, child bool) (context.Context, trace.Span) {
	opts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String(spanAttrLoop, name)),
	}
	if !child {
		opts = append(opts, trace.WithNewRoot())
	}
	return otel.Tracer(observability.InstrumentationScope).Start(ctx, "loop "+name, opts...)
}

// finish ends the span (when there is one) with the iteration's outcome and
// records the iteration.
func finish(ctx context.Context, span trace.Span, name string, err error, d time.Duration) {
	result := Result(err)
	if span != nil {
		span.SetAttributes(attribute.String("mcp_platform.loop.result", result))
		if result == observability.LoopResultError {
			span.RecordError(observability.RedactError(err))
			span.SetStatus(codes.Error, result)
		}
		span.End()
	}
	Metrics().RecordLoopIteration(ctx, name, result, d)
}

// Result is the result label an iteration's error becomes.
func Result(err error) string {
	switch {
	case err == nil, errors.Is(err, ErrStop):
		return observability.LoopResultOK
	case errors.Is(err, ErrSkipped):
		return observability.LoopResultSkipped
	default:
		return observability.LoopResultError
	}
}

// Events describes a loop that consumes a channel of events.
type Events[T any] struct {
	// Name labels the loop's series and spans (names.go).
	Name string
	// C is the channel consumed. The loop ends when it is closed.
	C <-chan T
	// Stop ends the loop, as ctx ending does. Optional.
	Stop <-chan struct{}
	// SpanPerUnit opens no span per event; for a high-rate, trivial event
	// such as a LISTEN wakeup, whose work is traced where it is done.
	SpanPerUnit bool
	// Body handles one event.
	Body func(ctx context.Context, ev T) error
}

// Consume runs e until ctx ends, e.Stop is closed or e.C is closed. It blocks.
func Consume[T any](ctx context.Context, e Events[T]) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.Stop:
			return
		case ev, ok := <-e.C:
			if !ok {
				return
			}
			err := iterate(ctx, Loop{Name: e.Name, SpanPerUnit: e.SpanPerUnit, Body: func(ctx context.Context) error {
				return e.Body(ctx, ev)
			}}, true)
			if errors.Is(err, ErrStop) {
				return
			}
		}
	}
}

// Stopped reports whether stop is closed or ctx has ended, without blocking:
// the check a drain loop makes between units of work.
func Stopped(ctx context.Context, stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return ctx.Err() != nil
	}
}
