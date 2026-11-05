//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2485 (epic #2469): what uses each operation. A script whose
// source calls an operation through api_invoke_endpoint is listed under it and
// under its connection; a script that computes the operation it calls is
// listed once a run has called it; an endpoint bound to such a script is
// listed with it; a prompt naming the operation and the connection is listed;
// and a change report names, for each changed operation, what uses it.
//
// Wire forms: manage_script create and run, manage_prompt create and
// api_invoke_endpoint take literal tools/call params (operation_id a string;
// the computed call builds it at run time); the used-by routes take no body.

// issue2485Caller is a script calling listPets on connection by id, named in
// its source.
func issue2485Caller(connection string) string {
	return `def main():
    """Lists the pets the vendor holds."""
    platform.call("api_invoke_endpoint", {"connection": "` + connection + `", "operation_id": "listPets",
        "purpose": "Acceptance for #2485: a script naming an operation in its source."})
    platform.result({"called": "listPets"})
`
}

// issue2485Computed is a script whose source computes the operation it calls,
// so only a run says which one.
func issue2485Computed(connection string) string {
	return `def main():
    """Reads one pet, the operation chosen at run time."""
    op = run.params.get("op", "getPet")
    platform.call("api_invoke_endpoint", {"connection": "` + connection + `", "operation_id": op, "path_params": {"id": "7"},
        "purpose": "Acceptance for #2485: a script computing the operation it calls."})
    platform.result({"called": op})
`
}

// issue2485UsedBy reads what uses operation through the catalog route.
func issue2485UsedBy(t *testing.T, admin *client, catalog, operation string) string {
	t.Helper()
	status, out := admin.rest(http.MethodGet, issue2483SpecPath(catalog)+"/operations/"+operation+"/used-by", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("used-by %s: %d %v", operation, status, out)
	}
	return fmt.Sprint(out["used_by"])
}

// TestIssue2485_ScriptsPromptsAndRunsAreListedAndReported.
func TestIssue2485_ScriptsPromptsAndRunsAreListedAndReported(t *testing.T) {
	admin := connect(t)
	up := newIssue2483Upstream(t, admin, "acc-2485-up", issue2483V1, `"v1"`)
	catalog := issue2483Catalog(t, admin, "acc-2485", map[string]any{"source_kind": "url", "source_url": up.url})

	byName := endpointName("acc-2485-src")
	admin.saveScript(map[string]any{"command": "create", "name": byName, "source": issue2485Caller(catalog)}, nil)
	computed := endpointName("acc-2485-run")
	admin.saveScript(map[string]any{"command": "create", "name": computed, "source": issue2485Computed(catalog)}, nil)

	promptName := fmt.Sprintf("acc-2485-prompt-%d", time.Now().UnixNano())
	prompt := admin.call("manage_prompt", map[string]any{
		"command": "create", "name": promptName, "display_name": "Acceptance 2485", "description": "Acceptance 2485 fixture.",
		"content": "On " + catalog + ", call deletePet only after the owner confirms.", "scope": "personal",
	})
	if prompt["id"] == nil {
		t.Fatalf("manage_prompt create: %v", prompt)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_prompt", map[string]any{"command": "delete", "name": promptName})
	})

	if got := issue2485UsedBy(t, admin, catalog, "listPets"); !strings.Contains(got, byName) || !strings.Contains(got, "source") {
		t.Fatalf("listPets used by: %s, want %s by source", got, byName)
	}
	if got := issue2485UsedBy(t, admin, catalog, "deletePet"); !strings.Contains(got, promptName) {
		t.Fatalf("deletePet used by: %s, want the prompt %s", got, promptName)
	}
	if got := issue2485UsedBy(t, admin, catalog, "getPet"); strings.Contains(got, computed) {
		t.Fatalf("a computed call is listed before any run made it: %s", got)
	}

	// A run of the computed script calls getPet; the audit pipeline records it.
	run := admin.call("run_script", map[string]any{"name": computed, "wait_seconds": 120})
	if run["status"] != "succeeded" {
		t.Fatalf("running %s: %v", computed, run)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(issue2485UsedBy(t, admin, catalog, "getPet"), computed) {
		if time.Now().After(deadline) {
			t.Fatalf("getPet never listed %s after its run: %s", computed, issue2485UsedBy(t, admin, catalog, "getPet"))
		}
		time.Sleep(time.Second)
	}
	if got := issue2485UsedBy(t, admin, catalog, "getPet"); !strings.Contains(got, "run") {
		t.Fatalf("getPet lists %s by run: %s", computed, got)
	}

	status, conn := admin.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+catalog+"/used-by", http.NoBody)
	if status != http.StatusOK || conn["catalog_id"] != catalog {
		t.Fatalf("the connection's used-by: %d %v", status, conn)
	}
	for _, want := range []string{"listPets", "deletePet", "getPet", byName, computed, promptName} {
		if !strings.Contains(fmt.Sprint(conn["operations"]), want) {
			t.Errorf("the connection's used-by lacks %s: %v", want, conn["operations"])
		}
	}

	// The vendor removes deletePet, changes listPets and deprecates getPet:
	// the report names what uses each.
	up.publish(issue2483V2, `"v2"`)
	change, _ := issue2483Refresh(t, admin, catalog)["change"].(map[string]any)
	report, _ := change["report"].(map[string]any)
	if got := fmt.Sprint(report["removed"]); !strings.Contains(got, promptName) {
		t.Errorf("the removed deletePet does not name the prompt: %s", got)
	}
	if got := fmt.Sprint(report["changed"]); !strings.Contains(got, byName) {
		t.Errorf("the changed listPets does not name %s: %s", byName, got)
	}
	if got := fmt.Sprint(report["deprecated"]); !strings.Contains(got, computed) {
		t.Errorf("the deprecated getPet does not name %s: %s", computed, got)
	}
}

// TestIssue2485_AnEndpointIsListedWithItsScript: an endpoint whose script
// names an operation is listed beside the script.
func TestIssue2485_AnEndpointIsListedWithItsScript(t *testing.T) {
	admin := connect(t)
	up := newIssue2483Upstream(t, admin, "acc-2485-ep-up", issue2483V1, `"v1"`)
	catalog := issue2483Catalog(t, admin, "acc-2485-ep", map[string]any{"source_kind": "url", "source_url": up.url})
	name := endpointName("acc-2485-ep")
	saveEndpointScript(t, admin, name, `def handle(req):
    """Answers with the vendor's pets."""
    out = platform.call("api_invoke_endpoint", {"connection": "`+catalog+`", "operation_id": "listPets",
        "purpose": "Acceptance for #2485: an endpoint naming an operation."})
    return {"json": {"status": out["status"]}}
`, `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "authenticated", "callers": []string{"*"}})
	got := issue2485UsedBy(t, admin, catalog, "listPets")
	if !strings.Contains(got, "endpoint") || !strings.Contains(got, name) {
		t.Fatalf("listPets used by: %s, want the endpoint %s", got, name)
	}
}
