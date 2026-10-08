package observability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBackgroundMetrics(t *testing.T) *Metrics {
	t.Helper()
	m, err := New(Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func TestBackgroundRecorders_ExportEverySeries(t *testing.T) {
	m := newBackgroundMetrics(t)
	ctx := context.Background()
	m.RecordLoopIteration(ctx, "l", LoopResultOK, time.Millisecond)
	m.RecordLoopIteration(ctx, "l", LoopResultError, time.Millisecond)
	m.RecordRowsPurged(ctx, "l", 3)
	m.RecordRowsPurged(ctx, "l", 0)
	m.RecordAuditWrite(ctx, StatusOK, time.Millisecond)
	m.RecordScriptRunFailure(ctx, "memory")
	m.RecordScriptRunShed(ctx)
	m.RecordScriptWorkerLoad(ctx, "memory", 0.5)
	m.RecordNotificationAttempt(ctx, "email", "failed")
	m.RecordConnectionOAuthRefresh(ctx, "api", "revoked")
	m.RecordConnectionOAuthCredentials(ctx, "api", "revoked", 2)
	m.RecordThumbnailRenderer(ctx, true)
	m.RecordThumbnailRender(ctx, "asset", time.Second)
	m.RecordThumbnailFailure(ctx, "asset", "renderer")
	m.RecordListenConnected(ctx, "listen_x", true, true)
	m.RecordListenConnected(ctx, "listen_y", false, false)
	m.RegisterAuditQueueDepth(func() int { return 4 })

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`background_loop_iterations_total{loop="l",result="ok"} 1`,
		`background_loop_iterations_total{loop="l",result="error"} 1`,
		`background_loop_duration_seconds_count{loop="l"} 2`,
		`background_loop_last_success_timestamp_seconds{loop="l"}`,
		`retention_rows_purged_total{loop="l"} 3`,
		`audit_write_duration_seconds_count{result="ok"} 1`,
		`audit_writer_queue_depth 4`,
		`script_run_failures_total{cause="memory"} 1`,
		`script_runs_shed_total 1`,
		`script_worker_load_ratio{reason="memory"} 0.5`,
		`notification_delivery_attempts_total{kind="email",result="failed"} 1`,
		`connection_oauth_refresh_total{kind="api",result="revoked"} 1`,
		`connection_oauth_credentials{kind="api",state="revoked"} 2`,
		`thumbnail_renderer_up 1`,
		`thumbnail_render_duration_seconds_count{kind="asset"} 1`,
		`thumbnail_render_failures_total{kind="asset",reason="renderer"} 1`,
		`pg_listen_connected{loop="listen_x"} 1`,
		`pg_listen_connected{loop="listen_y"} 0`,
		`pg_listen_reconnects_total{loop="listen_x"} 1`,
		`pg_listen_last_notification_age_seconds{loop="listen_x"}`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `pg_listen_last_notification_age_seconds{loop="listen_y"}`,
		"a connection that never came up has no notification clock")

	m.RecordThumbnailRenderer(ctx, false)
	assert.Contains(t, scrapeMetrics(t, m.Handler()), "thumbnail_renderer_up 0")
}

func TestBackgroundQueue_ReportsSamplesAndCachesThem(t *testing.T) {
	m := newBackgroundMetrics(t)
	var calls atomic.Int32
	m.RegisterBackgroundQueue("q", func(context.Context) ([]BackgroundQueueSample, error) {
		calls.Add(1)
		return []BackgroundQueueSample{{Kind: "k", Pending: 2, Running: 1, Waiting: 5, OldestAge: 90 * time.Second}}, nil
	})
	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`background_queue_items{kind="k",loop="q",state="pending"} 2`,
		`background_queue_items{kind="k",loop="q",state="running"} 1`,
		`background_queue_items{kind="k",loop="q",state="waiting"} 5`,
		`background_queue_oldest_age_seconds{kind="k",loop="q"} 90`,
	} {
		assert.Contains(t, body, want)
	}
	_ = scrapeMetrics(t, m.Handler())
	assert.Equal(t, int32(1), calls.Load(), "a second scrape inside the cache window reads the cache")
}

func TestBackgroundQueue_AFailingSamplerSkipsItsQueueOnly(t *testing.T) {
	m := newBackgroundMetrics(t)
	m.RegisterBackgroundQueue("broken", func(context.Context) ([]BackgroundQueueSample, error) {
		return nil, errors.New("database unreachable")
	})
	m.RegisterBackgroundQueue("empty", func(context.Context) ([]BackgroundQueueSample, error) { return nil, nil })
	m.RegisterBackgroundQueue("ignored", nil)
	body := scrapeMetrics(t, m.Handler())
	assert.NotContains(t, body, `loop="broken"`)
	assert.NotContains(t, body, `loop="ignored"`)
}

func TestBackgroundRecorders_NilSafe(_ *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.RecordLoopIteration(ctx, "l", LoopResultOK, 0)
	m.RecordRowsPurged(ctx, "l", 1)
	m.RegisterBackgroundQueue("q", func(context.Context) ([]BackgroundQueueSample, error) { return nil, nil })
	m.RegisterAuditQueueDepth(func() int { return 0 })
	m.RecordAuditWrite(ctx, StatusOK, 0)
	m.RecordScriptRunFailure(ctx, "script")
	m.RecordScriptRunShed(ctx)
	m.RecordScriptWorkerLoad(ctx, "cpu", 0)
	m.RecordNotificationAttempt(ctx, "email", "delivered")
	m.RecordConnectionOAuthRefresh(ctx, "api", "ok")
	m.RecordConnectionOAuthCredentials(ctx, "api", "ok", 1)
	m.RecordThumbnailRenderer(ctx, true)
	m.RecordThumbnailRender(ctx, "asset", 0)
	m.RecordThumbnailFailure(ctx, "asset", "document")
	m.RecordListenConnected(ctx, "l", true, false)
	m.RecordListenNotification("l")
}

// TestBackgroundQueue_ASlowSamplerHoldsNoNotification: while a scrape waits on
// a queue sampler (a database that has slowed down), a LISTEN adapter's
// notification is recorded at once and the other queues still answer. The
// sampler used to run under the mutex RecordListenNotification takes.
func TestBackgroundQueue_ASlowSamplerHoldsNoNotification(t *testing.T) {
	m := newBackgroundMetrics(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m.RegisterBackgroundQueue("slow", func(context.Context) ([]BackgroundQueueSample, error) {
		close(entered)
		<-release
		return []BackgroundQueueSample{{Pending: 1}}, nil
	})
	m.RegisterBackgroundQueue("quick", func(context.Context) ([]BackgroundQueueSample, error) {
		return []BackgroundQueueSample{{Pending: 2}}, nil
	})

	scraped := make(chan string)
	go func() { scraped <- scrapeMetrics(t, m.Handler()) }()
	<-entered

	recorded := make(chan struct{})
	go func() {
		m.RecordListenNotification("listen_session")
		close(recorded)
	}()
	select {
	case <-recorded:
	case <-time.After(10 * time.Second):
		t.Fatal("a notification waited on a queue sampler")
	}
	close(release)
	body := <-scraped
	assert.Contains(t, body, `background_queue_items{loop="quick",state="pending"} 2`)
	assert.Contains(t, body, `background_queue_items{loop="slow",state="pending"} 1`)
}

// TestListenClock_AReconnectDoesNotRestartIt: a connection's
// time-since-notification clock starts on its first connect, a reconnect
// leaves it running, and only a notification resets it.
func TestListenClock_AReconnectDoesNotRestartIt(t *testing.T) {
	m := newBackgroundMetrics(t)
	ctx := context.Background()
	m.RecordListenConnected(ctx, "listen_x", true, false)
	m.bg.mu.Lock()
	started := m.bg.lastNotify["listen_x"]
	long := started.Add(-time.Hour)
	m.bg.lastNotify["listen_x"] = long
	m.bg.mu.Unlock()

	m.RecordListenConnected(ctx, "listen_x", true, true)
	m.bg.mu.Lock()
	afterReconnect := m.bg.lastNotify["listen_x"]
	m.bg.mu.Unlock()
	assert.Equal(t, long, afterReconnect, "a reconnect restarted the clock")

	m.RecordListenNotification("listen_x")
	m.bg.mu.Lock()
	afterNotify := m.bg.lastNotify["listen_x"]
	m.bg.mu.Unlock()
	assert.True(t, afterNotify.After(long), "a notification did not reset the clock")
}
