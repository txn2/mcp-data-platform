package migrate

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var renameCols = []string{"name", "domains", "development_taken"}

// TestPendingDevRenamesAt pins #2608: what 000239 is about to rename is read
// only at the versions it applies to, and below 000236, which gives every site
// its default environment, without requiring one.
func TestPendingDevRenamesAt(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	q := regexp.QuoteMeta

	for _, v := range []uint{0, 234, 239, 243} {
		got, err := pendingDevRenamesAt(ctx, db, v)
		require.NoError(t, err)
		assert.Nil(t, got, "version %d: no query", v)
	}

	mock.ExpectQuery(q("GROUP BY s.id, s.name")).WithArgs(true).
		WillReturnRows(sqlmock.NewRows(renameCols).AddRow("docs", "dev.example.com", false))
	got, err := pendingDevRenamesAt(ctx, db, 235)
	require.NoError(t, err)
	assert.Equal(t, []DevRename{{Site: "docs", Domains: "dev.example.com", NewName: "development"}}, got)

	mock.ExpectQuery(q("GROUP BY s.id, s.name")).WithArgs(false).
		WillReturnRows(sqlmock.NewRows(renameCols).AddRow("shop", "a.example.com, b.example.com", true))
	got, err = pendingDevRenamesAt(ctx, db, 238)
	require.NoError(t, err)
	assert.Equal(t, []DevRename{{Site: "shop", Domains: "a.example.com, b.example.com", NewName: "dev-previous"}}, got)

	boom := errors.New("down")
	mock.ExpectQuery(q("FROM sites s")).WillReturnError(boom)
	_, err = pendingDevRenamesAt(ctx, db, 238)
	require.ErrorIs(t, err, boom)

	mock.ExpectQuery(q("FROM sites s")).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("x"))
	_, err = pendingDevRenamesAt(ctx, db, 238)
	require.Error(t, err, "a row of the wrong shape")

	mock.ExpectQuery(q("FROM sites s")).WillReturnRows(sqlmock.NewRows(renameCols).AddRow("a", "b", false).RowError(0, boom))
	_, err = pendingDevRenamesAt(ctx, db, 238)
	require.ErrorIs(t, err, boom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A database whose version cannot be read (a new one), whose migration state
// is dirty, or which 000239 has run on has nothing to rename; one before it is
// read.
func TestPendingDevRenames_ReadsTheVersionFirst(t *testing.T) {
	old := migratorFactory
	t.Cleanup(func() { migratorFactory = old })
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for name, m := range map[string]*mockMigrator{
		"unreadable": {versionErr: errors.New("no version")},
		"dirty":      {versionVal: 237, dirty: true},
		"past 239":   {versionVal: 243},
	} {
		migratorFactory = func(*sql.DB) (migrator, error) { return m, nil }
		got, err := PendingDevRenames(ctx, db)
		require.NoError(t, err, name)
		assert.Nil(t, got, name)
	}

	migratorFactory = func(*sql.DB) (migrator, error) { return &mockMigrator{versionVal: 238}, nil }
	mock.ExpectQuery(regexp.QuoteMeta("FROM sites s")).WithArgs(false).WillReturnRows(sqlmock.NewRows(renameCols))
	got, err := PendingDevRenames(ctx, db)
	require.NoError(t, err)
	assert.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}
