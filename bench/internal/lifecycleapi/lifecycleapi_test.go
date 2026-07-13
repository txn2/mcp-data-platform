package lifecycleapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeKnowledge serves the admin insights + changesets endpoints over httptest.
type fakeKnowledge struct {
	insights   []Insight
	changesets []Changeset
	approvals  map[string]string // id -> status set via PUT
	lastQuery  map[string]string // last insights query params seen
}

func newFake() *fakeKnowledge {
	return &fakeKnowledge{approvals: map[string]string{}, lastQuery: map[string]string{}}
}

func (f *fakeKnowledge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights", f.listInsights)
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights/{id}", f.getInsight)
	mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}/status", f.putStatus)
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets", f.listChangesets)
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets/{id}", f.getChangeset)
	return mux
}

func (f *fakeKnowledge) listInsights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for _, k := range []string{"captured_by", "entity_urn", "status"} {
		if v := q.Get(k); v != "" {
			f.lastQuery[k] = v
		}
	}
	var filtered []Insight
	for _, in := range f.insights {
		if v := q.Get("captured_by"); v != "" && in.CapturedBy != v {
			continue
		}
		if v := q.Get("status"); v != "" && in.Status != v {
			continue
		}
		if v := q.Get("entity_urn"); v != "" && !in.LinksEntity(v) {
			continue
		}
		filtered = append(filtered, in)
	}
	writePage(w, r, filtered)
}

func (f *fakeKnowledge) getInsight(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, in := range f.insights {
		if in.ID == id {
			writeJSON(w, in)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (f *fakeKnowledge) putStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, _ := io.ReadAll(r.Body)
	var upd statusUpdate
	if err := json.Unmarshal(body, &upd); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.approvals[id] = upd.Status
	for i := range f.insights {
		if f.insights[i].ID == id {
			f.insights[i].Status = upd.Status
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (f *fakeKnowledge) listChangesets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var filtered []Changeset
	for _, cs := range f.changesets {
		if v := q.Get("applied_by"); v != "" && cs.AppliedBy != v {
			continue
		}
		if v := q.Get("entity_urn"); v != "" && cs.TargetURN != v {
			continue
		}
		filtered = append(filtered, cs)
	}
	writeChangesetPage(w, r, filtered)
}

func (f *fakeKnowledge) getChangeset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, cs := range f.changesets {
		if cs.ID == id {
			writeJSON(w, cs)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

// writePage returns one page of insights honoring page/per_page, so pagination
// is exercised.
func writePage(w http.ResponseWriter, r *http.Request, all []Insight) {
	page, per := pageParams(r)
	lo, hi := window(len(all), page, per)
	writeJSON(w, insightEnvelope{Data: all[lo:hi], Total: len(all)})
}

func writeChangesetPage(w http.ResponseWriter, r *http.Request, all []Changeset) {
	page, per := pageParams(r)
	lo, hi := window(len(all), page, per)
	writeJSON(w, changesetEnvelope{Data: all[lo:hi], Total: len(all)})
}

func pageParams(r *http.Request) (page, per int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	per, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if per < 1 {
		per = 20
	}
	return page, per
}

func window(n, page, per int) (lo, hi int) {
	lo = min((page-1)*per, n)
	hi = min(lo+per, n)
	return lo, hi
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newClient(t *testing.T, f *fakeKnowledge) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client())
}

func TestListInsightsFiltersAndPaginates(t *testing.T) {
	f := newFake()
	// 150 insights for the teacher over two pages, plus one for another user.
	for i := range 150 {
		f.insights = append(f.insights, Insight{
			ID: "in-" + strconv.Itoa(i), CapturedBy: "teacher@apikey.local",
			Status: "pending", EntityURNs: []string{"urn:orders"},
		})
	}
	f.insights = append(f.insights, Insight{ID: "other", CapturedBy: "learner@apikey.local", Status: "pending"})

	got, err := newClient(t, f).ListInsights(context.Background(), InsightFilter{
		CapturedBy: "teacher@apikey.local", EntityURN: "urn:orders", Status: "pending",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 150 {
		t.Fatalf("got %d insights, want 150 (pagination or filter wrong)", len(got))
	}
	if f.lastQuery["captured_by"] != "teacher@apikey.local" || f.lastQuery["entity_urn"] != "urn:orders" {
		t.Fatalf("filters not forwarded: %v", f.lastQuery)
	}
}

func TestGetInsight(t *testing.T) {
	f := newFake()
	f.insights = []Insight{{ID: "in-1", Status: "applied", ChangesetRef: "cs-1"}}
	got, err := newClient(t, f).GetInsight(context.Background(), "in-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "applied" || got.ChangesetRef != "cs-1" {
		t.Fatalf("unexpected insight: %+v", got)
	}
}

func TestGetInsightNotFound(t *testing.T) {
	if _, err := newClient(t, newFake()).GetInsight(context.Background(), "missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestApproveSetsStatus(t *testing.T) {
	f := newFake()
	f.insights = []Insight{{ID: "in-1", Status: "pending"}}
	if err := newClient(t, f).Approve(context.Background(), "in-1", "looks good"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if f.approvals["in-1"] != "approved" {
		t.Fatalf("approve set status %q, want approved", f.approvals["in-1"])
	}
}

func TestChangesetsAndSourced(t *testing.T) {
	f := newFake()
	f.changesets = []Changeset{
		{ID: "cs-1", TargetURN: "urn:orders", AppliedBy: "admin@apikey.local",
			SourceInsightIDs: []string{"in-1"}, ChangeType: "update_description"},
		{ID: "cs-2", TargetURN: "urn:other", AppliedBy: "someone@apikey.local"},
	}
	c := newClient(t, f)
	byAdmin, err := c.ListChangesets(context.Background(), ChangesetFilter{AppliedBy: "admin@apikey.local"})
	if err != nil {
		t.Fatalf("list changesets: %v", err)
	}
	if len(byAdmin) != 1 || !byAdmin[0].Sourced("in-1") {
		t.Fatalf("unexpected changesets: %+v", byAdmin)
	}
	cs, err := c.GetChangeset(context.Background(), "cs-1")
	if err != nil {
		t.Fatalf("get changeset: %v", err)
	}
	if cs.RolledBack || !cs.Sourced("in-1") {
		t.Fatalf("unexpected changeset: %+v", cs)
	}
}

func TestServerErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, srv.Client())
	if _, err := c.ListInsights(context.Background(), InsightFilter{}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}
