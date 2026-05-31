package catalogindex

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// fakeJobs is an indexjobs.Store stub recording the last Enqueue/List
// inputs so the AdminStore translation can be asserted.
type fakeJobs struct {
	lastEnqueueKey     indexjobs.Key
	lastEnqueueTrigger indexjobs.Trigger
	lastFilter         indexjobs.ListFilter
	listResult         []indexjobs.Job
	getResult          *indexjobs.Job
}

func (f *fakeJobs) Enqueue(_ context.Context, k indexjobs.Key, tr indexjobs.Trigger) (bool, error) {
	f.lastEnqueueKey = k
	f.lastEnqueueTrigger = tr
	return true, nil
}

func (*fakeJobs) Claim(context.Context, string) (*indexjobs.Job, error) {
	return nil, indexjobs.ErrNoJob
}
func (*fakeJobs) Complete(context.Context, int64, string) error      { return nil }
func (*fakeJobs) Retry(context.Context, int64, string, string) error { return nil }
func (*fakeJobs) Fail(context.Context, int64, string, string) error  { return nil }
func (*fakeJobs) ReleaseExpiredLeases(context.Context) (int, error)  { return 0, nil }
func (*fakeJobs) RenewLease(context.Context, int64, string, time.Duration) error {
	return nil
}
func (*fakeJobs) UpdateProgress(context.Context, int64, string, int) error { return nil }
func (f *fakeJobs) Get(context.Context, int64) (*indexjobs.Job, error)     { return f.getResult, nil }
func (f *fakeJobs) List(_ context.Context, filter indexjobs.ListFilter) ([]indexjobs.Job, error) {
	f.lastFilter = filter
	return f.listResult, nil
}

func (*fakeJobs) Counts(context.Context, string) (*indexjobs.KindCounts, error) {
	return &indexjobs.KindCounts{}, nil
}

func (*fakeJobs) ActiveFailures(context.Context, string, int) ([]indexjobs.FailedUnit, error) {
	return nil, nil
}

func (*fakeJobs) ResolveFailures(context.Context, indexjobs.Key) (int, error) { return 0, nil }

func TestAdminStore_EnqueueEncodesAndMapsTrigger(t *testing.T) {
	t.Parallel()
	jobs := &fakeJobs{}
	s := NewAdminStore(jobs, nil)
	if _, err := s.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "s"}, KindManualRetry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if jobs.lastEnqueueKey.SourceKind != SourceKind {
		t.Errorf("source kind = %q; want %q", jobs.lastEnqueueKey.SourceKind, SourceKind)
	}
	if jobs.lastEnqueueKey.SourceID != EncodeSourceID("c", "s") {
		t.Errorf("source id = %q; want encoded c/s", jobs.lastEnqueueKey.SourceID)
	}
	if jobs.lastEnqueueTrigger != indexjobs.TriggerManualRetry {
		t.Errorf("trigger = %s; want manual_retry", jobs.lastEnqueueTrigger)
	}
}

func TestAdminStore_ListByCatalogUsesPrefix(t *testing.T) {
	t.Parallel()
	jobs := &fakeJobs{listResult: []indexjobs.Job{
		{ID: 1, SourceKind: SourceKind, SourceID: EncodeSourceID("c", "s"), Trigger: indexjobs.TriggerWrite, Status: indexjobs.StatusSucceeded, ItemsDone: 4},
	}}
	s := NewAdminStore(jobs, nil)

	// Catalog-only filter -> prefix match.
	got, err := s.List(context.Background(), ListFilter{CatalogID: "c", Status: StatusSucceeded})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if jobs.lastFilter.SourceIDPrefix != sourceIDPrefix("c") {
		t.Errorf("prefix = %q; want %q", jobs.lastFilter.SourceIDPrefix, sourceIDPrefix("c"))
	}
	if jobs.lastFilter.SourceID != "" {
		t.Errorf("source id should be empty for catalog-only filter; got %q", jobs.lastFilter.SourceID)
	}
	// Decoded job fields.
	if len(got) != 1 || got[0].CatalogID != "c" || got[0].SpecName != "s" {
		t.Errorf("decoded job = %+v", got)
	}
	if got[0].Kind != KindSpecWrite || got[0].EmbeddedSoFar != 4 {
		t.Errorf("mapped fields wrong: kind=%s embedded=%d", got[0].Kind, got[0].EmbeddedSoFar)
	}
}

func TestAdminStore_ListBySpecUsesExactID(t *testing.T) {
	t.Parallel()
	jobs := &fakeJobs{}
	s := NewAdminStore(jobs, nil)
	if _, err := s.List(context.Background(), ListFilter{CatalogID: "c", SpecName: "s", Kind: KindReconciler}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if jobs.lastFilter.SourceID != EncodeSourceID("c", "s") {
		t.Errorf("source id = %q; want encoded c/s", jobs.lastFilter.SourceID)
	}
	if jobs.lastFilter.SourceIDPrefix != "" {
		t.Errorf("prefix should be empty when spec is set; got %q", jobs.lastFilter.SourceIDPrefix)
	}
	if jobs.lastFilter.Trigger != indexjobs.TriggerReconciler {
		t.Errorf("trigger filter = %s; want reconciler", jobs.lastFilter.Trigger)
	}
}

func TestAdminStore_GetDecodes(t *testing.T) {
	t.Parallel()
	jobs := &fakeJobs{getResult: &indexjobs.Job{
		ID: 9, SourceKind: SourceKind, SourceID: EncodeSourceID("cat", "spec"), Trigger: indexjobs.TriggerReconciler,
	}}
	s := NewAdminStore(jobs, nil)
	job, err := s.Get(context.Background(), 9)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.CatalogID != "cat" || job.SpecName != "spec" || job.Kind != KindReconciler {
		t.Errorf("decoded job = %+v", job)
	}
}

func TestAdminStore_SpecStatuses(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	s := NewAdminStore(&fakeJobs{}, db)

	mock.ExpectQuery("FROM api_catalog_specs s").
		WithArgs("c", SourceKind, sourceIDDelim).
		WillReturnRows(sqlmock.NewRows([]string{
			"catalog_id", "spec_name", "operation_count", "embedding_count",
			"job_status", "job_attempts", "job_last_error", "job_updated_at", "items_done",
		}).AddRow("c", "s", 5, 5, "succeeded", 1, "", time.Now(), 5))

	rows, err := s.SpecStatuses(context.Background(), "c")
	if err != nil {
		t.Fatalf("SpecStatuses: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d; want 1", len(rows))
	}
	r := rows[0]
	if r.SpecName != "s" || r.OperationCount != 5 || r.EmbeddingCount != 5 || r.JobStatus != StatusSucceeded {
		t.Errorf("status row = %+v", r)
	}
}

func TestAdminStore_Health(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	s := NewAdminStore(&fakeJobs{}, db)

	mock.ExpectQuery("WITH spec_state AS").
		WithArgs("c", SourceKind, sourceIDDelim).
		WillReturnRows(sqlmock.NewRows([]string{"total", "indexed", "pending", "running", "failed"}).
			AddRow(10, 7, 2, 0, 1))

	h, err := s.Health(context.Background(), "c")
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.CatalogID != "c" || h.SpecsTotal != 10 || h.SpecsIndexed != 7 || h.SpecsPending != 2 || h.SpecsFailed != 1 {
		t.Errorf("health = %+v", h)
	}
}
