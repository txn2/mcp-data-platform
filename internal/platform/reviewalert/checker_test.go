package reviewalert

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// memState models the store contract the PostgreSQL claim implements: a claim
// wins when no alert is outstanding or the cooldown has elapsed, and Clear
// drops the outstanding marker while keeping the last-alert timestamp.
type memState struct {
	mu       sync.Mutex
	alerting bool
	lastAt   time.Time
	claims   int
	clears   int
	claimErr error
	clearErr error
}

func (s *memState) ClaimAlert(_ context.Context, cooldown time.Duration, now time.Time) (bool, error) {
	if s.claimErr != nil {
		return false, s.claimErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.alerting && !s.lastAt.IsZero() && s.lastAt.After(now.Add(-cooldown)) {
		return false, nil
	}
	s.alerting, s.lastAt = true, now
	return true, nil
}

func (s *memState) Clear(context.Context) error {
	if s.clearErr != nil {
		return s.clearErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clears++
	s.alerting = false
	return nil
}

// fakeInsights serves a fixed pending-review rollup through the
// PendingReviewStater fast path, which is the path PendingReviewOf takes.
type fakeInsights struct {
	knowledgekit.InsightStore
	review *knowledgekit.PendingReview
	err    error
}

func (f *fakeInsights) PendingReviewStats(context.Context) (*knowledgekit.PendingReview, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.review, nil
}

// captureQueue records what the enqueuer wrote.
type captureQueue struct {
	notification.QueueStore
	mu   sync.Mutex
	rows []notification.Notification
	err  error
}

func (q *captureQueue) Enqueue(_ context.Context, n notification.Notification) error {
	if q.err != nil {
		return q.err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows = append(q.rows, n)
	return nil
}

func (q *captureQueue) snapshot() []notification.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]notification.Notification(nil), q.rows...)
}

// defaultPrefs serves the platform default preferences for everyone.
type defaultPrefs struct{}

func (defaultPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func (defaultPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// harness bundles a checker with the doubles it was built over.
type harness struct {
	checker *Checker
	queue   *captureQueue
	state   *memState
	now     time.Time
}

// newHarness builds a checker over a queue that is over threshold by the given
// pending count and oldest-insight age.
func newHarness(t *testing.T, settings Settings, pending, oldestDays, over30d int) *harness {
	t.Helper()
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	review := &knowledgekit.PendingReview{TotalPending: pending, PendingOver30d: over30d}
	if pending > 0 {
		oldest := now.AddDate(0, 0, -oldestDays)
		review.OldestPendingAt = &oldest
	}
	queue := &captureQueue{}
	enq := notification.NewEnqueuer(defaultPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	state := &memState{}
	checker := New(Config{
		Target:   KnowledgeTarget(),
		Settings: &stubSettings{settings: &settings},
		State:    state,
		Source:   InsightSource{Insights: &fakeInsights{review: review}},
		Enqueuer: enq,
		BaseURL:  "https://data.example.com",
		Now:      func() time.Time { return now },
	})
	require.NotNil(t, checker)
	return &harness{checker: checker, queue: queue, state: state, now: now}
}

// alertSettings is a deliverable configuration alerting on the 30-day age.
func alertSettings() Settings {
	return Settings{
		Enabled: true, OldestPendingDays: 30, CooldownHours: 24,
		Recipients: []string{"ops@example.com", "lead@example.com"},
	}
}

func TestNew_MissingDependencyIsANoop(t *testing.T) {
	full := Config{
		Target:   KnowledgeTarget(),
		Settings: &stubSettings{}, State: &memState{},
		Source:   InsightSource{Insights: &fakeInsights{}},
		Enqueuer: notification.NewEnqueuer(defaultPrefs{}, &captureQueue{}, 13),
	}
	assert.NotNil(t, New(full))

	for _, tt := range []struct {
		name  string
		strip func(*Config)
	}{
		{"no settings", func(c *Config) { c.Settings = nil }},
		{"no state", func(c *Config) { c.State = nil }},
		{"no source", func(c *Config) { c.Source = nil }},
		{"no enqueuer", func(c *Config) { c.Enqueuer = nil }},
		{"no target", func(c *Config) { c.Target = Target{} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := full
			tt.strip(&cfg)
			checker := New(cfg)
			assert.Nil(t, checker)
			// The composition root brackets Start/Stop unconditionally.
			checker.Start(context.Background())
			checker.Stop()
		})
	}
}

func TestCheck_CrossedQueueEnqueuesOneAlertPerRecipient(t *testing.T) {
	h := newHarness(t, alertSettings(), 12, 94, 5)

	require.NoError(t, h.checker.Check(context.Background()))

	rows := h.queue.snapshot()
	require.Len(t, rows, 2, "one row per configured recipient")
	assert.Equal(t, "ops@example.com", rows[0].Recipient)
	assert.Equal(t, "lead@example.com", rows[1].Recipient)

	row := rows[0]
	assert.Equal(t, notification.CategoryReviewQueue, row.Category)
	assert.Equal(t, notification.KindReviewQueue, row.Payload.Kind)
	assert.Equal(t, "https://data.example.com/portal/knowledge#review", row.Payload.Link)
	require.NotNil(t, row.Payload.Review)
	assert.Equal(t, 12, row.Payload.Review.Pending)
	assert.Equal(t, 94, row.Payload.Review.OldestAgeDays)
	assert.Equal(t, 5, row.Payload.Review.StaleCount)
	assert.Equal(t, knowledgekit.PendingStalenessThresholdDays, row.Payload.Review.StaleAfterDays)
	assert.Empty(t, row.Payload.Actor, "the platform raised this alert, not a person")
}

func TestCheck_StaleQueueDoesNotRealertEveryInterval(t *testing.T) {
	h := newHarness(t, alertSettings(), 12, 94, 5)
	ctx := context.Background()

	for range 5 {
		require.NoError(t, h.checker.Check(ctx))
	}

	assert.Len(t, h.queue.snapshot(), 2,
		"a queue that stays stale alerts once per cooldown, not once per check")
	assert.Equal(t, 5, h.state.claims, "every check still contends for the claim")
}

func TestCheck_CooldownElapsedRealerts(t *testing.T) {
	h := newHarness(t, alertSettings(), 12, 94, 5)
	ctx := context.Background()
	require.NoError(t, h.checker.Check(ctx))

	// Move past the cooldown: the queue is still stale, so it is still news.
	h.state.mu.Lock()
	h.state.lastAt = h.now.Add(-25 * time.Hour)
	h.state.mu.Unlock()
	require.NoError(t, h.checker.Check(ctx))

	assert.Len(t, h.queue.snapshot(), 4, "one alert per recipient per cooldown window")
}

func TestCheck_QueueBackUnderThresholdClearsTheMarker(t *testing.T) {
	h := newHarness(t, alertSettings(), 3, 2, 0)

	require.NoError(t, h.checker.Check(context.Background()))

	assert.Empty(t, h.queue.snapshot(), "a queue under threshold alerts nobody")
	assert.Equal(t, 1, h.state.clears, "the marker is dropped so the next crossing is news again")
}

func TestCheck_ClearedMarkerAlertsImmediatelyOnTheNextCrossing(t *testing.T) {
	h := newHarness(t, alertSettings(), 12, 94, 5)
	ctx := context.Background()
	require.NoError(t, h.checker.Check(ctx))
	require.Len(t, h.queue.snapshot(), 2)

	// The queue is worked back down and then crosses again inside the cooldown.
	require.NoError(t, h.state.Clear(ctx))
	require.NoError(t, h.checker.Check(ctx))

	assert.Len(t, h.queue.snapshot(), 4,
		"a queue that recovered and crossed again is news, not repetition")
}

func TestCheck_UndeliverableConfigurationReadsNothing(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings Settings
	}{
		{"disabled", Settings{Enabled: false, OldestPendingDays: 30, Recipients: []string{"ops@example.com"}}},
		{"no recipients", Settings{Enabled: true, OldestPendingDays: 30}},
		{"no threshold", Settings{Enabled: true, Recipients: []string{"ops@example.com"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.settings, 12, 94, 5)
			require.NoError(t, h.checker.Check(context.Background()))
			assert.Empty(t, h.queue.snapshot())
			assert.Zero(t, h.state.claims, "an undeliverable configuration never reaches the queue state")
		})
	}
}

func TestCheck_EmptyQueueNeverAlerts(t *testing.T) {
	h := newHarness(t, Settings{
		Enabled: true, PendingThreshold: 1, OldestPendingDays: 1,
		Recipients: []string{"ops@example.com"},
	}, 0, 0, 0)

	require.NoError(t, h.checker.Check(context.Background()))
	assert.Empty(t, h.queue.snapshot())
}

func TestCheck_ErrorPaths(t *testing.T) {
	base := func(t *testing.T) Config {
		t.Helper()
		enq := notification.NewEnqueuer(defaultPrefs{}, &captureQueue{}, 13)
		t.Cleanup(enq.Close)
		settings := alertSettings()
		oldest := time.Now().AddDate(0, 0, -90)
		return Config{
			Target:   KnowledgeTarget(),
			Settings: &stubSettings{settings: &settings},
			State:    &memState{},
			Source: InsightSource{Insights: &fakeInsights{review: &knowledgekit.PendingReview{
				TotalPending: 4, OldestPendingAt: &oldest,
			}}},
			Enqueuer: enq,
		}
	}

	t.Run("a settings read failure is returned", func(t *testing.T) {
		cfg := base(t)
		cfg.Settings = &stubSettings{err: errors.New("settings down")}
		assert.ErrorContains(t, New(cfg).Check(context.Background()), "settings down")
	})

	t.Run("an insight store failure is returned", func(t *testing.T) {
		cfg := base(t)
		cfg.Source = InsightSource{Insights: &fakeInsights{err: errors.New("insights down")}}
		assert.ErrorContains(t, New(cfg).Check(context.Background()), "insights down")
	})

	t.Run("a claim failure is returned", func(t *testing.T) {
		cfg := base(t)
		cfg.State = &memState{claimErr: errors.New("claim down")}
		assert.ErrorContains(t, New(cfg).Check(context.Background()), "claim down")
	})

	t.Run("a clear failure is returned", func(t *testing.T) {
		cfg := base(t)
		cfg.State = &memState{clearErr: errors.New("clear down")}
		cfg.Source = InsightSource{Insights: &fakeInsights{review: &knowledgekit.PendingReview{TotalPending: 0}}}
		assert.ErrorContains(t, New(cfg).Check(context.Background()), "clear down")
	})

	t.Run("one recipient's enqueue failure does not stop the check", func(t *testing.T) {
		cfg := base(t)
		enq := notification.NewEnqueuer(defaultPrefs{}, &captureQueue{err: errors.New("queue down")}, 13)
		t.Cleanup(enq.Close)
		cfg.Enqueuer = enq
		assert.NoError(t, New(cfg).Check(context.Background()),
			"the claim is already stamped; giving up here would suppress the whole window")
	})
}

func TestStartRunsTheCheckOnItsInterval(t *testing.T) {
	h := newHarness(t, alertSettings(), 12, 94, 5)
	h.checker.cfg.Interval = 5 * time.Millisecond

	h.checker.Start(t.Context())
	defer h.checker.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(h.queue.snapshot()) < 2 {
		time.Sleep(time.Millisecond)
	}
	assert.Len(t, h.queue.snapshot(), 2, "the loop ran the check without being called directly")
}

func TestStopIsIdempotentAndCancelStopsTheLoop(t *testing.T) {
	h := newHarness(t, alertSettings(), 12, 94, 5)
	h.checker.cfg.Interval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	h.checker.Start(ctx)
	cancel()
	h.checker.Stop()
	h.checker.Stop()
}
