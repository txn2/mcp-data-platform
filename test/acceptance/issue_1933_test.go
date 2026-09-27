//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #1933: two drawings of the Automations pages said less than the
// platform knew. A run that failed on the line after a call whose upstream
// answered 500 was drawn with no failed card, and an every-minute schedule the
// fires route cut at its cap was drawn as stopping mid-morning. These
// criteria run the ticket's shapes through the tools and routes the portal
// uses: run_script against the api-test fixture's /v1/status/500, the run's
// flow route, and the fires route.
//
// Wire forms: manage_script's command, name, description, source, cron and
// timezone, and run_script's name are typed strings, and run_script's
// wait_seconds an integer, so each admits one JSON form and is sent as a
// literal tools/call parameter of it. The flow and fires routes take path and
// query-string parameters, one form each.

// weather1933 is the ticket's shape in main(), as the #1913 gates require of
// a saved script: the failing call and the fail() after it stay in forecast.
const weather1933 = `
def forecast(office):
    """Reads the office's forecast and fails when the upstream does not answer 200."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture",
        "method": "GET",
        "path": "/v1/status/500",
        "purpose": "Acceptance #1933: an upstream that answers 500",
    })
    if res["status"] != 200:
        fail("NWS returned %d for %s" % (res["status"], office))
    return res

def main():
    """Reads one office's forecast."""
    forecast("PSR")
`

// TestIssue1933_TheCardAFailedCallWasMadeFromIsTheFailedCard is the ticket's
// run: the traceback ends at fail() inside forecast, one line below the call.
// The card holding the failed call is marked failed, with the failed call and
// its message.
func TestIssue1933_TheCardAFailedCallWasMadeFromIsTheFailedCard(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1933-weather-%d", time.Now().UnixNano())
	created := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": weather1933,
		"description": "Acceptance #1933: a failure after an upstream 500.",
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	scriptID, _ := created["id"].(string)
	if scriptID == "" {
		t.Fatalf("manage_script create returned no id: %v", created)
	}
	runID, status := run1907(t, c, name)
	if status != "failed" {
		t.Fatalf("the run did not fail: %s", status)
	}

	flow := runFlow1907(t, c, scriptID, runID)
	card := idOf1907(t, flow, "API", "")
	if flow["failed_node"] != card {
		t.Fatalf("failed_node = %v; want the API card %s (%s)", flow["failed_node"], card, titleOf1907(flow, card))
	}
	nodes, _ := flow["nodes"].(map[string]any)
	node, _ := nodes[card].(map[string]any)
	if node["failed"] != true || node["failed_calls"] != float64(1) {
		t.Errorf("the API card's run: %v; want failed with its one failed call", node)
	}
	if e, _ := node["error"].(string); e != "Error in fail: fail: NWS returned 500 for PSR" {
		t.Errorf("the card's error = %q", e)
	}
	if e, _ := node["last_error"].(string); e == "" {
		t.Errorf("the card does not carry the failed call's message: %v", node)
	}
}

// TestIssue1933_ACutScheduleRowSaysWhereItsFiresEnd is the Schedules tab: an
// every-minute schedule is cut at the row cap, and the row names the window's
// last fire, so it can be drawn to the end of the day rather than to where
// the list stops.
func TestIssue1933_ACutScheduleRowSaysWhereItsFiresEnd(t *testing.T) {
	c := connect(t)
	id := schedule1891(t, c, "1933-minutely", "* * * * *", "UTC")
	_, row := row1891(t, fires1891(t, c, "UTC"), id)
	if row == nil {
		t.Fatalf("no row for the every-minute schedule")
	}
	if row["truncated"] != true {
		t.Fatalf("an every-minute row is not cut: %v", row["fire_count"])
	}
	fires := fireTimes1891(t, row)
	last, err := time.Parse(time.RFC3339, fmt.Sprint(row["last_fire"]))
	if err != nil {
		t.Fatalf("last_fire %v: %v", row["last_fire"], err)
	}
	if !last.After(fires[len(fires)-1]) {
		t.Errorf("last_fire %s is not past the last listed fire %s", last, fires[len(fires)-1])
	}
	if count := row["fire_count"].(float64); last.Sub(fires[0]) < time.Duration(count-2)*time.Minute {
		t.Errorf("last_fire %s does not reach as far as %v fires a minute apart from %s", last, count, fires[0])
	}
}
