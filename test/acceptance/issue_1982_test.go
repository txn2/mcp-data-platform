//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1982: selecting a call on a run's Timeline did not show that call. The
// panel can only show what a call did if the run's flow carries it, so each
// call on GET /api/v1/portal/scripts/{id}/runs/{runID}/flow now carries the
// arguments its audit row recorded, beside its tool, timing, outcome, line and
// card. What this holds, against the running platform: every call a run made
// is on the timeline with its arguments (the query here carries the value it
// bound), and a call made by a card names that card and the line it came
// from. The panel that renders them, and the
// statement a run recorded before call sites shows instead, are held by the
// Flow tab's component tests (ScriptFlowView.structure.test.tsx).
//
// Wire forms: run_script's `name` is a string, `args` an object and
// `wait_seconds` an integer; manage_script's command/name/description/source
// are strings and `params` an array of objects; the route's two path
// parameters are strings in the URL. Each is sent once in that one form.

const script1982 = `
def main():
    """Queries one row tagged with the report's name and exports it."""
    rows = platform.query("SELECT 41 AS units, :report AS report", connection = "acme", params = {"report": run.params["report"]})
    platform.export(name = run.params["report"], rows = rows["rows"], format = "csv")
`

func TestIssue1982_EachTimelineCallCarriesItsArguments(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	n := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	name, report := "acc-1982-"+n, "acc-1982-report-"+n
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1982: a run whose query is shown on its own.",
		"source":      script1982,
		"params": []any{map[string]any{
			"name": "report", "type": "string", "required": true, "description": "The output name.",
		}},
	}, map[string]any{"report": report + "-draft"})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	scriptID := scriptID1569(t, c, name)

	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"report": report}, "wait_seconds": 120})
	runID, _ := out["run_id"].(string)
	if status, _ := out["status"].(string); status != "succeeded" || runID == "" {
		t.Fatalf("run did not succeed: %v", out)
	}
	t.Cleanup(func() {
		for _, a := range ownedAssets1551(t, c) {
			if a["name"] == report {
				_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": a["id"]})
			}
		}
	})

	flow := runFlow1907(t, c, scriptID, runID)
	timeline, _ := flow["timeline"].([]any)
	if len(timeline) == 0 {
		t.Fatalf("the run's timeline holds no calls: %v", flow)
	}
	var withReport map[string]any
	for _, item := range timeline {
		call, _ := item.(map[string]any)
		args, _ := call["arguments"].(string)
		if args == "" {
			t.Errorf("a call carries no arguments: %v", call)
		}
		if strings.Contains(args, report) {
			withReport = call
		}
	}
	if withReport == nil {
		t.Fatalf("no call's arguments carry the value the run bound: %v", timeline)
	}
	if node, _ := withReport["node"].(string); node == "" {
		t.Errorf("the query names no card: %v", withReport)
	}
	if site, _ := withReport["call_site"].([]any); len(site) == 0 {
		t.Errorf("the query names no line: %v", withReport)
	}
	if withReport["success"] != true {
		t.Errorf("the query did not succeed: %v", withReport)
	}
}
