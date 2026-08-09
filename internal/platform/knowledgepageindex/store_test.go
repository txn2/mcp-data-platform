package knowledgepageindex

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func TestKind(t *testing.T) {
	assert.Equal(t, SourceKind, NewSource(nil, 0).Kind())
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
	require.NoError(t, RegisterConsumer(reg, db, "model-x", 6000))
	assert.Equal(t, 1, reg.calls)

	failing := &fakeRegistry{err: errors.New("boom")}
	assert.Error(t, RegisterConsumer(failing, db, "model-x", 6000))
}

// TestChunkIndexRejectsForeignItemIDs proves a vector row that is not a chunk of
// the page being written is refused rather than collapsed onto chunk 0, which
// would silently overwrite the page's first chunk with another unit's vector.
func TestChunkIndexRejectsForeignItemIDs(t *testing.T) {
	got, err := chunkIndex("kp1", itemID("kp1", 7))
	require.NoError(t, err)
	assert.Equal(t, 7, got)

	for _, id := range []string{"kp1", "kp2:0", "kp1:", "kp1:x", "kp1:-1"} {
		_, err := chunkIndex("kp1", id)
		assert.Error(t, err, "item id %q must be rejected", id)
	}
}

func TestGetContent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectQuery("SELECT title, body, tags FROM portal_knowledge_pages").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "body", "tags"}).
			AddRow("Title", "Body text", []byte(`["t1"]`)))
	content, err := store.GetContent(context.Background(), "kp1")
	require.NoError(t, err)
	assert.Equal(t, Content{Title: "Title", Body: "Body text", Tags: []string{"t1"}}, content)
}

func TestGetContent_NotIndexable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectQuery("SELECT title, body, tags").WithArgs("gone").
		WillReturnError(errNotIndexable)
	_, err = store.GetContent(context.Background(), "gone")
	assert.ErrorIs(t, err, errNotIndexable)
}

// TestSourceLoadItems_SplitsOversizedPage proves the unit yields one item per
// chunk for a page past the provider budget, and that every item's text stays
// within that budget — the property that keeps the provider from trimming a
// page's tail away before it is embedded (#1242).
func TestSourceLoadItems_SplitsOversizedPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	const budget = 900
	body := strings.Repeat("## Section\n\nOperational detail about the pipeline.\n\n", 60)
	mock.ExpectQuery("SELECT title, body, tags FROM portal_knowledge_pages").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "body", "tags"}).
			AddRow("Runbook", body, []byte(`["ops"]`)))

	items, err := NewSource(NewStore(db), budget).LoadItems(context.Background(), "kp1")
	require.NoError(t, err)
	require.Greater(t, len(items), 1, "a page well past the budget must yield several chunks")
	for i, item := range items {
		assert.Equal(t, itemID("kp1", i), item.ItemID)
		assert.LessOrEqual(t, len(item.Text), budget)
		assert.Contains(t, item.Text, "Runbook", "every chunk carries the page identity")
	}
}

// TestSourceLoadItems_SmallPageIsOneItem pins the unchanged common case: a page
// inside the budget is still exactly one vector.
func TestSourceLoadItems_SmallPageIsOneItem(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectQuery("SELECT title, body, tags FROM portal_knowledge_pages").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "body", "tags"}).
			AddRow("T", "B", []byte(`["x"]`)))
	src := NewSource(NewStore(db), 6000)
	src.OnSucceeded("kp1") // no-op

	items, err := src.LoadItems(context.Background(), "kp1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, itemID("kp1", 0), items[0].ItemID)
}

func TestListVectors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectQuery("SELECT chunk_index, text_hash, embedding, model").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "text_hash", "embedding", "model"}).
			AddRow(0, []byte("h0"), pgvector.NewVector([]float32{0.1, 0.2}), "model-x").
			AddRow(1, []byte("h1"), pgvector.NewVector([]float32{0.3, 0.4}), "model-x"))

	vecs, err := store.ListVectors(context.Background(), "kp1")
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	require.Contains(t, vecs, itemID("kp1", 1))
	assert.Equal(t, 2, vecs[itemID("kp1", 1)].Dim)
	assert.Equal(t, []byte("h1"), vecs[itemID("kp1", 1)].TextHash)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplaceVectors_PrunesChunksOutsideTheSet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").
		WithArgs("kp1", 0, []byte("h0"), pgvector.NewVector([]float32{0.1}), "model-x", 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").
		WithArgs("kp1", 0).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, store.ReplaceVectors(context.Background(), "kp1", []indexjobs.Vector{
		{ItemID: itemID("kp1", 0), Embedding: []float32{0.1}, Model: "model-x", TextHash: []byte("h0")},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplaceVectors_EmptySetClearsThePage covers the source-gone path: the
// worker replaces a deleted unit's vectors with nothing, which must remove every
// chunk rather than leave the page ranking on text that no longer exists.
func TestReplaceVectors_EmptySetClearsThePage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").
		WithArgs("kp1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, store.ReplaceVectors(context.Background(), "kp1", nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertVectors_LeavesOtherChunksAlone(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").
		WithArgs("kp1", 2, []byte("h2"), pgvector.NewVector([]float32{0.5}), "model-x", 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.UpsertVectors(context.Background(), "kp1", []indexjobs.Vector{
		{ItemID: itemID("kp1", 2), Embedding: []float32{0.5}, Model: "model-x", TextHash: []byte("h2")},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindGapsAndCoverage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewStore(db)

	mock.ExpectQuery("SELECT id FROM portal_knowledge_pages").
		WithArgs("model-x").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kp2"))
	gaps, err := store.FindGaps(context.Background(), "model-x")
	require.NoError(t, err)
	assert.Equal(t, []string{"kp2"}, gaps)

	mock.ExpectQuery("SELECT COUNT").WithArgs("model-x").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5))
	indexed, expected, err := store.Coverage(context.Background(), "model-x")
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

	mock.ExpectQuery("SELECT chunk_index, text_hash, embedding, model").
		WithArgs("kp1").
		WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "text_hash", "embedding", "model"}))
	vecs, err := sink.ListExisting(context.Background(), key)
	require.NoError(t, err)
	assert.Empty(t, vecs)

	// The convergence marker is the whole point of StampExpected for this kind:
	// it stamps the page with the model its chunk set was produced by.
	mock.ExpectExec("UPDATE portal_knowledge_pages SET embedding_model").
		WithArgs("kp1", "model-x").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.StampExpected(context.Background(), key, 2))

	rows := []indexjobs.Vector{
		{ItemID: itemID("kp1", 0), Embedding: []float32{0.1}, Model: "model-x", TextHash: []byte("h")},
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	require.NoError(t, sink.Upsert(context.Background(), key, rows))

	mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, sink.UpsertBatch(context.Background(), key, rows))

	mock.ExpectQuery("SELECT id FROM portal_knowledge_pages").WithArgs("model-x").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kp9"))
	gaps, err := sink.FindGaps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"kp9"}, gaps)

	mock.ExpectQuery("SELECT COUNT").WithArgs("model-x").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(1, 1))
	cov, err := sink.Coverage(context.Background())
	require.NoError(t, err)
	assert.True(t, cov.ExpectedKnown)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStoreErrorPaths covers the failure branches every store method must
// surface rather than swallow: a swallowed error here would let the worker
// record a converged page that has no usable vectors.
func TestStoreErrorPaths(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")

	t.Run("get content", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT title, body, tags").WillReturnError(boom)
		_, err := store.GetContent(ctx, "kp1")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("get content bad tags", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT title, body, tags").
			WillReturnRows(sqlmock.NewRows([]string{"title", "body", "tags"}).
				AddRow("T", "B", []byte(`{not json`)))
		_, err := store.GetContent(ctx, "kp1")
		assert.Error(t, err)
	})

	t.Run("list vectors query", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT chunk_index").WillReturnError(boom)
		_, err := store.ListVectors(ctx, "kp1")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("list vectors scan", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT chunk_index").
			WillReturnRows(sqlmock.NewRows([]string{"chunk_index", "text_hash", "embedding", "model"}).
				AddRow("not an int", []byte("h"), pgvector.NewVector([]float32{0.1}), "m"))
		_, err := store.ListVectors(ctx, "kp1")
		assert.Error(t, err)
	})

	t.Run("replace begin", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin().WillReturnError(boom)
		assert.ErrorIs(t, store.ReplaceVectors(ctx, "kp1", nil), boom)
	})

	t.Run("replace rejects a foreign item id", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin()
		mock.ExpectRollback()
		err := store.ReplaceVectors(ctx, "kp1", []indexjobs.Vector{{ItemID: "kp2:0", Embedding: []float32{0.1}}})
		assert.Error(t, err)
	})

	t.Run("replace upsert", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").WillReturnError(boom)
		mock.ExpectRollback()
		err := store.ReplaceVectors(ctx, "kp1", []indexjobs.Vector{
			{ItemID: itemID("kp1", 0), Embedding: []float32{0.1}},
		})
		assert.ErrorIs(t, err, boom)
	})

	t.Run("replace prune", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").WillReturnError(boom)
		mock.ExpectRollback()
		err := store.ReplaceVectors(ctx, "kp1", []indexjobs.Vector{
			{ItemID: itemID("kp1", 0), Embedding: []float32{0.1}},
		})
		assert.ErrorIs(t, err, boom)
	})

	t.Run("replace delete-all", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").WillReturnError(boom)
		mock.ExpectRollback()
		assert.ErrorIs(t, store.ReplaceVectors(ctx, "kp1", nil), boom)
	})

	t.Run("replace commit", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM portal_knowledge_page_embedding_chunks").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit().WillReturnError(boom)
		assert.ErrorIs(t, store.ReplaceVectors(ctx, "kp1", nil), boom)
	})

	t.Run("upsert batch", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectExec("INSERT INTO portal_knowledge_page_embedding_chunks").WillReturnError(boom)
		err := store.UpsertVectors(ctx, "kp1", []indexjobs.Vector{
			{ItemID: itemID("kp1", 0), Embedding: []float32{0.1}},
		})
		assert.ErrorIs(t, err, boom)
		assert.Error(t, store.UpsertVectors(ctx, "kp1", []indexjobs.Vector{{ItemID: "other:0"}}))
	})

	t.Run("stamp model", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectExec("UPDATE portal_knowledge_pages SET embedding_model").WillReturnError(boom)
		assert.ErrorIs(t, store.StampModel(ctx, "kp1", "m"), boom)
	})

	t.Run("find gaps", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT id FROM portal_knowledge_pages").WillReturnError(boom)
		_, err := store.FindGaps(ctx, "m")
		assert.ErrorIs(t, err, boom)

		mock.ExpectQuery("SELECT id FROM portal_knowledge_pages").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
		_, err = store.FindGaps(ctx, "m")
		assert.Error(t, err)
	})

	t.Run("coverage", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT COUNT").WillReturnError(boom)
		_, _, err := store.Coverage(ctx, "m")
		assert.ErrorIs(t, err, boom)

		mock.ExpectQuery("SELECT COUNT").WillReturnError(boom)
		_, err = NewSink(store, "m").Coverage(ctx)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("source load items", func(t *testing.T) {
		db, mock, store := newMockStore(t)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT title, body, tags").WillReturnError(boom)
		_, err := NewSource(store, 6000).LoadItems(ctx, "kp1")
		assert.ErrorIs(t, err, boom)
	})
}

func newMockStore(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *Store) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return db, mock, NewStore(db)
}
