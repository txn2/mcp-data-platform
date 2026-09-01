//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1575: a script can be deleted from the portal.
//
// The capability was complete on the tool surface and absent from the page. A
// person created a script, edited its code, documented it, scheduled it, ran
// it, dry-ran it, reset its state, read its history and handed it to another
// owner without leaving the portal, and then had to open an agent session to
// get rid of one.
//
// Every criterion below is executed against the route the page's control calls
// -- DELETE /api/v1/portal/scripts/{id} -- as the person the criterion is
// about, and the consequences are read back through the surfaces a person
// reads them on: the portal script listing, the schedule route, the run
// listing, the state route, the asset tool, and the producers route (#1569).
//
// One clause of criterion 1 and the whole of criterion 3 are about what a
// browser renders -- that the control is on the page and that the confirmation
// names what goes before it runs. Those are proved in
// ui/src/pages/scripts/ScriptDelete.test.tsx and ScriptDetailPage.test.tsx.
// What is executed here is the half those tests cannot reach: that the route
// behind the control removes what the confirmation says it removes, leaves
// what it says it leaves, and answers the same sentence back.
//
// Wire forms: this route takes no request body and no query parameters. Its
// one parameter is the {id} path segment, a string, and a path segment admits
// exactly one JSON form, so every call below sends it that way. The
// manage_script calls that set the scripts up are typed in the tool schema and
// admit one form each: command, name, description, source, cron and timezone
// are strings, params an array of objects, state an object, and run_script's
// args an object -- each sent below as a literal tools/call parameter of that
// form.

// scriptSource1575 writes a declared output and carries state, so one script
// exercises both halves of what a delete must and must not take: the asset it
// produced, which survives, and the state it carried, which does not.
const scriptSource1575 = `
platform.export(
    name=run.params["target"],
    rows=[{"region": "north", "units": 41}],
    format="csv",
)
platform.save_state({"last_target": run.params["target"]})
`

func unique1575() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createScript1575 saves a script owned by the calling person and returns the
// id the platform holds it under, which is what the portal route is addressed
// by. It removes the script when the test ends unless the test removed it.
func createScript1575(t *testing.T, c *client, name string) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1575: a script deleted from the portal.",
		"source":      scriptSource1575,
		"params": []any{
			map[string]any{
				"name": "target", "type": "string", "required": true,
				"description": "The output name this run writes.",
			},
		},
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	out := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("manage_script get returned no id for %q: %v", name, out)
	}
	return id
}

// runScript1575 runs the script and fails on a run that did not succeed.
func runScript1575(t *testing.T, c *client, name, target string) {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"target": target}, "wait_seconds": 120,
	})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("run of %s did not succeed: %v", name, out)
	}
}

// deleteFromPortal1575 calls the route the page's delete control calls.
func deleteFromPortal1575(t *testing.T, c *client, scriptID string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodDelete, "/api/v1/portal/scripts/"+scriptID, http.NoBody)
}

// listedInPortal1575 reports whether the portal's script listing -- the page a
// delete returns to -- still holds this script.
func listedInPortal1575(t *testing.T, c *client, scriptID string) bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET portal scripts: status %d: %v", status, body)
	}
	rows, _ := body["data"].([]any)
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sc, _ := row["script"].(map[string]any)
		if id, _ := sc["id"].(string); id == scriptID {
			return true
		}
	}
	return false
}

// runsOf1575 counts the runs of one script the caller can read. A run listing
// on a script that is gone is answered as not-found, which is itself the
// evidence the history went with it.
func runsOf1575(t *testing.T, c *client, scriptID string) (int, int) {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/runs", http.NoBody)
	rows, _ := body["data"].([]any)
	return status, len(rows)
}

// producersOf1575 reads the Written by panel's route for an asset.
func producersOf1575(t *testing.T, c *client, assetID string) []map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID+"/producers", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET asset %s producers: status %d: %v", assetID, status, body)
	}
	list, _ := body["data"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if row, ok := item.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

// ownedAssetID1575 finds an asset this person owns by name.
func ownedAssetID1575(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("manage_asset", map[string]any{"action": "list", "limit": 200})
	list, _ := out["assets"].([]any)
	for _, item := range list {
		a, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := a["name"].(string); got == name {
			id, _ := a["id"].(string)
			return id
		}
	}
	t.Fatalf("no asset named %q among this person's assets", name)
	return ""
}

// TestIssue1575_OwnerDeletesTheirOwnScriptFromThePortal is criterion 1: the
// person the script belongs to removes it through the route the page's control
// calls, and the listing the page returns to no longer holds it.
func TestIssue1575_OwnerDeletesTheirOwnScriptFromThePortal(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1575-own-" + unique1575()
	id := createScript1575(t, owner, name)

	if !listedInPortal1575(t, owner, id) {
		t.Fatalf("the script is not in its owner's listing before the delete")
	}

	status, body := deleteFromPortal1575(t, owner, id)
	if status != http.StatusOK {
		t.Fatalf("DELETE as the owner: status %d: %v", status, body)
	}
	if got, _ := body["status"].(string); got != "deleted" {
		t.Fatalf("the delete did not report the script deleted: %v", body)
	}
	if got, _ := body["name"].(string); got != name {
		t.Fatalf("the delete named %q rather than %q", got, name)
	}
	if listedInPortal1575(t, owner, id) {
		t.Fatalf("the script is still in the listing the page returns to")
	}
}

// TestIssue1575_TheDeleteSaysWhatWentAndWhatStayed is criterion 3's executable
// half: the sentence the page shows after the delete names what the
// confirmation named before it. The confirmation itself is a browser fact and
// is proved in ScriptDelete.test.tsx.
func TestIssue1575_TheDeleteSaysWhatWentAndWhatStayed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1575-says-" + unique1575()
	id := createScript1575(t, owner, name)

	status, body := deleteFromPortal1575(t, owner, id)
	if status != http.StatusOK {
		t.Fatalf("DELETE as the owner: status %d: %v", status, body)
	}
	message, _ := body["message"].(string)
	for _, named := range []string{
		"saved versions", "schedule", "run history", "state",
		"assets and resources it wrote remain",
	} {
		if !strings.Contains(message, named) {
			t.Fatalf("the delete's answer does not name %q: %q", named, message)
		}
	}
}

// TestIssue1575_AnAdministratorDeletesSomebodyElsesScript is criterion 2's
// first half: an administrator's unrestricted reach applies to this route as it
// does to every other script route.
func TestIssue1575_AnAdministratorDeletesSomebodyElsesScript(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	name := "acceptance-1575-admin-" + unique1575()
	id := createScript1575(t, owner, name)

	status, body := deleteFromPortal1575(t, admin, id)
	if status != http.StatusOK {
		t.Fatalf("DELETE as an administrator: status %d: %v", status, body)
	}
	if listedInPortal1575(t, owner, id) {
		t.Fatalf("the owner still sees a script an administrator deleted")
	}
}

// TestIssue1575_SomebodyElseIsRefused is criterion 2's second half: a
// signed-in person who is neither the owner nor an administrator is refused,
// and the script is untouched. The refusal is a not-found rather than a
// forbidden, which is what every other script route answers a caller who may
// not see the script: the difference would confirm that it exists.
func TestIssue1575_SomebodyElseIsRefused(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	name := "acceptance-1575-peer-" + unique1575()
	id := createScript1575(t, owner, name)

	status, body := deleteFromPortal1575(t, peer, id)
	if status != http.StatusNotFound {
		t.Fatalf("DELETE as somebody else: status %d (want 404): %v", status, body)
	}
	if !listedInPortal1575(t, owner, id) {
		t.Fatalf("a refused delete removed the script anyway")
	}
}

// TestIssue1575_TheScheduleAndTheRunsGoWithIt is criterion 4: a script with a
// schedule and a run history loses both, and nothing fires it afterwards.
//
// The fire is checked by waiting past a cadence that fires every minute rather
// than by reasoning from the absent schedule row: the criterion is about what
// happens next, and the run listing is where a fire would show up.
func TestIssue1575_TheScheduleAndTheRunsGoWithIt(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1575-sched-" + unique1575()
	id := createScript1575(t, owner, name)

	owner.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name,
		"cron": "* * * * *", "timezone": "UTC",
		"args": map[string]any{"target": "acceptance-1575-sched-output"},
	})
	runScript1575(t, owner, name, "acceptance-1575-sched-output")

	if status, runs := runsOf1575(t, owner, id); status != http.StatusOK || runs == 0 {
		t.Fatalf("the script has no run history to lose: status %d, %d runs", status, runs)
	}
	scheduleStatus, _ := owner.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/schedule", http.NoBody)
	if scheduleStatus != http.StatusOK {
		t.Fatalf("the script has no schedule to lose: status %d", scheduleStatus)
	}

	if status, body := deleteFromPortal1575(t, owner, id); status != http.StatusOK {
		t.Fatalf("DELETE as the owner: status %d: %v", status, body)
	}

	if status, _ := owner.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/schedule", http.NoBody); status != http.StatusNotFound {
		t.Fatalf("the schedule survived the delete: status %d", status)
	}
	if status, runs := runsOf1575(t, owner, id); status != http.StatusNotFound || runs != 0 {
		t.Fatalf("the run history survived the delete: status %d, %d runs", status, runs)
	}

	// Past the next fire the schedule would have had. A session of its own,
	// because the wait outlives the one the checks above were made in.
	time.Sleep(75 * time.Second)
	after := connectAs(t, devOwnerAPIKey)
	if status, runs := runsOf1575(t, after, id); status != http.StatusNotFound || runs != 0 {
		t.Fatalf("a fire happened after the delete: status %d, %d runs", status, runs)
	}
	if listedInPortal1575(t, after, id) {
		t.Fatalf("the deleted script came back")
	}
}

// TestIssue1575_WhatTheScriptWroteStays is criterion 5: the assets and
// resources a deleted script produced remain, and the producer rows recording
// that it wrote them remain with them (#1569). Deleting a script is not
// deleting the reports it wrote, and a person doing it is told so.
func TestIssue1575_WhatTheScriptWroteStays(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	unique := unique1575()
	name := "acceptance-1575-wrote-" + unique
	target := "acceptance-1575-output-" + unique
	id := createScript1575(t, owner, name)

	runScript1575(t, owner, name, target)
	assetID := ownedAssetID1575(t, owner, target)
	before := producersOf1575(t, owner, assetID)
	if !namesProducer1575(before, id) {
		t.Fatalf("the asset does not name the script that wrote it before the delete: %v", before)
	}

	if status, body := deleteFromPortal1575(t, owner, id); status != http.StatusOK {
		t.Fatalf("DELETE as the owner: status %d: %v", status, body)
	}

	// The asset is still the owner's, readable by the tool they read it with.
	if got := ownedAssetID1575(t, owner, target); got != assetID {
		t.Fatalf("the asset the script wrote is gone or changed identity: %q", got)
	}
	after := producersOf1575(t, owner, assetID)
	if !namesProducer1575(after, id) {
		t.Fatalf("the producer record naming the deleted script is gone: %v", after)
	}
}

// TestIssue1575_ThePortalAndTheToolLeaveTheSameState is criterion 6: two
// scripts made identically, one removed through the portal route and one
// through manage_script command=delete, are afterwards indistinguishable
// through every surface that could tell them apart.
func TestIssue1575_ThePortalAndTheToolLeaveTheSameState(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	unique := unique1575()
	viaPortal := "acceptance-1575-portal-" + unique
	viaTool := "acceptance-1575-tool-" + unique
	portalID := createScript1575(t, owner, viaPortal)
	toolID := createScript1575(t, owner, viaTool)

	for _, pair := range []struct{ name, target string }{
		{viaPortal, "acceptance-1575-portal-output-" + unique},
		{viaTool, "acceptance-1575-tool-output-" + unique},
	} {
		owner.call("manage_script", map[string]any{
			"command": "schedule_set", "name": pair.name,
			"cron": "0 7 * * *", "timezone": "UTC",
			"args": map[string]any{"target": pair.target},
		})
		runScript1575(t, owner, pair.name, pair.target)
	}

	if status, body := deleteFromPortal1575(t, owner, portalID); status != http.StatusOK {
		t.Fatalf("DELETE through the portal: status %d: %v", status, body)
	}
	owner.call("manage_script", map[string]any{"command": "delete", "name": viaTool})

	for _, id := range []string{portalID, toolID} {
		if listedInPortal1575(t, owner, id) {
			t.Fatalf("script %s is still listed", id)
		}
		if status, _ := owner.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/schedule", http.NoBody); status != http.StatusNotFound {
			t.Fatalf("script %s kept its schedule: status %d", id, status)
		}
		if status, runs := runsOf1575(t, owner, id); status != http.StatusNotFound || runs != 0 {
			t.Fatalf("script %s kept its runs: status %d, %d runs", id, status, runs)
		}
		if status, _ := owner.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/state", http.NoBody); status != http.StatusNotFound {
			t.Fatalf("script %s kept its state: status %d", id, status)
		}
		if status, _ := owner.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, http.NoBody); status != http.StatusNotFound {
			t.Fatalf("script %s still has a page: status %d", id, status)
		}
	}

	// And both of them left what they wrote behind, which is the half of the
	// equivalence a cascade could get wrong in one direction only.
	for _, target := range []string{
		"acceptance-1575-portal-output-" + unique,
		"acceptance-1575-tool-output-" + unique,
	} {
		ownedAssetID1575(t, owner, target)
	}
}

// TestIssue1575_DeletingWhatIsNotThereIsNotAnError is criterion 7: a script
// that never existed and one already deleted are both answered as not-found
// rather than as a server failure.
func TestIssue1575_DeletingWhatIsNotThereIsNotAnError(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1575-twice-" + unique1575()
	id := createScript1575(t, owner, name)

	if status, body := deleteFromPortal1575(t, owner, id); status != http.StatusOK {
		t.Fatalf("the first delete: status %d: %v", status, body)
	}
	if status, body := deleteFromPortal1575(t, owner, id); status != http.StatusNotFound {
		t.Fatalf("the second delete: status %d (want 404): %v", status, body)
	}
	absent := "3f4b2c18-0000-4000-8000-000000000000"
	if status, body := deleteFromPortal1575(t, owner, absent); status != http.StatusNotFound {
		t.Fatalf("deleting a script that never existed: status %d (want 404): %v", status, body)
	}
}

// namesProducer1575 reports whether a Written by listing carries a row for one
// producer id.
func namesProducer1575(rows []map[string]any, producerID string) bool {
	for _, row := range rows {
		if id, _ := row["id"].(string); id == producerID {
			return true
		}
	}
	return false
}
