package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	migrateTestFileCount    = 18
	migrateTestSuccess      = "success"
	migrateTestFactoryError = "factory error"
)

// mockMigrator implements the migrator interface for testing.
type mockMigrator struct {
	upErr      error
	downErr    error
	stepsErr   error
	versionVal uint
	dirty      bool
	versionErr error
}

func (m *mockMigrator) Up() error         { return m.upErr }
func (m *mockMigrator) Down() error       { return m.downErr }
func (m *mockMigrator) Steps(_ int) error { return m.stepsErr }
func (m *mockMigrator) Version() (version uint, dirty bool, err error) {
	return m.versionVal, m.dirty, m.versionErr
}

func TestMigrationsEmbedded(t *testing.T) {
	entries, err := migrations.ReadDir("migrations")
	assert.NoError(t, err)
	assert.NotEmpty(t, entries)
	assert.Len(t, entries, migrateTestFileCount)

	expectedFiles := []string{
		"000001_oauth_clients.up.sql",
		"000001_oauth_clients.down.sql",
		"000002_audit_logs.up.sql",
		"000002_audit_logs.down.sql",
		"000003_response_size.up.sql",
		"000003_response_size.down.sql",
		"000004_audit_schema_improvements.up.sql",
		"000004_audit_schema_improvements.down.sql",
		"000005_sessions.up.sql",
		"000005_sessions.down.sql",
		"000006_knowledge_insights.up.sql",
		"000006_knowledge_insights.down.sql",
		"000007_knowledge_lifecycle.up.sql",
		"000007_knowledge_lifecycle.down.sql",
		"000008_knowledge_changesets.up.sql",
		"000008_knowledge_changesets.down.sql",
		"000009_config_store.up.sql",
		"000009_config_store.down.sql",
	}

	fileNames := make(map[string]bool)
	for _, e := range entries {
		fileNames[e.Name()] = true
	}

	for _, expected := range expectedFiles {
		assert.True(t, fileNames[expected], "expected migration file %s to exist", expected)
	}
}

func TestMigrationFilesNotEmpty(t *testing.T) {
	files := []string{
		"migrations/000001_oauth_clients.up.sql",
		"migrations/000001_oauth_clients.down.sql",
		"migrations/000002_audit_logs.up.sql",
		"migrations/000002_audit_logs.down.sql",
		"migrations/000003_response_size.up.sql",
		"migrations/000003_response_size.down.sql",
		"migrations/000004_audit_schema_improvements.up.sql",
		"migrations/000004_audit_schema_improvements.down.sql",
		"migrations/000005_sessions.up.sql",
		"migrations/000005_sessions.down.sql",
		"migrations/000006_knowledge_insights.up.sql",
		"migrations/000006_knowledge_insights.down.sql",
		"migrations/000007_knowledge_lifecycle.up.sql",
		"migrations/000007_knowledge_lifecycle.down.sql",
		"migrations/000008_knowledge_changesets.up.sql",
		"migrations/000008_knowledge_changesets.down.sql",
	}

	for _, file := range files {
		content, err := migrations.ReadFile(file)
		assert.NoError(t, err, "failed to read %s", file)
		assert.NotEmpty(t, content, "migration file %s should not be empty", file)
	}
}

func TestMigrationUpFilesContainCreateTable(t *testing.T) {
	upFiles := []string{
		"migrations/000001_oauth_clients.up.sql",
		"migrations/000002_audit_logs.up.sql",
		"migrations/000005_sessions.up.sql",
		"migrations/000006_knowledge_insights.up.sql",
		"migrations/000008_knowledge_changesets.up.sql",
	}

	for _, file := range upFiles {
		content, err := migrations.ReadFile(file)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "CREATE TABLE", "up migration %s should contain CREATE TABLE", file)
	}
}

func TestMigrationDownFilesContainDropTable(t *testing.T) {
	downFiles := []string{
		"migrations/000001_oauth_clients.down.sql",
		"migrations/000002_audit_logs.down.sql",
		"migrations/000005_sessions.down.sql",
		"migrations/000006_knowledge_insights.down.sql",
		"migrations/000008_knowledge_changesets.down.sql",
	}

	for _, file := range downFiles {
		content, err := migrations.ReadFile(file)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "DROP TABLE", "down migration %s should contain DROP TABLE", file)
	}
}

func TestRun(t *testing.T) {
	origFactory := migratorFactory
	defer func() { migratorFactory = origFactory }()

	t.Run(migrateTestSuccess, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{versionVal: 2}, nil
		}

		err := Run(nil)
		assert.NoError(t, err)
	})

	t.Run("no change is not an error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{upErr: migrate.ErrNoChange, versionVal: 2}, nil
		}

		err := Run(nil)
		assert.NoError(t, err)
	})

	t.Run("up error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{upErr: errors.New("up failed")}, nil
		}

		err := Run(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "running migrations")
	})

	t.Run(migrateTestFactoryError, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return nil, errors.New("factory failed")
		}

		err := Run(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "factory failed")
	})

	t.Run("version error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{versionErr: errors.New("version failed")}, nil
		}

		err := Run(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "getting migration version")
	})

	t.Run("nil version is not an error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{versionErr: migrate.ErrNilVersion}, nil
		}

		err := Run(nil)
		assert.NoError(t, err)
	})

	t.Run("dirty state logs warning", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{versionVal: 2, dirty: true}, nil
		}

		err := Run(nil)
		assert.NoError(t, err)
	})
}

func TestVersion(t *testing.T) {
	origFactory := migratorFactory
	defer func() { migratorFactory = origFactory }()

	t.Run(migrateTestSuccess, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{versionVal: 5, dirty: false}, nil
		}

		version, dirty, err := Version(nil)
		assert.NoError(t, err)
		assert.Equal(t, uint(5), version)
		assert.False(t, dirty)
	})

	t.Run(migrateTestFactoryError, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return nil, errors.New("factory failed")
		}

		_, _, err := Version(nil)
		assert.Error(t, err)
	})
}

func TestDown(t *testing.T) {
	origFactory := migratorFactory
	defer func() { migratorFactory = origFactory }()

	t.Run(migrateTestSuccess, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{}, nil
		}

		err := Down(nil)
		assert.NoError(t, err)
	})

	t.Run("no change is not an error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{downErr: migrate.ErrNoChange}, nil
		}

		err := Down(nil)
		assert.NoError(t, err)
	})

	t.Run("down error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{downErr: errors.New("down failed")}, nil
		}

		err := Down(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "rolling back migrations")
	})

	t.Run(migrateTestFactoryError, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return nil, errors.New("factory failed")
		}

		err := Down(nil)
		assert.Error(t, err)
	})
}

func TestSteps(t *testing.T) {
	origFactory := migratorFactory
	defer func() { migratorFactory = origFactory }()

	t.Run(migrateTestSuccess, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{}, nil
		}

		err := Steps(nil, 1)
		assert.NoError(t, err)
	})

	t.Run("no change is not an error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{stepsErr: migrate.ErrNoChange}, nil
		}

		err := Steps(nil, 1)
		assert.NoError(t, err)
	})

	t.Run("steps error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{stepsErr: errors.New("steps failed")}, nil
		}

		err := Steps(nil, 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stepping migrations")
	})

	t.Run(migrateTestFactoryError, func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return nil, errors.New("factory failed")
		}

		err := Steps(nil, 1)
		assert.Error(t, err)
	})
}

func TestMigration004_UpContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000004_audit_schema_improvements.up.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	// Must drop the redundant column.
	assert.Contains(t, migrationSQL, "DROP COLUMN", "up migration should drop response_token_estimate")
	assert.Contains(t, migrationSQL, "response_token_estimate")

	// Must add all 7 new columns.
	newColumns := []string{
		"session_id", "request_chars", "transport",
		"enrichment_applied", "content_blocks", "authorized", "source",
	}
	for _, col := range newColumns {
		assert.Contains(t, migrationSQL, "ADD COLUMN "+col,
			"up migration should add column %s", col)
	}

	// Must create index on session_id.
	assert.Contains(t, migrationSQL, "CREATE INDEX")
	assert.Contains(t, migrationSQL, "idx_audit_logs_session_id")
}

func TestMigration004_DownContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000004_audit_schema_improvements.down.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	// Must drop the index.
	assert.Contains(t, migrationSQL, "DROP INDEX")
	assert.Contains(t, migrationSQL, "idx_audit_logs_session_id")

	// Must drop all 7 new columns.
	droppedColumns := []string{
		"source", "authorized", "content_blocks",
		"enrichment_applied", "transport", "request_chars", "session_id",
	}
	for _, col := range droppedColumns {
		assert.Contains(t, migrationSQL, "DROP COLUMN IF EXISTS "+col,
			"down migration should drop column %s", col)
	}

	// Must restore the redundant column.
	assert.Contains(t, migrationSQL, "ADD COLUMN response_token_estimate")
}

func TestMigration005_UpContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000005_sessions.up.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	assert.Contains(t, migrationSQL, "CREATE TABLE")
	assert.Contains(t, migrationSQL, "sessions")

	expectedColumns := []string{
		"id", "user_id", "created_at", "last_active_at", "expires_at", "state",
	}
	for _, col := range expectedColumns {
		assert.Contains(t, migrationSQL, col,
			"up migration should contain column %s", col)
	}

	assert.Contains(t, migrationSQL, "idx_sessions_expires_at")
	assert.Contains(t, migrationSQL, "idx_sessions_user_id")
}

func TestMigration005_DownContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000005_sessions.down.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	assert.Contains(t, migrationSQL, "DROP TABLE")
	assert.Contains(t, migrationSQL, "sessions")
}

func TestMigration006_UpContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000006_knowledge_insights.up.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	assert.Contains(t, migrationSQL, "CREATE TABLE")
	assert.Contains(t, migrationSQL, "knowledge_insights")

	expectedColumns := []string{
		"id", "created_at", "session_id", "captured_by", "persona",
		"category", "insight_text", "confidence", "entity_urns",
		"related_columns", "suggested_actions", "status",
	}
	for _, col := range expectedColumns {
		assert.Contains(t, migrationSQL, col,
			"up migration should contain column %s", col)
	}

	expectedIndexes := []string{
		"idx_knowledge_insights_session_id",
		"idx_knowledge_insights_captured_by",
		"idx_knowledge_insights_status",
		"idx_knowledge_insights_category",
		"idx_knowledge_insights_created_at",
	}
	for _, idx := range expectedIndexes {
		assert.Contains(t, migrationSQL, idx,
			"up migration should contain index %s", idx)
	}
}

func TestMigration006_DownContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000006_knowledge_insights.down.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	assert.Contains(t, migrationSQL, "DROP TABLE")
	assert.Contains(t, migrationSQL, "knowledge_insights")
}

func TestMigration007_UpContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000007_knowledge_lifecycle.up.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	// Must add lifecycle columns to knowledge_insights.
	assert.Contains(t, migrationSQL, "ALTER TABLE")
	assert.Contains(t, migrationSQL, "knowledge_insights")

	expectedColumns := []string{
		"reviewed_by",
		"reviewed_at",
		"review_notes",
	}
	for _, col := range expectedColumns {
		assert.Contains(t, migrationSQL, col,
			"up migration should add column %s", col)
	}
}

func TestMigration007_DownContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000007_knowledge_lifecycle.down.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	assert.Contains(t, migrationSQL, "ALTER TABLE")
	assert.Contains(t, migrationSQL, "knowledge_insights") //nolint:revive // test assertion

	droppedColumns := []string{
		"review_notes",
		"reviewed_at",
		"reviewed_by",
	}
	for _, col := range droppedColumns {
		assert.Contains(t, migrationSQL, "DROP COLUMN IF EXISTS "+col,
			"down migration should drop column %s", col)
	}
}

func TestMigration008_UpContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000008_knowledge_changesets.up.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	// Must create the knowledge_changesets table.
	assert.Contains(t, migrationSQL, "CREATE TABLE") //nolint:revive // test assertion
	assert.Contains(t, migrationSQL, "knowledge_changesets")

	expectedColumns := []string{
		"id", "created_at", "target_urn", "change_type",
		"previous_value", "new_value", "source_insight_ids",
		"approved_by", "applied_by", "rolled_back",
		"rolled_back_by", "rolled_back_at",
	}
	for _, col := range expectedColumns {
		assert.Contains(t, migrationSQL, col,
			"up migration should contain column %s", col)
	}

	// Must create indexes.
	expectedIndexes := []string{
		"idx_knowledge_changesets_target_urn",
		"idx_knowledge_changesets_applied_by",
		"idx_knowledge_changesets_rolled_back",
		"idx_knowledge_changesets_created_at",
	}
	for _, idx := range expectedIndexes {
		assert.Contains(t, migrationSQL, idx,
			"up migration should contain index %s", idx)
	}

	// Must also add apply tracking columns to knowledge_insights.
	applyColumns := []string{
		"applied_by",
		"applied_at",
		"changeset_ref",
	}
	for _, col := range applyColumns {
		assert.Contains(t, migrationSQL, col,
			"up migration should add apply tracking column %s to knowledge_insights", col)
	}
}

func TestMigration008_DownContent(t *testing.T) {
	content, err := migrations.ReadFile("migrations/000008_knowledge_changesets.down.sql")
	require.NoError(t, err)
	migrationSQL := string(content)

	// Must drop apply tracking columns from knowledge_insights.
	droppedColumns := []string{
		"changeset_ref",
		"applied_at",
		"applied_by",
	}
	for _, col := range droppedColumns {
		assert.Contains(t, migrationSQL, "DROP COLUMN IF EXISTS "+col,
			"down migration should drop column %s", col)
	}

	// Must drop the knowledge_changesets table.
	assert.Contains(t, migrationSQL, "DROP TABLE") //nolint:revive // test assertion
	assert.Contains(t, migrationSQL, "knowledge_changesets")
}

// TestMigrationTablesHaveConsumers verifies that every table created by a
// migration is actually referenced (INSERT, SELECT, UPDATE, or DELETE) in
// non-test, non-migration Go source code. This prevents "vaporware" tables
// that exist in the database but are never used by the running application.
//
// If this test fails, one of two things is true:
//  1. A migration creates a table that no Go code uses — delete the migration.
//  2. Go code exists but isn't wired up — wire it into the platform or delete it.
func TestMigrationTablesHaveConsumers(t *testing.T) {
	// 1. Extract all table names from CREATE TABLE statements in up migrations.
	entries, err := migrations.ReadDir("migrations")
	require.NoError(t, err)

	createTableRe := regexp.MustCompile(`(?i)CREATE TABLE\s+(?:IF NOT EXISTS\s+)?(\w+)`)

	var tables []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		content, readErr := migrations.ReadFile("migrations/" + entry.Name())
		require.NoError(t, readErr)

		matches := createTableRe.FindAllStringSubmatch(string(content), -1)
		for _, m := range matches {
			table := m[1]
			// Skip partition definitions (e.g. "audit_logs_default PARTITION OF audit_logs")
			if strings.HasSuffix(table, "_default") {
				continue
			}
			tables = append(tables, table)
		}
	}
	require.NotEmpty(t, tables, "migrations should contain CREATE TABLE statements")

	// 2. Collect all non-test, non-migration Go source files under pkg/.
	pkgRoot := "../../.."
	var goFiles []string
	collectErr := collectGoSourceFiles(pkgRoot+"/pkg", &goFiles)
	require.NoError(t, collectErr, "failed to walk pkg/ directory")
	require.NotEmpty(t, goFiles, "should find Go source files under pkg/")

	// 3. Read all source files into a single corpus.
	var corpus strings.Builder
	for _, path := range goFiles {
		content, readErr := os.ReadFile(path) //nolint:gosec // test reads source files, not user input
		require.NoError(t, readErr)
		corpus.Write(content)  //nolint:revive // strings.Builder.Write never returns an error
		corpus.WriteByte('\n') //nolint:revive // strings.Builder.WriteByte never returns an error
	}
	source := corpus.String()

	// 4. For each table, verify at least one DML reference exists.
	dmlPatterns := []string{
		`INSERT INTO %s`,
		`FROM %s`,
		`UPDATE %s`,
		`DELETE FROM %s`,
	}

	for _, table := range tables {
		found := false
		for _, pattern := range dmlPatterns {
			if strings.Contains(source, strings.ReplaceAll(
				pattern, "%s", table,
			)) {
				found = true
				break
			}
		}
		assert.True(t, found,
			"table %q is created by a migration but no non-test Go code references it "+
				"(INSERT, SELECT, UPDATE, or DELETE). Either wire up the table or remove the migration.",
			table)
	}
}

// collectGoSourceFiles walks dir recursively and appends non-test, non-migration
// Go source file paths to dst.
func collectGoSourceFiles(dir string, dst *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		path := dir + "/" + entry.Name()
		if entry.IsDir() {
			if entry.Name() == "migrate" || entry.Name() == "vendor" {
				continue // skip migration SQL and vendor
			}
			if err := collectGoSourceFiles(path, dst); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			*dst = append(*dst, path)
		}
	}
	return nil
}

// TestMigration004_ColumnConsistency verifies that columns added by
// migration 004 appear in the store's INSERT and SELECT queries.
// This catches drift between DDL (migration) and DML (store.go).
func TestMigration004_ColumnConsistency(t *testing.T) {
	// Read the up migration to extract ADD COLUMN names.
	migrationContent, err := migrations.ReadFile("migrations/000004_audit_schema_improvements.up.sql")
	require.NoError(t, err)

	addColRe := regexp.MustCompile(`ADD COLUMN\s+(\w+)`)
	matches := addColRe.FindAllStringSubmatch(string(migrationContent), -1)
	require.NotEmpty(t, matches, "migration should contain ADD COLUMN statements")

	addedColumns := make([]string, 0, len(matches))
	for _, m := range matches {
		addedColumns = append(addedColumns, m[1])
	}

	// Read the store source to get INSERT and SELECT column lists.
	storeSource, err := os.ReadFile("../../audit/postgres/store.go")
	require.NoError(t, err)
	storeStr := string(storeSource)

	// Extract INSERT column list (between "INSERT INTO audit_logs" and "VALUES").
	insertRe := regexp.MustCompile(`INSERT INTO audit_logs\s*\(([^)]+)\)`)
	insertMatch := insertRe.FindStringSubmatch(storeStr)
	require.Len(t, insertMatch, 2, "store.go should contain INSERT INTO audit_logs(...)")
	insertCols := insertMatch[1]

	// Extract SELECT column list (between "SELECT" and "FROM audit_logs").
	selectRe := regexp.MustCompile(`SELECT\s+([\w\s,]+)\s+FROM audit_logs`)
	selectMatch := selectRe.FindStringSubmatch(storeStr)
	require.Len(t, selectMatch, 2, "store.go should contain SELECT ... FROM audit_logs")
	selectCols := selectMatch[1]

	// Verify each column added by migration 004 appears in both INSERT and SELECT.
	for _, col := range addedColumns {
		col = strings.TrimSpace(col)
		assert.Contains(t, insertCols, col,
			"column %q added by migration 004 must appear in store INSERT column list", col)
		// created_date is INSERT-only (derived from timestamp), not in SELECT.
		// All migration-added columns should be in SELECT.
		assert.Contains(t, selectCols, col,
			"column %q added by migration 004 must appear in store SELECT column list", col)
	}
}
