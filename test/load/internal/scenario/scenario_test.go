package scenario

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/test/load/internal/report"
	"github.com/txn2/mcp-data-platform/test/load/internal/scrape"
	"github.com/txn2/mcp-data-platform/test/load/internal/stats"
)

func TestRegistryHasAllSpecifiedScenarios(t *testing.T) {
	want := []string{"audit-burst", "mcp-session-churn", "mcp-tool-call", "oauth-token", "portal-read", "soak"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("scenario count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], n)
		}
		sc, err := Get(n)
		if err != nil {
			t.Errorf("Get(%q): %v", n, err)
			continue
		}
		if sc.Name() != n {
			t.Errorf("scenario %q reports Name()=%q", n, sc.Name())
		}
		if sc.Description() == "" {
			t.Errorf("scenario %q has an empty description", n)
		}
	}
}

func TestGetUnknownScenario(t *testing.T) {
	if _, err := Get("nope"); err == nil {
		t.Error("expected an error for an unknown scenario")
	}
}

func TestErrorRateAssertion(t *testing.T) {
	rep := &report.Report{Operations: []stats.OpSummary{
		{Operation: "search", Count: 100, ErrorRate: 0.01},
	}}
	if a := errorRateAssertion(rep, "search", 0.02); !a.Passed {
		t.Errorf("expected pass at 1%% under 2%% threshold: %s", a.Message)
	}
	if a := errorRateAssertion(rep, "search", 0.005); a.Passed {
		t.Error("expected fail at 1% over 0.5% threshold")
	}
	if a := errorRateAssertion(rep, "missing", 0.5); a.Passed {
		t.Error("expected fail for an operation with no samples")
	}
}

func TestStabilityAssertion(t *testing.T) {
	mk := func(before, after float64) *report.Report {
		return &report.Report{Scrapes: []scrape.Snapshot{
			{Values: map[string]float64{"go_goroutines": before}},
			{Values: map[string]float64{"go_goroutines": after}},
		}}
	}
	if a := stabilityAssertion(mk(40, 44), "go_goroutines", 0.50); !a.Passed {
		t.Errorf("10%% growth should pass a 50%% threshold: %s", a.Message)
	}
	if a := stabilityAssertion(mk(40, 80), "go_goroutines", 0.50); a.Passed {
		t.Error("100% growth should fail a 50% threshold")
	}
	if a := stabilityAssertion(mk(0, 10), "go_goroutines", 0.50); a.Passed {
		t.Error("missing baseline should fail")
	}
	if a := stabilityAssertion(&report.Report{}, "go_goroutines", 0.5); a.Passed {
		t.Error("insufficient scrapes should fail")
	}
}

func TestCounterDeltaInfo(t *testing.T) {
	rep := &report.Report{Deltas: map[string]float64{"audit_events_dropped_total": 12}}
	a := counterDeltaInfo(rep, "audit_events_dropped_total")
	if !a.Passed {
		t.Error("counterDeltaInfo is informational and must always pass")
	}
	if !strings.Contains(a.Message, "12") {
		t.Errorf("expected the delta in the message, got %q", a.Message)
	}
	// Absent counter reports zero movement and still passes.
	if z := counterDeltaInfo(rep, "absent"); !z.Passed || !strings.Contains(z.Message, "+0") {
		t.Errorf("absent counter should report +0 and pass, got %q", z.Message)
	}
}

func TestThroughputAssertion(t *testing.T) {
	rep := &report.Report{Operations: []stats.OpSummary{
		{Operation: "oauth_register", Count: 50, ThroughputPerSec: 1.6},
	}}
	if a := throughputAssertion(rep, "oauth_register"); !a.Passed {
		t.Errorf("recorded op should pass: %s", a.Message)
	}
	if a := throughputAssertion(rep, "missing"); a.Passed {
		t.Error("missing op should fail")
	}
}
