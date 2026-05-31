package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// fakeIndexJobs is a configurable IndexJobsService for handler tests.
// It records the last List filter and Reindex args so tests can assert
// query-param plumbing.
type fakeIndexJobs struct {
	kinds           []string
	counts          map[string]*indexjobs.KindCounts
	countsErr       error
	coverage        map[string]*indexjobs.Coverage
	coverageErr     error
	jobs            []indexjobs.Job
	listErr         error
	lastFilter      indexjobs.ListFilter
	failures        []indexjobs.FailedUnit
	failuresErr     error
	lastFailKind    string
	lastFailLimit   int
	reindexIDs      []string
	reindexErr      error
	lastKind        string
	lastSource      string
	resolved        int
	resolveErr      error
	lastResolveKind string
	lastResolveSrc  string
}

func (f *fakeIndexJobs) Kinds() []string { return f.kinds }

func (f *fakeIndexJobs) Counts(_ context.Context, kind string) (*indexjobs.KindCounts, error) {
	if f.countsErr != nil {
		return nil, f.countsErr
	}
	if c, ok := f.counts[kind]; ok {
		return c, nil
	}
	return &indexjobs.KindCounts{SourceKind: kind}, nil
}

func (f *fakeIndexJobs) Coverage(_ context.Context, kind string) (*indexjobs.Coverage, error) {
	if f.coverageErr != nil {
		return nil, f.coverageErr
	}
	return f.coverage[kind], nil
}

func (f *fakeIndexJobs) List(_ context.Context, filter indexjobs.ListFilter) ([]indexjobs.Job, error) {
	f.lastFilter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.jobs, nil
}

func (f *fakeIndexJobs) ActiveFailures(_ context.Context, kind string, limit int) ([]indexjobs.FailedUnit, error) {
	f.lastFailKind, f.lastFailLimit = kind, limit
	if f.failuresErr != nil {
		return nil, f.failuresErr
	}
	return f.failures, nil
}

func (f *fakeIndexJobs) Reindex(_ context.Context, kind, sourceID string) ([]string, error) {
	f.lastKind, f.lastSource = kind, sourceID
	return f.reindexIDs, f.reindexErr
}

func (f *fakeIndexJobs) Resolve(_ context.Context, kind, sourceID string) (int, error) {
	f.lastResolveKind, f.lastResolveSrc = kind, sourceID
	return f.resolved, f.resolveErr
}

func indexJobsTestHandler(svc IndexJobsService, prov *stubProvider) *Handler {
	deps := Deps{IndexJobs: svc}
	if prov != nil {
		deps.Embedder = prov
	}
	return NewHandler(deps, nil)
}

func TestIndexJobsSummary_NilService(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(nil, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got indexJobsSummaryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Provider.Status != embeddingStatusUnconfigured {
		t.Errorf("provider status = %q; want unconfigured", got.Provider.Status)
	}
	if len(got.Kinds) != 0 {
		t.Errorf("kinds = %v; want empty", got.Kinds)
	}
}

func TestIndexJobsSummary_WithKinds(t *testing.T) {
	t.Parallel()
	activity := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	svc := &fakeIndexJobs{
		kinds: []string{"api_catalog", "tools"},
		counts: map[string]*indexjobs.KindCounts{
			"api_catalog": {SourceKind: "api_catalog", Succeeded: 4, Failed: 1, LastActivity: &activity},
			"tools":       {SourceKind: "tools", Running: 1},
		},
		coverage: map[string]*indexjobs.Coverage{
			"api_catalog": {Indexed: 18, Expected: 20, ExpectedKnown: true},
			"tools":       {Indexed: 50, ExpectedKnown: false},
		},
	}
	h := indexJobsTestHandler(svc, &stubProvider{kind: "ollama", model: "nomic-embed-text", dim: 768})
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got indexJobsSummaryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Provider.Status != embeddingStatusOK {
		t.Errorf("provider status = %q; want ok", got.Provider.Status)
	}
	if len(got.Kinds) != 2 {
		t.Fatalf("kinds = %d; want 2", len(got.Kinds))
	}
	api := got.Kinds[0]
	if api.Kind != "api_catalog" || api.Succeeded != 4 || api.Failed != 1 {
		t.Errorf("api_catalog summary = %+v", api)
	}
	if api.Coverage == nil || api.Coverage.Indexed != 18 || api.Coverage.Expected != 20 || !api.Coverage.ExpectedKnown {
		t.Errorf("api_catalog coverage = %+v", api.Coverage)
	}
	if api.LastActivity == nil || *api.LastActivity != "2026-05-30T12:00:00Z" {
		t.Errorf("last_activity = %v; want 2026-05-30T12:00:00Z", api.LastActivity)
	}
	tools := got.Kinds[1]
	if tools.Coverage == nil || tools.Coverage.ExpectedKnown {
		t.Errorf("tools coverage = %+v; want ExpectedKnown false", tools.Coverage)
	}
}

func TestIndexJobsSummary_CountsError(t *testing.T) {
	t.Parallel()
	svc := &fakeIndexJobs{kinds: []string{"tools"}, countsErr: errors.New("db down")}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestIndexJobsSummary_CoverageError(t *testing.T) {
	t.Parallel()
	svc := &fakeIndexJobs{
		kinds:       []string{"tools"},
		counts:      map[string]*indexjobs.KindCounts{"tools": {SourceKind: "tools"}},
		coverageErr: errors.New("boom"),
	}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestListIndexJobs_NilService(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(nil, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs/jobs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got map[string][]indexJobResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got["jobs"]) != 0 {
		t.Errorf("jobs = %v; want empty", got["jobs"])
	}
}

func TestListIndexJobs_MapsFieldsAndFilter(t *testing.T) {
	t.Parallel()
	lease := time.Date(2026, 5, 30, 13, 0, 0, 0, time.UTC)
	started := time.Date(2026, 5, 30, 12, 30, 0, 0, time.UTC)
	svc := &fakeIndexJobs{jobs: []indexjobs.Job{{
		ID: 1, SourceKind: "tools", SourceID: "platform", Trigger: indexjobs.TriggerReconciler,
		Status: indexjobs.StatusRunning, Attempts: 2, LastError: "timeout",
		NextRunAt: started, WorkerID: "w1", LeaseExpiresAt: &lease,
		CreatedAt: started, StartedAt: &started, ItemsDone: 7,
	}}}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodGet,
		"/api/v1/admin/index-jobs/jobs?kind=tools&status=running&source_id=platform&limit=10", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body %s", res.Code, res.Body.String())
	}
	if svc.lastFilter.SourceKind != "tools" || svc.lastFilter.Status != indexjobs.StatusRunning ||
		svc.lastFilter.SourceID != "platform" || svc.lastFilter.Limit != 10 {
		t.Errorf("filter = %+v", svc.lastFilter)
	}
	var got map[string][]indexJobResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	jobs := got["jobs"]
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d; want 1", len(jobs))
	}
	j := jobs[0]
	if j.Status != "running" || j.Attempts != 2 || j.LastError != "timeout" || j.ItemsDone != 7 {
		t.Errorf("job = %+v", j)
	}
	if j.LeaseExpiresAt == nil || *j.LeaseExpiresAt != "2026-05-30T13:00:00Z" {
		t.Errorf("lease = %v", j.LeaseExpiresAt)
	}
	if j.StartedAt == nil || j.CompletedAt != nil {
		t.Errorf("started=%v completed=%v; want started set, completed nil", j.StartedAt, j.CompletedAt)
	}
}

func TestListIndexJobs_InvalidStatus(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{}, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs/jobs?status=bogus", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestListIndexJobs_ListError(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{listErr: errors.New("boom")}, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs/jobs", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestReindex_NilService(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(nil, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/reindex",
		reindexRequest{Kind: "tools"})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", res.Code)
	}
}

func TestReindex_BadBody(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/reindex", "not-json-object-but-string")
	// A bare JSON string fails to decode into the struct? It actually
	// decodes as a type error; assert non-2xx.
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestReindex_MissingKind(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/reindex", reindexRequest{})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestReindex_UnknownKind(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{reindexErr: indexjobs.ErrUnknownKind}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/reindex", reindexRequest{Kind: "ghost"})
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", res.Code)
	}
}

func TestReindex_ServiceError(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{reindexErr: errors.New("boom")}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/reindex", reindexRequest{Kind: "tools"})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestReindex_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeIndexJobs{reindexIDs: []string{"c|v1", "c|v2"}}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/reindex",
		reindexRequest{Kind: "api_catalog", SourceID: ""})
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d; want 202; body %s", res.Code, res.Body.String())
	}
	if svc.lastKind != "api_catalog" {
		t.Errorf("reindex kind = %q", svc.lastKind)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	count, _ := got["count"].(float64)
	if got["status"] != "queued" || count != 2 {
		t.Errorf("response = %+v", got)
	}
}

func TestParseIndexJobsLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
	}{
		{"", indexJobsListDefaultLimit},
		{"abc", indexJobsListDefaultLimit},
		{"0", indexJobsListDefaultLimit},
		{"-5", indexJobsListDefaultLimit},
		{"25", 25},
		{"9999", indexJobsListMaxLimit},
	}
	for _, c := range cases {
		if got := parseIndexJobsLimit(c.in); got != c.want {
			t.Errorf("parseIndexJobsLimit(%q) = %d; want %d", c.in, got, c.want)
		}
	}
}

func TestValidJobStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"pending", "running", "succeeded", "failed"} {
		if !validJobStatus(s) {
			t.Errorf("validJobStatus(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"", "queued", "done"} {
		if validJobStatus(s) {
			t.Errorf("validJobStatus(%q) = true; want false", s)
		}
	}
}

func TestListIndexJobsFailures_NilService(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(nil, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs/failures", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got map[string][]failedUnitResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got["failures"]) != 0 {
		t.Errorf("failures = %v; want empty", got["failures"])
	}
}

func TestListIndexJobsFailures_MapsUnits(t *testing.T) {
	t.Parallel()
	first := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	last := time.Date(2026, 5, 30, 11, 0, 0, 0, time.UTC)
	succeeded := time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)
	svc := &fakeIndexJobs{failures: []indexjobs.FailedUnit{
		{
			SourceKind: "tools", SourceID: "platform", LatestJobID: 12, LastError: "ollama timeout",
			Attempts: 5, Occurrences: 3, FirstFailedAt: first, LastFailedAt: last, LastSucceededAt: &succeeded,
		},
		{
			SourceKind: "api_catalog", SourceID: "c|v1", LatestJobID: 8, LastError: "no consumer",
			Attempts: 5, Occurrences: 1, FirstFailedAt: last, LastFailedAt: last,
		},
	}}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs/failures?kind=tools&limit=10", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body %s", res.Code, res.Body.String())
	}
	if svc.lastFailKind != "tools" || svc.lastFailLimit != 10 {
		t.Errorf("ActiveFailures args = %q/%d; want tools/10", svc.lastFailKind, svc.lastFailLimit)
	}
	var got map[string][]failedUnitResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	f := got["failures"]
	if len(f) != 2 {
		t.Fatalf("failures = %d; want 2", len(f))
	}
	if f[0].SourceID != "platform" || f[0].LatestJobID != 12 || f[0].Occurrences != 3 {
		t.Errorf("failure[0] = %+v", f[0])
	}
	if f[0].FirstFailedAt != first.Format(time.RFC3339) || f[0].LastFailedAt != last.Format(time.RFC3339) {
		t.Errorf("failure[0] timestamps = %q / %q", f[0].FirstFailedAt, f[0].LastFailedAt)
	}
	if f[0].LastSucceededAt == nil || *f[0].LastSucceededAt != succeeded.Format(time.RFC3339) {
		t.Errorf("failure[0] LastSucceededAt = %v", f[0].LastSucceededAt)
	}
	if f[1].LastSucceededAt != nil {
		t.Errorf("failure[1] LastSucceededAt = %v; want nil (never succeeded)", f[1].LastSucceededAt)
	}
}

func TestListIndexJobsFailures_ServiceError(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{failuresErr: errors.New("boom")}, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs/failures", nil)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestDismiss_NilService(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(nil, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/dismiss",
		dismissRequest{Kind: "tools", SourceID: "platform"})
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409", res.Code)
	}
}

func TestDismiss_BadBody(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/dismiss", "not-an-object")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", res.Code)
	}
}

func TestDismiss_MissingFields(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{}, nil)
	for _, req := range []dismissRequest{{Kind: "tools"}, {SourceID: "platform"}, {}} {
		res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/dismiss", req)
		if res.Code != http.StatusBadRequest {
			t.Errorf("dismiss %+v status = %d; want 400", req, res.Code)
		}
	}
}

func TestDismiss_ServiceError(t *testing.T) {
	t.Parallel()
	h := indexJobsTestHandler(&fakeIndexJobs{resolveErr: errors.New("boom")}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/dismiss",
		dismissRequest{Kind: "tools", SourceID: "platform"})
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", res.Code)
	}
}

func TestDismiss_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeIndexJobs{resolved: 2}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/index-jobs/dismiss",
		dismissRequest{Kind: "tools", SourceID: "platform"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body %s", res.Code, res.Body.String())
	}
	if svc.lastResolveKind != "tools" || svc.lastResolveSrc != "platform" {
		t.Errorf("resolve args = %q/%q", svc.lastResolveKind, svc.lastResolveSrc)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	resolved, _ := got["resolved"].(float64)
	if got["status"] != "resolved" || resolved != 2 {
		t.Errorf("response = %+v", got)
	}
}

func TestIndexJobsSummary_VerdictAndUnresolved(t *testing.T) {
	t.Parallel()
	activity := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	svc := &fakeIndexJobs{
		kinds: []string{"degraded_kind", "idle_kind"},
		counts: map[string]*indexjobs.KindCounts{
			// Idle with open failures and full coverage -> degraded.
			"degraded_kind": {SourceKind: "degraded_kind", Succeeded: 5, Failed: 1, UnresolvedFailures: 1, LastActivity: &activity},
			// 100% coverage, no job history (seeded outside the queue) ->
			// the single resting state (healthy), same as any other
			// fully-indexed kind; must NOT read as "never".
			"idle_kind": {SourceKind: "idle_kind"},
		},
		coverage: map[string]*indexjobs.Coverage{
			"degraded_kind": {Indexed: 5, Expected: 5, ExpectedKnown: true},
			"idle_kind":     {Indexed: 34, Expected: 34, ExpectedKnown: true},
		},
	}
	h := indexJobsTestHandler(svc, nil)
	res := doJSON(t, h, http.MethodGet, "/api/v1/admin/index-jobs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	var got indexJobsSummaryResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byKind := map[string]indexKindSummary{}
	for _, k := range got.Kinds {
		byKind[k.Kind] = k
	}
	if byKind["degraded_kind"].Verdict != string(indexjobs.VerdictDegraded) {
		t.Errorf("degraded_kind verdict = %q; want degraded", byKind["degraded_kind"].Verdict)
	}
	if byKind["degraded_kind"].UnresolvedFailures != 1 {
		t.Errorf("degraded_kind unresolved = %d; want 1", byKind["degraded_kind"].UnresolvedFailures)
	}
	if byKind["idle_kind"].Verdict != string(indexjobs.VerdictHealthy) {
		t.Errorf("idle_kind verdict = %q; want healthy", byKind["idle_kind"].Verdict)
	}
	if byKind["idle_kind"].LastActivity != nil {
		t.Errorf("idle_kind last_activity = %v; want nil", byKind["idle_kind"].LastActivity)
	}
}
