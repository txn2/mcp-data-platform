package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/scrape"
	"github.com/txn2/mcp-data-platform/test/load/internal/stats"
)

func sampleReport() *Report {
	return &Report{
		Scenario:    "mcp-tool-call",
		Description: "desc",
		StartedAt:   time.Unix(1000, 0),
		FinishedAt:  time.Unix(1030, 0),
		WallSeconds: 30,
		Config: Config{
			Target: "http://localhost:8099", AuthMode: "apikey",
			Concurrency: 16, DurationSec: 30, ReleaseBuild: true,
		},
		Operations: []stats.OpSummary{
			{Operation: "search", Count: 300, ThroughputPerSec: 10, P50Ms: 12, P95Ms: 30, P99Ms: 50, MaxMs: 80},
		},
		Scrapes: []scrape.Snapshot{
			{Label: "before", Values: map[string]float64{"go_goroutines": 40, "audit_events_dropped_total": 0}},
			{Label: "after", Values: map[string]float64{"go_goroutines": 44, "audit_events_dropped_total": 12}},
		},
		Assertions: []Assertion{{Name: "error-rate:search", Passed: true, Message: "ok"}},
		Passed:     true,
	}
}

func TestComputeDeltas(t *testing.T) {
	r := sampleReport()
	r.ComputeDeltas()
	if r.Deltas["audit_events_dropped_total"] != 12 {
		t.Errorf("drop delta = %v, want 12", r.Deltas["audit_events_dropped_total"])
	}
	if r.Deltas["go_goroutines"] != 4 {
		t.Errorf("goroutine delta = %v, want 4", r.Deltas["go_goroutines"])
	}
}

func TestComputeDeltasAfterOnlyCounter(t *testing.T) {
	// An OTEL counter absent from "before" (not yet exported) but present in
	// "after" must show its full value as movement.
	r := &Report{Scrapes: []scrape.Snapshot{
		{Label: "before", Values: map[string]float64{"go_goroutines": 40}},
		{Label: "after", Values: map[string]float64{"go_goroutines": 41, "audit_events_dropped_total": 7}},
	}}
	r.ComputeDeltas()
	if r.Deltas["audit_events_dropped_total"] != 7 {
		t.Errorf("after-only counter delta = %v, want 7", r.Deltas["audit_events_dropped_total"])
	}
	if r.Deltas["go_goroutines"] != 1 {
		t.Errorf("goroutine delta = %v, want 1", r.Deltas["go_goroutines"])
	}
}

func TestComputeDeltasInsufficientScrapes(t *testing.T) {
	r := &Report{Scrapes: []scrape.Snapshot{{Label: "before"}}}
	r.ComputeDeltas()
	if r.Deltas != nil {
		t.Errorf("expected no deltas with a single scrape, got %v", r.Deltas)
	}
}

func TestAllAssertionsPassed(t *testing.T) {
	r := &Report{Assertions: []Assertion{{Passed: true}, {Passed: true}}}
	if !r.AllAssertionsPassed() {
		t.Error("expected all-passed")
	}
	r.Assertions = append(r.Assertions, Assertion{Passed: false})
	if r.AllAssertionsPassed() {
		t.Error("expected failure when one assertion fails")
	}
	// Vacuously true with no assertions.
	if !(&Report{}).AllAssertionsPassed() {
		t.Error("empty assertions should pass vacuously")
	}
}

func TestWriteJSONRoundTrip(t *testing.T) {
	r := sampleReport()
	r.ComputeDeltas()
	path := filepath.Join(t.TempDir(), "report.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Scenario != r.Scenario || len(back.Operations) != 1 {
		t.Errorf("round-trip mismatch: %+v", back)
	}
	if back.Deltas["audit_events_dropped_total"] != 12 {
		t.Errorf("deltas did not round-trip: %v", back.Deltas)
	}
}

func TestHumanSummaryContainsKeyFields(t *testing.T) {
	r := sampleReport()
	r.ComputeDeltas()
	out := r.HumanSummary()
	for _, want := range []string{"mcp-tool-call", "search", "audit_events_dropped_total", "PASS", "error-rate:search"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n%s", want, out)
		}
	}
}

func TestHumanSummaryFlagsNonReleaseBuild(t *testing.T) {
	r := sampleReport()
	r.Config.ReleaseBuild = false
	if !strings.Contains(r.HumanSummary(), "not publishable") {
		t.Error("expected a non-release-build warning in the summary")
	}
}
