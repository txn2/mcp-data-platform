package scriptpending

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresKeepsAndReturnsACreate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := NewPostgres(db)
	category := "reports"

	mock.ExpectExec("INSERT INTO script_pending_creates").
		WithArgs("jane@example.com", "flow", "def main():\n    pass\n", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Put(context.Background(), "jane@example.com", Create{
		Name: "flow", DisplayName: "Flow", Category: &category, Tags: []string{"a"}, Source: "def main():\n    pass\n",
	}))

	now := time.Now()
	mock.ExpectQuery("SELECT source, fields, updated_at FROM script_pending_creates").
		WithArgs("jane@example.com", "flow").
		WillReturnRows(sqlmock.NewRows([]string{"source", "fields", "updated_at"}).
			AddRow("src", []byte(`{"name":"flow","display_name":"Flow","category":"reports","tags":["a"]}`), now))
	got, err := store.Get(context.Background(), "jane@example.com", "flow")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "src", got.Source)
	assert.Equal(t, "Flow", got.DisplayName)
	assert.Equal(t, "reports", *got.Category)
	fields := got.Fields()
	assert.Equal(t, true, fields["pending"])
	assert.Equal(t, "src", fields["source"])

	mock.ExpectQuery("SELECT source").WillReturnRows(sqlmock.NewRows([]string{"source", "fields", "updated_at"}))
	none, err := store.Get(context.Background(), "jane@example.com", "other")
	require.NoError(t, err)
	assert.Nil(t, none)

	mock.ExpectExec("DELETE FROM script_pending_creates").WithArgs("jane@example.com", "flow").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Delete(context.Background(), "jane@example.com", "flow"))
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Contains(t, Notice("flow"), "command=patch name=flow")
}

func TestPostgresReportsFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := NewPostgres(db)
	boom := errors.New("db down")
	mock.ExpectExec("INSERT").WillReturnError(boom)
	assert.ErrorIs(t, store.Put(context.Background(), "o", Create{Name: "n"}), boom)
	mock.ExpectQuery("SELECT").WillReturnError(boom)
	_, err = store.Get(context.Background(), "o", "n")
	assert.ErrorIs(t, err, boom)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"source", "fields", "updated_at"}).
		AddRow("s", []byte(`not json`), time.Now()))
	_, err = store.Get(context.Background(), "o", "n")
	assert.ErrorContains(t, err, "decoding")
	mock.ExpectExec("DELETE").WillReturnError(boom)
	assert.ErrorIs(t, store.Delete(context.Background(), "o", "n"), boom)
	assert.Nil(t, NewPostgres(nil))
}
