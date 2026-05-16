package embedjobs

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

// anyArg matches any driver.Value. Used in sqlmock WithArgs for
// fields whose exact value is not under test (timestamps,
// generated worker ids, computed backoff intervals).
type anyArg struct{}

func (anyArg) Match(_ driver.Value) bool { return true }

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	mock.MatchExpectationsInOrder(false)
	return NewPostgresStore(db), mock, func() { _ = db.Close() }
}

// TestEnqueue_InsertsRow covers the happy path: a fresh spec
// produces a new job row, the NOTIFY fires, the helper returns
// created=true.
func TestEnqueue_InsertsRow(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO api_catalog_embedding_jobs`)).
		WithArgs("petstore", "default", string(KindSpecWrite)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_notify`)).
		WithArgs(NotifyChannel).
		WillReturnResult(sqlmock.NewResult(0, 0))
	created, err := store.Enqueue(context.Background(),
		SpecKey{CatalogID: "petstore", SpecName: "default"}, KindSpecWrite)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !created {
		t.Error("created=false; want true for fresh insert")
	}
}

// TestEnqueue_ConflictReturnsFalse covers the idempotent case
// where a pending or running job already exists. The partial
// unique index suppresses the insert; the helper returns
// created=false with no error and does not fire NOTIFY.
func TestEnqueue_ConflictReturnsFalse(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO api_catalog_embedding_jobs`)).
		WithArgs("petstore", "default", string(KindSpecWrite)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	created, err := store.Enqueue(context.Background(),
		SpecKey{CatalogID: "petstore", SpecName: "default"}, KindSpecWrite)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if created {
		t.Error("created=true on conflict; want false")
	}
}

// TestEnqueue_DBError surfaces the wrapped error.
func TestEnqueue_DBError(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO api_catalog_embedding_jobs`)).
		WillReturnError(errors.New("boom"))
	_, err := store.Enqueue(context.Background(),
		SpecKey{CatalogID: "p", SpecName: "d"}, KindSpecWrite)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestClaim_HappyPath proves the two-step transaction: SELECT
// FOR UPDATE SKIP LOCKED finds the row, UPDATE flips it to
// running and sets the lease + worker id, RETURNING the full
// job.
func TestClaim_HappyPath(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`FOR UPDATE SKIP LOCKED`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	now := time.Now()
	lease := now.Add(LeaseDuration)
	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE api_catalog_embedding_jobs`)).
		WithArgs(int64(42), "worker-x", int(LeaseDuration/time.Second)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "catalog_id", "spec_name", "kind", "status", "attempts",
			"last_error", "next_run_at", "worker_id", "lease_expires_at",
			"created_at", "started_at", "completed_at",
		}).AddRow(int64(42), "petstore", "default", "spec_write", "running", 1,
			"", now, "worker-x", lease, now, now, nil))
	mock.ExpectCommit()
	job, err := store.Claim(context.Background(), "worker-x")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if job.ID != 42 || job.Status != StatusRunning || job.WorkerID != "worker-x" {
		t.Errorf("unexpected job: %+v", job)
	}
}

// TestClaim_NoRowReturnsErrNoJob covers the idle case: SELECT
// finds nothing, Claim returns the sentinel that workers treat
// as "go back to sleep."
func TestClaim_NoRowReturnsErrNoJob(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`FOR UPDATE SKIP LOCKED`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	_, err := store.Claim(context.Background(), "worker-x")
	if !errors.Is(err, ErrNoJob) {
		t.Fatalf("err=%v want ErrNoJob", err)
	}
}

// TestComplete_HappyPath covers the worker's success path: the
// UPDATE matches a single row, no error.
func TestComplete_HappyPath(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalog_embedding_jobs`)).
		WithArgs(int64(42), "worker-x").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.Complete(context.Background(), 42, "worker-x"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// TestComplete_LeaseRotated proves Complete returns ErrNotFound
// when the worker_id no longer matches (the reaper released the
// lease between claim and complete, and another worker has
// since claimed the row).
func TestComplete_LeaseRotated(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalog_embedding_jobs`)).
		WithArgs(int64(42), "stale-worker").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.Complete(context.Background(), 42, "stale-worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

// TestFail_TerminalState covers the "attempts exhausted" path.
func TestFail_TerminalState(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalog_embedding_jobs`)).
		WithArgs(int64(42), "worker-x", "provider 502").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.Fail(context.Background(), 42, "worker-x", "provider 502"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
}

// TestReleaseExpiredLeases sweeps the running rows whose lease
// has elapsed, returns the count for log/metric output.
func TestReleaseExpiredLeases(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_catalog_embedding_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	n, err := store.ReleaseExpiredLeases(context.Background())
	if err != nil {
		t.Fatalf("ReleaseExpiredLeases: %v", err)
	}
	if n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

// TestReconcileGaps inserts pending jobs for specs whose
// embedding-row count diverges from operation_count. The
// reconciler uses ON CONFLICT DO NOTHING against the partial
// unique index so a spec already queued is a no-op.
func TestReconcileGaps(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_embedding_jobs`)).
		WithArgs(string(KindReconciler)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_notify`)).
		WithArgs(NotifyChannel).
		WillReturnResult(sqlmock.NewResult(0, 0))
	n, err := store.ReconcileGaps(context.Background())
	if err != nil {
		t.Fatalf("ReconcileGaps: %v", err)
	}
	if n != 2 {
		t.Errorf("got %d enqueued, want 2", n)
	}
}

// TestReconcileGaps_NoGaps proves the no-NOTIFY branch when the
// reconciler found nothing to enqueue.
func TestReconcileGaps_NoGaps(t *testing.T) {
	t.Parallel()
	store, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO api_catalog_embedding_jobs`)).
		WithArgs(string(KindReconciler)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// No pg_notify expectation: with 0 rows the helper skips it.
	n, err := store.ReconcileGaps(context.Background())
	if err != nil {
		t.Fatalf("ReconcileGaps: %v", err)
	}
	if n != 0 {
		t.Errorf("got %d, want 0", n)
	}
}

// TestComputeBackoffSeconds documents the exponential schedule.
// The store consumes this for the Retry path; a regression in
// the schedule would either retry too aggressively (hammering a
// flaky provider) or too slowly (delaying recovery).
func TestComputeBackoffSeconds(t *testing.T) {
	t.Parallel()
	base := int(retryBackoffBase / time.Second)
	cases := []struct{ attempts, want int }{
		{0, base},
		{1, base * 2},
		{2, base * 4},
		{3, base * 8},
		{4, base * 16},
		{5, base * 32},
		{-1, base}, // clamp negative
		{40, base << 30},
	}
	for _, c := range cases {
		got := computeBackoffSeconds(c.attempts)
		if got != c.want {
			t.Errorf("computeBackoffSeconds(%d) = %d; want %d", c.attempts, got, c.want)
		}
	}
}

// TestIntToStr_RoundTrips the inline integer formatter used by
// the dynamic-predicate builder.
func TestIntToStr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"}, {1, "1"}, {9, "9"}, {10, "10"},
		{99, "99"}, {123, "123"}, {-1, "-1"}, {-42, "-42"},
	}
	for _, c := range cases {
		got := intToStr(c.in)
		if got != c.want {
			t.Errorf("intToStr(%d) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestBuildListPredicates exercises the closed-set predicate
// builder. Empty filter produces an empty WHERE clause; each
// field appended in stable order with its placeholder.
func TestBuildListPredicates(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		where, args := buildListPredicates(ListFilter{})
		if where != "" {
			t.Errorf("empty filter produced %q", where)
		}
		if len(args) != 0 {
			t.Errorf("empty filter produced %d args", len(args))
		}
	})
	t.Run("full", func(t *testing.T) {
		where, args := buildListPredicates(ListFilter{
			CatalogID: "petstore", SpecName: "default",
			Status: StatusPending, Kind: KindSpecWrite,
		})
		if where == "" {
			t.Fatal("expected WHERE clause")
		}
		if len(args) != 4 {
			t.Errorf("got %d args, want 4", len(args))
		}
	})
}

// TestIsPGCode exercises the type-assert helper.
func TestIsPGCode(t *testing.T) {
	t.Parallel()
	if isPGCode(nil, pgUniqueViolation) {
		t.Error("nil should not match")
	}
	if isPGCode(errors.New("plain"), pgUniqueViolation) {
		t.Error("plain error should not match")
	}
	if !isPGCode(&pq.Error{Code: pgUniqueViolation}, pgUniqueViolation) {
		t.Error("matching pq.Error should match")
	}
}

// Avoid lint warning for the imported anyArg type when no test
// uses it. The placeholder is kept around because future tests
// (lease-rotation timing) will reach for it.
var _ = anyArg{}
