//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
)

// Issue #1972: the Flow tab draws a script as the order it runs in, from one
// Start to its exits (the Structure view), places a run's calls in time (the
// Timeline view), pages the run history with its true total, and shows the
// tests and coverage each saved version kept.
//
// What these hold, against the running platform, through the routes the
// portal reads: the graph route's structure has one Start, one End, a Stops
// node for the fail(), a decision with a yes and a no arm, a loop box and a
// helper box, and no cycle; a run that failed in a fail() inside a helper is
// drawn failing at that Stops node; a run's calls are each placed in time with
// the call site that made them; the run history reports every run in its
// total and reads further back a page at a time; and the version a save
// created carries the report of its tests.
//
// Wire forms: every route's path parameters are strings in the URL, and
// per_page and page are integers in the query string, each sent once.
// manage_script's command, name, source and description are strings, and
// run_script's name a string and wait_seconds an integer; each is sent once
// in the one form its schema admits.

// structured1972 has a helper with a decision and a fail(), a loop with a call
// in it, and a query and an export after it. The fail() is not taken, so a run
// ends at End.
const structured1972 = `
def check(n):
    """Refuses a count past the limit."""
    if n > 10:
        fail("count past the limit")
    return n

def main():
    """Checks the count, queries it twice, and exports the last rows."""
    n = check(2)
    rows = []
    for _ in range(n):
        rows = platform.query("SELECT 1 AS n", connection = "acme")
    platform.export("acc-1972", rows["rows"], format = "csv")
`

// failing1972 fails in its helper's fail(), on a line no platform call is on.
const failing1972 = `
def check(n):
    """Refuses a count past the limit."""
    if n > 10:
        fail("count past the limit")

def main():
    """Checks a count that is past the limit."""
    check(20)
`

// structure1972 reads a version's structure off the graph route.
func structure1972(t *testing.T, c *client, id string, version int) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/versions/%d/graph", id, version), nil)
	if status != http.StatusOK {
		t.Fatalf("GET graph: status %d: %v", status, out)
	}
	s, ok := out["structure"].(map[string]any)
	if !ok {
		t.Fatalf("the graph carries no structure: %v", out)
	}
	return s
}

func items1972(m map[string]any, key string) []map[string]any {
	raw, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if x, ok := r.(map[string]any); ok {
			out = append(out, x)
		}
	}
	return out
}

func ofKind1972(nodes []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, n := range nodes {
		if n["kind"] == kind {
			out = append(out, n)
		}
	}
	return out
}

// acyclic1972 reports whether the edges form a DAG.
func acyclic1972(edges []map[string]any) bool {
	succ := map[string][]string{}
	for _, e := range edges {
		from, _ := e["from"].(string)
		to, _ := e["to"].(string)
		succ[from] = append(succ[from], to)
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		if state[n] == 1 {
			return false
		}
		if state[n] == 2 {
			return true
		}
		state[n] = 1
		for _, m := range succ[n] {
			if !visit(m) {
				return false
			}
		}
		state[n] = 2
		return true
	}
	for n := range succ {
		if !visit(n) {
			return false
		}
	}
	return true
}

func TestIssue1972_TheStructureRunsFromOneStartToItsExits(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "structure", structured1972)
	s := structure1972(t, c, id, 1)
	nodes, edges, boxes := items1972(s, "nodes"), items1972(s, "edges"), items1972(s, "boxes")

	if n := len(ofKind1972(nodes, "start")); n != 1 {
		t.Errorf("%d Start nodes, want one", n)
	}
	if n := len(ofKind1972(nodes, "end")); n != 1 {
		t.Errorf("%d End nodes, want one", n)
	}
	stops := ofKind1972(nodes, "stop")
	if len(stops) != 1 || stops[0]["label"] != "count past the limit" {
		t.Errorf("stops %v, want the one fail() with its message", stops)
	}
	ifs := ofKind1972(nodes, "if")
	if len(ifs) != 1 || ifs[0]["label"] != "n > 10" {
		t.Fatalf("decisions %v, want the helper's if", ifs)
	}
	arms := map[string]bool{}
	for _, e := range edges {
		if e["from"] == ifs[0]["id"] {
			label, _ := e["label"].(string)
			arms[label] = true
		}
	}
	if !arms["yes"] || !arms["no"] {
		t.Errorf("the decision's arms are %v, want yes and no", arms)
	}
	kinds := map[string]string{}
	for _, b := range boxes {
		label, _ := b["label"].(string)
		kind, _ := b["kind"].(string)
		kinds[label] = kind
	}
	if kinds["check(n)"] != "function" || kinds["for _ in range(n)"] != "loop" {
		t.Errorf("boxes %v, want the helper and the loop", kinds)
	}
	if steps := ofKind1972(nodes, "step"); len(steps) != 2 {
		t.Errorf("%d calls, want the query and the export", len(steps))
	}
	if !acyclic1972(edges) {
		t.Errorf("the structure has a cycle: %v", edges)
	}
}

func TestIssue1972_AFailInAHelperIsTheFailedNode(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "failing", failing1972)
	runID, status := run1907(t, c, name)
	if status != "failed" {
		t.Fatalf("the run ended %s, want failed", status)
	}
	flow := runFlow1907(t, c, id, runID)
	g, _ := flow["graph"].(map[string]any)
	s, _ := g["structure"].(map[string]any)
	stops := ofKind1972(items1972(s, "nodes"), "stop")
	if len(stops) != 1 {
		t.Fatalf("stops %v, want one", stops)
	}
	if flow["structure_failed"] != stops[0]["id"] {
		t.Errorf("the run failed at %v, want the fail() at %v", flow["structure_failed"], stops[0]["id"])
	}
	if unplaced, _ := flow["unplaced"].(bool); unplaced {
		t.Errorf("a run on this release reads as unplaced")
	}
}

func TestIssue1972_EveryCallOfARunIsPlacedInTime(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "timeline", structured1972)
	runID, status := run1907(t, c, name)
	if status != "succeeded" {
		t.Fatalf("the run ended %s", status)
	}
	flow := runFlow1907(t, c, id, runID)
	timeline := items1972(flow, "timeline")
	calls, _ := flow["calls"].(float64)
	if len(timeline) == 0 || len(timeline) != int(calls) {
		t.Fatalf("%d calls on the timeline, want the run's %v", len(timeline), calls)
	}
	runMS, _ := flow["run_ms"].(float64)
	last := -1.0
	queries := 0
	for _, call := range timeline {
		start, _ := call["start_ms"].(float64)
		if start < last {
			t.Errorf("call at %v ms placed after one at %v ms", start, last)
		}
		last = start
		if site, _ := call["call_site"].([]any); len(site) == 0 {
			t.Errorf("a call has no call site: %v", call)
		}
		if call["tool"] == "trino_query" {
			queries++
			if node, _ := call["node"].(string); node == "" {
				t.Errorf("a query is on no card: %v", call)
			}
		}
		if runMS > 0 && start > runMS {
			t.Errorf("a call starts at %v ms, after the run's %v ms", start, runMS)
		}
	}
	if queries != 2 {
		t.Errorf("%d queries on the timeline, want the loop's two", queries)
	}
}

func TestIssue1972_TheRunHistoryPagesWithItsTrueTotal(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "paged", structured1972)
	for range 3 {
		run1907(t, c, name)
	}
	page := func(n int) (total int, ids []string) {
		status, out := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/runs?per_page=2&page=%d", id, n), nil)
		if status != http.StatusOK {
			t.Fatalf("GET runs page %d: status %d: %v", n, status, out)
		}
		tot, _ := out["total"].(float64)
		for _, r := range items1972(out, "data") {
			rid, _ := r["id"].(string)
			ids = append(ids, rid)
		}
		return int(tot), ids
	}
	total, first := page(1)
	if total != 3 || len(first) != 2 {
		t.Fatalf("page 1 holds %d runs of %d, want 2 of 3", len(first), total)
	}
	total, second := page(2)
	if total != 3 || len(second) != 1 {
		t.Fatalf("page 2 holds %d runs of %d, want 1 of 3", len(second), total)
	}
	for _, r := range first {
		if r == second[0] {
			t.Errorf("run %s is on both pages", r)
		}
	}
}

func TestIssue1972_TheSavedVersionKeepsItsTestReport(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "tests", structured1972)
	versions := c.list(fmt.Sprintf("/api/v1/portal/scripts/%s/versions", id))
	if len(versions) != 1 {
		t.Fatalf("%d versions, want the one created", len(versions))
	}
	v, _ := versions[0].(map[string]any)
	report, ok := v["tests"].(map[string]any)
	if !ok {
		t.Fatalf("the version carries no test report: %v", v)
	}
	tests := items1972(report, "tests")
	if len(tests) == 0 {
		t.Fatalf("the report lists no tests: %v", report)
	}
	for _, test := range tests {
		if passed, _ := test["passed"].(bool); !passed {
			t.Errorf("test %v did not pass at the save", test["name"])
		}
	}
	coverage, _ := report["coverage"].(map[string]any)
	percent, _ := coverage["percent"].(float64)
	if percent < 80 {
		t.Errorf("coverage %v%%, under the 80%% a save requires", percent)
	}
	if missed, ok := coverage["missed_lines"].([]any); !ok || missed == nil {
		t.Errorf("missed_lines is not a list: %v", coverage["missed_lines"])
	}
}
