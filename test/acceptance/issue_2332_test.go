//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2332: an automation runs on each event a webhook source
// receives. A form submission enqueues a run of the script that names the
// form's source, which reads the submitted fields from run.event; a source no
// script names enqueues nothing; ten events received at once enqueue ten
// runs, each with its own event id, which a script set to run one at a time
// runs one at a time; and a run that fails is recorded with its cause while
// the next event still runs.
//
// Wire forms: manage_script create, update (event_source a string, "" to
// clear), runs and get_run take their arguments as literal tools/call params;
// the portal route PUT /api/v1/portal/scripts/{id}/event-source takes
// {"source": "<name>"}, a JSON string, and GET .../events no body; each form
// submission is a JSON object.
//
// The criteria run against the local stack: the platform and its run
// workers, Trino with the acme-scratch-resources scratch catalog, the store
// the receiver writes to, and Postgres.

// issue2332Echo hands back the event the run is for: its id and the email it
// carries, or fails for the address fail@example.com.
const issue2332Echo = `def main():
    """Hands back the event this run is for, and fails for one address."""
    ev = run.event or {"id": "", "payload": {"values": {}}}
    email = ev["payload"]["values"].get("email", "")
    if email == "fail@example.com":
        fail("refusing the address " + email)
    platform.result({"event_id": ev["id"], "email": email})
`

// issue2332Automation saves the echo script under a fresh name and names
// source as what it runs for, through manage_script update.
func issue2332Automation(t *testing.T, admin *client, prefix, source string, extra map[string]any) string {
	t.Helper()
	name := endpointName(prefix)
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2332Echo}, nil)
	update := map[string]any{"command": "update", "name": name, "event_source": source}
	for k, v := range extra {
		update[k] = v
	}
	if got := admin.call("manage_script", update); got["status"] != "updated" {
		t.Fatalf("naming %s's event source: %v", name, got)
	}
	if got := admin.call("manage_script", map[string]any{"command": "get", "name": name}); got["event_source"] != source {
		t.Fatalf("%s's event source reads %v, want %s", name, got["event_source"], source)
	}
	t.Cleanup(func() {
		admin.callRaw("manage_script", map[string]any{"command": "update", "name": name, "event_source": ""})
	})
	return name
}

// issue2332Runs reads a script's event runs once want of them have ended, or
// fails after two minutes.
func issue2332Runs(t *testing.T, admin *client, name string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out := admin.call("manage_script", map[string]any{"command": "runs", "name": name, "limit": 50})
		list, _ := out["runs"].([]any)
		var ended []map[string]any
		for _, item := range list {
			r, _ := item.(map[string]any)
			if r["trigger"] == "event" && (r["status"] == "succeeded" || r["status"] == "failed") {
				ended = append(ended, r)
			}
		}
		if len(ended) >= want {
			return ended
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s has %d ended event runs, want %d: %v", name, len(ended), want, list)
		}
		time.Sleep(time.Second)
	}
}

// issue2332Result is a run's handed-back result.
func issue2332Result(t *testing.T, admin *client, runID string) map[string]any {
	t.Helper()
	got := admin.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
	res, _ := got["result"].(map[string]any)
	return res
}

// issue2332Submit sends one accepted public submission to form.
func issue2332Submit(t *testing.T, form, email string) {
	t.Helper()
	shown := showForm(t, baseURL(), form, "", nil)
	shown.waitToFill()
	if status, out := submitForm(t, baseURL(), form, shown.fields(map[string]string{"email": email}), true, nil); status != http.StatusOK {
		t.Fatalf("submitting %s to %s: %d %v", email, form, status, out)
	}
}

// TestIssue2332_ASubmissionRunsTheScriptThatNamesItsSource is criteria 1 and
// 2: the submission enqueues one run of the script naming the form's source,
// which reads the submitted email from run.event; a form no script names runs
// nothing.
func TestIssue2332_ASubmissionRunsTheScriptThatNamesItsSource(t *testing.T) {
	admin := connect(t)
	form, _ := bindForm(t, admin, "acc-2332-form", formFields, map[string]any{"auth_mode": "public"})
	quiet, _ := bindForm(t, admin, "acc-2332-quiet", formFields, map[string]any{"auth_mode": "public"})
	name := issue2332Automation(t, admin, "acc-2332-echo", "form-"+form, nil)

	issue2332Submit(t, quiet, "nobody@example.com")
	issue2332Submit(t, form, "ana@example.com")
	runs := issue2332Runs(t, admin, name, 1)
	r := runs[0]
	if r["status"] != "succeeded" || r["event_source"] != "form-"+form || r["event_id"] == nil {
		t.Fatalf("the event run: %v", r)
	}
	if res := issue2332Result(t, admin, fmt.Sprint(r["run_id"])); res["email"] != "ana@example.com" || res["event_id"] != r["event_id"] {
		t.Fatalf("the run read %v from run.event, want ana@example.com and its event id %v", res, r["event_id"])
	}
	time.Sleep(3 * time.Second)
	if again := issue2332Runs(t, admin, name, 1); len(again) != 1 {
		t.Fatalf("a submission to a form no script names ran the script: %v", again)
	}

	// The portal reports the source and how the event runs stand.
	scriptID := issue2179ScriptID(t, admin, name)
	if status, sum := admin.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/events", nil); status != http.StatusOK || sum["max_queued"] == nil {
		t.Fatalf("the script's event summary: %d %v", status, sum)
	}
	if status, out := admin.rest(http.MethodPut, "/api/v1/portal/scripts/"+scriptID+"/event-source", jsonBody(t, map[string]any{"source": "form-" + quiet})); status != http.StatusOK || out["source"] != "form-"+quiet {
		t.Fatalf("naming another source from the portal: %d %v", status, out)
	}
}

// TestIssue2332_TenEventsRunTenTimesOneAtATime is criterion 3: ten
// submissions received at once enqueue ten runs, each with its own event id,
// and the script set to run one at a time runs them one after another.
func TestIssue2332_TenEventsRunTenTimesOneAtATime(t *testing.T) {
	admin := connect(t)
	form, _ := bindForm(t, admin, "acc-2332-burst", formFields, map[string]any{"auth_mode": "public"})
	name := issue2332Automation(t, admin, "acc-2332-serial", "form-"+form, map[string]any{"exclusive": "queue"})

	shown := make([]formShown, 10)
	for i := range shown {
		shown[i] = showForm(t, baseURL(), form, "", nil)
	}
	shown[len(shown)-1].waitToFill()
	var wg sync.WaitGroup
	for i := range shown {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := shown[i].fields(map[string]string{"email": fmt.Sprintf("burst%d@example.com", i)})
			if status, out := submitForm(t, baseURL(), form, body, true, nil); status != http.StatusOK {
				t.Errorf("submission %d: %d %v", i, status, out)
			}
		}(i)
	}
	wg.Wait()

	runs := issue2332Runs(t, admin, name, 10)
	ids := map[string]bool{}
	type span struct{ start, end time.Time }
	spans := make([]span, 0, len(runs))
	for _, r := range runs {
		ids[fmt.Sprint(r["event_id"])] = true
		start, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(r["started_at"]))
		end, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(r["finished_at"]))
		spans = append(spans, span{start, end})
	}
	if len(runs) != 10 || len(ids) != 10 {
		t.Fatalf("%d runs with %d event ids, want ten of each", len(runs), len(ids))
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	for i := 1; i < len(spans); i++ {
		if spans[i].start.Before(spans[i-1].end) {
			t.Fatalf("run %d started at %s, before the one before it ended at %s", i, spans[i].start, spans[i-1].end)
		}
	}
}

// TestIssue2332_AFailedRunIsRecordedAndTheNextEventStillRuns is criterion 4.
func TestIssue2332_AFailedRunIsRecordedAndTheNextEventStillRuns(t *testing.T) {
	admin := connect(t)
	form, _ := bindForm(t, admin, "acc-2332-fails", formFields, map[string]any{"auth_mode": "public"})
	name := issue2332Automation(t, admin, "acc-2332-failing", "form-"+form, nil)

	issue2332Submit(t, form, "fail@example.com")
	failed := issue2332Runs(t, admin, name, 1)
	got := admin.call("manage_script", map[string]any{"command": "get_run", "run_id": failed[0]["run_id"]})
	if got["status"] != "failed" || got["cause"] != "script" {
		t.Fatalf("the failing event's run: %v", got)
	}
	issue2332Submit(t, form, "next@example.com")
	runs := issue2332Runs(t, admin, name, 2)
	var next map[string]any
	for _, r := range runs {
		if r["run_id"] != failed[0]["run_id"] {
			next = r
		}
	}
	if next == nil || next["status"] != "succeeded" {
		t.Fatalf("the next event's run after a failure: %v", runs)
	}
}
