//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2441: one handler model for every kind of script handler,
// with endpoints and forms on it. A script answers several endpoints; a save
// that a form handler could not serve is refused naming the handler; a name
// another script holds is refused naming its handler; the endpoint commands
// handler_* replaced are refused naming their replacement; a version 1
// application bundle imports as the handlers a version 2 export writes back;
// a handler id is no key to another person's script; and a webhook handler
// never carries its source's secret.
//
// Wire forms: manage_script handler_set takes kind and the endpoint settings
// as literal tools/call params (methods and callers JSON arrays of strings,
// path and auth_mode strings, timeout_seconds a JSON number); handler_get,
// handler_list, handler_disable and handler_delete take handler and name as
// strings; the portal routes take one JSON object and no body.

// issue2441Echo answers with the endpoint it was reached at.
const issue2441Echo = `def handle(req):
    """Answers with the path it was asked for."""
    return {"json": {"path": req["path"]}}
`

// TestIssue2441_AScriptWithTwoEndpointHandlersAnswersOnBoth: a script holds
// two endpoint handlers, each answers on its own path, and the script lists
// both.
func TestIssue2441_AScriptWithTwoEndpointHandlersAnswersOnBoth(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2441-two")
	saveEndpointScript(t, admin, name, issue2441Echo, `{"path": "/endpoints/x"}`)
	first, second := name+"-a", name+"-b"
	a := admin.handlerSet(name, "endpoint", map[string]any{"endpoint": first, "auth_mode": "authenticated", "callers": []string{"*"}, "methods": []string{"GET"}})
	b := admin.handlerSet(name, "endpoint", map[string]any{"endpoint": second, "auth_mode": "authenticated", "callers": []string{"*"}, "methods": []string{"GET"}, "path": "/{id}"})
	if idOf(a) == "" || idOf(a) == idOf(b) {
		t.Fatalf("two endpoint handlers want two ids: %v %v", a["handler"], b["handler"])
	}
	for path, want := range map[string]string{"/endpoints/" + first: "/endpoints/" + first, "/endpoints/" + second + "/7": "/endpoints/" + second + "/7"} {
		got := endpointRequest(t, admin.base, http.MethodGet, path, bearer(admin.apiKey), "")
		if got.status != http.StatusOK || !strings.Contains(got.body, want) {
			t.Errorf("GET %s: %d %s", path, got.status, got.body)
		}
	}
	held := admin.handlersOf(name, "endpoint", "")
	if len(held) != 2 {
		t.Fatalf("the script lists %d endpoint handlers, want 2: %v", len(held), held)
	}
}

// TestIssue2441_SavingAFormWithoutFormSubmitIsRefusedNamingTheHandler: a
// version of a bound form's script that defines no form_submit is refused,
// naming the form handler, and the form keeps the version it had.
func TestIssue2441_SavingAFormWithoutFormSubmitIsRefusedNamingTheHandler(t *testing.T) {
	admin := connect(t)
	name, _ := bindForm(t, admin, "acc-2441-form", formFields, map[string]any{"auth_mode": "authenticated", "callers": []string{"*"}})
	handler := admin.endpointOf(name, "")
	broken := strings.Replace(formSource(formFields), `def form_submit(req, values):
    """Accepts every submission."""
    return None
`, `def handle(req):
    """Answers every request."""
    return {"json": {}}
`, 1)
	broken = strings.Replace(broken, `, "accepted": form_submit({"caller": None}, {})`, "", 1)
	res, text, err := admin.callRaw("manage_script", map[string]any{"command": "update", "name": name, "source": broken})
	if err != nil {
		t.Fatal(err)
	}
	message := text
	if !res.IsError {
		refused := admin.parse(text)
		message, _ = refused["message"].(string)
		if refused["status"] != "invalid" {
			t.Fatalf("a form saved without form_submit was accepted: %s", text)
		}
	}
	if !strings.Contains(message, fmt.Sprintf("this script is the form %q (endpoint handler %s)", name, idOf(handler))) {
		t.Fatalf("the refusal does not name the form handler %s: %s", idOf(handler), text)
	}
	got := admin.call("manage_script", map[string]any{"command": "get", "name": name})
	if source, _ := got["source"].(string); !strings.Contains(source, "def form_submit(req, values):") {
		t.Fatalf("the refused version was saved: %v", got["version"])
	}
}

// TestIssue2441_ASecondHandlerOnATakenNameIsRefusedNamingIt: an endpoint name
// one script holds is refused to another, naming the handler holding it.
func TestIssue2441_ASecondHandlerOnATakenNameIsRefusedNamingIt(t *testing.T) {
	admin := connect(t)
	holder, taker := endpointName("acc-2441-holder"), endpointName("acc-2441-taker")
	saveEndpointScript(t, admin, holder, issue2441Echo, `{"path": "/endpoints/x"}`)
	saveEndpointScript(t, admin, taker, issue2441Echo, `{"path": "/endpoints/x"}`)
	held := admin.handlerSet(holder, "endpoint", map[string]any{"endpoint": holder, "auth_mode": "authenticated", "callers": []string{"*"}})
	out, text := admin.handlerSetRaw(taker, "endpoint", map[string]any{"endpoint": holder, "auth_mode": "authenticated", "callers": []string{"*"}})
	want := fmt.Sprintf("endpoint name %q is taken by endpoint handler %s of script %s", holder, idOf(held), holder)
	if out != nil || !strings.Contains(text, want) {
		t.Fatalf("taking a held name: accepted=%v, refusal %q, want %q", out != nil, text, want)
	}
}

// TestIssue2441_TheRemovedCommandsAreRefusedNamingTheirReplacement: each
// command the handler commands replaced is refused naming what replaces it,
// and update no longer takes event_source (#2434).
func TestIssue2441_TheRemovedCommandsAreRefusedNamingTheirReplacement(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2441-gone")
	saveEndpointScript(t, admin, name, issue2441Echo, `{"path": "/endpoints/x"}`)
	for command, replacement := range map[string]string{
		"endpoint_set": "handler_set with kind=endpoint", "endpoint_list": "handler_list with kind=endpoint",
		"endpoint_enable": "handler_enable", "endpoint_disable": "handler_disable",
		"schedule_set": "handler_set with kind=schedule", "schedule_list": "handler_list with kind=schedule",
		"schedule_enable": "handler_enable", "schedule_disable": "handler_disable",
	} {
		res, text, err := admin.callRaw("manage_script", map[string]any{"command": command, "name": name})
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		want := command + " was removed: use " + replacement
		if !res.IsError || !strings.Contains(text, want) {
			t.Errorf("%s answered error=%v %q, want %q", command, res.IsError, text, want)
		}
	}
	res, text, err := admin.callRaw("manage_script", map[string]any{"command": "update", "name": name, "event_source": "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "handler_set kind=webhook event_source=<source>") {
		t.Errorf("update with event_source answered error=%v %q", res.IsError, text)
	}
}

// TestIssue2441_AHandlerIsItsOwnersByItsID: another person holding a
// handler's id reads, turns off and deletes nothing with it, on either
// surface.
func TestIssue2441_AHandlerIsItsOwnersByItsID(t *testing.T) {
	owner, peer := connectAs(t, devOwnerAPIKey), connectAs(t, devPeerAPIKey)
	name := endpointName("acc-2441-mine")
	owner.saveScript(map[string]any{"command": "create", "name": name, "source": "def main():\n    \"\"\"Prints a line.\"\"\"\n    print(\"tick\")\n"}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	id := idOf(owner.scheduleSet(name, "0 4 * * *", "UTC", nil))
	for _, command := range []string{"handler_get", "handler_disable", "handler_delete"} {
		res, text, err := peer.callRaw("manage_script", map[string]any{"command": command, "handler": id})
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if !res.IsError || !strings.Contains(text, "is not yours") {
			t.Errorf("%s by another person: error=%v %q", command, res.IsError, text)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if status, out := peer.rest(method, "/api/v1/portal/handlers/"+id, http.NoBody); status != http.StatusForbidden {
			t.Errorf("%s /portal/handlers/{id} by another person: %d %v", method, status, out)
		}
	}
	if status, out := peer.rest(http.MethodPost, "/api/v1/portal/handlers/"+id+"/disable", http.NoBody); status != http.StatusForbidden {
		t.Errorf("POST .../disable by another person: %d %v", status, out)
	}
	if got := owner.call("manage_script", map[string]any{"command": "handler_get", "handler": id}); got["enabled"] != true || got["cron"] != "0 4 * * *" {
		t.Fatalf("the owner's schedule changed: %v", got)
	}
}

// TestIssue2441_AWebhookHandlerNeverCarriesItsSourcesSecret: no answer that
// names a webhook handler (handler_set, handler_get, handler_list, the
// portal's handler routes, Admin > Handlers and the Automations list) holds
// the source's signing secret, as written or encoded.
func TestIssue2441_AWebhookHandlerNeverCarriesItsSourcesSecret(t *testing.T) {
	admin := connect(t)
	secret := "acc2441-sekret-" + unique1579()
	source := issue1870Name("acc-2441")
	issue1870Create(t, admin, issue1870HMAC(source, secret, map[string]any{"event_id_path": "$.id"}))
	name := endpointName("acc-2441-hook")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": "def main():\n    \"\"\"Prints the event id.\"\"\"\n    print((run.event or {}).get(\"id\", \"\"))\n"}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	set := admin.webhookSet(name, source)
	scriptID := issue2179ScriptID(t, admin, name)

	sinks := map[string]any{
		"handler_set":                 set,
		"handler_get":                 admin.call("manage_script", map[string]any{"command": "handler_get", "handler": idOf(set)}),
		"handler_list":                admin.call("manage_script", map[string]any{"command": "handler_list", "name": name}),
		"handler_list (every script)": admin.call("manage_script", map[string]any{"command": "handler_list", "kind": "webhook"}),
	}
	for label, path := range map[string]string{
		"GET /portal/scripts/{id}/handlers": "/api/v1/portal/scripts/" + scriptID + "/handlers",
		"GET /portal/handlers/{id}":         "/api/v1/portal/handlers/" + idOf(set),
		"GET /admin/handlers":               "/api/v1/admin/handlers?type=webhook&search=" + url.QueryEscape(name),
		"GET /portal/scripts":               "/api/v1/portal/scripts?search=" + url.QueryEscape(name),
	} {
		status, out := admin.rest(http.MethodGet, path, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", label, status, out)
		}
		sinks[label] = out
	}
	encoded := map[string]string{
		"as written": secret, "base64": base64.StdEncoding.EncodeToString([]byte(secret)),
		"hex": hex.EncodeToString([]byte(secret)), "url-escaped": url.QueryEscape(secret),
	}
	quoted, _ := json.Marshal(secret)
	encoded["json-escaped"] = strings.Trim(string(quoted), `"`)
	for label, sink := range sinks {
		raw, _ := json.Marshal(sink)
		if !strings.Contains(string(raw), source) && label != "GET /portal/scripts" {
			t.Errorf("%s does not name the source %s: %s", label, source, raw)
		}
		for form, value := range encoded {
			if strings.Contains(string(raw), value) {
				t.Errorf("%s holds the source's secret %s", label, form)
			}
		}
	}
}

// TestIssue2441_AVersion1BundleImportsAsTheHandlerAVersion2ExportWritesBack:
// an application exported as format version 2 lists the script's endpoint
// under handlers; the same bundle written as version 1 (one endpoint per
// script) imports on a second deployment as that endpoint handler, which the
// second deployment's export writes back as version 2.
func TestIssue2441_AVersion1BundleImportsAsTheHandlerAVersion2ExportWritesBack(t *testing.T) {
	admin := connectFor(t, issue2441Long)
	app := "acc2441-" + unique1579()
	name := endpointName("acc-2441-app")
	scriptID := saveEndpointScript(t, admin, name, issue2441Echo, `{"path": "/endpoints/x"}`)
	admin.handlerSet(name, "endpoint", map[string]any{
		"endpoint": name, "methods": []string{"GET", "POST"}, "path": "/{id}", "auth_mode": "authenticated", "callers": []string{"*"}, "timeout_seconds": 7,
	})
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "Acceptance #2441."})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "script", "part_id": scriptID})
	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "first"})

	v2 := issue2441Export(t, admin, app)
	if v2["format_version"] != float64(2) {
		t.Fatalf("an export is format version %v, want 2", v2["format_version"])
	}
	sc := issue2441Script(t, v2, name)
	handlers, _ := sc["handlers"].(map[string]any)
	endpoints := listOf(handlers, "endpoints")
	if len(endpoints) != 1 || endpoints[0]["name"] != name || fmt.Sprint(endpoints[0]["timeout_seconds"]) != "7" {
		t.Fatalf("the export's handlers: %v", sc["handlers"])
	}

	v1 := v2
	v1["format_version"] = 1
	sc["endpoint"] = endpoints[0]
	delete(sc, "handlers")
	raw, _ := json.Marshal(v1)
	second := startSecond2412(t)
	if ok, text := importMCP2476(t, second, raw, map[string]string{}); !ok {
		t.Fatalf("the version 1 import was refused: %s", text)
	}
	bound := second.admin.endpointOf(name, "")
	if bound["bound"] != true || bound["endpoint"] != name || bound["path"] != "/{id}" || fmt.Sprint(bound["timeout_seconds"]) != "7" {
		t.Fatalf("the imported endpoint handler: %v", bound)
	}
	back := issue2441Script(t, issue2441Export(t, second.admin, app), name)
	backHandlers, _ := back["handlers"].(map[string]any)
	got, _ := json.Marshal(listOf(backHandlers, "endpoints"))
	want, _ := json.Marshal(endpoints)
	if string(got) != string(want) {
		t.Fatalf("the second deployment writes the endpoint back as\n%s\nwant\n%s", got, want)
	}
}

// issue2441Long is how long the bundle criterion's sessions last: a second
// deployment starts within it.
const issue2441Long = 5 * time.Minute

// issue2441Export is an application's newest release as a decoded bundle.
func issue2441Export(t *testing.T, c *client, app string) map[string]any {
	t.Helper()
	out := c.call("manage_application", map[string]any{"command": "export", "application": app})
	landing, _ := out["bundle"].(map[string]any)
	id, _ := landing["resource_id"].(string)
	got := endpointRequest(t, c.base, http.MethodGet, "/api/v1/resources/"+id+"/content", bearer(c.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("reading the bundle answered %d: %s", got.status, got.body)
	}
	var bundle map[string]any
	if err := json.Unmarshal([]byte(got.body), &bundle); err != nil {
		t.Fatalf("the bundle is not JSON: %v", err)
	}
	return bundle
}

// issue2441Script is the bundle's script with the name.
func issue2441Script(t *testing.T, bundle map[string]any, name string) map[string]any {
	t.Helper()
	for _, sc := range listOf(bundle, "scripts") {
		if sc["name"] == name {
			return sc
		}
	}
	t.Fatalf("the bundle holds no script %s", name)
	return nil
}
