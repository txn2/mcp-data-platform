//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1994: an analyst can see the automations that exist, their schedules
// and their run history, read only. The owner key saves and schedules a
// script and runs it; the analyst key, which owns no part of it, reads the
// Automations routes the portal page reads.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` are
// strings and `params` an array of objects; run_script's `name` is a string,
// `args` an object and `wait_seconds` a number. The portal routes are REST:
// their path and query values are strings.

const analystAPIKey1994 = "acme-analyst-key"

func TestIssue1994_AnAnalystReadsAnotherPersonsAutomation(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	analyst := connectAs(t, analystAPIKey1994)
	name := fmt.Sprintf("acc-1994-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1994: the monthly report built for somebody else.",
		"source":      "def main():\n    \"\"\"Fails, printing what it was working on.\"\"\"\n    print(\"secret-progress-line\")\n    fail(\"the monthly feed has not published\")\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	id := scriptID1569(t, owner, name)
	if status, out := owner.rest(http.MethodPut, "/api/v1/portal/scripts/"+id+"/schedule",
		jsonBody(t, map[string]any{"cron": "0 6 1 * *", "timezone": "UTC", "enabled": true})); status != http.StatusOK {
		t.Fatalf("schedule the script: HTTP %d: %v", status, out)
	}
	run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	runID, _ := run["run_id"].(string)

	// The listing opens on every automation, this one included, with its
	// last run.
	status, list := analyst.rest(http.MethodGet, "/api/v1/portal/scripts?search="+name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("list: HTTP %d: %v", status, list)
	}
	rows, _ := list["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the analyst's default listing does not hold the owner's automation: %v", list)
	}
	row, _ := rows[0].(map[string]any)
	last, _ := row["last_run"].(map[string]any)
	if row["owned"] != false || last["status"] != "failed" || last["id"] != runID {
		t.Errorf("the row does not carry its last run for the analyst: %v", row)
	}
	if _, named := last["requested_by"]; named {
		t.Errorf("the row names who requested another person's run: %v", last)
	}

	// The schedule, without what each fire binds.
	status, sched := analyst.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/schedule", http.NoBody)
	if status != http.StatusOK || sched["cron_spec"] != "0 6 1 * *" {
		t.Errorf("the analyst cannot read the schedule: HTTP %d: %v", status, sched)
	}

	// The run history and the run, as how it went and not what it printed.
	status, history := analyst.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/runs", http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprintf("%v", history), runID) {
		t.Errorf("the analyst cannot read the run history: HTTP %d: %v", status, history)
	}
	status, detail := analyst.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/runs/"+runID, http.NoBody)
	if status != http.StatusOK || detail["withheld"] != true || detail["status"] != "failed" {
		t.Fatalf("the analyst cannot read how the run went: HTTP %d: %v", status, detail)
	}
	if errText, _ := detail["error"].(string); !strings.Contains(errText, "the monthly feed has not published") {
		t.Errorf("the run's error is not shown: %v", detail)
	}
	if strings.Contains(fmt.Sprintf("%v", detail), "secret-progress-line") {
		t.Errorf("the analyst reads what the run printed: %v", detail)
	}

	// The Runs and Schedules tabs span every automation.
	status, runs := analyst.rest(http.MethodGet, "/api/v1/portal/scripts/runs?script_id="+id, http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprintf("%v", runs), runID) {
		t.Errorf("the Runs tab does not hold the run: HTTP %d: %v", status, runs)
	}
	status, fires := analyst.rest(http.MethodGet, "/api/v1/portal/scripts/fires?tz=UTC", http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprintf("%v", fires), id) {
		t.Errorf("the Schedules tab does not hold the schedule: HTTP %d", status)
	}

	// Acting stays the owner's.
	if status, _ := analyst.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs/"+runID+"/cancel", http.NoBody); status != http.StatusNotFound {
		t.Errorf("the analyst may cancel another person's run: HTTP %d", status)
	}
	if status, _ := analyst.rest(http.MethodPut, "/api/v1/portal/scripts/"+id+"/schedule",
		jsonBody(t, map[string]any{"cron": "@daily", "timezone": "UTC", "enabled": true})); status != http.StatusNotFound {
		t.Errorf("the analyst may change another person's schedule: HTTP %d", status)
	}
}
