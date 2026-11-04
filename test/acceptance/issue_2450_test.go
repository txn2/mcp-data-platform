//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2450: a script saved from a session authenticated with a
// configured API key, run after the same address has signed in through the
// browser, files its output where that key session's search and
// manage_resource find it. The address signs in first, through the dev
// stack's Keycloak, so the platform holds the subject that address signs in
// as; the criterion is about what happens once it does.
//
// Wire forms: manage_script takes command, name, description and source as
// strings; run_script takes name as a string and wait_seconds as an integer;
// search takes intent and purpose as strings and sources as a list;
// manage_resource takes action, reference, path and description as strings. No
// parameter admits a second form.

// keyEmail2450 is the address the dev stack's admin API key names, and the
// Keycloak user signing in as the same person.
const (
	keyEmail2450    = "admin@example.com"
	keyPassword2450 = "admin-password"
)

func TestIssue2450_AKeySavedScriptFilesWhereTheKeySessionFindsIt(t *testing.T) {
	browserSignIn(t, baseURL(), keyEmail2450, keyPassword2450)
	admin := connectFor(t, 5*time.Minute)
	stamp := unique1579()
	folder := "acc2450-" + stamp
	name := "acc-2450-" + stamp
	marker := "acc2450marker" + stamp
	admin.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #2450: a key-saved script's output.",
		"source": `def main():
    """Files one report into the library."""
    out = platform.export("acc2450 report", [{"marker": "` + marker + `", "n": 1}], format = "csv",
                          destination = "resources", key = "` + folder + `/report.csv")
    print(out["resource_id"])
`,
	}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := admin.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if run["status"] != "succeeded" {
		t.Fatalf("the run failed: %v", run)
	}
	log, _ := run["log"].(string)
	id := strings.TrimSpace(strings.SplitN(log, "\n", 2)[0])
	if id == "" {
		t.Fatalf("the run printed no resource id: %v", run)
	}
	ref := "mcp:resource:" + id
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref})
	})

	got := admin.call("manage_resource", map[string]any{"action": "get", "reference": ref})
	if !strings.Contains(asJSON(got), folder+"/report.csv") {
		t.Fatalf("the key session cannot read the run's file: %v", got)
	}
	listed := admin.call("manage_resource", map[string]any{"action": "list", "path": folder})
	if !strings.Contains(asJSON(listed), id) {
		t.Fatalf("the key session's listing of %s does not hold the run's file: %v", folder, listed)
	}
	updated := admin.call("manage_resource", map[string]any{"action": "update", "reference": ref, "description": "kept by #2450"})
	if strings.Contains(asJSON(updated), "another user's scope") {
		t.Fatalf("the key session cannot change the run's file: %v", updated)
	}
	waitFor(t, "the file in the key session's search", func() (bool, string) {
		out := admin.call("search", map[string]any{
			"intent": "acc2450 report " + folder, "sources": []any{"resources"}, "purpose": "Acceptance #2450: find the run's file.",
		})
		raw := asJSON(out)
		return strings.Contains(raw, id), raw
	})
}
