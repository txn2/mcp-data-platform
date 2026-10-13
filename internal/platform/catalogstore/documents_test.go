package catalogstore

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var docRef = catalog.SchemaDocumentRef("api", "billing", "openapi.json")

func docInput(content string) catalog.DocumentInput {
	return catalog.DocumentInput{
		ConnectionKind: "api", Connection: "billing", Filename: "openapi.json",
		MIMEType: "application/json", Content: []byte(content),
	}
}

func TestPutDocumentWritesNewContent(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO catalog_documents").
		WithArgs("api", "billing", "openapi.json", "application/json", []byte(`{"openapi":"3.0.0"}`),
			sha(`{"openapi":"3.0.0"}`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "updated_at"}).AddRow("doc1", 2, now))

	doc, changed, err := s.PutDocument(context.Background(), docInput(`{"openapi":"3.0.0"}`))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "doc1", doc.ID)
	assert.Equal(t, 2, doc.Version)
	assert.Equal(t, docRef.String(), doc.Reference)
	assert.Equal(t, 19, doc.SizeBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutDocumentLeavesContentAlreadyHeld(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO catalog_documents").WillReturnRows(sqlmock.NewRows([]string{"id", "version", "updated_at"}))
	mock.ExpectQuery("octet_length\\(content\\)").WithArgs("api", "billing", "openapi.json").
		WillReturnRows(sqlmock.NewRows([]string{"id", "mime_type", "size", "version", "updated_at"}).
			AddRow("doc1", "application/json", 19, 3, now))

	doc, changed, err := s.PutDocument(context.Background(), docInput(`{"openapi":"3.0.0"}`))
	require.NoError(t, err)
	assert.False(t, changed, "the same content records no version")
	assert.Equal(t, 3, doc.Version)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutDocumentFailures(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)

	_, _, err := s.PutDocument(ctx, catalog.DocumentInput{ConnectionKind: "api", Filename: "x.json", MIMEType: "application/json"})
	require.ErrorIs(t, err, catalog.ErrInvalidRef, "a document needs its connection")
	in := docInput("x")
	in.MIMEType = ""
	_, _, err = s.PutDocument(ctx, in)
	require.Error(t, err)

	mock.ExpectQuery("INSERT INTO catalog_documents").WillReturnError(errDB)
	_, _, err = s.PutDocument(ctx, docInput("x"))
	require.ErrorIs(t, err, errDB)

	mock.ExpectQuery("INSERT INTO catalog_documents").WillReturnRows(sqlmock.NewRows([]string{"id", "version", "updated_at"}))
	mock.ExpectQuery("octet_length\\(content\\)").WillReturnError(errDB)
	_, _, err = s.PutDocument(ctx, docInput("x"))
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDocument(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectQuery("SELECT id, mime_type, content").WithArgs("api", "billing", "openapi.json").
		WillReturnRows(sqlmock.NewRows([]string{"id", "mime_type", "content", "version", "updated_at"}).
			AddRow("doc1", "application/json", []byte(`{}`), 1, now))
	doc, err := s.GetDocument(ctx, docRef)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{}`), doc.Content)
	assert.Equal(t, 2, doc.SizeBytes)
	assert.Equal(t, "openapi.json", doc.Filename)

	_, err = s.GetDocument(ctx, dsRef)
	require.ErrorIs(t, err, catalog.ErrInvalidRef)

	mock.ExpectQuery("SELECT id, mime_type, content").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = s.GetDocument(ctx, docRef)
	require.ErrorIs(t, err, catalog.ErrNotFound)

	mock.ExpectQuery("SELECT id, mime_type, content").WillReturnError(errDB)
	_, err = s.GetDocument(ctx, docRef)
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDocuments(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t)
	now := time.Now()
	cols := []string{"id", "connection_kind", "connection_name", "filename", "mime_type", "size", "version", "updated_at"}
	mock.ExpectQuery("ORDER BY connection_kind").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("doc1", "api", "billing", "openapi.json", "application/json", 19, 2, now).
		AddRow("doc2", "graphql", "crm", "schema.graphql", "application/graphql", 400, 1, now))
	docs, err := s.ListDocuments(ctx)
	require.NoError(t, err)
	require.Len(t, docs, 2)
	assert.Equal(t, docRef.String(), docs[0].Reference)
	assert.Nil(t, docs[0].Content, "a listing carries no content")

	mock.ExpectQuery("ORDER BY connection_kind").WillReturnRows(sqlmock.NewRows(cols))
	docs, err = s.ListDocuments(ctx)
	require.NoError(t, err)
	assert.NotNil(t, docs, "no documents is an empty list")

	mock.ExpectQuery("ORDER BY connection_kind").WillReturnError(errDB)
	_, err = s.ListDocuments(ctx)
	require.ErrorIs(t, err, errDB)

	mock.ExpectQuery("ORDER BY connection_kind").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.ListDocuments(ctx)
	require.Error(t, err)

	mock.ExpectQuery("ORDER BY connection_kind").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("doc1", "api", "billing", "openapi.json", "application/json", 19, 2, now).RowError(0, errDB))
	_, err = s.ListDocuments(ctx)
	require.ErrorIs(t, err, errDB)
	require.NoError(t, mock.ExpectationsWereMet())
}

// sha is the hex sha256 of content, which the store records to tell a
// changed document from one it already holds.
func sha(content string) driver.Value {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
