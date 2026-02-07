package migrate

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
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

func (m *mockMigrator) Up() error                      { return m.upErr }
func (m *mockMigrator) Down() error                    { return m.downErr }
func (m *mockMigrator) Steps(_ int) error              { return m.stepsErr }
func (m *mockMigrator) Version() (uint, bool, error)   { return m.versionVal, m.dirty, m.versionErr }

func TestMigrationsEmbedded(t *testing.T) {
	entries, err := migrations.ReadDir("migrations")
	assert.NoError(t, err)
	assert.NotEmpty(t, entries)
	assert.Len(t, entries, 6)

	expectedFiles := []string{
		"000001_oauth_clients.up.sql",
		"000001_oauth_clients.down.sql",
		"000002_audit_logs.up.sql",
		"000002_audit_logs.down.sql",
		"000003_response_size.up.sql",
		"000003_response_size.down.sql",
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

	t.Run("success", func(t *testing.T) {
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

	t.Run("factory error", func(t *testing.T) {
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

	t.Run("success", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return &mockMigrator{versionVal: 5, dirty: false}, nil
		}

		version, dirty, err := Version(nil)
		assert.NoError(t, err)
		assert.Equal(t, uint(5), version)
		assert.False(t, dirty)
	})

	t.Run("factory error", func(t *testing.T) {
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

	t.Run("success", func(t *testing.T) {
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

	t.Run("factory error", func(t *testing.T) {
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

	t.Run("success", func(t *testing.T) {
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

	t.Run("factory error", func(t *testing.T) {
		migratorFactory = func(_ *sql.DB) (migrator, error) {
			return nil, errors.New("factory failed")
		}

		err := Steps(nil, 1)
		assert.Error(t, err)
	})
}
