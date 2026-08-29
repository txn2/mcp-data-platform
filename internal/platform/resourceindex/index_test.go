package resourceindex

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeBlobs is a blob reader whose behavior per key is scripted: content, a
// confirmed not-found, or a transient failure.
type fakeBlobs struct {
	objects map[string][]byte
	err     error
	calls   int
}

func (f *fakeBlobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	f.calls++
	if f.err != nil {
		return nil, "", f.err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, "", errors.New("s3 get: NoSuchKey: the specified key does not exist")
	}
	return body, "text/csv", nil
}

func newDB(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

// loadFixture is the resource row a scripted metadata read returns.
type loadFixture struct {
	mime        string
	s3Key       string
	contentText string
	size        int64
	settled     bool // content_indexed_at IS NOT NULL
}

// expectLoad scripts the Source's metadata read for a normally-sized resource
// whose content pass has never settled (the fresh-upload shape).
func expectLoad(mock sqlmock.Sqlmock, id, mime, s3Key, contentText string) {
	expectLoadRow(mock, id, loadFixture{mime: mime, s3Key: s3Key, contentText: contentText, size: 1024})
}

// expectLoadRow is expectLoad with the full fixture, for the
// too-large-to-read guard.
func expectLoadRow(mock sqlmock.Sqlmock, id string, f loadFixture) {
	mock.ExpectQuery("SELECT display_name, description, path, filename, tags, mime_type, size_bytes, s3_key, content_text").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{
			"display_name", "description", "category", "filename", "tags", "mime_type", "size_bytes", "s3_key",
			"content_text", "content_settled",
		}).AddRow("Sales Dictionary", "Field reference", "references", "sales.csv",
			pq.Array([]string{"finance"}), f.mime, f.size, f.s3Key, f.contentText, f.settled))
}

// The framework routes a job to the Source and Sink registered under one kind,
// so a mismatch is a wiring bug the registry rejects. Registering the real pair
// is what proves they agree, not comparing a constant to itself.
func TestConsumerRegistersAsOnePair(t *testing.T) {
	store, _ := newDB(t)
	reg := indexjobs.NewRegistry()
	if err := reg.Register(NewSource(store, nil, ""), NewSink(store, "m")); err != nil {
		t.Fatalf("registering the resources consumer: %v", err)
	}
	if kinds := reg.Kinds(); len(kinds) != 1 || kinds[0] != SourceKind {
		t.Fatalf("kinds = %v, want [%s]", kinds, SourceKind)
	}
}

// The point of this consumer: the indexed text carries what is INSIDE the file,
// not just its metadata, and the extracted prefix is written back for the
// lexical index.
func TestLoadItems_ExtractsAndPersistsContent(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_1", "text/csv", "resources/global/res_1/sales.csv", "")
	mock.ExpectExec("UPDATE resources SET content_text").
		WithArgs("res_1", "column,description\ngross_margin_pct,margin\n").
		WillReturnResult(sqlmock.NewResult(0, 1))

	blobs := &fakeBlobs{objects: map[string][]byte{
		"resources/global/res_1/sales.csv": []byte("column,description\ngross_margin_pct,margin\n"),
	}}
	items, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 1 || items[0].ItemID != "res_1" {
		t.Fatalf("items = %+v", items)
	}
	if !strings.Contains(items[0].Text, "gross_margin_pct") {
		t.Errorf("indexed text does not carry the file content: %q", items[0].Text)
	}
	if !strings.Contains(items[0].Text, "Sales Dictionary") {
		t.Errorf("indexed text lost the metadata: %q", items[0].Text)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// Binary content is indexed on metadata alone: no blob read, no content write.
func TestLoadItems_BinarySkipsBlobRead(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_png", "image/png", "resources/global/res_png/logo.png", "")
	// There is nothing to extract, so the content pass settles: without the stamp
	// the row would stay a gap and be re-enqueued on every sweep forever.
	mock.ExpectExec("UPDATE resources SET content_text").
		WithArgs("res_png", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	blobs := &fakeBlobs{}
	items, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_png")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if blobs.calls != 0 {
		t.Errorf("binary resource should not be fetched from blob storage (calls=%d)", blobs.calls)
	}
	if len(items) != 1 || !strings.Contains(items[0].Text, "Sales Dictionary") {
		t.Fatalf("items = %+v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// A transient blob failure must not blank an already-indexed resource, and must
// leave the row UNSETTLED so the next sweep retries. The metadata embed succeeds
// either way, so a settled row here would silently drop out of the gap query
// with its file contents never indexed.
func TestLoadItems_TransientBlobFailureKeepsPriorContentAndStaysOwed(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_1", "text/csv", "k", "previously extracted text")

	blobs := &fakeBlobs{err: errors.New("connection reset by peer")}
	items, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if !strings.Contains(items[0].Text, "previously extracted text") {
		t.Errorf("transient failure discarded the prior content: %q", items[0].Text)
	}
	// No write at all: content_indexed_at stays NULL, so FindGaps returns the row
	// again. A scripted UPDATE would fail this expectation.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// A row that has settled and whose text is unchanged writes nothing: the
// steady-state re-index must not touch the table.
func TestLoadItems_SettledUnchangedContentSkipsWrite(t *testing.T) {
	store, mock := newDB(t)
	expectLoadRow(mock, "res_1", loadFixture{
		mime: "text/csv", s3Key: "k", contentText: "same body", size: 1024, settled: true,
	})

	blobs := &fakeBlobs{objects: map[string][]byte{"k": []byte("same body")}}
	if _, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_1"); err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// An unsettled row whose extracted text is empty (an empty file) must still be
// stamped, or it would be re-read on every sweep forever.
func TestLoadItems_UnsettledEmptyContentIsStillStamped(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_1", "text/csv", "k", "")
	mock.ExpectExec("UPDATE resources SET content_text").
		WithArgs("res_1", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	blobs := &fakeBlobs{objects: map[string][]byte{"k": {}}}
	if _, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_1"); err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// A confirmed missing object clears the stale extracted text, so search stops
// answering with content no reader can fetch — but the row itself is left alone.
func TestLoadItems_MissingObjectClearsContent(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_1", "text/csv", "gone", "stale text")
	mock.ExpectExec("UPDATE resources SET content_text").
		WithArgs("res_1", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	blobs := &fakeBlobs{objects: map[string][]byte{}}
	items, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if strings.Contains(items[0].Text, "stale text") {
		t.Errorf("orphaned resource kept indexing removed content: %q", items[0].Text)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// A deleted resource reports ErrSourceGone so the worker resolves the job
// instead of recording a permanent failure.
func TestLoadItems_DeletedResourceIsSourceGone(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT display_name").WithArgs("res_x").WillReturnError(errNoRows())

	_, err := NewSource(store, nil, "").LoadItems(context.Background(), "res_x")
	if !errors.Is(err, indexjobs.ErrSourceGone) {
		t.Fatalf("err = %v, want ErrSourceGone", err)
	}
}

// A store read failure is NOT a gone signal: the unit is unreadable and must
// surface as an error.
func TestLoadItems_StoreErrorIsNotGone(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT display_name").WithArgs("res_x").WillReturnError(errors.New("db down"))

	_, err := NewSource(store, nil, "").LoadItems(context.Background(), "res_x")
	if err == nil || errors.Is(err, indexjobs.ErrSourceGone) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

// A failed content write must not fail the job: the text just extracted is still
// indexed and the next sweep retries the write.
func TestLoadItems_ContentWriteFailureDoesNotFailJob(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_1", "text/csv", "k", "")
	mock.ExpectExec("UPDATE resources SET content_text").WillReturnError(errors.New("db down"))

	blobs := &fakeBlobs{objects: map[string][]byte{"k": []byte("body text")}}
	items, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if !strings.Contains(items[0].Text, "body text") {
		t.Errorf("extracted text should still be indexed: %q", items[0].Text)
	}
}

// The blob API has no range read, so an oversized object would be pulled whole
// into memory to keep 32 KiB of it. Such a resource is indexed on metadata alone.
func TestLoadItems_OversizedContentSkipsBlobRead(t *testing.T) {
	store, mock := newDB(t)
	expectLoadRow(mock, "res_big", loadFixture{
		mime: "text/csv", s3Key: "k", size: resource.MaxContentReadBytes + 1,
	})
	mock.ExpectExec("UPDATE resources SET content_text").
		WithArgs("res_big", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	blobs := &fakeBlobs{objects: map[string][]byte{"k": []byte("huge body")}}
	items, err := NewSource(store, blobs, "bucket").LoadItems(context.Background(), "res_big")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if blobs.calls != 0 {
		t.Errorf("oversized resource must not be fetched (calls=%d)", blobs.calls)
	}
	if !strings.Contains(items[0].Text, "Sales Dictionary") {
		t.Errorf("items = %+v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name  string
		body  []byte
		limit int
		want  string
	}{
		{"plain text passes through", []byte("hello world"), 100, "hello world"},
		{"bounded by limit", []byte("abcdefghij"), 4, "abcd"},
		{"NUL bytes removed (postgres TEXT rejects them)", []byte("a\x00b"), 100, "ab"},
		{"invalid UTF-8 removed", []byte{'a', 0xff, 'b'}, 100, "ab"},
		{"truncation does not split a rune", []byte("aé"), 2, "a"},
		{"empty body", nil, 100, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractText(tt.body, tt.limit); got != tt.want {
				t.Errorf("extractText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSink_VectorRoundTrip(t *testing.T) {
	store, mock := newDB(t)
	sink := NewSink(store, "model-a")
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "res_1"}

	// No stored vector yet: an empty map, so the worker embeds.
	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM resources").
		WithArgs("res_1").WillReturnError(errNoRows())
	existing, err := sink.ListExisting(context.Background(), key)
	if err != nil || len(existing) != 0 {
		t.Fatalf("ListExisting = %v, %v", existing, err)
	}

	mock.ExpectExec("UPDATE resources SET embedding =").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := sink.Upsert(context.Background(), key, []indexjobs.Vector{{
		ItemID: "res_1", Embedding: []float32{0.1, 0.2}, Model: "model-a", TextHash: []byte("h"),
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// An empty row set writes nothing.
	if err := sink.UpsertBatch(context.Background(), key, nil); err != nil {
		t.Fatalf("UpsertBatch(empty): %v", err)
	}
	if err := sink.StampExpected(context.Background(), key, 1); err != nil {
		t.Fatalf("StampExpected: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSink_FindGapsAndCoverage(t *testing.T) {
	store, mock := newDB(t)
	sink := NewSink(store, "model-a")

	mock.ExpectQuery("SELECT id FROM resources\\s+WHERE embedding IS NULL OR embedding_model IS DISTINCT FROM \\$1 OR content_indexed_at IS NULL").
		WithArgs("model-a").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("res_1").AddRow("res_2"))
	gaps, err := sink.FindGaps(context.Background())
	if err != nil || len(gaps) != 2 {
		t.Fatalf("FindGaps = %v, %v", gaps, err)
	}

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"indexed", "expected"}).AddRow(1, 2))
	cov, err := sink.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if cov.Indexed != 1 || cov.Expected != 2 || !cov.ExpectedKnown {
		t.Fatalf("coverage = %+v", cov)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSink_CoverageErrorSurfaces(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("db down"))
	if _, err := NewSink(store, "m").Coverage(context.Background()); err == nil {
		t.Fatal("expected the store error to surface")
	}
}

// errNoRows is the missing-row error the database/sql layer returns, which the
// store maps to "gone" (Load) or "no vector yet" (ListVectors).
func errNoRows() error { return sql.ErrNoRows }

// A resource with no blob reader wired (no S3 connection) indexes metadata only,
// and settles: with no blob storage the upload path stored no bytes either, so
// there is genuinely nothing to extract.
func TestLoadItems_NoBlobReaderIndexesMetadataOnly(t *testing.T) {
	store, mock := newDB(t)
	expectLoad(mock, "res_1", "text/csv", "k", "")
	mock.ExpectExec("UPDATE resources SET content_text").
		WithArgs("res_1", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	items, err := NewSource(store, nil, "").LoadItems(context.Background(), "res_1")
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if !strings.Contains(items[0].Text, "Sales Dictionary") {
		t.Errorf("items = %+v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_ListVectorsReturnsStoredVector(t *testing.T) {
	store, mock := newDB(t)
	mock.ExpectQuery("SELECT embedding, embedding_model, embedding_text_hash FROM resources").
		WithArgs("res_1").
		WillReturnRows(sqlmock.NewRows([]string{"embedding", "embedding_model", "embedding_text_hash"}).
			AddRow("[0.25,0.5]", "model-a", []byte("hash")))

	got, err := store.ListVectors(context.Background(), "res_1")
	if err != nil {
		t.Fatalf("ListVectors: %v", err)
	}
	v, ok := got["res_1"]
	if !ok {
		t.Fatalf("vectors = %+v", got)
	}
	if v.Model != "model-a" || v.Dim != 2 || string(v.TextHash) != "hash" {
		t.Errorf("vector = %+v", v)
	}
}

func TestStore_ErrorsSurface(t *testing.T) {
	t.Run("list vectors", func(t *testing.T) {
		store, mock := newDB(t)
		mock.ExpectQuery("SELECT embedding").WillReturnError(errors.New("db down"))
		if _, err := store.ListVectors(context.Background(), "res_1"); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("upsert vectors", func(t *testing.T) {
		store, mock := newDB(t)
		mock.ExpectExec("UPDATE resources SET embedding =").WillReturnError(errors.New("db down"))
		if err := store.UpsertVectors(context.Background(), "res_1",
			[]indexjobs.Vector{{ItemID: "res_1"}}); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("find gaps query", func(t *testing.T) {
		store, mock := newDB(t)
		mock.ExpectQuery("SELECT id FROM resources").WillReturnError(errors.New("db down"))
		if _, err := store.FindGaps(context.Background(), "m"); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("find gaps scan", func(t *testing.T) {
		store, mock := newDB(t)
		mock.ExpectQuery("SELECT id FROM resources").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
		if _, err := store.FindGaps(context.Background(), "m"); err == nil {
			t.Fatal("expected a scan error")
		}
	})
}
