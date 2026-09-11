package subjects

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
func TestPostgresStore_RecordAndLookup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := NewPostgresStore(db)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO identity_subjects").WithArgs("jane@example.com", "sub-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Record(ctx, "jane@example.com", "sub-1"))

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
	require.Error(t, store.Record(ctx, "jane@example.com", "sub-1"))
	mock.ExpectQuery("SELECT subject FROM identity_subjects").WillReturnError(errors.New("connection reset"))
	_, err = store.Lookup(ctx, "jane@example.com")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_WithoutADatabaseKnowsNothing(t *testing.T) {
	ctx := context.Background()
	for _, store := range []*PostgresStore{nil, NewPostgresStore(nil)} {
		require.NoError(t, store.Record(ctx, "jane@example.com", "sub-1"))
		got, err := store.Lookup(ctx, "jane@example.com")
		require.NoError(t, err)
		require.Equal(t, "", got)
	}
}
