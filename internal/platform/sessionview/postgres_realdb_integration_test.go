//go:build integration

package sessionview

// Real-Postgres test for the session read model. The rollup is the whole
// feature — window functions, FILTER aggregates, array_agg, the correlated
// counts, and the persona overlay off the live session row — and sqlmock
// rubber-stamps every one of them. What is asserted here is that a session's
// calls, the asset it saved, and the insight it captured come back as one
// session, written through the same stores the platform writes through.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpg "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/session"
	sessionpg "github.com/txn2/mcp-data-platform/pkg/session/postgres"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	realDBSession   = "dps_realdb0000000000000000000001"
	realDBOther     = "dpx_realdb0000000000000000000002"
	realDBUser      = "analyst-realdb"
	realDBCallCount = 5
)

// realDBFixture writes one agent session of five calls (the fourth failed),
// one asset and one insight it produced, and a second session belonging to a
// different caller so every assertion below has something to exclude.
func realDBFixture(t *testing.T) (*PostgresStore, context.Context) {
	t.Helper()
	db := testdb.New(t)
	ctx := context.Background()

	events := auditpg.New(db, auditpg.Config{RetentionDays: 30})
	base := time.Now().Add(-time.Hour)

	calls := []struct {
		tool       string
		purpose    string
		connection string
		success    bool
		errMsg     string
	}{
		{"search", "Finding the table behind Q3 revenue.", "", true, ""},
		{"trino_describe_table", "Reading the revenue table's columns.", "acme-warehouse", true, ""},
		{"trino_query", "Summing Q3 revenue by region for the board deck.", "acme-warehouse", true, ""},
		{"trino_query", "Adding the prior-year comparison.", "acme-warehouse", false, "line 1:8: column not found"},
		{"save_asset", "Saving the finished table for the board deck.", "", true, ""},
	}
	for i, c := range calls {
		ev := audit.NewEvent(c.tool).
			WithUser(realDBUser, "analyst@example.com").
			WithPersona("analyst").
			WithSessionID(realDBSession).
			WithPurpose(c.purpose).
			WithConnection(c.connection).
			WithResult(c.success, c.errMsg, int64(10+i))
		ev.Timestamp = base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, events.Log(ctx, *ev), "log call %d", i)
	}

	other := audit.NewEvent("run_script").
		WithUser("someone-else", "ops@example.com").
		WithSessionID(realDBOther).
		WithResult(true, "", 1)
	require.NoError(t, events.Log(ctx, *other))

	// The live session row carries the persona the handle was minted under.
	sessions := sessionpg.New(db, sessionpg.Config{})
	require.NoError(t, sessions.Create(ctx, &session.Session{
		ID:           realDBSession,
		UserID:       realDBUser,
		CreatedAt:    base,
		LastActiveAt: base,
		ExpiresAt:    time.Now().Add(time.Hour),
		State: map[string]any{
			session.StateKeyMintedBy: session.MintedByPlatformInfo,
			session.StateKeyPersona:  "data-engineer",
		},
	}))

	assets := portalstore.NewPostgresAssetStore(db)
	require.NoError(t, assets.Insert(ctx, portaldomain.Asset{
		ID:          "ast_realdb_1",
		OwnerID:     realDBUser,
		OwnerEmail:  "analyst@example.com",
		Name:        "Q3 revenue by region",
		ContentType: "text/csv",
		S3Bucket:    "portal-assets",
		S3Key:       "assets/ast_realdb_1/content.csv",
		SessionID:   realDBSession,
	}))

	// Insights are written through the knowledge toolkit's memory adapter,
	// which is the platform's only insight write path since migration 000031
	// folded knowledge_insights into memory_records.
	insights := knowledge.NewMemoryInsightAdapter(memory.NewPostgresStore(db))
	require.NoError(t, insights.Insert(ctx, knowledge.Insight{
		ID:          "ins_realdb_1",
		SessionID:   realDBSession,
		CapturedBy:  "analyst@example.com",
		Persona:     "analyst",
		Source:      "user",
		Category:    "correction",
		InsightText: "revenue.amount excludes returns.",
		Confidence:  "high",
		Status:      "pending",
	}))

	return NewPostgresStore(db), ctx
}

// TestSessionView_RealDB_ListsOneSessionWithWhatItProduced is the acceptance
// criterion: five calls, one of them failed, plus a saved asset list as ONE
// session with the right counts and the asset attached.
func TestSessionView_RealDB_ListsOneSessionWithWhatItProduced(t *testing.T) {
	store, ctx := realDBFixture(t)

	got, err := store.List(ctx, Filter{UserID: realDBUser, Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1, "five calls are one session, not five rows")

	s := got[0]
	assert.Equal(t, realDBSession, s.SessionID)
	assert.Equal(t, KindAgent, s.Kind)
	assert.Equal(t, realDBCallCount, s.CallCount)
	assert.Equal(t, 1, s.FailureCount)
	assert.Equal(t, 1, s.AssetCount)
	assert.Equal(t, 1, s.InsightCount)
	assert.Equal(t, "analyst@example.com", s.UserEmail)
	assert.Equal(t, "data-engineer", s.Persona,
		"the live session row's minted persona outranks the events' resolved persona")
	assert.Equal(t,
		[]string{"save_asset", "search", "trino_describe_table", "trino_query"},
		s.Tools, "distinct tools, sorted, one entry for the repeated tool")
	assert.Equal(t, []string{"acme-warehouse"}, s.Connections)
	assert.True(t, s.LastActiveAt.After(s.StartedAt), "the window spans the calls")

	total, err := store.Count(ctx, Filter{UserID: realDBUser})
	require.NoError(t, err)
	assert.Equal(t, 1, total, "the count counts sessions, not the events behind them")
}

// TestSessionView_RealDB_LoadReturnsOrderedTimeline covers the detail: the
// calls come back in the order they were made, each carrying the purpose the
// agent stated, alongside what the session produced.
func TestSessionView_RealDB_LoadReturnsOrderedTimeline(t *testing.T) {
	store, ctx := realDBFixture(t)

	detail, err := Load(ctx, store, realDBSession, 25, 0)
	require.NoError(t, err)
	require.Len(t, detail.Timeline, realDBCallCount)
	assert.Equal(t, realDBCallCount, detail.TimelineTotal)

	assert.Equal(t,
		[]string{"search", "trino_describe_table", "trino_query", "trino_query", "save_asset"},
		toolNames(detail.Timeline), "oldest first")
	assert.Equal(t, "Finding the table behind Q3 revenue.", detail.Timeline[0].Purpose)
	assert.False(t, detail.Timeline[3].Success)
	assert.Contains(t, detail.Timeline[3].ErrorMessage, "column not found")

	require.Len(t, detail.Assets, 1)
	assert.Equal(t, "Q3 revenue by region", detail.Assets[0].Name)
	assert.Equal(t, "text/csv", detail.Assets[0].ContentType)
	require.Len(t, detail.Insights, 1)
	assert.Equal(t, "correction", detail.Insights[0].Category)

	_, err = Load(ctx, store, "dps_never_ran", 25, 0)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestSessionView_RealDB_ToleratesNullColumns covers a row from before the
// platform wrote every column: audit_logs leaves connection, persona, and the
// caller nullable, and a NULL inside an aggregated array fails the scan of the
// whole page rather than of the row that carries it.
func TestSessionView_RealDB_ToleratesNullColumns(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	store := NewPostgresStore(db)

	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_logs (id, timestamp, session_id, tool_name, success, created_date)
		VALUES ($1, NOW(), $2, 'legacy_tool', true, CURRENT_DATE)`,
		"evt_legacy_realdb", realDBSession)
	require.NoError(t, err, "insert a row carrying only its NOT NULL columns")

	got, err := store.List(ctx, Filter{SessionID: realDBSession})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Connections, "a NULL connection is no connection")
	assert.Equal(t, []string{"legacy_tool"}, got[0].Tools)
	assert.Empty(t, got[0].UserID)
}

// TestSessionView_RealDB_TimelinePages proves the paging is over the session's
// own calls and that the total stays the session's full count.
func TestSessionView_RealDB_TimelinePages(t *testing.T) {
	store, ctx := realDBFixture(t)

	page, total, err := store.Timeline(ctx, realDBSession, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, realDBCallCount, total)
	require.Len(t, page, 2)
	assert.Equal(t, []string{"trino_query", "trino_query"}, toolNames(page))
}

// TestSessionView_RealDB_Filters exercises each list filter against the real
// rollup, where a predicate in the wrong clause (HAVING vs WHERE) is the
// difference between narrowing sessions and narrowing their events.
func TestSessionView_RealDB_Filters(t *testing.T) {
	store, ctx := realDBFixture(t)

	tests := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"agent kind", Filter{Kind: KindAgent}, []string{realDBSession}},
		{"script kind", Filter{Kind: KindScript}, []string{realDBOther}},
		{"transport kind excludes every minted prefix", Filter{Kind: KindTransport}, nil},
		{"has assets", Filter{HasAssets: true}, []string{realDBSession}},
		{"has failures", Filter{HasFailures: true}, []string{realDBSession}},
		{"user", Filter{UserID: "someone-else"}, []string{realDBOther}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.List(ctx, tt.filter)
			require.NoError(t, err)
			assert.Equal(t, tt.want, sessionIDs(got))

			count, err := store.Count(ctx, tt.filter)
			require.NoError(t, err)
			assert.Equal(t, len(tt.want), count, "the count matches the page it counts")
		})
	}
}

// TestSessionView_RealDB_TimeRangeBoundsEvents proves the time range bounds a
// session's events: a window covering only its later calls still returns the
// session, narrowed to what happened inside the window.
func TestSessionView_RealDB_TimeRangeBoundsEvents(t *testing.T) {
	store, ctx := realDBFixture(t)

	full, err := store.List(ctx, Filter{SessionID: realDBSession})
	require.NoError(t, err)
	require.Len(t, full, 1)

	from := full[0].StartedAt.Add(90 * time.Second)
	got, err := store.List(ctx, Filter{SessionID: realDBSession, StartTime: &from})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 3, got[0].CallCount, "only the calls inside the window")

	before := full[0].StartedAt.Add(-time.Minute)
	none, err := store.List(ctx, Filter{SessionID: realDBSession, EndTime: &before})
	require.NoError(t, err)
	assert.Empty(t, none, "a window before the session began holds no session")
}

func toolNames(entries []TimelineEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.ToolName)
	}
	return names
}

func sessionIDs(sessions []Summary) []string {
	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.SessionID)
	}
	return ids
}
