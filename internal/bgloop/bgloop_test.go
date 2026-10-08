package bgloop

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// withMetrics installs a live recorder for the test and returns a scrape.
func withMetrics(t *testing.T) func() string {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	SetDefaultMetrics(m)
	t.Cleanup(func() {
		SetDefaultMetrics(nil)
		_ = m.Shutdown(context.Background())
	})
	return func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
		body, _ := io.ReadAll(rec.Body)
		return string(body)
	}
}

// withSpans installs an in-memory span recorder as the global tracer.
func withSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	observability.NewTracerFromProvider(tp, observability.TracingConfig{Enabled: true})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return rec
}

func TestRun_CountsTimesAndStampsEachIteration(t *testing.T) {
	scrape := withMetrics(t)
	spans := withSpans(t)
	stop := make(chan struct{})
	var n atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(context.Background(), Loop{
			Name: "test_loop", Every: time.Millisecond, Immediate: true, Stop: stop,
			Body: func(ctx context.Context) error {
				// The body's context carries the iteration's span, so its
				// logging is correlated with it.
				assert.True(t, trace.SpanContextFromContext(ctx).IsValid())
				switch n.Add(1) {
				case 1:
					return nil
				case 2:
					return errors.New("boom")
				case 3:
					return ErrSkipped
				default:
					close(stop)
					return nil
				}
			},
		})
	}()
	<-done

	body := scrape()
	assert.Contains(t, body, `background_loop_iterations_total{loop="test_loop",result="error"} 1`)
	assert.Contains(t, body, `background_loop_iterations_total{loop="test_loop",result="skipped"} 1`)
	assert.Contains(t, body, `background_loop_last_success_timestamp_seconds{loop="test_loop"}`)
	assert.Contains(t, body, `background_loop_duration_seconds_count{loop="test_loop"}`)

	ended := spans.Ended()
	require.NotEmpty(t, ended)
	for _, s := range ended {
		assert.Equal(t, "loop test_loop", s.Name())
		assert.False(t, s.Parent().IsValid(), "an iteration's span is a root")
	}
}

func TestRun_WakeStartsAnIterationAndWokenSaysSo(t *testing.T) {
	withMetrics(t)
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	woken := make(chan bool, 1)
	go Run(ctx, Loop{
		Name: "wake_loop", Every: time.Hour, Wake: wake,
		Body: func(ctx context.Context) error { woken <- Woken(ctx); return nil },
	})
	wake <- struct{}{}
	select {
	case got := <-woken:
		assert.True(t, got)
	case <-time.After(5 * time.Second):
		t.Fatal("a wake did not start an iteration")
	}
	cancel()
}

func TestRun_ErrStopEndsTheLoopAndFinalRuns(t *testing.T) {
	withMetrics(t)
	var final atomic.Bool
	calls := 0
	Run(context.Background(), Loop{
		Name: "stop_loop", Immediate: true, Every: time.Millisecond,
		Next: func() time.Duration { return 0 },
		Body: func(context.Context) error {
			calls++
			if calls == 2 {
				return ErrStop
			}
			return nil
		},
		Final: func(ctx context.Context) {
			assert.NoError(t, ctx.Err())
			final.Store(true)
		},
	})
	assert.Equal(t, 2, calls)
	assert.True(t, final.Load())
}

func TestRun_ImmediateErrStopReturnsAtOnce(t *testing.T) {
	calls := 0
	Run(context.Background(), Loop{
		Name: "stop_at_once", Immediate: true,
		Body: func(context.Context) error { calls++; return ErrStop },
	})
	assert.Equal(t, 1, calls)
}

func TestRun_NoIntervalWaitsForAWakeOnly(t *testing.T) {
	l := Loop{Name: "wake_only"}
	assert.Equal(t, forever, l.wait())
	l.Every = time.Second
	assert.Equal(t, time.Second, l.wait())
	l.Next = func() time.Duration { return -time.Second }
	assert.Equal(t, time.Duration(0), l.wait())
}

func TestRun_ChildSpanNestsUnderTheCaller(t *testing.T) {
	spans := withSpans(t)
	ctx, job := otel.Tracer("test").Start(context.Background(), "job")
	Run(ctx, Loop{
		Name: "heartbeat", Immediate: true, Child: true,
		Body: func(context.Context) error { return ErrStop },
	})
	job.End()
	var hb sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.Name() == "loop heartbeat" {
			hb = s
		}
	}
	require.NotNil(t, hb)
	assert.Equal(t, job.SpanContext().SpanID(), hb.Parent().SpanID())
}

func TestRun_SpanPerUnitOpensNoIterationSpan(t *testing.T) {
	spans := withSpans(t)
	Run(context.Background(), Loop{
		Name: "poll", Immediate: true, SpanPerUnit: true,
		Body: func(ctx context.Context) error {
			assert.False(t, trace.SpanContextFromContext(ctx).IsValid())
			return Unit(ctx, "unit_of_work", func(ctx context.Context) error {
				assert.True(t, trace.SpanContextFromContext(ctx).IsValid())
				return ErrStop
			})
		},
	})
	ended := spans.Ended()
	names := make([]string, 0, len(ended))
	for _, s := range ended {
		names = append(names, s.Name())
		assert.False(t, s.Parent().IsValid(), "a unit outside an iteration span is a root")
	}
	assert.Equal(t, []string{"loop unit_of_work"}, names)
}

func TestUnit_NestsUnderTheIterationAndRecordsItsResult(t *testing.T) {
	scrape := withMetrics(t)
	spans := withSpans(t)
	Run(context.Background(), Loop{
		Name: "outer", Immediate: true,
		Body: func(ctx context.Context) error {
			err := Unit(ctx, "inner", func(context.Context) error { return errors.New("failed") })
			assert.Error(t, err)
			return ErrStop
		},
	})
	assert.Contains(t, scrape(), `background_loop_iterations_total{loop="inner",result="error"} 1`)
	var outer, inner sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		switch s.Name() {
		case "loop outer":
			outer = s
		case "loop inner":
			inner = s
		}
	}
	require.NotNil(t, outer)
	require.NotNil(t, inner)
	assert.Equal(t, outer.SpanContext().SpanID(), inner.Parent().SpanID())
	assert.Equal(t, "Error", inner.Status().Code.String())
}

func TestPurged_CountsRows(t *testing.T) {
	scrape := withMetrics(t)
	Purged(context.Background(), "sweep", 7)
	Purged(context.Background(), "sweep", 0)
	assert.Contains(t, scrape(), `retention_rows_purged_total{loop="sweep"} 7`)
}

func TestResult(t *testing.T) {
	assert.Equal(t, observability.LoopResultOK, Result(nil))
	assert.Equal(t, observability.LoopResultOK, Result(ErrStop))
	assert.Equal(t, observability.LoopResultSkipped, Result(ErrSkipped))
	assert.Equal(t, observability.LoopResultError, Result(errors.New("x")))
}

func TestConsume_HandlesEachEventUntilTheChannelCloses(t *testing.T) {
	scrape := withMetrics(t)
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	close(ch)
	var got []int
	Consume(context.Background(), Events[int]{
		Name: "events", C: ch,
		Body: func(_ context.Context, v int) error { got = append(got, v); return nil },
	})
	assert.Equal(t, []int{1, 2}, got)
	assert.Contains(t, scrape(), `background_loop_iterations_total{loop="events",result="ok"} 2`)
}

func TestConsume_StopsOnStopContextAndErrStop(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	Consume(context.Background(), Events[int]{
		Name: "stopped", C: make(chan int), Stop: stop,
		Body: func(context.Context, int) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Consume(ctx, Events[int]{
		Name: "canceled", C: make(chan int),
		Body: func(context.Context, int) error { return nil },
	})

	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	n := 0
	Consume(context.Background(), Events[int]{
		Name: "errstop", C: ch,
		Body: func(context.Context, int) error { n++; return ErrStop },
	})
	assert.Equal(t, 1, n)
}

func TestStopped(t *testing.T) {
	stop := make(chan struct{})
	ctx := context.Background()
	assert.False(t, Stopped(ctx, stop))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	assert.True(t, Stopped(canceled, stop))
	close(stop)
	assert.True(t, Stopped(ctx, stop))
}

func TestListen_ReportsConnectionState(t *testing.T) {
	scrape := withMetrics(t)
	l := NewListen("listen_test")
	assert.Equal(t, "listen_test", l.Name())
	l.Event(pq.ListenerEventConnected)
	l.Event(pq.ListenerEventDisconnected)
	l.Event(pq.ListenerEventReconnected)
	l.Notified()
	body := scrape()
	assert.Contains(t, body, `pg_listen_connected{loop="listen_test"} 1`)
	assert.Contains(t, body, `pg_listen_reconnects_total{loop="listen_test"} 1`)
	assert.True(t, strings.Contains(body, `pg_listen_last_notification_age_seconds{loop="listen_test"}`))

	l.Event(pq.ListenerEventConnectionAttemptFailed)
	assert.Contains(t, scrape(), `pg_listen_connected{loop="listen_test"} 0`)

	var none *Listen
	assert.Empty(t, none.Name())
	none.Event(pq.ListenerEventConnected)
	none.Notified()
}

func TestMetrics_ReturnsTheInstalledRecorder(t *testing.T) {
	SetDefaultMetrics(nil)
	assert.Nil(t, Metrics())
	withMetrics(t)
	assert.NotNil(t, Metrics())
}
