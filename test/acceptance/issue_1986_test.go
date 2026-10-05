//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1986: a script set to run one at a time never has two runs open,
// whatever started them.
//
// A run is held open by the script itself: it calls the mcp-test fixture's
// slow tool for as long as its arguments say, so the run is genuinely running
// on a worker while the criteria act. A schedule fire is made due by moving
// its next_run_at into the past in the dev database, the one step no surface
// drives (a fire otherwise arrives on the minute); the platform's scheduler
// then materializes it on its own pass, within its 30-second interval.
//
// Wire forms: manage_script's command, name, description, source, run_id,
// cron and timezone are typed strings, exclusive is a typed boolean, and args
// is a typed object; run_script's name is a typed string, args a typed object
// and wait_seconds a typed integer (negative: queue and return at once). Each admits exactly one JSON form and each
// is sent below as a literal tools/call parameter of that form. The portal's
// exclusive route takes {"exclusive": <boolean>} and its run route
// {"params": <object>}.

// holdMS1986 is how long one slow call holds a run open, under the fixture
// connection's ten-second call timeout, and holds1986 how many a held run
// makes: long enough to outlast a scheduler pass.
const (
	holdMS1986 = 8000
	holds1986  = 12
)

// slowTool1986 is the fixture's slow tool as the platform lists it.
const slowTool1986 = "mcp-test-fixture__slow"

// issue1986Script saves a script whose run lasts holds × hold_ms and returns
// its name and id.
func issue1986Script(t *testing.T, c *client, label string) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("acc-1986-%s-%d", label, time.Now().UnixNano())
	created := c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1986: a run held open by the fixture's slow tool.",
		"params": []any{
			map[string]any{"name": "hold_ms", "type": "int", "required": true, "description": "How long each slow call lasts."},
			map[string]any{"name": "holds", "type": "int", "required": true, "description": "How many slow calls the run makes."},
		},
		"source": `def main():
    """Holds the run open for holds slow calls of hold_ms each."""
    for _ in range(run.params["holds"]):
        platform.call("` + slowTool1986 + `", {"milliseconds": run.params["hold_ms"]})
`,
	}, map[string]any{"hold_ms": 10, "holds": 1})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ = got["id"].(string)
	if id == "" {
		t.Fatalf("get returned no id: %v (created %v)", got, created)
	}
	return name, id
}

// setExclusive1986 sets the setting through manage_script update.
func setExclusive1986(t *testing.T, c *client, name string, on bool) {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "update", "name": name, "exclusive": on})
	if out["status"] != "updated" {
		t.Fatalf("update exclusive=%v was not applied: %v", on, out)
	}
}

// startHeld1986 queues a held run with run_script and returns its id once a
// worker is executing it.
func startHeld1986(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": holdMS1986, "holds": holds1986}, "wait_seconds": -1,
	})
	id, _ := out["run_id"].(string)
	if id == "" {
		t.Fatalf("run_script returned no run_id: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": id}) })
	await1986(t, c, id, "running")
	return id
}

// await1986 polls get_run until the run reaches status.
func await1986(t *testing.T, c *client, runID, status string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		run := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
		if run["status"] == status {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach %s: %v", runID, status, run)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// runs1986 lists the script's runs.
func runs1986(t *testing.T, c *client, name string) []map[string]any {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "runs", "name": name})
	rows, _ := out["runs"].([]any)
	runs := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			runs = append(runs, m)
		}
	}
	return runs
}

// schedule1986 gives the script an every-minute schedule binding a held run,
// and makes its next fire due now.
func schedule1986(t *testing.T, c *client, name, scriptID string) {
	t.Helper()
	out := c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "* * * * *", "timezone": "UTC",
		"args": map[string]any{"hold_ms": holdMS1986, "holds": holds1986},
	})
	if out["cron"] == nil && out["status"] == nil {
		t.Fatalf("schedule_set answered %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "schedule_disable", "name": name})
	})
	fireNow1986(t, scriptID)
}

// fireNow1986 makes the script's schedule due, so the scheduler's next pass
// fires it.
func fireNow1986(t *testing.T, scriptID string) {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := db.ExecContext(ctx,
		`UPDATE script_schedules SET next_run_at = NOW() - INTERVAL '1 second' WHERE script_id = $1`, scriptID)
	if err != nil {
		t.Fatalf("making the fire due: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("no schedule row for script %s", scriptID)
	}
}

// awaitRun1986 polls the script's runs until one satisfies match.
func awaitRun1986(t *testing.T, c *client, name, what string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(75 * time.Second)
	for {
		for _, r := range runs1986(t, c, name) {
			if match(r) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no run of %s became %s: %v", name, what, runs1986(t, c, name))
		}
		time.Sleep(time.Second)
	}
}

// TestIssue1986_TheSettingIsSetByUpdateAndShownByGet is the setting itself on
// both surfaces that change it: manage_script update and the portal route.
func TestIssue1986_TheSettingIsSetByUpdateAndShownByGet(t *testing.T) {
	c := connect(t)
	name, id := issue1986Script(t, c, "setting")
	if got := c.call("manage_script", map[string]any{"command": "get", "name": name}); got["exclusive"] != false {
		t.Fatalf("a new script reads exclusive=%v, want false", got["exclusive"])
	}
	setExclusive1986(t, c, name, true)
	if got := c.call("manage_script", map[string]any{"command": "get", "name": name}); got["exclusive"] != true {
		t.Errorf("get reads exclusive=%v after update exclusive=true", got["exclusive"])
	}

	status, out := c.rest(http.MethodPut, "/api/v1/portal/scripts/"+id+"/exclusive", jsonBody(t, map[string]any{"exclusive": false}))
	if status != http.StatusOK || out["exclusive"] != false {
		t.Fatalf("the portal route answered %d %v", status, out)
	}
	status, contract := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("reading the script: %d %v", status, contract)
	}
	inner, _ := contract["contract"].(map[string]any)
	if inner == nil {
		inner = contract
	}
	if inner["exclusive"] != false {
		t.Errorf("the portal contract reads exclusive=%v after the route set it false", inner["exclusive"])
	}
}

// TestIssue1986_ARunWhileAToolRunIsOpenIsRefusedNamingIt covers the manual
// triggers: with a run_script run open, run_script and a portal run are both
// refused naming it, and nothing is queued.
func TestIssue1986_ARunWhileAToolRunIsOpenIsRefusedNamingIt(t *testing.T) {
	c := connect(t)
	name, id := issue1986Script(t, c, "manual")
	setExclusive1986(t, c, name, true)
	open := startHeld1986(t, c, name)

	res, text, err := c.callRaw("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": 10, "holds": 1}, "wait_seconds": -1,
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("a second run_script was accepted while %s is open: %s", open, text)
	}
	for _, want := range []string{"run " + open, "started by run_script", "nothing was queued"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %q does not say %q", text, want)
		}
	}

	status, out := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs",
		jsonBody(t, map[string]any{"params": map[string]any{"hold_ms": 10, "holds": 1}}))
	if status != http.StatusConflict {
		t.Errorf("the portal run answered %d, want 409: %v", status, out)
	}
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "run "+open) {
		t.Errorf("the portal refusal does not name the open run: %v", out)
	}

	if n := len(runs1986(t, c, name)); n != 1 {
		t.Errorf("the script has %d runs, want only the open one: nothing else was queued", n)
	}
}

// TestIssue1986_AFireWhileAToolRunIsOpenIsSkippedNamingIt is criterion 1:
// the schedule fires while a run_script run is open, and the fire is recorded
// as skipped_overlap naming that run.
func TestIssue1986_AFireWhileAToolRunIsOpenIsSkippedNamingIt(t *testing.T) {
	c := connect(t)
	name, id := issue1986Script(t, c, "fire")
	setExclusive1986(t, c, name, true)
	open := startHeld1986(t, c, name)
	schedule1986(t, c, name, id)

	skip := awaitRun1986(t, c, name, "skipped_overlap", func(r map[string]any) bool { return r["status"] == "skipped_overlap" })
	errText, _ := skip["reason"].(string)
	for _, want := range []string{"run " + open, "started by run_script"} {
		if !strings.Contains(errText, want) {
			t.Errorf("the skip's reason %q does not say %q", errText, want)
		}
	}
}

// TestIssue1986_RunScriptWhileAScheduledRunIsOpenIsRefused is criterion 2:
// a scheduled run is executing, and run_script is refused naming it with
// nothing queued.
func TestIssue1986_RunScriptWhileAScheduledRunIsOpenIsRefused(t *testing.T) {
	c := connect(t)
	name, id := issue1986Script(t, c, "scheduled")
	setExclusive1986(t, c, name, true)
	schedule1986(t, c, name, id)
	fired := awaitRun1986(t, c, name, "running", func(r map[string]any) bool {
		return r["trigger"] == "schedule" && r["status"] == "running"
	})
	firedID, _ := fired["run_id"].(string)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": firedID})
	})

	res, text, err := c.callRaw("run_script", map[string]any{
		"name": name, "args": map[string]any{"hold_ms": 10, "holds": 1}, "wait_seconds": -1,
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("run_script was accepted while the scheduled run %s is open: %s", firedID, text)
	}
	for _, want := range []string{"run " + firedID, "started by its schedule"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %q does not say %q", text, want)
		}
	}
	if n := len(runs1986(t, c, name)); n != 1 {
		t.Errorf("the script has %d runs, want only the scheduled one", n)
	}
}

// TestIssue1986_ANonExclusiveScriptBehavesAsToday is criterion 3, and the
// regression the ticket asks for on a schedule slower than its interval: two
// run_script runs are both queued, and a fire while the schedule's own run is
// open is still skipped.
func TestIssue1986_ANonExclusiveScriptBehavesAsToday(t *testing.T) {
	c := connect(t)
	name, id := issue1986Script(t, c, "loose")
	first := startHeld1986(t, c, name)
	second := startHeld1986(t, c, name)
	if first == second {
		t.Fatalf("two run_script calls returned one run")
	}

	schedule1986(t, c, name, id)
	fired := awaitRun1986(t, c, name, "running from the schedule", func(r map[string]any) bool {
		return r["trigger"] == "schedule" && (r["status"] == "running" || r["status"] == "pending")
	})
	firedID, _ := fired["run_id"].(string)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": firedID})
	})

	fireNow1986(t, id)
	skip := awaitRun1986(t, c, name, "skipped_overlap", func(r map[string]any) bool { return r["status"] == "skipped_overlap" })
	if errText, _ := skip["reason"].(string); !strings.Contains(errText, "run "+firedID) {
		t.Errorf("the skip's reason %q does not name the schedule's open run %s", errText, firedID)
	}
}

// TestIssue1986_HelpDescribesTheSetting is criterion 5 for the tool: help
// names the setting and says what happens without it.
func TestIssue1986_HelpDescribesTheSetting(t *testing.T) {
	c := connect(t)
	help := c.call("manage_script", map[string]any{"command": "help"})
	overlap, _ := help["overlap"].(string)
	for _, want := range []string{"exclusive=true", "may overlap", "state check", "skipped_overlap"} {
		if !strings.Contains(overlap, want) {
			t.Errorf("help's overlap note does not say %q: %q", want, overlap)
		}
	}
}
