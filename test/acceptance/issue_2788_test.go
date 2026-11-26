//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2788, run in the refscheme environment
// (server.reference_scheme: acme): a managed script receives tool results in
// the canonical mcp: form, so a comparison with a literal mcp: prefix keeps
// working; a script's source keeps the deployment's scheme as a literal when
// saved and read back through MCP; and an endpoint's request reaches its
// script canonical while its JSON answer leaves under the scheme.
//
// Wire forms: manage_script takes command, name and source as strings and
// params as a list of objects; run_script takes name, args as an object of
// strings and wait_seconds as a JSON number; handler_set takes kind, endpoint,
// auth_mode and path as strings and callers and methods as JSON arrays of
// strings. An endpoint request is an HTTP GET with the reference as a path
// segment, in the acme form.

// script2788Compare compares the reference a tool hands it with the canonical
// prefix, as live scripts do.
const script2788Compare = `def main():
    """Compares a tool-given reference with the canonical prefix."""
    got = platform.call("manage_resource", {"action": "get", "reference": run.params["ref"]})
    print("canonical %s %s" % (got["reference"].startswith("mcp:resource:"), got["reference"]))
`

// script2788Literal holds the deployment's scheme as a literal.
const script2788Literal = `def main():
    """Rewrites the deployment's scheme to the canonical one."""
    print("rewritten " + run.params["s"].replace("acme://", "mcp://"))
`

// TestIssue2788_AScriptComparesWithTheCanonicalPrefix: a tool result reaches
// the script in the mcp: form, whichever form the script asked with.
func TestIssue2788_AScriptComparesWithTheCanonicalPrefix(t *testing.T) {
	needsScheme2776(t)
	owner := connectAs(t, keyOwner2616)
	id, _ := personalResource2616(t, owner, "script-compare-2788")
	name := "acc2788-compare-" + unique1579()
	params := []any{map[string]any{"name": "ref", "type": "string", "required": true, "description": "The resource."}}
	owner.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2788: compares with mcp:.", "source": script2788Compare, "params": params},
		map[string]any{"ref": "mcp:resource:" + id})
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	for _, ref := range []string{scheme2776 + ":resource:" + id, "mcp:resource:" + id} {
		run := owner.call("run_script", map[string]any{"name": name, "args": map[string]any{"ref": ref}, "wait_seconds": 120})
		if run["status"] != "succeeded" || !strings.Contains(fmt.Sprint(run["log"]), "canonical True mcp:resource:"+id) {
			t.Errorf("asked with %s, the script saw a reference not under mcp:: %v", ref, run["log"])
		}
	}
}

// TestIssue2788_AScriptsSourceKeepsTheSchemeAsWritten: a source holding
// "acme://" is saved, read back and run as written.
func TestIssue2788_AScriptsSourceKeepsTheSchemeAsWritten(t *testing.T) {
	needsScheme2776(t)
	owner := connectAs(t, keyOwner2616)
	name := "acc2788-literal-" + unique1579()
	params := []any{map[string]any{"name": "s", "type": "string", "required": true, "description": "A string to rewrite."}}
	owner.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2788: a literal scheme.", "source": script2788Literal, "params": params},
		map[string]any{"s": "acme://global/x"})
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := owner.call("manage_script", map[string]any{"command": "get", "name": name})
	if source, _ := got["source"].(string); !strings.Contains(source, `replace("acme://", "mcp://")`) {
		t.Errorf("the source does not read back as saved:\n%s", source)
	}
	run := owner.call("run_script", map[string]any{"name": name, "args": map[string]any{"s": "acme://global/x"}, "wait_seconds": 120})
	if run["status"] != "succeeded" || !strings.Contains(fmt.Sprint(run["log"]), "rewritten mcp://global/x") {
		t.Errorf("the stored literal was rewritten: %v", run["log"])
	}
}

// script2788Endpoint answers with the path it was reached at, whether that
// path holds the canonical form, and a reference of its own.
const script2788Endpoint = `def handle(req):
    """Answers whether the reference in its path is canonical."""
    return {"json": {"canonical": req["path"].endswith("/mcp:resource:r2788"), "made": "mcp:resource:r2788"}}
`

// TestIssue2788_AnEndpointGetsCanonicalReferencesAndAnswersUnderTheScheme: a
// reference a browser sends back under the scheme reaches the script
// canonical, and the script's JSON answer leaves under the scheme.
func TestIssue2788_AnEndpointGetsCanonicalReferencesAndAnswersUnderTheScheme(t *testing.T) {
	needsScheme2776(t)
	admin := connect(t)
	name := endpointName("acc-2788")
	saveEndpointScript(t, admin, name, script2788Endpoint, `{"path": "/endpoints/x/mcp:resource:r2788"}`)
	admin.handlerSet(name, "endpoint", map[string]any{"endpoint": name, "auth_mode": "authenticated", "callers": []string{"*"}, "methods": []string{"GET"}, "path": "/{ref}"})
	got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name+"/"+scheme2776+":resource:r2788", bearer(admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET: %d %s", got.status, got.body)
	}
	if !strings.Contains(got.body, `"canonical":true`) && !strings.Contains(got.body, `"canonical": true`) {
		t.Errorf("the script saw the reference under the scheme: %s", got.body)
	}
	if !strings.Contains(got.body, scheme2776+":resource:r2788") || strings.Contains(got.body, "mcp:resource:r2788") {
		t.Errorf("the answer's reference is not under the scheme: %s", got.body)
	}
}
