//go:build integration

package postgres

// Real-Postgres round-trip test for the audit store. Logging is synchronous, so
// a logged event is immediately queryable. This exercises the full INSERT
// against the real audit_logs schema (every NOT NULL column, defaults).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

func TestAuditStore_Log_RealDB_RoundTrip(t *testing.T) {
	store := New(testdb.New(t), Config{RetentionDays: 30})
	ctx := context.Background()

	event := audit.NewEvent("realdb_test_tool").
		WithUser("user@example.com", "user@example.com").
		WithPersona("admin").
		WithResult(true, "", 42)
	require.NoError(t, store.Log(ctx, *event), "log audit event")

	got, err := store.Query(ctx, audit.QueryFilter{ToolName: "realdb_test_tool", Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, got, "logged event is queryable")
	assert.Equal(t, "realdb_test_tool", got[0].ToolName)
	assert.True(t, got[0].Success)
}

// TestResourceUsage_RealDB_AggregatesReadEvents exercises the resource usage
// rollup against the real partitioned audit_logs: the FILTER-ed window counts,
// the per-surface grouping, and the JSONB parameter extraction the partial
// index is built on. sqlmock verifies none of that.
func TestResourceUsage_RealDB_AggregatesReadEvents(t *testing.T) {
	store := New(testdb.New(t), Config{RetentionDays: 90})
	ctx := context.Background()

	log := func(resourceID, surface string) {
		t.Helper()
		ev := audit.NewEvent(surface).
			WithEventKind(audit.EventTypeResourceRead).
			WithUser("analyst@example.com", "analyst@example.com").
			WithParameters(map[string]any{
				"resource_id":  resourceID,
				"resource_uri": "mcp://global/runbooks/" + resourceID + ".md",
				"surface":      surface,
			})
		require.NoError(t, store.Log(ctx, *ev))
	}

	log("res_usage_1", "mcp_read")
	log("res_usage_1", "mcp_read")
	log("res_usage_1", "rest_download")
	log("res_usage_2", "fetch")

	usage, err := store.ResourceUsage(ctx, []string{"res_usage_1", "res_usage_2", "res_usage_never"})
	require.NoError(t, err)

	first := usage["res_usage_1"]
	assert.Equal(t, int64(3), first.Reads30d, "reads sum across surfaces")
	assert.Equal(t, int64(3), first.Reads90d)
	assert.Equal(t, int64(2), first.BySurface30d["mcp_read"])
	assert.Equal(t, int64(1), first.BySurface30d["rest_download"])
	require.NotNil(t, first.LastReadAt)

	assert.Equal(t, int64(1), usage["res_usage_2"].Reads90d)

	_, present := usage["res_usage_never"]
	assert.False(t, present, "a resource never read has no entry")
}

// TestFilterValues_RealDB_LooseScanMatchesDistinct is #2272: the filter
// dropdowns read each column through its index one value at a time, and that
// lists exactly what SELECT DISTINCT lists, NULLs excluded, in order; the user
// labels pair each user with a non-empty email; and the bounded count stops at
// its limit and counts in full below it.
func TestFilterValues_RealDB_LooseScanMatchesDistinct(t *testing.T) {
	db := testdb.New(t)
	store := New(db, Config{RetentionDays: 90})
	ctx := context.Background()

	for i, e := range []struct{ user, email, tool, kind, source string }{
		{"u-b", "b@example.com", "trino_query", "trino", "mcp"},
		{"u-a", "", "search", "search", "script"},
		{"u-a", "a@example.com", "search", "search", "mcp"},
		{"u-c", "c@example.com", "fetch", "", "portal"},
		{"u-b", "b@example.com", "trino_query", "trino", "mcp"},
	} {
		ev := audit.NewEvent(e.tool).WithUser(e.user, e.email).WithToolkit(e.kind, e.kind).WithTransport("http", e.source).WithResult(i%2 == 0, "", 1)
		require.NoError(t, store.Log(ctx, *ev))
	}
	// A row written before event_kind existed carries NULL there, and NULL is
	// not a value a filter can choose.
	_, err := db.ExecContext(ctx, `UPDATE audit_logs SET event_kind = NULL WHERE tool_name = 'fetch'`)
	require.NoError(t, err)

	for _, col := range []string{colUserID, colToolName, colToolkitKind, colSource, colEventKind} {
		rows, err := db.QueryContext(ctx, `SELECT DISTINCT `+col+` FROM audit_logs WHERE `+col+` IS NOT NULL ORDER BY `+col)
		require.NoError(t, err)
		var want []string
		for rows.Next() {
			var v string
			require.NoError(t, rows.Scan(&v))
			want = append(want, v)
		}
		require.NoError(t, rows.Close())
		got, err := store.Distinct(ctx, col, nil, nil)
		require.NoError(t, err, col)
		assert.Equal(t, want, got, col)
	}

	labels, err := store.DistinctPairs(ctx, colUserID, colUserEmail, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"u-a": "a@example.com", "u-b": "b@example.com", "u-c": "c@example.com"}, labels)

	for limit, want := range map[int]int{3: 3, 5: 5, 50: 5} {
		n, err := store.CountUpTo(ctx, audit.QueryFilter{}, limit)
		require.NoError(t, err)
		assert.Equal(t, want, n, "limit %d", limit)
	}
	n, err := store.CountUpTo(ctx, audit.QueryFilter{ToolName: "search"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// TestMetrics_RealDB_HoldsAreNeitherSuccessNorFailure is #2819: a call held
// for an approval or an answer is counted apart. The success rate leaves it
// out of both sides, the error count leaves it out, and a tool whose only
// non-successes are holds reads 100%. A resource read under the tool's name
// is not one of its calls when the breakdown asks for tool calls, and is
// counted under its own event kind.
func TestMetrics_RealDB_HoldsAreNeitherSuccessNorFailure(t *testing.T) {
	store := New(testdb.New(t), Config{RetentionDays: 30})
	ctx := context.Background()
	const tool = "realdb_holds_tool"
	logCall := func(success bool, category string) {
		t.Helper()
		ev := audit.NewEvent(tool).WithEventKind(audit.EventTypeMCPToolCall).
			WithUser("u@example.com", "u@example.com").WithResult(success, "", 5)
		if category != "" {
			ev = ev.WithErrorCategory(category)
		}
		require.NoError(t, store.Log(ctx, *ev))
	}
	logCall(true, "")
	logCall(true, "")
	logCall(false, audit.CategoryInputRequired)
	logCall(false, audit.CategoryInputRequired)
	read := audit.NewEvent(tool).WithEventKind(audit.EventTypeResourceRead).WithResult(true, "", 1)
	require.NoError(t, store.Log(ctx, *read))

	rows, err := store.Breakdown(ctx, audit.BreakdownFilter{
		GroupBy: audit.BreakdownByToolName, ToolName: tool, EventKind: string(audit.EventTypeMCPToolCall),
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 4, rows[0].Count, "the resource read is not a call of the tool")
	assert.InDelta(t, 1.0, rows[0].SuccessRate, 1e-9, "holds are not failures")
	assert.Equal(t, 2, rows[0].HoldCount)

	logCall(false, "tool_error")
	o, err := store.Overview(ctx, audit.MetricsFilter{EventKind: string(audit.EventTypeMCPToolCall)})
	require.NoError(t, err)
	assert.Equal(t, 1, o.ErrorCount, "a hold is not an error")
	assert.Equal(t, 2, o.HoldCount)
	assert.InDelta(t, 2.0/3.0, o.SuccessRate, 1e-9, "two successes of three calls that finished")

	kinds, err := store.Breakdown(ctx, audit.BreakdownFilter{GroupBy: audit.BreakdownByEventKind})
	require.NoError(t, err)
	byKind := map[string]int{}
	for _, k := range kinds {
		byKind[k.Dimension] = k.Count
	}
	assert.Equal(t, 1, byKind[string(audit.EventTypeResourceRead)])
	assert.Equal(t, 5, byKind[string(audit.EventTypeMCPToolCall)])
}

// TestBreakdown_RealDB_FailuresByCategorySourceAndPersonaAreExact is #2795:
// over a window holding more than the event list's 10,000-row count cap, the
// failures grouped by category, by source and by persona are exact, and so is
// the window's total.
func TestBreakdown_RealDB_FailuresByCategorySourceAndPersonaAreExact(t *testing.T) {
	db := testdb.New(t)
	store := New(db, Config{RetentionDays: 30})
	ctx := context.Background()
	// 10,050 successes in one statement, then the failures one by one.
	_, err := db.ExecContext(ctx, `INSERT INTO audit_logs (id, timestamp, duration_ms, user_id, persona, tool_name, success, source, event_kind, created_date)
		SELECT 'bulk-' || g, now(), 1, 'u', 'analyst', 'search', true, 'mcp', 'mcp_tool_call', CURRENT_DATE
		  FROM generate_series(1, 10050) AS g`)
	require.NoError(t, err)
	fail := func(source, persona, category string) {
		t.Helper()
		ev := audit.NewEvent("trino_query").WithEventKind(audit.EventTypeMCPToolCall).WithPersona(persona).
			WithTransport("http", source).WithResult(false, "refused", 1).WithErrorCategory(category)
		require.NoError(t, store.Log(ctx, *ev))
	}
	fail("mcp", "analyst", "schema_required")
	fail("mcp", "analyst", "schema_required")
	fail("mcp", "engineer", "search_required")
	fail("script", "engineer", "upstream_5xx")

	failed := false
	count := func(dim audit.BreakdownDimension) map[string]int {
		t.Helper()
		rows, err := store.Breakdown(ctx, audit.BreakdownFilter{GroupBy: dim, Success: &failed})
		require.NoError(t, err)
		out := map[string]int{}
		for _, r := range rows {
			out[r.Dimension] = r.Count
		}
		return out
	}
	assert.Equal(t, map[string]int{"schema_required": 2, "search_required": 1, "upstream_5xx": 1}, count(audit.BreakdownByErrorCategory))
	assert.Equal(t, map[string]int{"mcp": 3, "script": 1}, count(audit.BreakdownBySource))
	assert.Equal(t, map[string]int{"analyst": 2, "engineer": 2}, count(audit.BreakdownByPersona))

	all, err := store.Breakdown(ctx, audit.BreakdownFilter{GroupBy: audit.BreakdownBySource})
	require.NoError(t, err)
	total := 0
	for _, r := range all {
		total += r.Count
	}
	assert.Equal(t, 10054, total, "every call in the window, past the event list's cap")
	o, err := store.Overview(ctx, audit.MetricsFilter{})
	require.NoError(t, err)
	assert.Equal(t, 10054, o.TotalCalls)
}
