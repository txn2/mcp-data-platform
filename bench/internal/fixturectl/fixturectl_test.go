package fixturectl

// The client and the state grader are exercised against the real fixture
// service, mirroring how the runner uses them: reset, mutate over HTTP,
// grade the post-run dump.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

func newFixture(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(apisvc.New(apisvc.Options{APIKey: "k"}))
	t.Cleanup(ts.Close)
	return New(ts.URL, "k", 10*time.Second), ts
}

// post sends one JSON mutation to the fixture surface.
func post(t *testing.T, ts *httptest.Server, method, path string, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("%s %s: status %d", method, path, res.StatusCode)
	}
}

// TestGradeStateEndToEnd runs the real p3 grading loop: reset, perform
// the mutation a correct agent would, grade every state task whose
// mutation was performed, and verify a do-nothing episode fails.
func TestGradeStateEndToEnd(t *testing.T) {
	client, ts := newFixture(t)
	ctx := context.Background()
	ds := gen.Generate()
	var pending gen.Order
	for _, o := range ds.Orders {
		if o.Status == "pending" {
			pending = o
			break
		}
	}
	checks := []task.StateCheck{{Resource: "orders", ID: int64(pending.ID), Fields: map[string]any{"status": "canceled"}}}

	// Do-nothing episode: grade must fail.
	if err := client.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	ok, detail, err := client.GradeState(ctx, checks)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("do-nothing episode graded correct")
	}
	if detail == "" {
		t.Error("failed grade carries no detail")
	}

	// Correct episode: cancel, then grade passes.
	post(t, ts, http.MethodPost, "/commerce/orders/"+itoa(pending.ID)+":cancel", map[string]any{})
	ok, detail, err = client.GradeState(ctx, checks)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("correct episode graded wrong: %s", detail)
	}

	// Existence-mode check for a created row.
	post(t, ts, http.MethodPost, "/commerce/orders", map[string]any{"customer_id": 12, "amount": 15000})
	ok, detail, err = client.GradeState(ctx, []task.StateCheck{{
		Resource: "orders",
		Fields:   map[string]any{"customer_id": 12, "amount": 15000, "status": "pending"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("creation check failed: %s", detail)
	}

	// Reset isolates the next attempt.
	if err := client.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ = client.GradeState(ctx, checks); ok {
		t.Fatal("mutation survived reset")
	}
}

// TestRequestsLog verifies the access log surfaces through the client.
func TestRequestsLog(t *testing.T) {
	client, ts := newFixture(t)
	ctx := context.Background()
	if err := client.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/crm/customers/1", nil)
	req.Header.Set("X-API-Key", "k")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	reqs, err := client.Requests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].OperationID != "get_customer" {
		t.Fatalf("requests = %+v, want one get_customer", reqs)
	}
}

// TestEveryStateTaskGradable asserts each generated state task's checks
// evaluate cleanly against a fresh dump (wrong pre-mutation, no
// evaluation errors), so no task ships with an ungradable check.
func TestEveryStateTaskGradable(t *testing.T) {
	client, _ := newFixture(t)
	ctx := context.Background()
	c := apigen.BuildCatalog()
	for _, tk := range apigen.Tasks(apigen.GenerateState(c)) {
		if tk.Grading.Kind != task.GradeState {
			continue
		}
		ok, detail, err := client.GradeState(ctx, tk.Grading.StateChecks)
		if err != nil {
			t.Errorf("task %s: grading error: %v", tk.ID, err)
			continue
		}
		if ok {
			t.Errorf("task %s: passes without any mutation", tk.ID)
		}
		if detail == "" {
			t.Errorf("task %s: no failure detail", tk.ID)
		}
	}
}

// itoa is a tiny strconv wrapper for path building.
func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// TestAccessorsAndErrors covers the b2 context accessors and the error
// contract: auth failure, unknown collection, and a dead endpoint.
func TestAccessorsAndErrors(t *testing.T) {
	client, ts := newFixture(t)
	ctx := context.Background()
	if client.BaseURL() != ts.URL || client.APIKey() != "k" {
		t.Errorf("accessors = %q/%q", client.BaseURL(), client.APIKey())
	}
	if _, err := client.StateDump(ctx, "nope/nothing"); err == nil {
		t.Error("unknown collection dump returned nil error")
	}
	bad := New(ts.URL, "wrong-key", 5*time.Second)
	if err := bad.Reset(ctx); err == nil {
		t.Error("wrong key reset returned nil error")
	}
	if _, err := bad.Requests(ctx); err == nil {
		t.Error("wrong key requests returned nil error")
	}
	ts.Close()
	if err := client.Reset(ctx); err == nil {
		t.Error("dead endpoint reset returned nil error")
	}
}

// TestValueEqualSpread covers the numeric-type normalization between
// JSON dumps and YAML-loaded checks.
func TestValueEqualSpread(t *testing.T) {
	cases := []struct {
		got, want any
		equal     bool
	}{
		{float64(15000), int64(15000), true},
		{float64(15000), 15000, true},
		{json.Number("15000"), int64(15000), true},
		{json.Number("bogus"), int64(15000), false},
		{"pending", "pending", true},
		{"pending", "canceled", false},
		{float64(1), "1", false},
	}
	for _, c := range cases {
		if got := valueEqual(c.got, c.want); got != c.equal {
			t.Errorf("valueEqual(%v, %v) = %v, want %v", c.got, c.want, got, c.equal)
		}
	}
}
