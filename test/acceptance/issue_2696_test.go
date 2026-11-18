//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2696: an application runs in ordered environments. A
// release lands in the first; a promote copies an environment's release to
// the next; a rollback moves one environment back. /endpoints/<name> serves
// the last and /environments/<env>/endpoints/<name> another, each running
// the version its release pins and reading its own variables through
// platform.env, which change with no release. A variable holding
// {{secret:<name>}} reaches the upstream filled and no answer holds the
// value. An agent does not promote into an environment that requires
// approval; the portal does. A schedule handler names the environment its
// runs execute in.
//
// Wire forms: manage_application takes command, application, name,
// environment, after, variable and value as JSON strings, requires_approval
// as a JSON boolean and to_release as a JSON number; manage_script
// handler_set takes kind and cron, and handler_environment handler and
// environment, as strings; the portal routes take JSON objects, a create's
// environments a JSON array of strings. No parameter admits a second form.

// handler2696 answers the version it was saved at, the environment it runs
// in, its GREETING, and the headers the echo upstream received with TOKEN
// sent as X-Portal-Login, a header the gateway does not redact by name.
func handler2696(version int) string {
	return fmt.Sprintf(`def handle(req):
    """Says where it runs."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "api-test-fixture", "method": "GET", "path": "/v1/echo",
        "headers": {"X-Portal-Login": platform.env("TOKEN", default="none")},
        "purpose": "Acceptance #2696: a variable's secret reference reaches the upstream.",
    })
    echoed = res["body"]["headers"] if type(res.get("body")) == "dict" else res.get("body")
    return {"json": {"v": %d, "env": platform.environment(), "greeting": platform.env("GREETING", default="none"), "echo": echoed}}
`, version)
}

const sample2696 = `{"params": {}, "query": {}, "caller": None}`

// app2696 is an application with dev and prod holding one endpoint script.
type app2696 struct {
	admin          *client
	name, endpoint string
}

func newApp2696(t *testing.T) *app2696 {
	t.Helper()
	admin := connectFor(t, 5*time.Minute)
	a := &app2696{admin: admin, name: "acc2696-" + unique1579(), endpoint: endpointName("acc-2696")}
	// The portal creates it with dev and prod, as its New application dialog
	// does.
	if status := admin.restJSON(http.MethodPost, "/api/v1/portal/applications", map[string]any{
		"name": a.name, "description": "Acceptance #2696", "environments": []string{"dev", "prod"},
	}); status != http.StatusOK {
		t.Fatalf("the portal created the application with %d", status)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": a.name})
	})
	api := saveEndpointScript(t, admin, a.endpoint, handler2696(1), sample2696)
	bindEndpoint(t, admin, a.endpoint, "", map[string]any{
		"endpoint": a.endpoint, "methods": []string{"GET"}, "path": "/",
		"auth_mode": "authenticated", "callers": []string{"*"},
	})
	admin.application(map[string]any{"command": "add_part", "application": a.name, "kind": "script", "part_id": api})
	return a
}

// answer is what env's address answers ("" for the last, /endpoints/).
func (a *app2696) answer2696(t *testing.T, env, query string) (int, string) {
	t.Helper()
	path := "/endpoints/" + a.endpoint + "/"
	if env != "" {
		path = "/environments/" + env + path
	}
	got := endpointRequest(t, a.admin.base, http.MethodGet, path+query, bearer(a.admin.apiKey), "")
	return got.status, strings.TrimSpace(got.body)
}

func (a *app2696) wants2696(t *testing.T, env, want string) {
	t.Helper()
	status, body := a.answer2696(t, env, "")
	if status != http.StatusOK || !strings.Contains(body, want) {
		t.Fatalf("environment %q answered %d %s; want %s", env, status, body, want)
	}
}

func (a *app2696) edit2696(t *testing.T, version int) {
	t.Helper()
	a.admin.saveEdit(map[string]any{"command": "update", "name": a.endpoint, "source": handler2696(version) + draftMain(sample2696)}, nil)
}

func TestIssue2696_AReleaseLandsInDevAndAPromoteMovesItToProd(t *testing.T) {
	a := newApp2696(t)
	rel := a.admin.application(map[string]any{"command": "release", "application": a.name, "note": "first"})
	if number(t, rel, "number") != 1 {
		t.Fatalf("the first release: %v", rel)
	}
	a.wants2696(t, "dev", `"v":1`)
	a.wants2696(t, "dev", `"env":"dev"`)

	a.admin.application(map[string]any{"command": "promote", "application": a.name, "environment": "dev"})
	a.wants2696(t, "", `"v":1`)
	a.wants2696(t, "", `"env":"prod"`)

	// Version 2 is released to dev; prod keeps running version 1.
	a.edit2696(t, 2)
	a.admin.application(map[string]any{"command": "release", "application": a.name, "note": "second"})
	a.wants2696(t, "dev", `"v":2`)
	a.wants2696(t, "", `"v":1`)

	// dev rolls back alone.
	a.admin.application(map[string]any{"command": "rollback", "application": a.name, "environment": "dev", "to_release": 1})
	a.wants2696(t, "dev", `"v":1`)
	got := a.admin.application(map[string]any{"command": "get", "application": a.name})
	if s := asJSON(got); !strings.Contains(s, `"name":"dev","release":1`) || !strings.Contains(s, `"name":"prod","release":1`) {
		t.Fatalf("the application does not read dev and prod at release 1: %s", s)
	}
	if status, _ := a.answer2696(t, "qa", ""); status != http.StatusNotFound {
		t.Errorf("an environment the application lacks answered %d", status)
	}
	refusal := a.admin.applicationRefused(map[string]any{"command": "promote", "application": a.name, "environment": "prod"})
	if !strings.Contains(refusal, "last environment") {
		t.Errorf("promoting the last environment: %s", refusal)
	}
}

func TestIssue2696_AVariableIsReadPerEnvironmentWithNoRelease(t *testing.T) {
	a := newApp2696(t)
	a.admin.application(map[string]any{"command": "release", "application": a.name})
	a.admin.application(map[string]any{"command": "promote", "application": a.name, "environment": "dev"})
	a.wants2696(t, "dev", `"greeting":"none"`)

	a.admin.application(map[string]any{"command": "set_variable", "application": a.name, "variable": "GREETING", "value": "hello"})
	a.admin.application(map[string]any{"command": "set_variable", "application": a.name, "variable": "GREETING", "value": "hi from prod", "environment": "prod"})
	a.wants2696(t, "dev", `"greeting":"hello"`)
	a.wants2696(t, "", `"greeting":"hi from prod"`)

	a.admin.application(map[string]any{"command": "delete_variable", "application": a.name, "variable": "GREETING", "environment": "prod"})
	a.wants2696(t, "", `"greeting":"hello"`)

	// The portal reads and writes the same variables.
	if status := a.admin.restJSON(http.MethodPut, "/api/v1/portal/applications/"+a.name+"/variables/GREETING",
		map[string]any{"environment": "dev", "value": "hey dev"}); status != http.StatusOK {
		t.Fatalf("the portal set a variable with %d", status)
	}
	a.wants2696(t, "dev", `"greeting":"hey dev"`)
	status, listed := a.admin.rest(http.MethodGet, "/api/v1/portal/applications/"+a.name+"/variables", http.NoBody)
	if status != http.StatusOK || !strings.Contains(asJSON(listed), "hey dev") {
		t.Fatalf("the portal's variables: %d %v", status, listed)
	}
	refusal := a.admin.applicationRefused(map[string]any{"command": "set_variable", "application": a.name, "variable": "lower", "value": "x"})
	if !strings.Contains(refusal, "uppercase") {
		t.Errorf("a malformed name: %s", refusal)
	}
}

func TestIssue2696_ASecretReferenceReachesTheUpstreamAndNoAnswer(t *testing.T) {
	a := newApp2696(t)
	name, value := secret2051(t, a.admin, nil, fixture2051)
	a.admin.application(map[string]any{"command": "release", "application": a.name})
	a.admin.application(map[string]any{
		"command": "set_variable", "application": a.name, "variable": "TOKEN",
		"value": "{{secret:" + name + "}}", "environment": "dev",
	})

	status, body := a.answer2696(t, "dev", "")
	if status != http.StatusOK {
		t.Fatalf("the endpoint answered %d %s", status, body)
	}
	// The upstream received the value: the redaction stands where it was.
	if !strings.Contains(body, "[REDACTED:"+name+"]") {
		t.Errorf("the echo does not show the value redacted: %s", body)
	}
	listed := asJSON(a.admin.application(map[string]any{"command": "variables", "application": a.name}))
	for _, where := range []struct{ label, text string }{{"the endpoint's answer", body}, {"the variables listing", listed}} {
		for _, form := range []string{value, base64.StdEncoding.EncodeToString([]byte(value)), hex.EncodeToString([]byte(value)), url.QueryEscape(value)} {
			if strings.Contains(where.text, form) {
				t.Errorf("%s holds the secret (%q)", where.label, form)
			}
		}
	}
	if !strings.Contains(listed, "{{secret:"+name+"}}") {
		t.Errorf("the variable does not hold the reference as written: %s", listed)
	}
}

func TestIssue2696_AnAgentDoesNotPromoteIntoAnEnvironmentThatRequiresApproval(t *testing.T) {
	a := newApp2696(t)
	a.admin.application(map[string]any{"command": "release", "application": a.name})
	a.admin.application(map[string]any{
		"command": "add_environment", "application": a.name, "environment": "stage",
		"after": "dev", "requires_approval": true,
	})
	refusal := a.admin.applicationRefused(map[string]any{"command": "promote", "application": a.name, "environment": "dev"})
	if !strings.Contains(refusal, "APPROVAL_REQUIRED") {
		t.Fatalf("an agent's promote into stage: %s", refusal)
	}
	// The person promotes on the application's page.
	status, out := a.admin.rest(http.MethodPost, "/api/v1/portal/applications/"+a.name+"/environments/dev/promote", http.NoBody)
	if status != http.StatusOK || out["to"] != "stage" {
		t.Fatalf("the portal's promote: %d %v", status, out)
	}
	a.wants2696(t, "stage", `"env":"stage"`)
}

func TestIssue2696_AScheduleHandlerNamesItsEnvironment(t *testing.T) {
	a := newApp2696(t)
	sched := a.admin.handlerSet(a.endpoint, "schedule", map[string]any{"cron": "0 3 * * *"})
	id, _ := sched["handler"].(string)
	out := a.admin.call("manage_script", map[string]any{"command": "handler_environment", "handler": id, "environment": "dev"})
	if out["environment"] != "dev" {
		t.Fatalf("the schedule handler does not name dev: %v", out)
	}
	res, text, err := a.admin.callRaw("manage_script", map[string]any{"command": "handler_environment", "handler": id, "environment": "qa"})
	if err != nil || !res.IsError || !strings.Contains(text, `no environment "qa"`) {
		t.Errorf("an environment the application lacks: %s %v", text, err)
	}
	got := a.admin.call("manage_script", map[string]any{"command": "handler_get", "handler": id})
	if got["environment"] != "dev" {
		t.Errorf("handler_get does not name dev: %v", got)
	}
	status, handlers := a.admin.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID2696(t, a)+"/handlers", http.NoBody)
	if status != http.StatusOK || !strings.Contains(asJSON(handlers["environments"]), `"dev","prod"`) {
		t.Fatalf("the portal's handlers do not offer dev and prod: %d %v", status, handlers["environments"])
	}
}

func scriptID2696(t *testing.T, a *app2696) string {
	t.Helper()
	got := a.admin.call("manage_script", map[string]any{"command": "get", "name": a.endpoint})
	id, _ := got["id"].(string)
	return id
}
