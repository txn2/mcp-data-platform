package postgres

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

var collectionColumns = []string{
	"id", "name", "description", "created_by", "count", "created_at", "updated_at",
}

func collectionRow(id, name string) []driver.Value {
	return []driver.Value{id, name, "desc", "jane@example.com", 2, testRowTime, testRowTime}
}

func newCollectionMock(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return New(db), mock, func() { _ = db.Close() }
}

func TestCreateCollection(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("INSERT INTO prompt_collections").
		WithArgs("Sales", "Sales SOPs", "jane@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("col-1", testRowTime, testRowTime))

	c := &prompt.Collection{Name: "Sales", Description: "Sales SOPs", CreatedBy: "jane@example.com"}
	require.NoError(t, store.CreateCollection(context.Background(), c))
	assert.Equal(t, "col-1", c.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateCollection_NameCollision(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("INSERT INTO prompt_collections").
		WillReturnError(&pq.Error{Code: pqUniqueViolation})

	err := store.CreateCollection(context.Background(), &prompt.Collection{Name: "Sales"})
	assert.ErrorIs(t, err, prompt.ErrCollectionExists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCollection(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("SELECT .+ FROM prompt_collections").WithArgs("col-1").
		WillReturnRows(sqlmock.NewRows(collectionColumns).AddRow(collectionRow("col-1", "Sales")...))

	c, err := store.GetCollection(context.Background(), "col-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, "Sales", c.Name)
	assert.Equal(t, 2, c.PromptCount)

	// Not found and a malformed id both map to nil, nil.
	mock.ExpectQuery("SELECT .+ FROM prompt_collections").WithArgs("missing").
		WillReturnRows(sqlmock.NewRows(collectionColumns))
	c, err = store.GetCollection(context.Background(), "missing")
	assert.NoError(t, err)
	assert.Nil(t, c)

	mock.ExpectQuery("SELECT .+ FROM prompt_collections").WithArgs("not-a-uuid").
		WillReturnError(&pq.Error{Code: pqInvalidTextRepresentation})
	c, err = store.GetCollection(context.Background(), "not-a-uuid")
	assert.NoError(t, err)
	assert.Nil(t, c)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCollections(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("SELECT .+ FROM prompt_collections").
		WillReturnRows(sqlmock.NewRows(collectionColumns).
			AddRow(collectionRow("col-1", "Marketing")...).
			AddRow(collectionRow("col-2", "Sales")...))

	cols, err := store.ListCollections(context.Background())
	require.NoError(t, err)
	require.Len(t, cols, 2)
	assert.Equal(t, "Marketing", cols[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCollections_QueryError(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("SELECT .+ FROM prompt_collections").WillReturnError(assert.AnError)
	_, err := store.ListCollections(context.Background())
	assert.ErrorContains(t, err, "list prompt collections")

	// A malformed row surfaces as a scan error.
	mock.ExpectQuery("SELECT .+ FROM prompt_collections").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("col-1"))
	_, err = store.ListCollections(context.Background())
	assert.ErrorContains(t, err, "scan prompt collection")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteCollection_ExecError(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectExec("DELETE FROM prompt_collections").WillReturnError(assert.AnError)
	assert.ErrorContains(t, store.DeleteCollection(context.Background(), "col-1"), "delete prompt collection")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListCollections_Empty(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("SELECT .+ FROM prompt_collections").
		WillReturnRows(sqlmock.NewRows(collectionColumns))

	cols, err := store.ListCollections(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, cols, "empty list is non-nil for stable JSON")
	assert.Empty(t, cols)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateCollection(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectExec("UPDATE prompt_collections").
		WithArgs("col-1", "Sales Ops", "renamed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.UpdateCollection(context.Background(), "col-1", "Sales Ops", "renamed"))

	// A rename onto an existing name is the sentinel; a missing row errors.
	mock.ExpectExec("UPDATE prompt_collections").
		WillReturnError(&pq.Error{Code: pqUniqueViolation})
	assert.ErrorIs(t, store.UpdateCollection(context.Background(), "col-1", "Taken", ""), prompt.ErrCollectionExists)

	mock.ExpectExec("UPDATE prompt_collections").
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorContains(t, store.UpdateCollection(context.Background(), "missing", "X", ""), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteCollection(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectExec("DELETE FROM prompt_collections").WithArgs("col-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	assert.NoError(t, store.DeleteCollection(context.Background(), "col-1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSetPromptCollection(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectExec("UPDATE prompts SET collection_id").
		WithArgs("uuid-123", "col-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.SetPromptCollection(context.Background(), "uuid-123", "col-1"))

	// Clearing binds NULL.
	mock.ExpectExec("UPDATE prompts SET collection_id").
		WithArgs("uuid-123", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.SetPromptCollection(context.Background(), "uuid-123", ""))

	// A dangling collection FK and a malformed collection id both map to the
	// not-found sentinel; a missing prompt errors.
	mock.ExpectExec("UPDATE prompts SET collection_id").
		WillReturnError(&pq.Error{Code: pqForeignKeyViolation})
	assert.ErrorIs(t, store.SetPromptCollection(context.Background(), "uuid-123", "gone"), prompt.ErrCollectionNotFound)

	mock.ExpectExec("UPDATE prompts SET collection_id").
		WillReturnError(&pq.Error{Code: pqInvalidTextRepresentation})
	assert.ErrorIs(t, store.SetPromptCollection(context.Background(), "uuid-123", "not-a-uuid"), prompt.ErrCollectionNotFound)

	mock.ExpectExec("UPDATE prompts SET collection_id").
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorContains(t, store.SetPromptCollection(context.Background(), "missing", "col-1"), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryOne_MalformedIDIsNotFound(t *testing.T) {
	store, mock, done := newCollectionMock(t)
	defer done()

	mock.ExpectQuery("SELECT .+ FROM prompts WHERE id").WithArgs("not-a-uuid").
		WillReturnError(&pq.Error{Code: pqInvalidTextRepresentation})
	p, err := store.GetByID(context.Background(), "not-a-uuid")
	assert.NoError(t, err, "a malformed id names no row: not-found, not a 500")
	assert.Nil(t, p)
	assert.NoError(t, mock.ExpectationsWereMet())
}
