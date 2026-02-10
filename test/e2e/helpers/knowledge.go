//go:build integration

package helpers

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq" // PostgreSQL driver

	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// KnowledgeTestDB holds database resources for knowledge e2e tests.
type KnowledgeTestDB struct {
	DB             *sql.DB
	InsightStore   knowledge.InsightStore
	ChangesetStore knowledge.ChangesetStore
}

// NewKnowledgeTestDB opens a connection, runs migrations, and creates stores.
// It drops conflicting tables created by the docker init script (01_init.sql)
// so that golang-migrate can apply the canonical schema.
func NewKnowledgeTestDB(t *testing.T, dsn string) *KnowledgeTestDB {
	t.Helper()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("pinging database: %v", err)
	}

	// The docker init script (01_init.sql) creates tables with schemas that
	// differ from the golang-migrate migrations. Drop them so migrations own
	// the schema. CASCADE handles FK references between init-script tables.
	initTables := []string{
		"oauth_refresh_tokens", "oauth_access_tokens",
		"oauth_authorization_codes", "oauth_clients",
		"audit_logs", "schema_migrations",
	}
	for _, tbl := range initTables {
		//nolint:gosec // test-only, table names are hardcoded constants
		if _, err := db.Exec("DROP TABLE IF EXISTS " + tbl + " CASCADE"); err != nil {
			t.Fatalf("dropping init table %s: %v", tbl, err)
		}
	}

	if err := migrate.Run(db); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	return &KnowledgeTestDB{
		DB:             db,
		InsightStore:   knowledge.NewPostgresStore(db),
		ChangesetStore: knowledge.NewPostgresChangesetStore(db),
	}
}

// TruncateKnowledgeTables removes all rows from knowledge tables.
func (k *KnowledgeTestDB) TruncateKnowledgeTables(t *testing.T) {
	t.Helper()

	_, err := k.DB.Exec("TRUNCATE knowledge_insights, knowledge_changesets")
	if err != nil {
		t.Fatalf("truncating knowledge tables: %v", err)
	}
}

// CountRows returns the number of rows in the given table.
func (k *KnowledgeTestDB) CountRows(t *testing.T, table string) int {
	t.Helper()

	var count int
	// table is always a hardcoded literal from test code, not user input
	err := k.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count) //nolint:gosec // test-only, table name from test code
	if err != nil {
		t.Fatalf("counting rows in %s: %v", table, err)
	}
	return count
}

// Close closes the database connection.
func (k *KnowledgeTestDB) Close() error {
	return k.DB.Close()
}

// TestInsight builds an Insight with sensible defaults for testing.
func TestInsight(id, category, text string, entityURNs []string) knowledge.Insight {
	return knowledge.Insight{
		ID:               id,
		SessionID:        "e2e-session-001",
		CapturedBy:       "e2e-user",
		Persona:          "analyst",
		Category:         category,
		InsightText:      text,
		Confidence:       "medium",
		EntityURNs:       entityURNs,
		RelatedColumns:   []knowledge.RelatedColumn{},
		SuggestedActions: []knowledge.SuggestedAction{},
		Status:           knowledge.StatusPending,
	}
}

// InsertTestInsight is a convenience that inserts a TestInsight and fails on error.
func (k *KnowledgeTestDB) InsertTestInsight(t *testing.T, id, category, text string, entityURNs []string) {
	t.Helper()

	insight := TestInsight(id, category, text, entityURNs)
	if err := k.InsightStore.Insert(context.Background(), insight); err != nil {
		t.Fatalf("inserting test insight %s: %v", id, err)
	}
}
