package auditapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAudit serves paginated events per session with optional delayed
// visibility (simulating rows landing over time).
type fakeAudit struct {
	mu     sync.Mutex
	events []Event
}

func (f *fakeAudit) add(e Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeAudit) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("missing bearer auth: %q", r.Header.Get("Authorization"))
		}
		session := r.URL.Query().Get("session_id")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		f.mu.Lock()
		var matched []Event
		for _, e := range f.events {
			if e.SessionID == session {
				matched = append(matched, e)
			}
		}
		f.mu.Unlock()
		start := (page - 1) * perPage
		end := min(start+perPage, len(matched))
		var pageData []Event
		if start < len(matched) {
			pageData = matched[start:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": pageData, "total": len(matched), "page": page, "per_page": perPage,
		})
	}
}

// authedClient wraps the test server with the Bearer credential.
type authedTransport struct{ base http.RoundTripper }

func (a authedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer key")
	return a.base.RoundTrip(r)
}

func newTestClient(t *testing.T, f *fakeAudit) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return New(srv.URL, &http.Client{Transport: authedTransport{base: http.DefaultTransport}})
}

func TestEventsForSessionPaginates(t *testing.T) {
	f := &fakeAudit{}
	for range 450 { // > 2 pages at pageSize 200
		f.add(Event{SessionID: "dps_x", ToolName: "t", Success: true, DurationMS: 1})
	}
	f.add(Event{SessionID: "dps_other", ToolName: "t"})
	c := newTestClient(t, f)
	events, err := c.EventsForSession(context.Background(), "dps_x")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 450 {
		t.Errorf("got %d events, want 450", len(events))
	}
}

func TestWaitForSessionConverges(t *testing.T) {
	f := &fakeAudit{}
	f.add(Event{SessionID: "dps_x", Success: true})
	c := newTestClient(t, f)
	go func() {
		time.Sleep(300 * time.Millisecond)
		f.add(Event{SessionID: "dps_x", Success: true})
	}()
	events, err := c.WaitForSession(context.Background(), "dps_x", 2, 2, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("got %d, want 2", len(events))
	}
}

func TestWaitForSessionTimeoutIsLoud(t *testing.T) {
	f := &fakeAudit{}
	f.add(Event{SessionID: "dps_x", Success: true})
	c := newTestClient(t, f)
	_, err := c.WaitForSession(context.Background(), "dps_x", 3, 3, 600*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "got 1 of at least 3") {
		t.Fatalf("want loud missing-rows error, got %v", err)
	}
}

func TestWaitForSessionOvercountFails(t *testing.T) {
	f := &fakeAudit{}
	f.add(Event{SessionID: "dps_x"})
	f.add(Event{SessionID: "dps_x"})
	c := newTestClient(t, f)
	if _, err := c.WaitForSession(context.Background(), "dps_x", 1, 1, time.Second); err == nil ||
		!strings.Contains(err.Error(), "overcount") {
		t.Fatalf("want overcount error, got %v", err)
	}
}

func TestWaitForSessionZeroExpected(t *testing.T) {
	c := newTestClient(t, &fakeAudit{})
	events, err := c.WaitForSession(context.Background(), "dps_x", 0, 0, time.Second)
	if err != nil || len(events) != 0 {
		t.Fatalf("zero-expected should return immediately: %v %v", events, err)
	}
}

func TestWaitForSessionIndeterminateBand(t *testing.T) {
	// min=1 confirmed, max=2 (one indeterminate call): with exactly one row
	// present and stable, the wait accepts without demanding the second.
	f := &fakeAudit{}
	f.add(Event{SessionID: "dps_x", Success: true})
	c := newTestClient(t, f)
	events, err := c.WaitForSession(context.Background(), "dps_x", 1, 2, 5*time.Second)
	if err != nil || len(events) != 1 {
		t.Fatalf("stable in-band count rejected: %v %v", events, err)
	}
	// A third row exceeds max: the accounting is wrong and must fail.
	f.add(Event{SessionID: "dps_x"})
	f.add(Event{SessionID: "dps_x"})
	if _, err := c.WaitForSession(context.Background(), "dps_x", 1, 2, time.Second); err == nil ||
		!strings.Contains(err.Error(), "overcount") {
		t.Fatalf("want overcount error, got %v", err)
	}
}

func TestClientErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, srv.Client())
	if _, err := c.EventsForSession(context.Background(), "dps_x"); err == nil ||
		!strings.Contains(err.Error(), "status 500") {
		t.Fatalf("want status error, got %v", err)
	}
}

func TestSummarize(t *testing.T) {
	events := []Event{
		{Success: true, DurationMS: 10, EnrichmentApplied: true, EnrichmentTokensFull: 300, EnrichmentTokensDedup: 100},
		{Success: false, DurationMS: 5},
		{Success: true, DurationMS: 20, EnrichmentApplied: true, EnrichmentTokensFull: 200, EnrichmentTokensDedup: 50},
	}
	m := Summarize(events)
	want := Metrics{AuditedCalls: 3, Errors: 1, TotalDurationMS: 35, EnrichedCalls: 2, EnrichmentTokensFull: 500, EnrichmentTokensDedup: 150}
	if m != want {
		t.Errorf("Summarize = %+v, want %+v", m, want)
	}
}
