//go:build integration

package notifyqueue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// TestHistoryRealDB proves the history read path against real Postgres: the
// filters compose, the ordering is newest-first, paging walks the whole set
// without repeating a row, and the per-status counts match the rows listed.
func TestHistoryRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	// created_at is stamped explicitly so the newest-first ordering has a
	// deterministic answer rather than depending on insert timing.
	seed := func(recipient, category, status string, ageMinutes int) int64 {
		var id int64
		require.NoError(t, db.QueryRowContext(ctx,
			`INSERT INTO notifications (recipient, category, payload, status, last_error, created_at)
			 VALUES ($1, $2, '{"kind":"asset","item_title":"Report","actor":"o@example.com"}', $3, $4,
			         NOW() - ($5 || ' minutes')::INTERVAL)
			 RETURNING id`,
			recipient, category, status, errFor(status), ageMinutes).Scan(&id))
		return id
	}

	newest := seed("a@example.com", notification.CategoryShare, notification.StatusFailed, 1)
	middle := seed("a@example.com", notification.CategoryComment, notification.StatusSent, 5)
	oldest := seed("a@example.com", notification.CategoryShare, notification.StatusPending, 9)
	other := seed("b@example.com", notification.CategoryShare, notification.StatusSent, 3)

	t.Run("lists newest first", func(t *testing.T) {
		rows, err := store.List(ctx, notification.HistoryFilter{})
		require.NoError(t, err)
		require.Len(t, rows, 4)
		require.Equal(t, []int64{newest, other, middle, oldest},
			[]int64{rows[0].ID, rows[1].ID, rows[2].ID, rows[3].ID})
		// The payload survives the round trip, which the subject line needs.
		require.Equal(t, "Report", rows[0].Payload.ItemTitle)
		require.Equal(t, "smtp refused", rows[0].LastError)
	})

	t.Run("scopes to one recipient", func(t *testing.T) {
		rows, err := store.List(ctx, notification.HistoryFilter{Recipient: "b@example.com"})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, other, rows[0].ID)
	})

	t.Run("composes recipient, status, and category", func(t *testing.T) {
		rows, err := store.List(ctx, notification.HistoryFilter{
			Recipient: "a@example.com",
			Status:    notification.StatusFailed,
			Category:  notification.CategoryShare,
		})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, newest, rows[0].ID)
	})

	t.Run("pages without repeating or dropping a row", func(t *testing.T) {
		var seen []int64
		for offset := 0; offset < 4; offset += 2 {
			rows, err := store.List(ctx, notification.HistoryFilter{Limit: 2, Offset: offset})
			require.NoError(t, err)
			require.Len(t, rows, 2)
			for _, r := range rows {
				seen = append(seen, r.ID)
			}
		}
		require.Equal(t, []int64{newest, other, middle, oldest}, seen)
	})

	t.Run("counts match the filter", func(t *testing.T) {
		total, err := store.Count(ctx, notification.HistoryFilter{})
		require.NoError(t, err)
		require.Equal(t, 4, total)

		mine, err := store.Count(ctx, notification.HistoryFilter{Recipient: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, 3, mine)

		// Paging fields must not narrow a count.
		paged, err := store.Count(ctx, notification.HistoryFilter{Limit: 1, Offset: 3})
		require.NoError(t, err)
		require.Equal(t, 4, paged)
	})

	t.Run("counts by status", func(t *testing.T) {
		counts, err := store.CountsByStatus(ctx, notification.HistoryFilter{})
		require.NoError(t, err)
		require.Equal(t, map[string]int{
			notification.StatusFailed:  1,
			notification.StatusSent:    2,
			notification.StatusPending: 1,
		}, counts)

		scoped, err := store.CountsByStatus(ctx, notification.HistoryFilter{Recipient: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, map[string]int{
			notification.StatusFailed:  1,
			notification.StatusSent:    1,
			notification.StatusPending: 1,
		}, scoped)
	})

	t.Run("an unmatched filter is an empty page, not an error", func(t *testing.T) {
		rows, err := store.List(ctx, notification.HistoryFilter{Recipient: "nobody@example.com"})
		require.NoError(t, err)
		require.Empty(t, rows)

		counts, err := store.CountsByStatus(ctx, notification.HistoryFilter{Status: "not-a-status"})
		require.NoError(t, err)
		require.Empty(t, counts)
	})
}

// errFor gives failed rows the error text an admin drills into, and leaves
// every other status with the empty string the column defaults to.
func errFor(status string) string {
	if status == notification.StatusFailed {
		return "smtp refused"
	}
	return ""
}
