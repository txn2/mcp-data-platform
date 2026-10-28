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
