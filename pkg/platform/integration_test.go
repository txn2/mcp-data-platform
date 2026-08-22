//go:build integration

package platform_test

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// TestAuditLogging_EndToEnd_RealDB tests that audit logging works with a real PostgreSQL database.
func TestAuditLogging_EndToEnd_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// testdb.New rather than a container of this test's own: it clones a
	// migrated template on the one server the gate starts, so no container
	// and no Ryuk reaper per test. A reaper wait is what made the sibling
	// progress test flaky under load.
	db := testdb.New(t)
	var err error
	require.NoError(t, err, "failed to run migrations")

	// Create audit store
	store := auditpostgres.New(db, auditpostgres.Config{
		RetentionDays: 30,
	})

	// Log an audit event
	event := audit.NewEvent("trino_query").
		WithRequestID("req-123").
		WithUser("user@example.com", "user@example.com").
		WithPersona("analyst").
		WithToolkit("trino", "production").
		WithConnection("trino://prod").
		WithParameters(map[string]any{"sql": "SELECT 1"}).
		WithResult(true, "", 100)

	err = store.Log(ctx, *event)
	require.NoError(t, err, "failed to log event")

	// Query for the event
	events, err := store.Query(ctx, audit.QueryFilter{
		UserID: "user@example.com",
		Limit:  10,
	})
	require.NoError(t, err, "failed to query events")
	require.Len(t, events, 1, "expected 1 event")

	// Verify event fields
	got := events[0]
	assert.Equal(t, "trino_query", got.ToolName)
	assert.Equal(t, "user@example.com", got.UserID)
	assert.Equal(t, "analyst", got.Persona)
	assert.Equal(t, "trino", got.ToolkitKind)
	assert.Equal(t, "production", got.ToolkitName)
	assert.True(t, got.Success)
	assert.Equal(t, int64(100), got.DurationMS)
}

// TestAuditAdapter_Integration_RealDB tests the middleware adapter with a real database.
func TestAuditAdapter_Integration_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// testdb.New rather than a container of this test's own: it clones a
	// migrated template on the one server the gate starts, so no container
	// and no Ryuk reaper per test. A reaper wait is what made the sibling
	// progress test flaky under load.
	db := testdb.New(t)
	var err error
	require.NoError(t, err, "failed to run migrations")

	// Create audit store and adapter
	store := auditpostgres.New(db, auditpostgres.Config{
		RetentionDays: 30,
	})
	adapter := middleware.NewAuditStoreAdapter(store)

	// Log via adapter (simulating middleware usage)
	event := middleware.AuditEvent{
		Timestamp:    time.Now(),
		RequestID:    "req-456",
		UserID:       "admin@example.com",
		UserEmail:    "admin@example.com",
		Persona:      "admin",
		ToolName:     "datahub_search",
		ToolkitKind:  "datahub",
		ToolkitName:  "primary",
		Parameters:   map[string]any{"query": "test"},
		Success:      true,
		ErrorMessage: "",
		DurationMS:   50,
	}

	err = adapter.Log(ctx, event)
	require.NoError(t, err, "failed to log via adapter")

	// Verify event was logged
	events, err := store.Query(ctx, audit.QueryFilter{
		UserID: "admin@example.com",
		Limit:  10,
	})
	require.NoError(t, err, "failed to query events")
	require.Len(t, events, 1, "expected 1 event")
	assert.Equal(t, "datahub_search", events[0].ToolName)
}

// TestPlatform_WithDatabase_RealDB tests platform initialization with a real database.
func TestPlatform_WithDatabase_RealDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// NewWithDSN because platform.New opens its own pool from
	// config.Database.DSN. It clones a migrated template on the one server
	// the gate starts rather than running a container and a Ryuk reaper of
	// this test's own; migrate.Run is idempotent, so the platform applying
	// them again against the same schema is a no-op.
	_, dsn := testdb.NewWithDSN(t)

	// Create platform with database config
	cfg := &platform.Config{
		Server: platform.ServerConfig{
			Name:    "integration-test",
			Version: "1.0.0",
		},
		Database: platform.DatabaseConfig{
			DSN:          dsn,
			MaxOpenConns: 5,
		},
		Audit: platform.AuditConfig{
			Enabled:       boolPtr(true),
			LogToolCalls:  boolPtr(true),
			RetentionDays: 30,
		},
	}

	p, err := platform.New(platform.WithConfig(cfg))
	require.NoError(t, err, "failed to create platform")
	defer p.Close()

	// Verify platform components
	assert.NotNil(t, p.MCPServer())
	assert.NotNil(t, p.Config())
	assert.NotNil(t, p.RuleEngine())
}

func boolPtr(v bool) *bool { return &v }
