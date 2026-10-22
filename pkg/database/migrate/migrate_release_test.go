package migrate

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryCallReleasesItsMigrator pins #2249: each wrapper closes the
// migrator it built, whether the migration succeeded or not, so the
// connection it checked out goes back to the caller's pool.
func TestEveryCallReleasesItsMigrator(t *testing.T) {
	origFactory := migratorFactory
	defer func() { migratorFactory = origFactory }()

	failing := errors.New("failed")
	calls := map[string]func(db *sql.DB) error{
		"Run":     Run,
		"Down":    Down,
		"Steps":   func(db *sql.DB) error { return Steps(db, -1) },
		"Version": func(db *sql.DB) error { _, _, err := Version(db); return err },
	}
	for name, call := range calls {
		for _, mock := range []*mockMigrator{{}, {upErr: failing, downErr: failing, stepsErr: failing, versionErr: failing}} {
			migratorFactory = func(_ *sql.DB) (migrator, error) { return mock, nil }
			_ = call(nil)
			assert.Equal(t, 1, mock.closed, "%s closed its migrator %d times", name, mock.closed)
		}
	}
}

// A migrator that cannot be built gives back the connection it checked out:
// one whose pool is closed fails before checking one out, and one whose
// driver cannot read the database closes the connection it took.
func TestNewMigratorFailures(t *testing.T) {
	closed, closedMock, err := sqlmock.New()
	require.NoError(t, err)
	closedMock.ExpectClose()
	require.NoError(t, closed.Close())
	_, err = newMigrator(closed)
	assert.ErrorContains(t, err, "creating postgres driver")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(".*").WillReturnError(errors.New("no database"))
	_, err = newMigrator(db)
	assert.ErrorContains(t, err, "creating postgres driver")
	assert.Equal(t, 0, db.Stats().InUse, "the connection went back to the pool")
}

// A migrator whose close fails is logged and not reported: the migration's
// own outcome is what the caller acts on.
func TestReleaseLogsACloseFailure(t *testing.T) {
	m := &mockMigrator{closeSrcErr: errors.New("source"), closeDBErr: errors.New("database")}
	release(m)
	assert.Equal(t, 1, m.closed)
}

// A migrator built over a database the driver can read holds one connection
// until it is released, and release gives it back (#2249).
func TestNewMigratorHoldsOneConnectionUntilReleased(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT CURRENT_DATABASE").WillReturnRows(sqlmock.NewRows([]string{"d"}).AddRow("platform"))
	mock.ExpectQuery("SELECT CURRENT_SCHEMA").WillReturnRows(sqlmock.NewRows([]string{"s"}).AddRow("public"))
	mock.ExpectExec("SELECT pg_advisory_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))

	m, err := newMigrator(db)
	require.NoError(t, err)
	assert.Equal(t, 1, db.Stats().InUse, "the migrator holds its connection")
	release(m)
	assert.Equal(t, 0, db.Stats().InUse, "release gives it back")
}
