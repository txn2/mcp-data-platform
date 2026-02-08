package migrate

import (
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	migrateTestFileCount    = 8
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
