package memoryindex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func TestSource_KindAndLoadItems(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	src := NewSource(st)

	if src.Kind() != SourceKind {
		t.Errorf("Kind() = %q; want %q", src.Kind(), SourceKind)
	}

	mock.ExpectQuery("SELECT content FROM memory_records").WithArgs("mem-1").
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("orders_fact is partitioned by day"))
	items, err := src.LoadItems(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 1 || items[0].ItemID != "mem-1" || items[0].Text != "orders_fact is partitioned by day" {
		t.Errorf("items = %+v", items)
	}

	// OnSucceeded is a no-op; it must not panic.
	src.OnSucceeded("mem-1")
}

func TestSource_LoadItems_ArchivedYieldsEmpty(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT content FROM memory_records").WithArgs("gone").
		WillReturnError(sql.ErrNoRows)
	items, err := NewSource(st).LoadItems(context.Background(), "gone")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if items != nil {
		t.Errorf("archived/missing record must yield no items, got %+v", items)
	}
}

func TestSource_LoadItems_DBError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT content FROM memory_records").WillReturnError(errors.New("boom"))
	if _, err := NewSource(st).LoadItems(context.Background(), "x"); err == nil {
		t.Error("LoadItems should surface a non-missing store error")
	}
}

func TestSink_KindAndCoverage(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	sink := NewSink(st, "nomic-embed-text", nil)

	if sink.Kind() != SourceKind {
		t.Errorf("Kind() = %q; want %q", sink.Kind(), SourceKind)
	}

	mock.ExpectQuery("FROM memory_records").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(3, 5))
	cov, err := sink.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if cov.Indexed != 3 || cov.Expected != 5 || !cov.ExpectedKnown {
		t.Errorf("coverage = %+v; want {3 5 true}", cov)
	}
}

func TestSink_CoverageError(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("FROM memory_records").WillReturnError(errors.New("boom"))
	if _, err := NewSink(st, "m", nil).Coverage(context.Background()); err == nil {
		t.Error("Sink.Coverage should surface store error")
	}
}

func TestSink_StampExpectedAndFindGaps(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	sink := NewSink(st, "nomic-embed-text", nil)
	ctx := context.Background()

	// StampExpected is a no-op (no DB).
	if err := sink.StampExpected(ctx, indexjobs.Key{}, 7); err != nil {
		t.Fatalf("StampExpected: %v", err)
	}

	mock.ExpectQuery("FROM memory_records").WithArgs("nomic-embed-text").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("mem-null"))
	gaps, err := sink.FindGaps(ctx)
	if err != nil {
		t.Fatalf("FindGaps: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != "mem-null" {
		t.Errorf("gaps = %v", gaps)
	}
}

// TestConsumerRoundTrip is the integration test for the memory consumer
// contract: it drives the real Source and Sink end-to-end the way the
// indexjobs worker does (LoadItems -> dedup read -> embed -> Upsert ->
// ListExisting) and asserts the record's vector round-trips with the
// current provider model stamped. It proves the two halves of the
// consumer actually compose (the worker queue itself is covered by
// pkg/indexjobs).
func TestConsumerRoundTrip(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()

	const id = "mem-42"
	const content = "the orders_fact table is partitioned by event_day"
	const model = "nomic-embed-text"

	src := NewSource(st)
	sink := NewSink(st, model, nil)
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: id}

	// 1. Worker loads the unit's items.
	mock.ExpectQuery("SELECT content FROM memory_records").WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow(content))
	items, err := src.LoadItems(ctx, id)
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}

	// 2. Worker reads existing vectors for dedup: none yet (NULL embedding).
	mock.ExpectQuery("FROM memory_records").WithArgs(id).WillReturnError(sql.ErrNoRows)
	existing, err := sink.ListExisting(ctx, key)
	if err != nil {
		t.Fatalf("ListExisting: %v", err)
	}
	if len(existing) != 0 {
		t.Fatalf("want no existing vectors, got %d", len(existing))
	}

	// 3. Embed the item (stand-in for the worker's embedding provider) and
	//    build the Vector the framework would, including the model + hash.
	sum := sha256.Sum256([]byte(items[0].Text))
	vector := indexjobs.Vector{
		ItemID:    items[0].ItemID,
		TextHash:  sum[:],
		Embedding: []float32{0.11, 0.22, 0.33},
		Model:     model,
		Dim:       3,
	}

	// 4. Worker upserts the computed vector back onto the record.
	mock.ExpectExec("UPDATE memory_records").
		WithArgs(id, sqlmock.AnyArg(), model, sum[:]).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := sink.Upsert(ctx, key, []indexjobs.Vector{vector}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// 5. A subsequent ListExisting now returns the vector with the model
	//    stamped, so a later reconcile dedups instead of re-embedding.
	mock.ExpectQuery("FROM memory_records").WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow(pgVecLiteral(vector.Embedding), model, sum[:]))
	after, err := sink.ListExisting(ctx, key)
	if err != nil {
		t.Fatalf("ListExisting (after): %v", err)
	}
	got, ok := after[id]
	if !ok {
		t.Fatalf("expected vector for %s after upsert, got %+v", id, after)
	}
	if got.Model != model || got.Dim != 3 {
		t.Errorf("round-tripped vector = %+v; want model %q dim 3", got, model)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestSink_TellsOnEmbeddedAfterTheWrite pins the order the recall-first check
// depends on (#1987): the hook hears the record and the vector only once the
// vector is on the row, and not at all when the write fails or writes nothing.
func TestSink_TellsOnEmbeddedAfterTheWrite(t *testing.T) {
	t.Parallel()
	st, mock, done := newMockStore(t)
	defer done()
	ctx := context.Background()

	var heard []string
	var vec []float32
	sink := NewSink(st, "m", func(_ context.Context, id string, emb []float32) {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("hook ran before the vector was written: %v", err)
		}
		heard = append(heard, id)
		vec = emb
	})
	row := indexjobs.Vector{ItemID: "mem-1", Embedding: []float32{1, 0}, Model: "m", TextHash: []byte{1}}

	mock.ExpectExec("UPDATE memory_records").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := sink.Upsert(ctx, indexjobs.Key{SourceKind: SourceKind, SourceID: "mem-1"}, []indexjobs.Vector{row}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	mock.ExpectExec("UPDATE memory_records").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := sink.UpsertBatch(ctx, indexjobs.Key{SourceKind: SourceKind, SourceID: "mem-2"}, []indexjobs.Vector{row}); err != nil {
		t.Fatalf("UpsertBatch: %v", err)
	}
	mock.ExpectExec("UPDATE memory_records").WillReturnError(errors.New("boom"))
	if err := sink.Upsert(ctx, indexjobs.Key{SourceKind: SourceKind, SourceID: "mem-3"}, []indexjobs.Vector{row}); err == nil {
		t.Fatal("a failed write must surface")
	}
	if err := sink.Upsert(ctx, indexjobs.Key{SourceKind: SourceKind, SourceID: "mem-4"}, nil); err != nil {
		t.Fatalf("empty Upsert: %v", err)
	}

	if len(heard) != 2 || heard[0] != "mem-1" || heard[1] != "mem-2" {
		t.Errorf("heard = %v; want [mem-1 mem-2]", heard)
	}
	if len(vec) != 2 || vec[0] != 1 {
		t.Errorf("hook got vector %v; want the written one", vec)
	}
}
