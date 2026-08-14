package reviewalert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeReviews serves a fixed script review queue.
type fakeReviews struct {
	pending []script.PendingReview
	err     error
}

func (f fakeReviews) ListPendingReviews(context.Context) ([]script.PendingReview, error) {
	return f.pending, f.err
}

func TestScriptSourcePending(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	t.Run("counts the queue and reports the oldest", func(t *testing.T) {
		src := ScriptSource{Reviews: fakeReviews{pending: []script.PendingReview{
			{CreatedAt: now.AddDate(0, 0, -2)},
			{CreatedAt: now.AddDate(0, 0, -9)},
			{CreatedAt: now.AddDate(0, 0, -5)},
		}}}

		got, err := src.Pending(context.Background(), now)
		require.NoError(t, err)
		assert.Equal(t, 3, got.Pending)
		assert.Equal(t, 9, got.OldestAgeDays,
			"the oldest is the maximum age, not whichever row the query returned first")
		assert.Zero(t, got.StaleCount, "scripts have no second staleness window to report")
	})

	t.Run("an empty queue crosses nothing", func(t *testing.T) {
		src := ScriptSource{Reviews: fakeReviews{pending: nil}}
		got, err := src.Pending(context.Background(), now)
		require.NoError(t, err)
		assert.Zero(t, got.Pending)
		assert.False(t, alertSettings().Crossed(got.Pending, got.OldestAgeDays))
	})

	t.Run("a store failure is reported rather than read as an empty queue", func(t *testing.T) {
		src := ScriptSource{Reviews: fakeReviews{err: errors.New("boom")}}
		_, err := src.Pending(context.Background(), now)
		assert.ErrorContains(t, err, "reading the script review queue")
	})
}

// TestScriptCheckerAlertsOnItsOwnTarget proves the second queue runs the same
// mechanism end to end: its own category, its own link, and its own settings.
func TestScriptCheckerAlertsOnItsOwnTarget(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	queue := &captureQueue{}
	enq := notification.NewEnqueuer(defaultPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	settings := Settings{
		Enabled: true, OldestPendingDays: 7, CooldownHours: 24,
		Recipients: []string{"reviewer@example.com"},
	}
	checker := New(Config{
		Target:   ScriptTarget(),
		Settings: &stubSettings{settings: &settings},
		State:    &memState{},
		Source: ScriptSource{Reviews: fakeReviews{pending: []script.PendingReview{
			{CreatedAt: now.AddDate(0, 0, -8)},
		}}},
		Enqueuer: enq,
		BaseURL:  "https://data.example.com",
		Now:      func() time.Time { return now },
	})
	require.NotNil(t, checker)

	require.NoError(t, checker.Check(context.Background()))

	rows := queue.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "script_review", rows[0].Category)
	assert.Equal(t, "script_review", rows[0].Payload.Kind)
	assert.Equal(t, "https://data.example.com/portal/admin/scripts", rows[0].Payload.Link)
	require.NotNil(t, rows[0].Payload.Review)
	assert.Equal(t, 1, rows[0].Payload.Review.Pending)
}
