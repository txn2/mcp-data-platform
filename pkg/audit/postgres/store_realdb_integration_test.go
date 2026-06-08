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
