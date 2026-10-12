//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// #2086: a saved script's run has no caller token, so a call it made on an
// identity-passthrough connection failed before any authorization ran, while
// the same call in a draft (the caller's session, the caller's token)
// succeeded. The platform-admin connection now serves a saved run's call
// inside the platform as the script's identity, which the admin API checks as
// it checks a token; any other passthrough connection refuses it saying why.
//
// Wire forms: manage_script's command, name, description and source, and
// run_script's name and wait_seconds, are typed strings and numbers with one
// form each. The script's own api_invoke_endpoint call sends connection,
// method and path as strings from Starlark; it sends no body, the one
// parameter that admits two forms.

// script2086 reads the platform's personas through platform-admin and hands
// back what the admin API answered.
const script2086 = `def main():
    """Reads the platform's personas through the platform-admin connection."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "platform-admin",
        "method": "GET",
        "path": "/api/v1/admin/personas",
        "purpose": "Acceptance #2086: a saved script reads the admin API.",
    }, on_error = "return")
    platform.result({"status": res.get("status", 0), "text": str(res)[:400]})
`

// save2086 saves source as a new script owned by c's caller, deleted after the
// test, and returns its name.
func save2086(t *testing.T, c *client, label, source string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2086-%s-%d", label, time.Now().UnixNano())
	c.saveScript(map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #2086: a saved script on an identity-passthrough connection.",
		"source":      source,
	}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name
}

// result2086 runs the saved script with run_script and returns what it handed
// back.
func result2086(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	out := c.call("run_script", map[string]any{"name": name, "wait_seconds": 120})
	if out["status"] != "succeeded" {
		t.Fatalf("run of %s: %v", name, out)
	}
	res, _ := out["result"].(map[string]any)
	return res
}

// TestIssue2086_AnAdminsSavedScriptReachesTheAdminAPI: the script an
// administrator saved reads the admin API from run_script and from its
// schedule, and the call is audited as the script.
func TestIssue2086_AnAdminsSavedScriptReachesTheAdminAPI(t *testing.T) {
	c := connect(t)
	name := save2086(t, c, "admin", script2086)
	draft := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": script2086})
	if res, _ := draft["result"].(map[string]any); fmt.Sprint(res["status"]) != "200" {
		t.Fatalf("a draft (the caller's own token) did not reach the admin API: %v", draft)
	}
	if res := result2086(t, c, name); fmt.Sprint(res["status"]) != "200" {
		t.Fatalf("run_script: the admin API answered %v; want 200 (%v)", res["status"], res["text"])
	}

	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := got["id"].(string)
	c.call("manage_script", map[string]any{"command": "schedule_set", "name": name, "cron": "* * * * *", "timezone": "UTC"})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "schedule_disable", "name": name})
	})
	fireNow1986(t, scriptID)
	run := awaitRun1986(t, c, name, "a finished scheduled run", func(r map[string]any) bool {
		return r["trigger"] == "schedule" && (r["status"] == "succeeded" || r["status"] == "failed")
	})
	if run["status"] != "succeeded" {
		t.Fatalf("the scheduled run: %v", run)
	}
	detail := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if res, _ := detail["result"].(map[string]any); fmt.Sprint(res["status"]) != "200" {
		t.Fatalf("the scheduled run: the admin API answered %v; want 200", detail["result"])
	}

	var owner string
	waitFor(t, "the run's audited call", func() (bool, string) {
		err := devDB(t).QueryRowContext(t.Context(), `
			SELECT COALESCE(user_email, '') FROM audit_logs
			 WHERE tool_name = 'api_invoke_endpoint' AND user_id = $1 AND connection = 'platform-admin' AND source = 'script'
			 ORDER BY timestamp DESC LIMIT 1`, "script:"+name).Scan(&owner)
		return err == nil, fmt.Sprint(err)
	})
	if owner != "admin@example.com" {
		t.Errorf("the audit row of script:%s names owner %q; want admin@example.com", name, owner)
	}
}

// TestIssue2086_ANonAdminsScriptIsRefusedNamingThePersona: the same script
// owned by a collaborator, whose persona reaches the connection but not the
// admin API, is refused by the admin API's own rule, naming the persona and
// the roles, not with the missing-token error.
func TestIssue2086_ANonAdminsScriptIsRefusedNamingThePersona(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := save2086(t, owner, "collab", script2086)
	res := result2086(t, owner, name)
	text := fmt.Sprint(res["text"])
	if fmt.Sprint(res["status"]) != "403" {
		t.Fatalf("the admin API answered %v; want 403 (%s)", res["status"], text)
	}
	for _, want := range []string{`requires the persona \"admin\"`, "[admin", "[collaborator]"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %s does not name %s", text, want)
		}
	}
	if strings.Contains(text, "caller token") {
		t.Errorf("the refusal is the missing-token error: %s", text)
	}
}

// TestIssue2086_AnotherPassthroughConnectionSaysWhy: a passthrough connection
// to an upstream that checks the token itself cannot serve a saved run; the
// run is told why and what to use, and validate warns before the first run.
func TestIssue2086_AnotherPassthroughConnectionSaysWhy(t *testing.T) {
	c := connect(t)
	conn := fmt.Sprintf("acc-2086-pass-%d", time.Now().UnixNano())
	if status, out := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+conn, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url": apiTestFixtureURL(), "auth_mode": "none", "identity_passthrough": true,
		},
		"description": "Acceptance #2086: forwards the caller's token.",
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("creating %s: %d %v", conn, status, out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody) })

	source := strings.ReplaceAll(strings.ReplaceAll(script2086, "platform-admin", conn), "/api/v1/admin/personas", "/v1/echo")
	validated := c.call("manage_script", map[string]any{"command": "validate", "source": source})
	if !strings.Contains(fmt.Sprint(validated["findings"]), "passthrough-connection") {
		t.Errorf("validate does not warn of %s: %v", conn, validated["findings"])
	}

	name := save2086(t, c, "pass", source)
	res := result2086(t, c, name)
	text := fmt.Sprint(res["text"])
	for _, want := range []string{"a saved script's run has none", "holds its own credentials", conn} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %s does not say %q", text, want)
		}
	}
}
