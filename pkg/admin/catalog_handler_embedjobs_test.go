package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/embedjobs"
)

// fakeEmbedJobs implements admin.EmbedJobsStore in-memory.
// Exists only for admin-handler tests; the production store is
// Postgres-backed and tested separately.
type fakeEmbedJobs struct {
	mu     sync.Mutex
	jobs   []embedjobs.Job
	nextID int64

	enqueueErr  error
	listErr     error
	getErr      error
	statusesErr error
	healthErr   error

	statuses []embedjobs.SpecStatusRow
	health   *embedjobs.CatalogHealth
}

func (f *fakeEmbedJobs) Enqueue(_ context.Context, key embedjobs.SpecKey, kind embedjobs.Kind) (bool, error) {
	if f.enqueueErr != nil {
		return false, f.enqueueErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.CatalogID == key.CatalogID && j.SpecName == key.SpecName &&
			(j.Status == embedjobs.StatusPending || j.Status == embedjobs.StatusRunning) {
			return false, nil
		}
	}
	f.nextID++
	f.jobs = append(f.jobs, embedjobs.Job{
		ID:        f.nextID,
		CatalogID: key.CatalogID,
		SpecName:  key.SpecName,
		Kind:      kind,
		Status:    embedjobs.StatusPending,
		CreatedAt: time.Now(),
	})
	return true, nil
}

func (f *fakeEmbedJobs) List(_ context.Context, filter embedjobs.ListFilter) ([]embedjobs.Job, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]embedjobs.Job, 0, len(f.jobs))
	// Iterate newest-first to mirror the Postgres ORDER BY id DESC.
	for i := len(f.jobs) - 1; i >= 0; i-- {
		j := f.jobs[i]
		if filter.CatalogID != "" && j.CatalogID != filter.CatalogID {
			continue
		}
		if filter.SpecName != "" && j.SpecName != filter.SpecName {
			continue
		}
		if filter.Status != "" && j.Status != filter.Status {
			continue
		}
		out = append(out, j)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeEmbedJobs) Get(_ context.Context, id int64) (*embedjobs.Job, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID == id {
			cp := j
			return &cp, nil
		}
	}
	return nil, embedjobs.ErrNotFound
}

func (f *fakeEmbedJobs) SpecStatuses(_ context.Context, catalogID string) ([]embedjobs.SpecStatusRow, error) {
	if f.statusesErr != nil {
		return nil, f.statusesErr
	}
	out := make([]embedjobs.SpecStatusRow, 0)
	for _, s := range f.statuses {
		if s.CatalogID == catalogID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeEmbedJobs) Health(_ context.Context, catalogID string) (*embedjobs.CatalogHealth, error) {
	if f.healthErr != nil {
		return nil, f.healthErr
	}
	if f.health == nil {
		return &embedjobs.CatalogHealth{CatalogID: catalogID}, nil
	}
	cp := *f.health
	cp.CatalogID = catalogID
	return &cp, nil
}

// newCatalogTestHandlerWithJobs wires a catalog store + the
// fake queue. Mirrors newCatalogTestHandler so existing
// scaffolding patterns transfer.
func newCatalogTestHandlerWithJobs(t *testing.T) (*Handler, *apicatalog.MemoryStore, *fakeEmbedJobs) {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	jobs := &fakeEmbedJobs{}
	h := NewHandler(Deps{
		APICatalogStore:   store,
		EmbedJobs:         jobs,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	return h, store, jobs
}

// minimalSpec is the smallest OpenAPI document the admin write
// path will accept. One GET operation so operation_count = 1
// and the reconciler has a non-zero target.
const minimalSpec = `openapi: 3.0.0
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses:
        "200":
          description: ok`

// TestSpecUpsert_EnqueuesJob covers the producer hook the queue
// architecture rests on: every spec write enqueues a pending
// job. Without this, the reconciler is the only path to
// indexed vectors and the operator's expectation that "saving
// the spec triggers indexing" silently breaks.
func TestSpecUpsert_EnqueuesJob(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     minimalSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
	if len(jobs.jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(jobs.jobs))
	}
	j := jobs.jobs[0]
	if j.CatalogID != "petstore" || j.SpecName != "default" || j.Kind != embedjobs.KindSpecWrite {
		t.Errorf("unexpected job %+v", j)
	}
}

// TestSpecUpsert_OperationCountStored proves the admin handler
// stamps the parsed operation count onto the spec row. Without
// this the reconciler cannot detect gaps in pure SQL.
func TestSpecUpsert_OperationCountStored(t *testing.T) {
	t.Parallel()
	h, store, _ := newCatalogTestHandlerWithJobs(t)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     minimalSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
	spec, err := store.GetSpec(context.Background(), "petstore", "default")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if spec.OperationCount != 1 {
		t.Errorf("OperationCount = %d; want 1", spec.OperationCount)
	}
}

// TestSpecUpsert_NoQueueIsNoOp proves the handler degrades
// gracefully when EmbedJobs is unwired (file mode / no DB).
// The spec write still succeeds; nothing is enqueued because
// there's no queue.
func TestSpecUpsert_NoQueueIsNoOp(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{ID: "p", Name: "p", DisplayName: "P"})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/d", map[string]any{
		"source_kind": "inline",
		"content":     minimalSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
}

// TestListCatalogEmbeddingStatuses_ReturnsRows drives the new
// admin endpoint that the portal polls every 5 seconds for the
// per-spec embedding badge.
func TestListCatalogEmbeddingStatuses_ReturnsRows(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	now := time.Now()
	jobs.statuses = []embedjobs.SpecStatusRow{
		{
			CatalogID:      "petstore",
			SpecName:       "users",
			OperationCount: 3,
			EmbeddingCount: 3,
			JobStatus:      embedjobs.StatusSucceeded,
			JobUpdatedAt:   &now,
		},
		{
			CatalogID:      "petstore",
			SpecName:       "orders",
			OperationCount: 5,
			EmbeddingCount: 2,
			JobStatus:      embedjobs.StatusRunning,
			JobAttempts:    1,
		},
	}
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-status", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status: %d %s", res.Code, res.Body.String())
	}
	var body struct {
		Specs []embeddingStatusResponse `json:"specs"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if len(body.Specs) != 2 {
		t.Fatalf("got %d rows, want 2", len(body.Specs))
	}
	if body.Specs[0].SpecName != "users" || body.Specs[0].EmbeddingCount != 3 {
		t.Errorf("unexpected first row %+v", body.Specs[0])
	}
	if body.Specs[0].JobUpdatedAt == "" {
		t.Error("job_updated_at not rendered")
	}
}

// TestListCatalogEmbeddingStatuses_StoreError surfaces a 500
// when the underlying query fails.
func TestListCatalogEmbeddingStatuses_StoreError(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	jobs.statusesErr = errors.New("query timeout")
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-status", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestGetCatalogEmbeddingHealth_ReturnsRollup drives the
// catalog-level health endpoint. The portal renders this at the
// top of the catalog editor.
func TestGetCatalogEmbeddingHealth_ReturnsRollup(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	jobs.health = &embedjobs.CatalogHealth{
		SpecsTotal: 9, SpecsIndexed: 7, SpecsPending: 1, SpecsRunning: 1, SpecsFailed: 0,
	}
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-health", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("health: %d %s", res.Code, res.Body.String())
	}
	var body embeddingHealthResponse
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if body.SpecsTotal != 9 || body.SpecsIndexed != 7 || body.SpecsPending != 1 || body.SpecsRunning != 1 {
		t.Errorf("unexpected health: %+v", body)
	}
}

// TestGetCatalogEmbeddingHealth_StoreError surfaces a 500 on
// query failure.
func TestGetCatalogEmbeddingHealth_StoreError(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	jobs.healthErr = errors.New("boom")
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-health", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestListCatalogEmbeddingJobs_FiltersByStatus drives the
// status filter that the admin job history view uses to find
// failed jobs in a busy queue.
func TestListCatalogEmbeddingJobs_FiltersByStatus(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	jobs.jobs = []embedjobs.Job{
		{ID: 1, CatalogID: "petstore", SpecName: "a", Status: embedjobs.StatusSucceeded, Kind: embedjobs.KindSpecWrite},
		{ID: 2, CatalogID: "petstore", SpecName: "b", Status: embedjobs.StatusFailed, Kind: embedjobs.KindReconciler, LastError: "provider 502"},
	}
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-jobs?status=failed", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("jobs: %d %s", res.Code, res.Body.String())
	}
	var body struct {
		Jobs []embeddingJobResponse `json:"jobs"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if len(body.Jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(body.Jobs))
	}
	if body.Jobs[0].Status != "failed" || body.Jobs[0].LastError != "provider 502" {
		t.Errorf("unexpected job: %+v", body.Jobs[0])
	}
}

// TestListCatalogEmbeddingJobs_FiltersBySpecName covers the
// per-spec history view (operator drills into "why did this
// spec fail").
func TestListCatalogEmbeddingJobs_FiltersBySpecName(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	jobs.jobs = []embedjobs.Job{
		{ID: 1, CatalogID: "petstore", SpecName: "a", Status: embedjobs.StatusSucceeded},
		{ID: 2, CatalogID: "petstore", SpecName: "b", Status: embedjobs.StatusSucceeded},
	}
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-jobs?spec_name=b", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("jobs: %d %s", res.Code, res.Body.String())
	}
	var body struct {
		Jobs []embeddingJobResponse `json:"jobs"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if len(body.Jobs) != 1 || body.Jobs[0].SpecName != "b" {
		t.Errorf("unexpected jobs: %+v", body.Jobs)
	}
}

// TestListCatalogEmbeddingJobs_StoreError surfaces 500.
func TestListCatalogEmbeddingJobs_StoreError(t *testing.T) {
	t.Parallel()
	h, _, jobs := newCatalogTestHandlerWithJobs(t)
	jobs.listErr = errors.New("boom")
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/embedding-jobs", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestManualRetryEmbedding_Enqueues202 covers the operator
// escape hatch. The endpoint returns 202 Accepted with the
// queue's created flag so the portal can render the queued
// state immediately.
func TestManualRetryEmbedding_Enqueues202(t *testing.T) {
	t.Parallel()
	h, store, jobs := newCatalogTestHandlerWithJobs(t)
	_ = store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "default", Content: minimalSpec, SourceKind: apicatalog.SourceInline,
	})
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/petstore/specs/default/reembed", nil)
	if res.Code != http.StatusAccepted {
		t.Fatalf("reembed: %d %s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if body["status"] != "queued" {
		t.Errorf("status=%v, want queued", body["status"])
	}
	if len(jobs.jobs) != 1 || jobs.jobs[0].Kind != embedjobs.KindManualRetry {
		t.Errorf("expected one manual_retry job, got %+v", jobs.jobs)
	}
}

// TestManualRetryEmbedding_MissingSpecReturns404 covers the
// 404 path when the operator names a spec that doesn't exist.
func TestManualRetryEmbedding_MissingSpecReturns404(t *testing.T) {
	t.Parallel()
	h, _, _ := newCatalogTestHandlerWithJobs(t)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/petstore/specs/ghost/reembed", nil)
	if res.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", res.Code)
	}
}

// TestManualRetryEmbedding_EnqueueErrorReturns500 covers the
// queue-write error path.
func TestManualRetryEmbedding_EnqueueErrorReturns500(t *testing.T) {
	t.Parallel()
	h, store, jobs := newCatalogTestHandlerWithJobs(t)
	_ = store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "default", Content: minimalSpec, SourceKind: apicatalog.SourceInline,
	})
	jobs.enqueueErr = errors.New("queue down")
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/petstore/specs/default/reembed", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestSpecToResponseWithEmbedding_PopulatesJobFields verifies
// the read-side hook that pulls job state into the spec list /
// detail response. The portal renders these fields directly so
// any drift in the wiring is operator-visible.
func TestSpecToResponseWithEmbedding_PopulatesJobFields(t *testing.T) {
	t.Parallel()
	h, store, jobs := newCatalogTestHandlerWithJobs(t)
	_ = store.UpsertSpec(context.Background(), "petstore", apicatalog.SpecEntry{
		SpecName: "default", Content: minimalSpec, SourceKind: apicatalog.SourceInline,
		OperationCount: 1,
	})
	jobs.jobs = []embedjobs.Job{
		{
			ID: 1, CatalogID: "petstore", SpecName: "default",
			Status: embedjobs.StatusRunning, Attempts: 2,
			Kind: embedjobs.KindSpecWrite,
		},
	}
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs/petstore/specs/default", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("get spec: %d %s", res.Code, res.Body.String())
	}
	var body specResponse
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	if body.OperationCount != 1 {
		t.Errorf("operation_count = %d, want 1", body.OperationCount)
	}
	if body.EmbeddingStatus != "running" || body.EmbeddingAttempts != 2 {
		t.Errorf("missing job state on spec response: %+v", body)
	}
}
