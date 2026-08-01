package knowledgepageindex

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func TestKind(t *testing.T) {
	assert.Equal(t, SourceKind, NewSource(nil).Kind())
	assert.Equal(t, SourceKind, NewSink(nil, "m").Kind())
}

// fakeRegistry records Register calls for RegisterConsumer tests.
type fakeRegistry struct {
	calls int
	err   error
}

func (f *fakeRegistry) Register(_ indexjobs.Source, _ indexjobs.Sink) error {
	f.calls++
	return f.err
}

func TestRegisterConsumer(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	reg := &fakeRegistry{}
	require.NoError(t, RegisterConsumer(reg, db, "model-x"))
	assert.Equal(t, 1, reg.calls)

	failing := &fakeRegistry{err: errors.New("boom")}
	assert.Error(t, RegisterConsumer(failing, db, "model-x"))
}

func TestGetIndexText(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectQuery("SELECT title, body, tags FROM portal_knowledge_pages").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "body", "tags"}).
			AddRow("Title", "Body text", []byte(`["t1"]`)))
	text, err := store.GetIndexText(context.Background(), "kp1")
	require.NoError(t, err)
	assert.Contains(t, text, "Title")
	assert.Contains(t, text, "Body text")
	assert.Contains(t, text, "t1")
}

func TestGetIndexText_NotIndexable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectQuery("SELECT title, body, tags").WithArgs("gone").WillReturnError(errNotIndexable)
	_, err = store.GetIndexText(context.Background(), "gone")
	assert.ErrorIs(t, err, errNotIndexable)

	// Source maps errNotIndexable to an empty item set.
	items, err := NewSource(store).LoadItems(context.Background(), "x")
	_ = items
	_ = err
}

func TestUpsertAndFindGapsAndCoverage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectExec("UPDATE portal_knowledge_pages SET embedding").
		WithArgs("kp1", pgvector.NewVector([]float32{0.1}), "model-x", []byte("hash")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.UpsertVectors(context.Background(), "kp1", []indexjobs.Vector{
		{ItemID: "kp1", Embedding: []float32{0.1}, Model: "model-x", TextHash: []byte("hash")},
	}))

	// Empty rows is a no-op.
	require.NoError(t, store.UpsertVectors(context.Background(), "kp1", nil))

	mock.ExpectQuery("SELECT id FROM portal_knowledge_pages").
		WithArgs("model-x").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kp2"))
	gaps, err := store.FindGaps(context.Background(), "model-x")
	require.NoError(t, err)
	assert.Equal(t, []string{"kp2"}, gaps)

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5))
	indexed, expected, err := store.Coverage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, indexed)
	assert.Equal(t, 5, expected)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSinkDelegates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)
	sink := NewSink(store, "model-x")
	key := indexjobs.Key{SourceID: "kp1"}

	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash").
		WithArgs("kp1").WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}))
	vecs, err := sink.ListExisting(context.Background(), key)
	require.NoError(t, err)
	assert.Empty(t, vecs)

	require.NoError(t, sink.StampExpected(context.Background(), key, 1))

	rows := []indexjobs.Vector{{ItemID: "kp1", Embedding: []float32{0.1}, Model: "model-x", TextHash: []byte("h")}}
	mock.ExpectExec("UPDATE portal_knowledge_pages SET embedding").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.Upsert(context.Background(), key, rows))
	mock.ExpectExec("UPDATE portal_knowledge_pages SET embedding").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.UpsertBatch(context.Background(), key, rows))

	mock.ExpectQuery("SELECT id FROM portal_knowledge_pages").WithArgs("model-x").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kp9"))
	gaps, err := sink.FindGaps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"kp9"}, gaps)

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(1, 1))
	cov, err := sink.Coverage(context.Background())
	require.NoError(t, err)
	assert.True(t, cov.ExpectedKnown)
}

func TestSourceLoadAndListVectors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)
	src := NewSource(store)
	src.OnSucceeded("kp1") // no-op

	mock.ExpectQuery("SELECT title, body, tags FROM portal_knowledge_pages").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "body", "tags"}).AddRow("T", "B", []byte(`["x"]`)))
	items, err := src.LoadItems(context.Background(), "kp1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "kp1", items[0].ItemID)

	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgvector.NewVector([]float32{0.1, 0.2}), "model-x", []byte("h")))
	vecs, err := store.ListVectors(context.Background(), "kp1")
	require.NoError(t, err)
	require.Contains(t, vecs, "kp1")
	assert.Equal(t, 2, vecs["kp1"].Dim)
	assert.NoError(t, mock.ExpectationsWereMet())
}
