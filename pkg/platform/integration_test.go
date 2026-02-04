//go:build integration

package platform_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// TestAuditLogging_EndToEnd tests that audit logging works with a real PostgreSQL database.
func TestAuditLogging_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err, "failed to start postgres container")
	defer func() { _ = pgContainer.Terminate(ctx) }()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	// Connect to database
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "failed to open database")
	defer db.Close()

	// Run migrations
	err = runMigrations(db)
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

// TestAuditAdapter_Integration tests the middleware adapter with a real database.
func TestAuditAdapter_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err, "failed to start postgres container")
	defer func() { _ = pgContainer.Terminate(ctx) }()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	// Connect to database
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "failed to open database")
	defer db.Close()

	// Run migrations
	err = runMigrations(db)
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

// TestRuleEngine_Integration tests that rule engine actually affects behavior.
func TestRuleEngine_Integration(t *testing.T) {
	rules := &tuning.Rules{
		RequireDataHubCheck: true,
		WarnOnDeprecated:    true,
		QualityThreshold:    0.7,
		MaxQueryLimit:       10000,
	}

	engine := tuning.NewRuleEngine(rules)

	// Verify rule engine configuration
	assert.True(t, engine.ShouldRequireDataHubCheck())
	assert.Equal(t, 10000, engine.GetMaxQueryLimit())

	// Test rule violations
	metadata := tuning.QueryMetadata{
		QualityScore: floatPtr(0.5), // Below threshold
		IsDeprecated: true,
	}

	violations := engine.CheckQueryExecution(metadata)
	assert.Len(t, violations, 2, "expected 2 violations")

	// Verify violation types
	var hasQualityViolation, hasDeprecatedViolation bool
	for _, v := range violations {
		if v.Rule == "quality_threshold" {
			hasQualityViolation = true
		}
		if v.Rule == "deprecated_data" {
			hasDeprecatedViolation = true
		}
	}
	assert.True(t, hasQualityViolation, "expected quality threshold violation")
	assert.True(t, hasDeprecatedViolation, "expected deprecated data violation")
}

// TestPersonaPrompts_Integration tests that persona prompts are properly combined.
func TestPersonaPrompts_Integration(t *testing.T) {
	registry := persona.NewRegistry()

	// Register persona with full prompt config
	err := registry.Register(&persona.Persona{
		Name:        "analyst",
		DisplayName: "Data Analyst",
		Description: "Analyze data and run queries",
		Roles:       []string{"analyst"},
		Prompts: persona.PromptConfig{
			SystemPrefix: "You are a data analyst assistant.",
			Instructions: "Always check DataHub before running queries.",
			SystemSuffix: "Return results in JSON format.",
		},
		Priority: 10,
	})
	require.NoError(t, err)

	// Get persona and verify prompt
	p, ok := registry.Get("analyst")
	require.True(t, ok)

	prompt := p.GetFullSystemPrompt()
	assert.Contains(t, prompt, "You are a data analyst assistant.")
	assert.Contains(t, prompt, "Always check DataHub before running queries.")
	assert.Contains(t, prompt, "Return results in JSON format.")

	// Verify parts are separated by double newlines
	assert.Contains(t, prompt, "\n\n")
}

// TestPlatform_WithDatabase tests platform initialization with a real database.
func TestPlatform_WithDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err, "failed to start postgres container")
	defer func() { _ = pgContainer.Terminate(ctx) }()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	// Connect to run migrations
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	err = runMigrations(db)
	require.NoError(t, err, "failed to run migrations")
	db.Close()

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
			Enabled:       true,
			LogToolCalls:  true,
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
	assert.NotNil(t, p.HintManager())
}

// runMigrations executes all SQL migrations in the migrations directory.
func runMigrations(db *sql.DB) error {
	// Find migrations directory
	migrationsDir := findMigrationsDir()
	if migrationsDir == "" {
		// Create minimal migration for testing
		_, err := db.Exec(`
			CREATE TABLE IF NOT EXISTS audit_logs (
				id              VARCHAR(32) NOT NULL,
				timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				duration_ms     INTEGER,
				request_id      VARCHAR(255),
				user_id         VARCHAR(255),
				user_email      VARCHAR(255),
				persona         VARCHAR(100),
				tool_name       VARCHAR(255) NOT NULL,
				toolkit_kind    VARCHAR(100),
				toolkit_name    VARCHAR(100),
				connection      VARCHAR(100),
				parameters      JSONB,
				success         BOOLEAN NOT NULL,
				error_message   TEXT,
				created_date    DATE NOT NULL DEFAULT CURRENT_DATE,
				PRIMARY KEY (id, created_date)
			)
		`)
		return err
	}

	// Read and execute migration files
	files, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) != ".sql" {
			continue
		}

		content, err := os.ReadFile(filepath.Join(migrationsDir, file.Name()))
		if err != nil {
			return err
		}

		_, err = db.Exec(string(content))
		if err != nil {
			return err
		}
	}

	return nil
}

// findMigrationsDir locates the migrations directory.
func findMigrationsDir() string {
	// Try common paths
	paths := []string{
		"../../migrations",
		"migrations",
		"../../../migrations",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

// floatPtr returns a pointer to a float64 value.
func floatPtr(v float64) *float64 {
	return &v
}
