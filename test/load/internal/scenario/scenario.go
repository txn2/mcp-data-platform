// Package scenario defines the named load workloads and a registry to look them
// up by name. Each scenario implements harness.Scenario. Scenarios that share
// per-run state across their workers keep it on the scenario struct (guarded by
// atomics), since one scenario instance is reused for every worker in a run.
package scenario

import (
	"fmt"
	"sort"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
)

// factory builds a fresh scenario instance (with zeroed shared counters).
type factory func() harness.Scenario

// registry maps scenario name to its factory.
var registry = map[string]factory{
	"mcp-tool-call":     func() harness.Scenario { return &mcpToolCall{} },
	"mcp-session-churn": func() harness.Scenario { return &mcpSessionChurn{} },
	"oauth-token":       func() harness.Scenario { return &oauthRegister{} },
	"portal-read":       func() harness.Scenario { return &portalRead{} },
	"audit-burst":       func() harness.Scenario { return &auditBurst{} },
	"soak":              func() harness.Scenario { return &soak{} },
}

// Get returns a fresh scenario by name.
func Get(name string) (harness.Scenario, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown scenario %q (known: %v)", name, Names())
	}
	return f(), nil
}

// Names returns the registered scenario names, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// --- shared assertion helpers ---

// errorRateAssertion passes when the measured error rate for op is at or below
// maxFrac. A missing op (no samples) fails: the scenario recorded nothing.
func errorRateAssertion(rep *report.Report, op string, maxFrac float64) report.Assertion {
	for _, o := range rep.Operations {
		if o.Operation != op {
			continue
		}
		passed := o.ErrorRate <= maxFrac
		return report.Assertion{
			Name:   "error-rate:" + op,
			Passed: passed,
			Message: fmt.Sprintf("error rate %.2f%% over %d calls (threshold %.2f%%)",
				o.ErrorRate*100, o.Count, maxFrac*100),
		}
	}
	return report.Assertion{
		Name:    "error-rate:" + op,
		Passed:  false,
		Message: fmt.Sprintf("no samples recorded for %q", op),
	}
}

// throughputAssertion passes when op recorded at least one call, reporting the
// sustained throughput. Used where the point is to measure a ceiling, not to
// gate on an error rate that may legitimately be high (e.g. an engaged limiter).
func throughputAssertion(rep *report.Report, op string) report.Assertion {
	for _, o := range rep.Operations {
		if o.Operation != op {
			continue
		}
		return report.Assertion{
			Name:    "throughput:" + op,
			Passed:  o.Count > 0,
			Message: fmt.Sprintf("%.1f/s sustained over %d calls", o.ThroughputPerSec, o.Count),
		}
	}
	return report.Assertion{Name: "throughput:" + op, Passed: false, Message: "no samples recorded"}
}

// counterDeltaInfo is an always-passing informational assertion reporting a
// counter's movement over the run (0 when the counter never fired). It records
// an outcome rather than gating one: audit-burst uses it so the async run
// (drops move) and the sync run (no drops, higher latency) both record their
// result without one legitimately failing.
func counterDeltaInfo(rep *report.Report, metric string) report.Assertion {
	delta := rep.Deltas[metric]
	return report.Assertion{
		Name:    "counter:" + metric,
		Passed:  true,
		Message: fmt.Sprintf("moved %+.0f over the run", delta),
	}
}

// stabilityAssertion passes when a gauge grew by no more than maxGrowthFrac from
// the first to the last scrape — the soak flat-RSS / flat-goroutine check.
func stabilityAssertion(rep *report.Report, metric string, maxGrowthFrac float64) report.Assertion {
	name := "stable:" + metric
	if len(rep.Scrapes) < 2 {
		return report.Assertion{Name: name, Passed: false, Message: "need at least two scrapes to assess stability"}
	}
	before := rep.Scrapes[0].Values[metric]
	after := rep.Scrapes[len(rep.Scrapes)-1].Values[metric]
	if before <= 0 {
		return report.Assertion{
			Name: name, Passed: false,
			Message: fmt.Sprintf("%s not observed at run start (before=%.0f)", metric, before),
		}
	}
	growth := (after - before) / before
	passed := growth <= maxGrowthFrac
	return report.Assertion{
		Name:   name,
		Passed: passed,
		Message: fmt.Sprintf("%s %.0f -> %.0f (%+.1f%%, threshold +%.0f%%)",
			metric, before, after, growth*100, maxGrowthFrac*100),
	}
}
