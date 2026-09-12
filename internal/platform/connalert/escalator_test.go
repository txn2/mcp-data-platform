package connalert

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// escalatorHarness builds an escalator over recording fakes, with the rows the
// sweep will claim already staged.
func escalatorHarness(t *testing.T, settings Settings, due ...Alert) (*Escalator, *recordingQueue) {
	t.Helper()
	alerts := newFakeAlerts()
	alerts.claimed = due
	queue := &recordingQueue{}
	enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	e := NewEscalator(Config{
		Settings: stubSettings{settings: &settings},
		Alerts:   alerts,
		Enqueuer: enq,
		BaseURL:  "https://platform.example.com",
	})
	require.NotNil(t, e)
	return e, queue
}

func dueAlert() Alert {
	return Alert{
		Kind: "api", Name: "billing", AuthorizedBy: "ops@example.com",
		IDPHost: "idp.example.com", Reason: "invalid_grant",
		RevokedAt: time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC),
	}
}

// TestEscalator_RaisesWhatNobodyActedOn is the escalation criterion: a
// revocation the authorizing operator has not come back to reaches the
// addresses the operator named, and says whose authorization it was.
func TestEscalator_RaisesWhatNobodyActedOn(t *testing.T) {
	e, queue := escalatorHarness(t, Settings{
		Enabled: true, EscalateAfterHours: 12,
		Recipients: []string{"platform-admin@example.com", "oncall@example.com"},
	}, dueAlert())

	require.NoError(t, e.Sweep(t.Context()))

	rows := queue.all()
	require.Len(t, rows, 2)
	assert.Equal(t, "platform-admin@example.com", rows[0].recipient)
	assert.Equal(t, "oncall@example.com", rows[1].recipient)

	conn := rows[0].payload.Connection
	require.NotNil(t, conn)
	assert.True(t, conn.Escalated)
	assert.Equal(t, 12, conn.EscalatedAfterHours)
	assert.Equal(t, "ops@example.com", conn.AuthorizedBy,
		"the recipient is somebody else, so the mail says whose authorization lapsed")
	assert.Equal(t, "billing", conn.Name)
}

func TestEscalator_QuietPaths(t *testing.T) {
	t.Run("no recipients escalates nowhere", func(t *testing.T) {
		e, queue := escalatorHarness(t, DefaultSettings(), dueAlert())
		require.NoError(t, e.Sweep(t.Context()))
		assert.Empty(t, queue.all())
	})

	t.Run("a blank recipient is not an address", func(t *testing.T) {
		e, queue := escalatorHarness(t, Settings{
			Enabled: true, Recipients: []string{"  ", ""},
		}, dueAlert())
		require.NoError(t, e.Sweep(t.Context()))
		assert.Empty(t, queue.all())
	})

	t.Run("a disabled deployment escalates nothing", func(t *testing.T) {
		e, queue := escalatorHarness(t, Settings{
			Enabled: false, Recipients: []string{"admin@example.com"},
		}, dueAlert())
		require.NoError(t, e.Sweep(t.Context()))
		assert.Empty(t, queue.all())
	})

	t.Run("nothing due escalates nothing", func(t *testing.T) {
		e, queue := escalatorHarness(t, Settings{
			Enabled: true, Recipients: []string{"admin@example.com"},
		})
		require.NoError(t, e.Sweep(t.Context()))
		assert.Empty(t, queue.all())
	})

	t.Run("a failed claim is reported", func(t *testing.T) {
		alerts := newFakeAlerts()
		alerts.claimErr = errors.New("boom")
		queue := &recordingQueue{}
		enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
		t.Cleanup(enq.Close)
		e := NewEscalator(Config{
			Settings: stubSettings{settings: &Settings{
				Enabled: true, Recipients: []string{"admin@example.com"},
			}},
			Alerts:   alerts,
			Enqueuer: enq,
		})
		require.NotNil(t, e)
		assert.ErrorContains(t, e.Sweep(t.Context()), "sweeping open connection revocations")
	})

	t.Run("an unreadable configuration is reported", func(t *testing.T) {
		queue := &recordingQueue{}
		enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
		t.Cleanup(enq.Close)
		e := NewEscalator(Config{
			Settings: stubSettings{err: errors.New("boom")},
			Alerts:   newFakeAlerts(),
			Enqueuer: enq,
		})
		require.NotNil(t, e)
		assert.Error(t, e.Sweep(t.Context()))
	})

	t.Run("a nil escalator is safe to bracket", func(t *testing.T) {
		var e *Escalator
		assert.NotPanics(t, func() {
			e.Start(t.Context())
			e.Stop()
		})
		assert.NoError(t, e.Sweep(t.Context()))
	})

	t.Run("a missing dependency yields no sweep", func(t *testing.T) {
		assert.Nil(t, NewEscalator(Config{}))
	})
}

// TestEscalator_StartSweepsOnItsTicker proves the loop runs the sweep rather
// than only holding a goroutine: the type's whole value is that nobody has to
// ask.
func TestEscalator_StartSweepsOnItsTicker(t *testing.T) {
	alerts := newFakeAlerts()
	alerts.claimed = []Alert{dueAlert()}
	queue := &recordingQueue{}
	enq := notification.NewEnqueuer(openPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	e := NewEscalator(Config{
		Settings: stubSettings{settings: &Settings{
			Enabled: true, EscalateAfterHours: 1,
			Recipients: []string{"admin@example.com"},
		}},
		Alerts:   alerts,
		Enqueuer: enq,
		Interval: 10 * time.Millisecond,
	})
	require.NotNil(t, e)
	e.Start(t.Context())
	defer e.Stop()

	assert.Eventually(t, func() bool { return len(queue.all()) > 0 },
		2*time.Second, 10*time.Millisecond)
}
