// Package report is the self-contained result of one load run: the run
// configuration, per-operation throughput/latency, the before/during/after
// Prometheus scrapes, scenario assertions, and any captured profile paths. It
// marshals to JSON and renders a human summary.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/scrape"
	"github.com/txn2/mcp-data-platform/test/load/internal/stats"
)

// Report is the top-level run result written to disk.
type Report struct {
	Scenario    string             `json:"scenario"`
	Description string             `json:"description"`
	StartedAt   time.Time          `json:"started_at"`
	FinishedAt  time.Time          `json:"finished_at"`
	WallSeconds float64            `json:"wall_seconds"`
	Config      Config             `json:"config"`
	Operations  []stats.OpSummary  `json:"operations"`
	Scrapes     []scrape.Snapshot  `json:"scrapes"`
	Deltas      map[string]float64 `json:"metric_deltas"` // after - before for tracked counters/gauges
	Assertions  []Assertion        `json:"assertions"`
	Profiles    []string           `json:"profiles,omitempty"`
	Passed      bool               `json:"passed"`
}

// Config is the run configuration echoed into the report for reproducibility.
type Config struct {
	Target       string  `json:"target"`
	AuthMode     string  `json:"auth_mode"`
	Concurrency  int     `json:"concurrency"`
	DurationSec  float64 `json:"duration_sec"`
	WarmupSec    float64 `json:"warmup_sec"`
	RatePerSec   float64 `json:"rate_per_sec"` // 0 = unbounded
	ReleaseBuild bool    `json:"release_build"`
}

// Assertion is a scenario-specific pass/fail check (e.g. soak flat memory,
// audit-burst drop movement).
type Assertion struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

// AllAssertionsPassed reports whether every assertion passed (vacuously true
// when there are none).
func (r *Report) AllAssertionsPassed() bool {
	for _, a := range r.Assertions {
		if !a.Passed {
			return false
		}
	}
	return true
}

// ComputeDeltas fills Deltas with (after - before) for every metric in the union
// of the first and last scrape, treating a metric absent from one scrape as 0.
// The absent-as-zero rule matters for OTEL counters (audit_events_dropped_total,
// mcp_rate_limited_total) that are not exported until their first event: such a
// counter is missing from the "before" scrape yet its full value is real
// movement over the run.
func (r *Report) ComputeDeltas() {
	if len(r.Scrapes) < 2 {
		return
	}
	before := r.Scrapes[0].Values
	after := r.Scrapes[len(r.Scrapes)-1].Values
	r.Deltas = make(map[string]float64)
	for name := range unionKeys(before, after) {
		r.Deltas[name] = after[name] - before[name]
	}
}

// unionKeys returns the set of keys present in either map.
func unionKeys(a, b map[string]float64) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// WriteJSON marshals the report (indented) to path.
func (r *Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing report to %s: %w", path, err)
	}
	return nil
}

// HumanSummary renders a plain-text summary for the terminal.
func (r *Report) HumanSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scenario: %s\n", r.Scenario)
	if r.Description != "" {
		fmt.Fprintf(&b, "  %s\n", r.Description)
	}
	fmt.Fprintf(&b, "Target: %s  auth=%s  concurrency=%d  duration=%.0fs  warmup=%.0fs",
		r.Config.Target, r.Config.AuthMode, r.Config.Concurrency, r.Config.DurationSec, r.Config.WarmupSec)
	if r.Config.RatePerSec > 0 {
		fmt.Fprintf(&b, "  rate=%.1f/s", r.Config.RatePerSec)
	}
	b.WriteString("\n")
	if !r.Config.ReleaseBuild {
		b.WriteString("  WARNING: target was not confirmed to be a release build; numbers are not publishable.\n")
	}

	b.WriteString("\nOperations:\n")
	writeOpsTable(&b, r.Operations)

	if len(r.Deltas) > 0 {
		b.WriteString("\nPlatform metric deltas (after - before):\n")
		writeDeltas(&b, r.Deltas)
	}

	if len(r.Assertions) > 0 {
		b.WriteString("\nAssertions:\n")
		for _, a := range r.Assertions {
			status := "PASS"
			if !a.Passed {
				status = "FAIL"
			}
			fmt.Fprintf(&b, "  [%s] %s: %s\n", status, a.Name, a.Message)
		}
	}

	if len(r.Profiles) > 0 {
		b.WriteString("\nProfiles:\n")
		for _, p := range r.Profiles {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}

	fmt.Fprintf(&b, "\nResult: %s\n", passLabel(r.Passed))
	return b.String()
}

func writeOpsTable(b *strings.Builder, ops []stats.OpSummary) {
	if len(ops) == 0 {
		b.WriteString("  (no operations recorded)\n")
		return
	}
	fmt.Fprintf(b, "  %-22s %8s %8s %9s %9s %9s %9s %9s\n",
		"operation", "count", "err%", "thr/s", "p50ms", "p95ms", "p99ms", "maxms")
	for _, o := range ops {
		fmt.Fprintf(b, "  %-22s %8d %7.2f%% %9.1f %9.1f %9.1f %9.1f %9.1f\n",
			o.Operation, o.Count, o.ErrorRate*100, o.ThroughputPerSec, o.P50Ms, o.P95Ms, o.P99Ms, o.MaxMs)
	}
}

func writeDeltas(b *strings.Builder, deltas map[string]float64) {
	names := make([]string, 0, len(deltas))
	for n := range deltas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(b, "  %-32s %+.3f\n", n, deltas[n])
	}
}

func passLabel(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}
