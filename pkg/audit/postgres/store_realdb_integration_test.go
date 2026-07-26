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
