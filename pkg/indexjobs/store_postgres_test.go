package indexjobs

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return NewPostgresStore(db, WithLeaseDuration(5*time.Minute)), mock, func() { _ = db.Close() }
}

// jobRow builds a *sqlmock.Rows with the canonical job column set in
// scanJob order.
func jobRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "source_kind", "source_id", "trigger_kind", "status", "attempts",
		"last_error", "next_run_at", "worker_id", "lease_expires_at",
		"created_at", "started_at", "completed_at", "items_done",
	}).AddRow(int64(7), "api_catalog", "c\x1fs", "write", "running", 1,
		"", time.Now(), "w1", time.Now(), time.Now(), nil, nil, 0)
}

func TestStore_LeaseDuration(t *testing.T) {
	t.Parallel()
	s, _, done := newMockStore(t)
	defer done()
	if s.LeaseDuration() != 5*time.Minute {
		t.Errorf("LeaseDuration = %v; want 5m", s.LeaseDuration())
	}
}

func TestStore_EnqueueCreated(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("INSERT INTO index_jobs").
		WithArgs("api_catalog", "c\x1fs", "write").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))

	created, err := s.Enqueue(context.Background(), Key{SourceKind: "api_catalog", SourceID: "c\x1fs"}, TriggerWrite)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !created {
		t.Error("created should be true on a fresh insert")
	}
}

func TestStore_EnqueueConflict(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("INSERT INTO index_jobs").
		WillReturnError(sql.ErrNoRows)

	created, err := s.Enqueue(context.Background(), Key{SourceKind: "k", SourceID: "s"}, TriggerWrite)
	if err != nil {
		t.Fatalf("conflict should not error; got %v", err)
	}
	if created {
		t.Error("created should be false when an open job already exists")
	}
}

func TestStore_ClaimReturnsJob(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id\\s+FROM index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery("UPDATE index_jobs").
		WillReturnRows(jobRow())
	mock.ExpectCommit()

	job, err := s.Claim(context.Background(), "w1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if job.ID != 7 || job.SourceKind != "api_catalog" {
		t.Errorf("claimed job = %+v", job)
	}
}

func TestStore_ClaimNoJob(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id\\s+FROM index_jobs").WillReturnError(sql.ErrNoRows)

	_, err := s.Claim(context.Background(), "w1")
	if !errors.Is(err, ErrNoJob) {
		t.Errorf("err = %v; want ErrNoJob", err)
	}
}

func TestStore_CompleteOwnership(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	// Happy path: one row affected, then the best-effort sweep that
	// resolves any open failure superseded by this success.
	mock.ExpectExec("UPDATE index_jobs").
		WithArgs(int64(7), "w1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE index_jobs f").
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	if err := s.Complete(context.Background(), 7, "w1"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Rotated lease: zero rows -> ErrNotFound, and the resolve sweep
	// must NOT run (no superseding success happened).
	mock.ExpectExec("UPDATE index_jobs").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := s.Complete(context.Background(), 7, "stale"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rotated Complete err = %v; want ErrNotFound", err)
	}
}

func TestStore_UpdateProgress(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE index_jobs").
		WithArgs(int64(7), "w1", 5).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateProgress(context.Background(), 7, "w1", 5); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
}

func TestStore_RenewLeaseRotatedReturnsNotFound(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE index_jobs").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := s.RenewLease(context.Background(), 7, "stale", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestStore_Retry(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	// lastAttempts SELECT, then the UPDATE ... RETURNING attempts.
	mock.ExpectQuery("SELECT attempts FROM index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"attempts"}).AddRow(2))
	mock.ExpectQuery("UPDATE index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"attempts"}).AddRow(2))
	if err := s.Retry(context.Background(), 7, "w1", "boom"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
}

func TestStore_FailAndRelease(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE index_jobs").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.Fail(context.Background(), 7, "w1", "dead"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	mock.ExpectExec("UPDATE index_jobs").WillReturnResult(sqlmock.NewResult(0, 3))
	n, err := s.ReleaseExpiredLeases(context.Background())
	if err != nil {
		t.Fatalf("ReleaseExpiredLeases: %v", err)
	}
	if n != 3 {
		t.Errorf("released = %d; want 3", n)
	}
}

func TestStore_GetAndList(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT .* FROM index_jobs WHERE id = ").WillReturnRows(jobRow())
	job, err := s.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.ID != 7 {
		t.Errorf("Get id = %d; want 7", job.ID)
	}

	mock.ExpectQuery("SELECT .* FROM index_jobs").WillReturnRows(jobRow())
	jobs, err := s.List(context.Background(), ListFilter{SourceKind: "api_catalog", SourceIDPrefix: "c\x1f", Status: StatusRunning, Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("List len = %d; want 1", len(jobs))
	}
}

func TestStore_GetNotFound(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("SELECT .* FROM index_jobs WHERE id = ").WillReturnError(sql.ErrNoRows)
	if _, err := s.Get(context.Background(), 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestStore_Counts(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	activity := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("WITH last AS").WithArgs("api_catalog").
		WillReturnRows(sqlmock.NewRows([]string{"pending", "running", "succeeded", "failed", "last_activity", "unresolved_failures"}).
			AddRow(1, 2, 3, 4, activity, 2))
	c, err := s.Counts(context.Background(), "api_catalog")
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.Pending != 1 || c.Running != 2 || c.Succeeded != 3 || c.Failed != 4 {
		t.Errorf("counts = %+v", c)
	}
	if c.UnresolvedFailures != 2 {
		t.Errorf("UnresolvedFailures = %d; want 2", c.UnresolvedFailures)
	}
	if c.LastActivity == nil || !c.LastActivity.Equal(activity) {
		t.Errorf("LastActivity = %v; want %v", c.LastActivity, activity)
	}
}

// TestStore_CountsNoActivity covers the NULL last_activity path (a kind
// with no jobs): LastActivity stays nil.
func TestStore_CountsNoActivity(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("WITH last AS").WithArgs("tools").
		WillReturnRows(sqlmock.NewRows([]string{"pending", "running", "succeeded", "failed", "last_activity", "unresolved_failures"}).
			AddRow(0, 0, 0, 0, nil, 0))
	c, err := s.Counts(context.Background(), "tools")
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.LastActivity != nil {
		t.Errorf("LastActivity = %v; want nil", c.LastActivity)
	}
}

func TestEscapeLikePrefix(t *testing.T) {
	t.Parallel()
	got := escapeLikePrefix(`a%b_c\d`)
	want := `a\%b\_c\\d`
	if got != want {
		t.Errorf("escapeLikePrefix = %q; want %q", got, want)
	}
}

func TestStore_EnqueueNotifyErrorStillCreated(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("INSERT INTO index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	// pg_notify fails: Enqueue must still report created=true (NOTIFY is
	// best-effort; the poll tick is the backstop).
	mock.ExpectExec("pg_notify").WillReturnError(errors.New("no listen privilege"))

	created, err := s.Enqueue(context.Background(), Key{SourceKind: "k", SourceID: "s"}, TriggerWrite)
	if err != nil {
		t.Fatalf("notify failure should not fail enqueue; got %v", err)
	}
	if !created {
		t.Error("created should be true even when NOTIFY fails")
	}
}

func TestLeaseSeconds_FloorsAtOne(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]int{
		5 * time.Minute:        300,
		time.Second:            1,
		500 * time.Millisecond: 1, // sub-second must not truncate to 0
		0:                      1,
	}
	for d, want := range cases {
		if got := leaseSeconds(d); got != want {
			t.Errorf("leaseSeconds(%v) = %d; want %d", d, got, want)
		}
	}
}

func TestStore_CompleteResolveSweepErrorIsBestEffort(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	// Success flip affects one row; the resolve sweep then errors. The
	// success is already durable, so Complete must still return nil.
	mock.ExpectExec("UPDATE index_jobs").
		WithArgs(int64(9), "w1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE index_jobs f").
		WithArgs(int64(9)).
		WillReturnError(errors.New("sweep failed"))
	if err := s.Complete(context.Background(), 9, "w1"); err != nil {
		t.Fatalf("Complete with failing resolve sweep should be nil; got %v", err)
	}
}

func TestStore_ResolveFailures(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE index_jobs").
		WithArgs("tools", "platform").
		WillReturnResult(sqlmock.NewResult(0, 4))
	n, err := s.ResolveFailures(context.Background(), Key{SourceKind: "tools", SourceID: "platform"})
	if err != nil {
		t.Fatalf("ResolveFailures: %v", err)
	}
	if n != 4 {
		t.Errorf("resolved = %d; want 4", n)
	}
}

func TestStore_ResolveFailuresError(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectExec("UPDATE index_jobs").WillReturnError(errors.New("db down"))
	if _, err := s.ResolveFailures(context.Background(), Key{SourceKind: "tools", SourceID: "p"}); err == nil {
		t.Fatal("expected resolve-failures error")
	}
}

func TestStore_ActiveFailures(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	first := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	last := time.Date(2026, 5, 30, 11, 0, 0, 0, time.UTC)
	succeeded := time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)
	// Empty kind lists across kinds; the default limit (50) is applied
	// when the caller passes 0.
	mock.ExpectQuery("WITH open_failed AS").
		WithArgs("", 50).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_kind", "source_id", "last_error", "attempts",
			"occ", "first_failed", "last_failed", "last_succeeded",
		}).
			AddRow(int64(12), "tools", "platform", "ollama timeout", 5, 3, first, last, succeeded).
			AddRow(int64(8), "api_catalog", "c\x1fv1", "no consumer", 5, 1, last, last, nil))
	units, err := s.ActiveFailures(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ActiveFailures: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %d; want 2", len(units))
	}
	u := units[0]
	if u.LatestJobID != 12 || u.SourceKind != "tools" || u.SourceID != "platform" {
		t.Errorf("unit[0] = %+v", u)
	}
	if u.Occurrences != 3 || u.Attempts != 5 || u.LastError != "ollama timeout" {
		t.Errorf("unit[0] aggregates = %+v", u)
	}
	if !u.FirstFailedAt.Equal(first) || !u.LastFailedAt.Equal(last) {
		t.Errorf("unit[0] timestamps = %v / %v", u.FirstFailedAt, u.LastFailedAt)
	}
	if u.LastSucceededAt == nil || !u.LastSucceededAt.Equal(succeeded) {
		t.Errorf("unit[0] LastSucceededAt = %v; want %v", u.LastSucceededAt, succeeded)
	}
	if units[1].LastSucceededAt != nil {
		t.Errorf("unit[1] LastSucceededAt = %v; want nil (never succeeded)", units[1].LastSucceededAt)
	}
}

func TestStore_ActiveFailuresClampsLimit(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	// An oversized limit clamps to the default rather than letting a
	// hostile value pull the whole table.
	mock.ExpectQuery("WITH open_failed AS").
		WithArgs("tools", defaultListLimit).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "source_kind", "source_id", "last_error", "attempts",
			"occ", "first_failed", "last_failed", "last_succeeded",
		}))
	if _, err := s.ActiveFailures(context.Background(), "tools", maxListLimit+1); err != nil {
		t.Fatalf("ActiveFailures: %v", err)
	}
}

func TestStore_ActiveFailuresQueryError(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	mock.ExpectQuery("WITH open_failed AS").WillReturnError(errors.New("db down"))
	if _, err := s.ActiveFailures(context.Background(), "", 10); err == nil {
		t.Fatal("expected active-failures query error")
	}
}
