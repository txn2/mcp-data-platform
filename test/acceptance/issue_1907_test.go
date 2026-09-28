//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1907: a run drawn on the Flow tab, served at
// GET /api/v1/portal/scripts/{id}/runs/{runID}/flow. A script's tool calls
// record where in the script they were made (audit_logs.call_site), so each is
// drawn on the card that made it.
//
// What these hold, against the running platform: the calls on the cards plus
// the calls no card made equal the audit rows whose session is the run; a
// helper called from two places puts each call on its own card; a run that
// failed at its export names the export card, with the recorded cause, and the
// step after it unreached; and a run of an older version is drawn on that
// version's diagram.
//
// Wire forms: the route's two path parameters are strings in the URL, sent
// once each. run_script's `name` is a string and `wait_seconds` an integer;
// manage_script's command/name/source/description are strings. Each is sent
// once in the one form its schema admits.

// run1907 runs a script, waits for it to end, and returns its run id and
// status.
func run1907(t *testing.T, c *client, name string) (id, status string) {
	t.Helper()
	out := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	for _, key := range []string{"run_id", "id"} {
		if v, _ := out[key].(string); v != "" {
			id = v
		}
	}
	status, _ = out["status"].(string)
	if id == "" {
		t.Fatalf("run_script returned no run id: %v", out)
	}
	return id, status
}

// runFlow1907 reads one run drawn on its diagram.
func runFlow1907(t *testing.T, c *client, scriptID, runID string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/runs/%s/flow", scriptID, runID), nil)
	if status != http.StatusOK {
		t.Fatalf("GET run flow: status %d: %v", status, out)
	}
	return out
}

func cardsCalls1907(flow map[string]any) (onCards int) {
	nodes, _ := flow["nodes"].(map[string]any)
	for _, n := range nodes {
		m, _ := n.(map[string]any)
		c, _ := m["calls"].(float64)
		onCards += int(c)
	}
	return onCards
}

// titleOf finds a node id's title in a run's graph.
func titleOf1907(flow map[string]any, id string) string {
	g, _ := flow["graph"].(map[string]any)
	nodes, _ := g["nodes"].([]any)
	for _, n := range nodes {
		m, _ := n.(map[string]any)
		if m["id"] == id {
			title, _ := m["title"].(string)
			return title
		}
	}
	return ""
}

// idOf finds the id of the graph node whose title starts with prefix, and
// whose subtitle contains sub.
func idOf1907(t *testing.T, flow map[string]any, prefix, sub string) string {
	t.Helper()
	g, _ := flow["graph"].(map[string]any)
	nodes, _ := g["nodes"].([]any)
	for _, n := range nodes {
		m, _ := n.(map[string]any)
		title, _ := m["title"].(string)
		subtitle, _ := m["subtitle"].(string)
		if strings.HasPrefix(title, prefix) && strings.Contains(subtitle, sub) {
			id, _ := m["id"].(string)
			return id
		}
	}
	t.Fatalf("no node %q / %q in the run's graph", prefix, sub)
	return ""
}

const helperRun1907 = `
def fetch(path):
    """Reads one path from the fixture."""
    return platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture", "method": "GET", "path": path,
        "purpose": "Acceptance #1907: read " + path,
    })

def main():
    """Queries a row, reads two fixture paths, and exports the row."""
    rows = platform.query("SELECT 1 AS n", connection = "acme")
    fetch("/echo")
    fetch("/identity")
    platform.export("acc-1907", rows["rows"], format = "csv")
`

func TestIssue1907_EveryAuditedCallOfARunIsOnACardOrListed(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "run", helperRun1907)
	runID, status := run1907(t, c, name)
	if status != "succeeded" {
		t.Fatalf("the run ended %s", status)
	}
	admin := connect(t)
	var flow map[string]any
	var audited int
	// The audit log is written after the call returns, so the run's rows are
	// read until the diagram and the log agree or the bound is reached.
	for attempt := 0; attempt < 40; attempt++ {
		audited = 0
		for _, row := range admin.list("/api/v1/admin/audit/events?per_page=200&session_id=" + runID) {
			// The run's lifecycle event shares its session; it is not a call.
			if m, _ := row.(map[string]any); m["event_kind"] != "script_run" {
				audited++
			}
		}
		flow = runFlow1907(t, c, id, runID)
		total, _ := flow["calls"].(float64)
		if audited > 0 && int(total) == audited {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	other, _ := flow["other_calls"].([]any)
	onCards := cardsCalls1907(flow)
	if onCards+len(other) != audited {
		t.Fatalf("%d calls on cards + %d other calls, want the %d audit rows of the run", onCards, len(other), audited)
	}
	if len(other) != 0 {
		t.Errorf("calls no card made: %v", other)
	}
	nodes, _ := flow["nodes"].(map[string]any)
	for _, path := range []string{"/echo", "/identity"} {
		n, _ := nodes[idOf1907(t, flow, "API api-test-fixture", path)].(map[string]any)
		if calls, _ := n["calls"].(float64); calls != 1 {
			t.Errorf("the %s card made %v calls, want its one", path, n["calls"])
		}
	}
	exp, _ := nodes[idOf1907(t, flow, "Export CSV to portal", "")].(map[string]any)
	if reached, _ := exp["reached"].(bool); !reached {
		t.Errorf("the export card is not reached: %v", exp)
	}
}

// failRun1907 counts its runs in state, the read of run.state a script that
// saves state has to make; the save is the step after the failing export.
const failRun1907 = `
def main():
    """Exports a row registered on a connection that does not exist, then saves state."""
    runs = run.state.get("runs", 0) + 1
    rows = platform.query("SELECT 1 AS n", connection = "acme")
    platform.export("acc-1907-fail", rows["rows"], format = "csv", destination = "resources",
                    key = "acc-1907/fail.csv", register = {"connection": "no-such-connection"})
    platform.save_state({"after": 1, "runs": runs})
`

func TestIssue1907_AFailedExportIsTheFailedCard(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, name := script1906(t, c, "fail", failRun1907)
	runID, status := run1907(t, c, name)
	if status != "failed" {
		t.Fatalf("the run ended %s, want failed", status)
	}
	flow := runFlow1907(t, c, id, runID)
	failed, _ := flow["failed_node"].(string)
	if !strings.HasPrefix(titleOf1907(flow, failed), "Export CSV to resources") {
		t.Fatalf("the failed card is %q (%v), want the export", failed, titleOf1907(flow, failed))
	}
	if cause, _ := flow["cause"].(string); cause == "" {
		t.Errorf("the run's cause is not given: %v", flow["cause"])
	}
	nodes, _ := flow["nodes"].(map[string]any)
	f, _ := nodes[failed].(map[string]any)
	if msg, _ := f["error"].(string); msg == "" {
		t.Errorf("the failed card carries no message: %v", f)
	}
	save, _ := nodes[idOf1907(t, flow, "Save state", "")].(map[string]any)
	if reached, _ := save["reached"].(bool); reached {
		t.Errorf("the step after the failed export was reached: %v", save)
	}
}

func TestIssue1907_ARunOfAnOlderVersionIsDrawnOnThatVersion(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	v1 := `def main():
    """Queries one row."""
    rows = platform.query("SELECT 1 AS n", connection = "acme")
    print(len(rows["rows"]))
`
	id, name := script1906(t, c, "older", v1)
	runID, _ := run1907(t, c, name)
	v2 := v1 + `    platform.export("acc-1907-v2", rows["rows"], format = "csv")
`
	if out := c.saveEdit(map[string]any{"command": "update", "name": name, "source": v2}, nil); out["status"] != "updated" {
		t.Fatalf("update refused: %v", out)
	}
	flow := runFlow1907(t, c, id, runID)
	if v, _ := flow["version"].(float64); v != 1 {
		t.Fatalf("the run is drawn on version %v, want 1", flow["version"])
	}
	g, _ := flow["graph"].(map[string]any)
	if nodes, _ := g["nodes"].([]any); len(nodes) != 1 {
		t.Errorf("version 1's diagram has %d cards, want its one query", len(nodes))
	}
}
