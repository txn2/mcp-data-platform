//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2635: a watch handler, a script that runs after a tool call
// it matches succeeds. The run is queued off the request path, reads the call
// from run.call, runs with the approving administrator's authority, and is
// readable by administrators only. Calls go through the real client to the
// api-test fixture: PUT /v1/echo for an update carrying the record's id in its
// body, GET /v1/status/{code} for a call that fails.
//
// Wire forms: manage_script handler_set takes kind "watch", connection a
// string, operations a JSON array of strings, conditions a JSON array of
// objects with in, name and equals strings, approve a JSON boolean; the
// watched api_invoke_endpoint body is sent as a JSON object and as a string of
// JSON, which queue the same run.

// watchScript notifies the channel it names when it runs, with the id of the
// record the call changed and marker in the title.
func watchScript(channel, marker string) string {
	return fmt.Sprintf(`def changed(call):
    """The record the watched call changed; a draft, which has no call, changed none."""
    if call == None:
        return 0
    return call["body"]["id"]

def main():
    """Tells the account's owner that their customer changed."""
    platform.notify(channel = %q, title = "Customer %%s changed %s" %% changed(run.call), body = "A watched update.")
`, channel, marker)
}

// countingWatch prints the call's body so its run's prints hold what it saw.
const countingWatch = `def main():
    """Prints the body of the call it watched; a draft has none."""
    print(run.call["body"] if run.call != None else "no call")
`

// watchedRuns is every run of the script name, owned by owner ("" for the
// administrator's own), as an administrator lists them.
func watchedRuns(t *testing.T, admin *client, name string, owner ...string) []map[string]any {
	t.Helper()
	args := map[string]any{"command": "runs", "name": name}
	if len(owner) > 0 && owner[0] != "" {
		args["owner_email"] = owner[0]
	}
	return listOf(admin.call("manage_script", args), "runs")
}

// awaitWatchRuns waits until the script name has want runs that finished, and
// returns them.
func awaitWatchRuns(t *testing.T, admin *client, name string, want int, owner ...string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		runs := watchedRuns(t, admin, name, owner...)
		done := 0
		for _, r := range runs {
			if r["status"] == "succeeded" || r["status"] == "failed" {
				done++
			}
		}
		if done >= want {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s has %d finished runs, want %d: %v", name, done, want, runs)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestIssue2635_AWatchOnOneRecordNotifiesOnce: a watch on PUT /v1/echo for
// the record with id 123 notifies the named person once when record 123 is
// updated through the platform, and queues nothing when record 124 is.
func TestIssue2635_AWatchOnOneRecordNotifiesOnce(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	channel := handlerNonce("acc-2635-mail")
	putChannel(t, admin, channel, map[string]any{"kind": "email", "recipients": []string{"acc-2635-owner@example.com"}})
	t.Cleanup(func() { deleteChannel(t, admin, channel) })
	marker := handlerNonce("m2635")
	name := handlerNonce("acc-2635-one")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": watchScript(channel, marker)}, nil)
	bindHandler(t, admin, name, "", "watch", map[string]any{
		"operations": []string{"PUT /v1/echo"},
		"conditions": []any{map[string]any{"in": "body", "name": "id", "equals": "123"}},
	})
	if res, text := echoPut(t, owner, handlerNonce("t2635-124"), map[string]any{"id": 124, "name": "Other"}); res.IsError {
		t.Fatalf("updating record 124: %s", text)
	}
	if res, text := echoPut(t, owner, handlerNonce("t2635-123"), map[string]any{"id": 123, "name": "Watched"}); res.IsError {
		t.Fatalf("updating record 123: %s", text)
	}
	awaitWatchRuns(t, admin, name, 1)
	time.Sleep(2 * time.Second)
	if runs := watchedRuns(t, admin, name); len(runs) != 1 {
		t.Errorf("the watch queued %d runs for one matching update and one other, want 1: %v", len(runs), runs)
	}
	status, body := admin.rest(http.MethodGet, "/api/v1/admin/notifications?channel="+channel+"&per_page=50", nil)
	raw, _ := json.Marshal(body)
	sent := 0
	rows, _ := body["data"].([]any)
	for _, row := range rows {
		if m, _ := row.(map[string]any); m["item_title"] == "Customer 123 changed "+marker {
			sent++
		}
	}
	if status != http.StatusOK || sent != 1 {
		t.Errorf("the named person was notified %d times about record 123, want once: %d %s", sent, status, raw)
	}
	if strings.Contains(string(raw), "Customer 124") {
		t.Errorf("record 124's update notified: %s", raw)
	}
}

// gateWatch waits while it holds a watched call, so on a script that runs
// one at a time every run queued behind it waits its turn.
const gateWatch = `def main():
    """Waits on a watched call, so the runs behind it wait their turn."""
    platform.sleep(60 if run.call != None else 0.01)
    print("done")
`

// TestIssue2635_AWatchDoesNotDelayTheCall: the update's p50 over 200 calls
// with the watch bound is within 5% of the same calls with none. What is
// measured is the call's own path, matching it and queuing its run: the
// watch's script runs one at a time behind a run that waits, so the runs are
// written and none executes on the replicas while the calls are timed (the
// work a run does is the worker's, wherever it runs).
func TestIssue2635_AWatchDoesNotDelayTheCall(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	p50 := func() time.Duration {
		var d []time.Duration
		for i := range 200 {
			start := time.Now()
			if res, text := echoPut(t, owner, handlerNonce("t2635-lat"), map[string]any{"id": 900000 + i}); res.IsError {
				t.Fatalf("an update failed: %s", text)
			}
			d = append(d, time.Since(start))
		}
		slices.Sort(d)
		return d[len(d)/2]
	}
	name := handlerNonce("acc-2635-lat")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": gateWatch}, nil)
	admin.call("manage_script", map[string]any{"command": "update", "name": name, "exclusive": "queue"})
	t.Cleanup(func() {
		for _, r := range watchedRuns(t, admin, name) {
			if r["status"] == "running" {
				admin.callRaw("manage_script", map[string]any{"command": "cancel_run", "run_id": fmt.Sprint(r["run_id"])})
			}
		}
		admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	p50()
	before := p50()
	h := bindHandler(t, admin, name, "", "watch", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	with := p50()
	admin.handlerOff(h["handler"])
	after := p50()
	base := (before + after) / 2
	if float64(with) > float64(base)*1.05 {
		t.Errorf("the update's p50 with a watch is %v, more than 5%% over %v", with, base)
	}
	if runs := watchedRuns(t, admin, name); len(runs) == 0 {
		t.Errorf("the watch queued no runs: its calls were not matched")
	}
}

// TestIssue2635_AFailedCallQueuesNothing: a call that fails queues no run; the
// same call succeeding queues one.
func TestIssue2635_AFailedCallQueuesNothing(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2635-fail")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": countingWatch}, nil)
	bindHandler(t, admin, name, "", "watch", map[string]any{"operations": []string{"GET /v1/status/*"}, "users": []string{devOwnerEmail}})
	for _, code := range []string{"500", "200"} {
		res, text, err := owner.callRaw("api_invoke_endpoint", map[string]any{"purpose": handlerPurpose, "connection": handlerConnection, "method": "GET", "path": "/v1/status/" + code})
		if err != nil || res.IsError {
			t.Fatalf("GET /v1/status/%s: %v %s", code, err, text)
		}
	}
	awaitWatchRuns(t, admin, name, 1)
	time.Sleep(2 * time.Second)
	if runs := watchedRuns(t, admin, name); len(runs) != 1 {
		t.Errorf("a failed and a succeeding call queued %d runs, want 1: %v", len(runs), runs)
	}
}

// TestIssue2635_OnlyAnAdministratorCreatesOrReadsAWatch: a non-administrator
// cannot create the watch, and its owner, who is not one, cannot read its
// runs.
func TestIssue2635_OnlyAnAdministratorCreatesOrReadsAWatch(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	name := handlerNonce("acc-2635-who")
	owner.saveScript(map[string]any{"command": "create", "name": name, "source": countingWatch}, nil)
	if out, text := owner.handlerSetRaw(name, "watch", map[string]any{"connection": handlerConnection, "operations": []string{"PUT /v1/echo"}}); out != nil {
		t.Fatalf("a non-administrator created a watch: %v", out)
	} else if !strings.Contains(text, "administrator") {
		t.Errorf("the refusal does not say an administrator creates a watch: %s", text)
	}
	bindHandler(t, admin, name, devOwnerEmail, "watch", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devPeerEmailAddr}})
	marker := handlerNonce("peer-body")
	echoPut(t, peer, handlerNonce("t2635-who"), map[string]any{"marker": marker})
	runs := awaitWatchRuns(t, admin, name, 1, devOwnerEmail)
	id := fmt.Sprint(runs[0]["run_id"])
	full := admin.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
	if raw, _ := json.Marshal(full); !strings.Contains(string(raw), marker) {
		t.Errorf("the administrator's run does not carry the call it watched: %s", raw)
	}
	res, text, _ := owner.callRaw("manage_script", map[string]any{"command": "get_run", "run_id": id})
	if res != nil && !res.IsError && strings.Contains(text, marker) {
		t.Errorf("the owner read a watch run's input: %s", text)
	}
	if raw, _ := json.Marshal(owner.call("manage_script", map[string]any{"command": "runs", "name": name})); strings.Contains(string(raw), marker) {
		t.Errorf("the owner's run list carries a watch run's input: %s", raw)
	}
	if status, body := owner.rest(http.MethodGet, "/api/v1/portal/scripts/runs/"+id, nil); status == http.StatusOK {
		if raw, _ := json.Marshal(body); strings.Contains(string(raw), marker) {
			t.Errorf("the owner's portal read of the run carries its input: %s", raw)
		}
	}
}

// TestIssue2635_AStoredSecretReachesNoWatchSink: a {{secret:<name>}} value in
// a watched call reaches no watch run input, print, recording, audit row, log
// or span, searched as written, base64, hex, URL- and JSON-escaped.
func TestIssue2635_AStoredSecretReachesNoWatchSink(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	secretName := handlerNonce("acc-2635-secret")
	value := handlerNonce("SeCrEt-2635-value")
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secretName, map[string]any{
		"description": "Acceptance.", "value": value, "allow_connections": []string{handlerConnection}, "allow_personas": []string{"collaborator"},
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the secret answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secretName, nil) })
	name := handlerNonce("acc-2635-sink")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": countingWatch}, nil)
	bindHandler(t, admin, name, "", "watch", map[string]any{"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail}})
	res, _, err := owner.callRaw("api_invoke_endpoint", map[string]any{
		"purpose":    handlerPurpose,
		"connection": handlerConnection, "method": "PUT", "path": "/v1/echo",
		"headers": map[string]any{"X-Account-Key": "{{secret:" + secretName + "}}"},
		"body":    map[string]any{"key": "{{secret:" + secretName + "}}"},
	})
	if err != nil || res.IsError {
		t.Fatalf("the watched call failed: %v %s", err, firstText(res))
	}
	runs := awaitWatchRuns(t, admin, name, 1)
	id := fmt.Sprint(runs[0]["run_id"])
	sinks := map[string]string{}
	run := admin.call("manage_script", map[string]any{"command": "get_run", "run_id": id})
	raw, _ := json.Marshal(run)
	sinks["the run's input and prints"] = string(raw)
	if !strings.Contains(string(raw), "{{secret:"+secretName+"}}") {
		t.Errorf("the run was not given the placeholder: %s", raw)
	}
	rec, _, _ := admin.callRaw("manage_script", map[string]any{"command": "recording", "run_id": id})
	if rec != nil {
		sinks["the run's recording"] = firstText(rec)
	}
	rows := admin.list("/api/v1/admin/audit/events?session_id=" + owner.sessionID + "&per_page=50")
	raw, _ = json.Marshal(rows)
	sinks["audit rows"] = string(raw)
	sinks["logs and spans"] = devSinkText(t)
	for sink, text := range sinks {
		for _, form := range secretForms(value) {
			if strings.Contains(text, form) {
				t.Errorf("the secret's value reached %s as %q", sink, form)
			}
		}
	}
}

// TestIssue2635_BothBodyFormsQueueTheSameRun: the watched body sent as an
// object and as a string of JSON queues the same run.
func TestIssue2635_BothBodyFormsQueueTheSameRun(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	name := handlerNonce("acc-2635-forms")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": countingWatch}, nil)
	bindHandler(t, admin, name, "", "watch", map[string]any{
		"operations": []string{"PUT /v1/echo"}, "users": []string{devOwnerEmail},
		"conditions": []any{map[string]any{"in": "body", "name": "id", "equals": "777"}},
	})
	body := map[string]any{"id": 777, "name": "Both forms"}
	for _, form := range []any{body, asJSONString(t, body)} {
		if res, text := echoPut(t, owner, handlerNonce("t2635-forms"), form); res.IsError {
			t.Fatalf("the watched call failed: %s", text)
		}
	}
	runs := awaitWatchRuns(t, admin, name, 2)
	if len(runs) != 2 {
		t.Fatalf("the two body forms queued %d runs, want 2: %v", len(runs), runs)
	}
	var calls []string
	for _, r := range runs {
		run := admin.call("manage_script", map[string]any{"command": "get_run", "run_id": fmt.Sprint(r["run_id"])})
		call, _ := run["call"].(map[string]any)
		raw, _ := json.Marshal(map[string]any{"operation": call["operation"], "body": call["body"], "items": call["items"]})
		calls = append(calls, string(raw))
	}
	if calls[0] != calls[1] || !strings.Contains(calls[0], "Both forms") {
		t.Errorf("the two forms queued runs with different calls:\n%s\n%s", calls[0], calls[1])
	}
}
