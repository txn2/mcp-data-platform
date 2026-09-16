package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// The store's two statements, over a mocked connection. The real database runs
// them in the RealDB test; this pins the branches: no db, a row, no row, and a
// database that will not answer.
func TestStore_RecordAndLookup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := New(db)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO identity_subjects").WithArgs("jane@example.com", "sub-1", true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Record(ctx, "jane@example.com", "sub-1", true))

	mock.ExpectQuery("SELECT subject FROM identity_subjects").WithArgs("jane@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"subject"}).AddRow("sub-1"))
	got, err := store.Lookup(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-1", got)

	mock.ExpectQuery("SELECT subject FROM identity_subjects").WithArgs("nobody@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"subject"}))
	got, err = store.Lookup(ctx, "nobody@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got, "an address never seen is unknown, not an error")

	mock.ExpectExec("INSERT INTO identity_subjects").WillReturnError(errors.New("connection reset"))
	require.Error(t, store.Record(ctx, "jane@example.com", "sub-1", true))
	mock.ExpectQuery("SELECT subject FROM identity_subjects").WillReturnError(errors.New("connection reset"))
	_, err = store.Lookup(ctx, "jane@example.com")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_WithoutADatabaseKnowsNothing(t *testing.T) {
	ctx := context.Background()
	for _, store := range []*Store{nil, New(nil)} {
		require.NoError(t, store.Record(ctx, "jane@example.com", "sub-1", true))
		got, err := store.Lookup(ctx, "jane@example.com")
		require.NoError(t, err)
		require.Equal(t, "", got)
		got, err = store.LookupPerson(ctx, "jane@example.com")
		require.NoError(t, err)
		require.Equal(t, "", got)
	}
}

// TestStore_LookupPerson is #1759: the resolver behind a key that
// authenticates AS somebody reads only a pair that person recorded themselves,
// so a key they hold can never decide who they are.
func TestStore_LookupPerson(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := New(db)
	ctx := context.Background()

	mock.ExpectQuery("SELECT subject FROM identity_subjects WHERE address = \\$1 AND from_person").
		WithArgs("jane@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"subject"}).AddRow("sub-1"))
	got, err := store.LookupPerson(ctx, "jane@example.com")
	require.NoError(t, err)
	require.Equal(t, "sub-1", got)

	// A row only a key wrote answers nothing here, while Lookup still sees it.
	mock.ExpectQuery("AND from_person").WithArgs("keyed@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"subject"}))
	got, err = store.LookupPerson(ctx, "keyed@example.com")
	require.NoError(t, err)
	require.Equal(t, "", got)

	mock.ExpectQuery("AND from_person").WillReturnError(errors.New("connection reset"))
	_, err = store.LookupPerson(ctx, "jane@example.com")
	require.Error(t, err, "a failed read must not be answered as nobody")

	require.NoError(t, mock.ExpectationsWereMet())
}
