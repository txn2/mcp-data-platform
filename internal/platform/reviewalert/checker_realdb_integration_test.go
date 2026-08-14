//go:build integration

package reviewalert

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyprefs"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyqueue"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// TestCheckerAgainstRealDB runs the scheduled check over the stores a
// database-backed deployment actually wires: the memory-backed insight
// adapter (migration 000031 dropped knowledge_insights in favor of
// memory_records, so this -- not the legacy Postgres insight store -- is what
// the running platform reads), the real settings and claim tables, and the
// real notification queue.
//
// The unit tests pin the policy against doubles; this pins that the policy is
// reading and writing the same tables the server does.
func TestCheckerAgainstRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	insights := knowledgekit.NewMemoryInsightAdapter(memory.NewPostgresStore(db))
	seedStalePending(t, db, insights, 3)

	store := NewPostgresStore(db, KnowledgeTarget())
	require.NoError(t, store.Set(ctx, Settings{
		Enabled: true, OldestPendingDays: 30, CooldownHours: 24,
		Recipients: []string{"ops@example.com"},
	}, "admin@example.com"))

	queue := notifyqueue.NewPostgresStore(db)
	enq := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), queue, 13)
	t.Cleanup(enq.Close)

	checker := New(Config{
		Target: KnowledgeTarget(), Settings: store, State: store,
		Source:   InsightSource{Insights: insights},
		Enqueuer: enq, BaseURL: "https://data.example.com",
	})
	require.NotNil(t, checker)

	// 1. The queue is over the age threshold, so the check alerts.
	require.NoError(t, checker.Check(ctx))
	rows := queuedAlerts(t, db)
	require.Len(t, rows, 1, "a stale queue alerts its configured recipient")
	assert.Equal(t, "ops@example.com", rows[0].Recipient)
	require.NotNil(t, rows[0].Payload.Review, "the rollup must survive the JSONB round trip")
	assert.Equal(t, 3, rows[0].Payload.Review.Pending)
	assert.GreaterOrEqual(t, rows[0].Payload.Review.OldestAgeDays, 90)
	assert.Equal(t, "https://data.example.com/portal/knowledge#review", rows[0].Payload.Link)

	// 2. The queue is still stale on the next check; the cooldown holds.
	require.NoError(t, checker.Check(ctx))
	assert.Len(t, queuedAlerts(t, db), 1,
		"a queue that stays stale must not mail again on the next interval")
}

// seedStalePending writes n pending insights aged 94 days through the real
// insight store, then backdates them: Insert stamps created_at itself, which
// is exactly the field the staleness rollup reads.
func seedStalePending(t *testing.T, db *sql.DB, store knowledgekit.InsightStore, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		require.NoError(t, store.Insert(ctx, knowledgekit.Insight{
			ID:          "stale-insight-" + string(rune('a'+i)),
			SessionID:   "sess-realdb",
			CapturedBy:  "analyst@example.com",
			Category:    "correction",
			InsightText: "The amount column is gross margin, not revenue.",
			Confidence:  "high",
			Status:      knowledgekit.StatusPending,
		}))
	}
	_, err := db.ExecContext(ctx,
		`UPDATE memory_records SET created_at = NOW() - INTERVAL '94 days'`)
	require.NoError(t, err)
}

// queuedAlerts reads the review-queue rows the check wrote to the real
// notifications table.
func queuedAlerts(t *testing.T, db *sql.DB) []notification.Notification {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT recipient, payload FROM notifications WHERE category = $1 ORDER BY id`,
		notification.CategoryReviewQueue)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var out []notification.Notification
	for rows.Next() {
		var n notification.Notification
		var raw []byte
		require.NoError(t, rows.Scan(&n.Recipient, &raw))
		require.NoError(t, json.Unmarshal(raw, &n.Payload))
		out = append(out, n)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestCheckerAgainstRealDB_UnderThreshold is the converse: a fresh queue
// alerts nobody and leaves no claim outstanding, so the first real crossing
// is not swallowed by a cooldown that was never earned.
func TestCheckerAgainstRealDB_UnderThreshold(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	insights := knowledgekit.NewMemoryInsightAdapter(memory.NewPostgresStore(db))
	require.NoError(t, insights.Insert(ctx, knowledgekit.Insight{
		ID: "fresh-insight", SessionID: "sess-realdb", CapturedBy: "analyst@example.com",
		Category: "correction", InsightText: "Fresh capture.", Confidence: "high",
		Status: knowledgekit.StatusPending,
	}))

	store := NewPostgresStore(db, KnowledgeTarget())
	require.NoError(t, store.Set(ctx, Settings{
		Enabled: true, OldestPendingDays: 30, CooldownHours: 24,
		Recipients: []string{"ops@example.com"},
	}, "admin@example.com"))

	queue := notifyqueue.NewPostgresStore(db)
	enq := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), queue, 13)
	t.Cleanup(enq.Close)

	checker := New(Config{
		Target: KnowledgeTarget(), Settings: store, State: store,
		Source:   InsightSource{Insights: insights},
		Enqueuer: enq, BaseURL: "https://data.example.com",
	})
	require.NoError(t, checker.Check(ctx))
	assert.Empty(t, queuedAlerts(t, db))

	var alerting bool
	err := db.QueryRowContext(ctx,
		`SELECT alerting FROM review_alert_state`).Scan(&alerting)
	if err == nil {
		assert.False(t, alerting, "an under-threshold check leaves no claim outstanding")
	} else {
		assert.ErrorIs(t, err, sql.ErrNoRows, "or no state row at all")
	}

	// The queue then ages past the threshold: the first crossing alerts at
	// once rather than waiting out a cooldown.
	_, err = db.ExecContext(ctx, `UPDATE memory_records SET created_at = NOW() - INTERVAL '94 days'`)
	require.NoError(t, err)
	require.NoError(t, checker.Check(ctx))
	assert.Len(t, queuedAlerts(t, db), 1)
}
