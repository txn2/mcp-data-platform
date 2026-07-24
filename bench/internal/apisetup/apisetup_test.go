package apisetup

// Registration flows are exercised against a fake admin API that records
// requests and replays the platform's response contract (including the
// 409-idempotency path and the embed-drain polling states).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAdmin records admin API calls.
type fakeAdmin struct {
	catalogStatus int
	calls         []string
	bodies        map[string]map[string]any
	embedPolls    atomic.Int32
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{catalogStatus: http.StatusCreated, bodies: map[string]map[string]any{}}
}

func (f *fakeAdmin) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	record := func(r *http.Request) string {
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.bodies[key] = body
		if r.Header.Get("Authorization") != "Bearer admin-key" {
			t.Errorf("%s: missing admin bearer", key)
		}
		return key
	}
	mux.HandleFunc("POST /api/v1/admin/api-catalogs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(f.catalogStatus)
	})
	mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("PUT /api/v1/admin/connection-instances/{kind}/{name}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-status", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		n := f.embedPolls.Add(1)
		count := 10
		if n >= 2 {
			count = 53
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"specs": []map[string]any{
			{"spec_name": CatalogID, "operation_count": 53, "embedding_count": count, "job_status": "running"},
		}})
	})
	return mux
}

func TestRegisterB1(t *testing.T) {
	fake := newFakeAdmin()
	ts := httptest.NewServer(fake.handler(t))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "admin-key", 5*time.Second)
	if err := c.RegisterB1(context.Background(), `{"openapi":"3.0.3"}`, "http://fixture:8110", "fk"); err != nil {
		t.Fatal(err)
	}
	spec := fake.bodies["PUT /api/v1/admin/api-catalogs/"+CatalogID+"/specs/"+CatalogID]
	if spec["source_kind"] != "inline" || spec["content"] == "" {
		t.Errorf("spec upsert body = %v", spec)
	}
	conn := fake.bodies["PUT /api/v1/admin/connection-instances/api/"+ConnectionName]
	cfg, _ := conn["config"].(map[string]any)
	if cfg["base_url"] != "http://fixture:8110" || cfg["auth_mode"] != "api_key" || cfg["catalog_id"] != CatalogID {
		t.Errorf("connection config = %v", cfg)
	}
}

// TestRegisterB1CatalogConflictIsIdempotent: a rerun against an existing
// catalog proceeds to the spec and connection steps.
func TestRegisterB1CatalogConflictIsIdempotent(t *testing.T) {
	fake := newFakeAdmin()
	fake.catalogStatus = http.StatusConflict
	ts := httptest.NewServer(fake.handler(t))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "admin-key", 5*time.Second)
	if err := c.RegisterB1(context.Background(), `{}`, "http://fixture:8110", "fk"); err != nil {
		t.Fatalf("conflict not treated as idempotent: %v", err)
	}
	if len(fake.calls) != 3 {
		t.Errorf("calls = %v, want catalog+spec+connection", fake.calls)
	}
}

func TestRegisterB0(t *testing.T) {
	fake := newFakeAdmin()
	ts := httptest.NewServer(fake.handler(t))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "admin-key", 5*time.Second)
	if err := c.RegisterB0(context.Background(), "http://epmcp:8111"); err != nil {
		t.Fatal(err)
	}
	conn := fake.bodies["PUT /api/v1/admin/connection-instances/mcp/"+ConnectionName]
	cfg, _ := conn["config"].(map[string]any)
	if cfg["endpoint"] != "http://epmcp:8111" {
		t.Errorf("connection config = %v", cfg)
	}
}

// TestWaitEmbedDrain polls until the fake reports full coverage.
func TestWaitEmbedDrain(t *testing.T) {
	fake := newFakeAdmin()
	ts := httptest.NewServer(fake.handler(t))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "admin-key", 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.WaitEmbedDrain(ctx, 53, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if fake.embedPolls.Load() < 2 {
		t.Errorf("drained after %d polls, want at least 2", fake.embedPolls.Load())
	}
}
