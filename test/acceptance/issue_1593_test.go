//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1593: manage_script command=delete answers with the same account of
// the removal the portal route answers with.
//
// Both surfaces perform one cascade through one store. The route explained it
// ("... is gone, with its saved versions, its schedule, its run history and the
// state it carried. The assets and resources it wrote remain ...") while the
// tool answered {"name": ..., "status": "deleted"}, so an agent deleting a
// script for a person had nothing to tell them about the schedule and history
// that went, or the files that stayed, and either said nothing or invented it.
//
// Every criterion below is executed against the surface it is about: the tool
// through a real tools/call, the route through DELETE
// /api/v1/portal/scripts/{id}, both as the person who owns the script.
//
// Criterion 4's audit found two more answers composed twice, and they are
// executed here as well: the sentence a save answers with, and the sentence a
// reset of the carried state answers with.
//
// Wire forms: the manage_script parameters these calls send are typed in the
// tool's input schema and each admits exactly one JSON form -- command, name,
// source, state_action, cron and timezone are strings, args and state are
// objects, wait_seconds a number -- and every call below sends them as literal
// tools/call parameters of that form. Of the portal routes, DELETE
// /portal/scripts/{id} and DELETE /portal/scripts/{id}/state take no request
// body and no query parameters, and PUT /portal/scripts/{id}/source takes one
// typed field, `source`, a string. Every parameter touched is a string, an
// object, or a path segment, and each admits exactly one JSON form, so there
// is no second form of any of them to send.

// scriptSource1593 writes an output and carries state, so one script exercises
// both halves of the account: the asset it produced, which survives the
// delete, and the state it carried, which does not.
const scriptSource1593 = `
platform.export(
    name=run.params["target"],
    rows=[{"region": "north", "units": 41}],
    format="csv",
)
platform.save_state({"last_target": run.params["target"]})
`

func unique1593() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createScript1593 saves a script owned by the calling person and returns the
// id the portal route addresses it by.
func createScript1593(t *testing.T, c *client, name string) string {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #1593: the account a delete gives of itself.",
		"source":      scriptSource1593,
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

// loadUp1593 gives the script everything a delete can take with it: a cadence,
// a run history, and a carried state object. The run produces all three at
// once -- save_state writes the state and the run itself is the history -- so
// the script is in the state the fullest account describes.
func loadUp1593(t *testing.T, c *client, name string) {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name,
		"cron": "0 3 * * *", "timezone": "UTC",
		"args": map[string]any{"target": "acceptance-1593-scheduled"},
	})
	out := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"target": "acceptance-1593-" + unique1593()},
		"wait_seconds": 120,
	})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("the run that gives %s a history and a state did not succeed: %v", name, out)
	}
}

// deleteByTool1593 removes the script through manage_script and returns the
// sentence the tool answered with.
func deleteByTool1593(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("manage_script", map[string]any{"command": "delete", "name": name})
	if got, _ := out["status"].(string); got != "deleted" {
		t.Fatalf("manage_script delete did not report the script deleted: %v", out)
	}
	message, _ := out["message"].(string)
	if message == "" {
		t.Fatalf("manage_script delete answered no account of the removal: %v", out)
	}
	return message
}

// deleteByPortal1593 removes the script through the route the page's control
// calls and returns the sentence the route answered with.
func deleteByPortal1593(t *testing.T, c *client, scriptID string) string {
	t.Helper()
	status, body := c.rest(http.MethodDelete, "/api/v1/portal/scripts/"+scriptID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("DELETE portal script %s: status %d: %v", scriptID, status, body)
	}
	message, _ := body["message"].(string)
	if message == "" {
		t.Fatalf("the portal delete answered no account of the removal: %v", body)
	}
	return message
}

// TestIssue1593_TheToolReportsWhatWentAndWhatStayed is criterion 1. A script
// that had all four -- versions, a schedule, a run history and carried state --
// is deleted through the tool, and the answer names each of them and states
// that the assets it wrote are still there.
func TestIssue1593_TheToolReportsWhatWentAndWhatStayed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acceptance-1593-tool-" + unique1593()
	createScript1593(t, owner, name)
	loadUp1593(t, owner, name)

	message := deleteByTool1593(t, owner, name)

	for _, went := range []string{"saved versions", "schedule", "run history", "state it carried"} {
		if !strings.Contains(message, went) {
			t.Fatalf("the tool's account does not name the %s the delete removed: %q", went, message)
		}
	}
	if !strings.Contains(message, "remain") {
		t.Fatalf("the tool's account does not say the assets and resources it wrote stayed: %q", message)
	}
	if !strings.Contains(message, "still record that it wrote them") {
		t.Fatalf("the tool's account does not say the producer records stayed: %q", message)
	}
	t.Logf("manage_script command=delete answered: %s", message)
}

// TestIssue1593_BothSurfacesGiveTheSameAccount is criterion 2, executed rather
// than reasoned about: two scripts in the same state are deleted, one through
// each surface, and the two sentences are the same words.
//
// The comparison substitutes each script's own name out, because the name is
// the one part of the sentence that is supposed to differ.
func TestIssue1593_BothSurfacesGiveTheSameAccount(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)

	toolName := "acceptance-1593-same-tool-" + unique1593()
	createScript1593(t, owner, toolName)
	loadUp1593(t, owner, toolName)

	portalName := "acceptance-1593-same-portal-" + unique1593()
	portalID := createScript1593(t, owner, portalName)
	loadUp1593(t, owner, portalName)

	toolMessage := deleteByTool1593(t, owner, toolName)
	portalMessage := deleteByPortal1593(t, owner, portalID)

	t.Logf("tool:   %s", toolMessage)
	t.Logf("portal: %s", portalMessage)

	toolShape := strings.Replace(toolMessage, toolName, "<script>", 1)
	portalShape := strings.Replace(portalMessage, portalName, "<script>", 1)
	if toolShape != portalShape {
		t.Fatalf("the two surfaces gave different accounts of the same removal:\n  tool:   %q\n  portal: %q",
			toolShape, portalShape)
	}
}

// TestIssue1593_NeitherSurfaceNamesWhatTheScriptNeverHad is criterion 3. A
// script that was never scheduled, never ran and saved no state loses none of
// the three, and reporting them destroyed would be as wrong as saying nothing.
func TestIssue1593_NeitherSurfaceNamesWhatTheScriptNeverHad(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)

	toolName := "acceptance-1593-bare-tool-" + unique1593()
	createScript1593(t, owner, toolName)
	portalName := "acceptance-1593-bare-portal-" + unique1593()
	portalID := createScript1593(t, owner, portalName)

	accounts := map[string]string{
		"tool":   deleteByTool1593(t, owner, toolName),
		"portal": deleteByPortal1593(t, owner, portalID),
	}
	for surface, message := range accounts {
		if !strings.Contains(message, "saved versions") {
			t.Fatalf("the %s account does not name the saved versions the delete removed: %q", surface, message)
		}
		for _, absent := range []string{"schedule", "run history", "state it carried"} {
			if strings.Contains(message, absent) {
				t.Fatalf("the %s account names the %s a script that never had one lost: %q",
					surface, absent, message)
			}
		}
		if !strings.Contains(message, "remain") {
			t.Fatalf("the %s account does not say what stayed: %q", surface, message)
		}
		t.Logf("%s: %s", surface, message)
	}
}

// TestIssue1593_SaveSaysTheSameOnBothSurfaces is the first of criterion 4's
// two other pairs. The tool composed one sentence about what a saved version
// means for whether anything runs it and the portal route composed another,
// with the same three refusals and different words; both now answer
// script.SavedMessage.
func TestIssue1593_SaveSaysTheSameOnBothSurfaces(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)

	toolName := "acceptance-1593-save-tool-" + unique1593()
	createScript1593(t, owner, toolName)
	portalName := "acceptance-1593-save-portal-" + unique1593()
	portalID := createScript1593(t, owner, portalName)

	edited := scriptSource1593 + "\n# edited by acceptance #1593\n"
	toolOut := owner.call("manage_script", map[string]any{
		"command": "update", "name": toolName, "source": edited,
	})
	toolMessage, _ := toolOut["message"].(string)

	status, body := owner.rest(http.MethodPut, "/api/v1/portal/scripts/"+portalID+"/source",
		strings.NewReader(mustJSON1593(t, map[string]any{"source": edited})))
	if status != http.StatusOK {
		t.Fatalf("PUT portal script source: status %d: %v", status, body)
	}
	portalMessage, _ := body["message"].(string)

	t.Logf("tool:   %s", toolMessage)
	t.Logf("portal: %s", portalMessage)
	if toolMessage == "" || toolMessage != portalMessage {
		t.Fatalf("the two surfaces gave different accounts of the same save:\n  tool:   %q\n  portal: %q",
			toolMessage, portalMessage)
	}
}

// TestIssue1593_StateResetSaysTheSameOnBothSurfaces is criterion 4's other
// pair: the two sentences were byte-identical string literals in two files,
// which is drift waiting to happen rather than drift that had happened.
func TestIssue1593_StateResetSaysTheSameOnBothSurfaces(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)

	toolName := "acceptance-1593-state-tool-" + unique1593()
	createScript1593(t, owner, toolName)
	portalName := "acceptance-1593-state-portal-" + unique1593()
	portalID := createScript1593(t, owner, portalName)

	toolOut := owner.call("manage_script", map[string]any{
		"command": "state", "name": toolName, "state_action": "clear",
	})
	toolMessage, _ := toolOut["message"].(string)

	status, body := owner.rest(http.MethodDelete, "/api/v1/portal/scripts/"+portalID+"/state", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("DELETE portal script state: status %d: %v", status, body)
	}
	portalMessage, _ := body["message"].(string)

	t.Logf("tool:   %s", toolMessage)
	t.Logf("portal: %s", portalMessage)
	if toolMessage == "" || toolMessage != portalMessage {
		t.Fatalf("the two surfaces gave different accounts of the same reset:\n  tool:   %q\n  portal: %q",
			toolMessage, portalMessage)
	}
}

// mustJSON1593 encodes a request body, failing the test rather than the call.
func mustJSON1593(t *testing.T, v map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding a request body: %v", err)
	}
	return string(raw)
}
