package connalert

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeAlerts is an in-memory AlertStore recording what the alerter did.
type fakeAlerts struct {
	mu       sync.Mutex
	open     map[string]Alert
	cleared  []string
	claimed  []Alert
	openErr  error
	claimErr error
}

func newFakeAlerts() *fakeAlerts { return &fakeAlerts{open: map[string]Alert{}} }

func (f *fakeAlerts) Open(_ context.Context, a Alert) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return false, f.openErr
	}
	key := a.Kind + "/" + a.Name
	if _, exists := f.open[key]; exists {
		return false, nil
	}
	f.open[key] = a
	return true, nil
}

func (f *fakeAlerts) Clear(_ context.Context, kind, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.open, kind+"/"+name)
	f.cleared = append(f.cleared, kind+"/"+name)
	return nil
}

func (f *fakeAlerts) ClaimEscalations(_ context.Context, _ time.Duration, _ time.Time) ([]Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claimed, f.claimErr
}

// queued is one notification the substrate accepted.
type queued struct {
	recipient string
	category  string
	payload   notification.Payload
}

// recordingQueue is a notification.QueueStore that keeps what was enqueued, so
// a test reads what a recipient would actually receive rather than asserting
// that a function was called.
type recordingQueue struct {
	mu   sync.Mutex
	rows []queued
}

func (q *recordingQueue) Enqueue(_ context.Context, n notification.Notification) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows = append(q.rows, queued{recipient: n.Recipient, category: n.Category, payload: n.Payload})
	return nil
}

func (q *recordingQueue) all() []queued {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]queued, len(q.rows))
	copy(out, q.rows)
	return out
}

// Unused QueueStore methods: the enqueue path is the only half under test.
func (*recordingQueue) ClaimImmediate(context.Context, time.Duration) (*notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*recordingQueue) ClaimDigest(context.Context, time.Duration) ([]notification.Notification, error) {
	return nil, notification.ErrNoWork
}
func (*recordingQueue) MarkSent(context.Context, []int64) error { return nil }

func (*recordingQueue) Retry(context.Context, []int64, string, time.Duration) error { return nil }
func (*recordingQueue) Fail(context.Context, []int64, string) error                 { return nil }

func (*recordingQueue) PurgeOld(context.Context, time.Duration, time.Duration) (int64, error) {
	return 0, nil
}

// openPrefs is the platform default for every address: immediate delivery, no
// category muted. It models the real store's contract, which answers for an
// address that has never been written rather than reporting it absent.
type openPrefs struct{}

func (openPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func (openPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// harness builds an alerter over recording fakes.
func harness(t *testing.T, settings Settings) (*Alerter, *fakeAlerts, *recordingQueue) {
	t.Helper()
	alerts := newFakeAlerts()
	queue := &recordingQueue{}
	enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	a := NewAlerter(Config{
		Settings: stubSettings{settings: &settings},
		Alerts:   alerts,
		Enqueuer: enq,
		BaseURL:  "https://platform.example.com",
	})
	require.NotNil(t, a)
	return a, alerts, queue
}

func revocation() authevents.Revocation {
	return authevents.Revocation{
		Kind: "api", Name: "billing", AuthorizedBy: "ops@example.com",
		IDPHost: "idp.example.com", Reason: "invalid_grant",
		At: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC),
	}
}

// TestAlerter_TellsTheOperatorWhoAuthorizedIt is the ticket's central
// criterion: the revocation reaches the person whose authorization lapsed,
// naming the connection, the upstream, what it said, and when.
func TestAlerter_TellsTheOperatorWhoAuthorizedIt(t *testing.T) {
	a, alerts, queue := harness(t, DefaultSettings())

	a.Revoked(t.Context(), revocation())

	rows := queue.all()
	require.Len(t, rows, 1)
	assert.Equal(t, "ops@example.com", rows[0].recipient)
	assert.Equal(t, notification.CategoryConnectionAuth, rows[0].category)

	conn := rows[0].payload.Connection
	require.NotNil(t, conn)
	assert.Equal(t, "api", conn.Kind)
	assert.Equal(t, "billing", conn.Name)
	assert.Equal(t, "idp.example.com", conn.IDPHost)
	assert.Equal(t, "invalid_grant", conn.Reason)
	assert.Equal(t, revocation().At, conn.RevokedAt)
	assert.False(t, conn.Escalated, "the first alert is not an escalation")
	assert.Equal(t, "https://platform.example.com/portal/admin/connections", rows[0].payload.Link,
		"the fix is one click from the mail")

	assert.Len(t, alerts.open, 1, "the revocation stays open until it is reauthorized")
}

// TestAlerter_AnnouncesOncePerRevocation is the ticket's de-duplication
// criterion: a connection that keeps being called is announced once, not once
// per rejected call.
func TestAlerter_AnnouncesOncePerRevocation(t *testing.T) {
	a, alerts, queue := harness(t, DefaultSettings())

	a.Revoked(t.Context(), revocation())
	a.Revoked(t.Context(), revocation())
	a.Revoked(t.Context(), revocation())
	assert.Len(t, queue.all(), 1)

	// Reauthorizing forgets the revocation, so the next one is news again.
	require.NoError(t, alerts.Clear(t.Context(), "api", "billing"))
	a.Revoked(t.Context(), revocation())
	assert.Len(t, queue.all(), 2)
}

func TestAlerter_QuietPaths(t *testing.T) {
	t.Run("a disabled deployment tells nobody and records nothing", func(t *testing.T) {
		a, alerts, queue := harness(t, Settings{Enabled: false})
		a.Revoked(t.Context(), revocation())
		assert.Empty(t, queue.all())
		assert.Empty(t, alerts.open)
	})

	t.Run("an unattributed connection is still recorded, so it can escalate", func(t *testing.T) {
		a, alerts, queue := harness(t, DefaultSettings())
		rev := revocation()
		rev.AuthorizedBy = ""
		a.Revoked(t.Context(), rev)
		assert.Empty(t, queue.all(), "there is nobody to tell")
		assert.Len(t, alerts.open, 1, "but the operator's recipients still can be")
	})

	t.Run("a failed write announces nothing", func(t *testing.T) {
		a, alerts, queue := harness(t, DefaultSettings())
		alerts.openErr = errors.New("boom")
		a.Revoked(t.Context(), revocation())
		assert.Empty(t, queue.all())
	})

	t.Run("an unreadable configuration announces nothing", func(t *testing.T) {
		queue := &recordingQueue{}
		enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
		t.Cleanup(enq.Close)
		a := NewAlerter(Config{
			Settings: stubSettings{err: errors.New("boom")},
			Alerts:   newFakeAlerts(),
			Enqueuer: enq,
		})
		require.NotNil(t, a)
		a.Revoked(t.Context(), revocation())
		assert.Empty(t, queue.all())
	})

	t.Run("a revocation with no time still carries one", func(t *testing.T) {
		a, _, queue := harness(t, DefaultSettings())
		rev := revocation()
		rev.At = time.Time{}
		a.Revoked(t.Context(), rev)
		rows := queue.all()
		require.Len(t, rows, 1)
		assert.False(t, rows[0].payload.Connection.RevokedAt.IsZero())
	})

	t.Run("a nil alerter is a usable sink", func(t *testing.T) {
		var a *Alerter
		assert.NotPanics(t, func() { a.Revoked(t.Context(), revocation()) })
	})
}

// TestNewAlerter_RequiresItsDependencies proves the composition root's "this
// deployment has no database" path: a missing dependency yields the nil sink
// rather than one that fails on every revocation.
func TestNewAlerter_RequiresItsDependencies(t *testing.T) {
	assert.Nil(t, NewAlerter(Config{}))
	assert.Nil(t, NewAlerter(Config{Settings: stubSettings{}, Alerts: newFakeAlerts()}),
		"no enqueuer means nowhere to send")
}
