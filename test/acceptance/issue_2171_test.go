//go:build integration

package acceptance

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2171: a managed script bound to an HTTP route. An
// administrator binds a script through manage_script and through the portal,
// and the binding is listed on the script's page; everybody else, the
// script's owner included, is refused on both surfaces; a bound script saved
// without handle(req) is refused naming the function; and a public binding is
// refused while the deployment has no bot check until an administrator sets
// the override.
//
// Wire forms: manage_script endpoint_set takes typed arguments (methods a
// JSON array of strings, path and auth_mode strings, timeout_seconds a JSON
// number), sent as the literal tools/call params; the portal route takes one
// JSON object with the same fields.

const (
	issue2171BotCheckPath = "/api/v1/admin/settings/bot-check"
	issue2171EchoHandler  = `def handle(req):
    """Answers with the id it was asked for and who asked."""
    caller = req["caller"]
    return {"json": {"id": req["params"].get("id"), "caller": caller["email"] if caller else None}}
`
)

// endpointName is a name no other run of the suite has taken.
func endpointName(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
}

// draftMain is the main() a handler is drafted with: one request built by
// hand, so the draft's recording answers what handle calls.
func draftMain(req string) string {
	return "\ndef main():\n    \"\"\"Drafts one request.\"\"\"\n    platform.result(handle(" + req + "))\n"
}

// saveEndpointScript saves handler as a script named name, drafted with
// sampleReq, and returns the script's id.
func saveEndpointScript(t *testing.T, c *client, name, handler, sampleReq string) string {
	t.Helper()
	c.saveScript(map[string]any{"command": "create", "name": name, "source": handler + draftMain(sampleReq)}, nil)
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("get %s carried no id: %v", name, got)
	}
	return id
}

// bindEndpoint binds the script name owner owns, as admin, and fails the
// test on a refusal.
func bindEndpoint(t *testing.T, admin *client, name, owner string, args map[string]any) map[string]any {
	t.Helper()
	out, text := endpointSet(t, admin, name, owner, args)
	if out == nil {
		t.Fatalf("endpoint_set %s was refused: %s", name, text)
	}
	return out
}

// endpointSet is endpoint_set as c, returning the result or, refused, nil
// and the refusal.
func endpointSet(t *testing.T, c *client, name, owner string, args map[string]any) (map[string]any, string) {
	t.Helper()
	params := map[string]any{"command": "endpoint_set", "name": name}
	if owner != "" {
		params["owner_email"] = owner
	}
	for k, v := range args {
		params[k] = v
	}
	res, text, err := c.callRaw("manage_script", params)
	if err != nil {
		t.Fatalf("endpoint_set %s: %v", name, err)
	}
	if res.IsError {
		return nil, text
	}
	return c.call("manage_script", map[string]any{"command": "endpoint_list", "name": name, "owner_email": owner}), ""
}

// endpointResponse is one answer an endpoint gave.
type endpointResponse struct {
	status int
	header http.Header
	body   string
}

// endpointRequest sends one request to the platform at base, with the
// headers given, and reads the answer.
func endpointRequest(t *testing.T, base, method, path string, header http.Header, body string) endpointResponse {
	t.Helper()
	return endpointRequestWith(t, http.DefaultClient, base, method, path, header, body)
}

func endpointRequestWith(t *testing.T, hc *http.Client, base, method, path string, header http.Header, body string) endpointResponse {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, base+path, reader) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	return endpointResponse{status: res.StatusCode, header: res.Header, body: string(raw)}
}

func bearer(key string) http.Header { return http.Header{"Authorization": {"Bearer " + key}} }

// putBotCheck writes the bot check settings and restores the defaults when
// the test ends.
func putBotCheck(t *testing.T, admin *client, body map[string]any) {
	t.Helper()
	status, out := admin.rest(http.MethodPut, issue2171BotCheckPath, jsonBody(t, body))
	if status != http.StatusOK {
		t.Fatalf("PUT %s: %d %v", issue2171BotCheckPath, status, out)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodPut, issue2171BotCheckPath, jsonBody(t, map[string]any{
			"provider": "pow", "site_key": "", "secret_name": "", "allow_public_without_check": false,
			"difficulty": 16, "max_difficulty": 22, "rate_threshold": 30, "challenge_ttl_seconds": 300,
		}))
	})
}

// TestIssue2171_AnAdministratorBindsOnBothSurfacesAndItIsListed: a binding
// set through manage_script and one set through the portal are both listed
// on the script's page, and the endpoint answers.
func TestIssue2171_AnAdministratorBindsOnBothSurfacesAndItIsListed(t *testing.T) {
	admin := connect(t)
	viaTool, viaPortal := endpointName("acc-2171-tool"), endpointName("acc-2171-portal")
	saveEndpointScript(t, admin, viaTool, issue2171EchoHandler, `{"params": {"id": "1"}, "caller": None}`)
	portalID := saveEndpointScript(t, admin, viaPortal, issue2171EchoHandler, `{"params": {"id": "1"}, "caller": None}`)

	listed := bindEndpoint(t, admin, viaTool, "", map[string]any{
		"endpoint": viaTool, "methods": []string{"GET"}, "path": "/{id}", "auth_mode": "signed_in", "timeout_seconds": 5,
	})
	if listed["bound"] != true || listed["endpoint"] != viaTool || fmt.Sprint(listed["timeout_seconds"]) != "5" {
		t.Fatalf("endpoint_list after endpoint_set: %v", listed)
	}

	status, out := admin.rest(http.MethodPut, fmt.Sprintf("/api/v1/portal/scripts/%s/endpoint", portalID), jsonBody(t, map[string]any{
		"endpoint": viaPortal, "methods": []string{"GET"}, "path": "/{id}", "auth_mode": "signed_in",
	}))
	if status != http.StatusOK {
		t.Fatalf("PUT portal endpoint: %d %v", status, out)
	}
	status, page := admin.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/endpoint", portalID), nil)
	if status != http.StatusOK || page["bound"] != true || page["endpoint"] != viaPortal {
		t.Fatalf("the script's page does not list the binding: %d %v", status, page)
	}

	for _, name := range []string{viaTool, viaPortal} {
		got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name+"/7", bearer(admin.apiKey), "")
		if got.status != http.StatusOK || !strings.Contains(got.body, `"id":"7"`) {
			t.Errorf("GET /endpoints/%s/7: %d %s", name, got.status, got.body)
		}
	}
}

// TestIssue2171_ANonAdministratorIsRefusedOnBothSurfaces: the script's own
// owner may not bind it, through the tool or the portal.
func TestIssue2171_ANonAdministratorIsRefusedOnBothSurfaces(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := endpointName("acc-2171-owner")
	id := saveEndpointScript(t, owner, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)

	out, refusal := endpointSet(t, owner, name, "", map[string]any{"endpoint": name, "auth_mode": "signed_in"})
	if out != nil || !strings.Contains(refusal, "only an administrator") {
		t.Fatalf("the owner's endpoint_set: want refused, got %v %q", out, refusal)
	}
	status, body := owner.rest(http.MethodPut, fmt.Sprintf("/api/v1/portal/scripts/%s/endpoint", id),
		jsonBody(t, map[string]any{"endpoint": name, "auth_mode": "signed_in"}))
	if status != http.StatusForbidden {
		t.Fatalf("the owner's portal PUT: want 403, got %d %v", status, body)
	}
	status, page := owner.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/endpoint", id), nil)
	if status != http.StatusOK || page["bound"] != false {
		t.Fatalf("the owner reads an unbound script's page: %d %v", status, page)
	}
}

// TestIssue2171_SavingABoundScriptWithoutHandleIsRefused: the refusal names
// the missing function, and the endpoint keeps answering with the version it
// had.
func TestIssue2171_SavingABoundScriptWithoutHandleIsRefused(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2171-gate")
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "signed_in"})

	update := admin.tested(map[string]any{
		"command": "update", "name": name,
		"source": "def main():\n    \"\"\"Has no handler.\"\"\"\n    platform.result(1)\n",
	}, nil)
	update["change_summary"], update["user_agreed"] = "Removes the handler.", true
	res, text, err := admin.callRaw("manage_script", update)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(text, "was not saved") || !strings.Contains(text, "defines no handle(req)") ||
		!strings.Contains(text, name) {
		t.Fatalf("saving the bound script without handle: want refused naming handle(req), got %s", text)
	}
	if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, bearer(admin.apiKey), ""); got.status != http.StatusOK {
		t.Fatalf("the endpoint after the refused save: %d %s", got.status, got.body)
	}
}

// TestIssue2171_APublicBindingNeedsABotCheckUntilTheOverride: with the
// provider none, a public binding is refused; with the override it is
// accepted.
func TestIssue2171_APublicBindingNeedsABotCheckUntilTheOverride(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2171-public")
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)

	putBotCheck(t, admin, map[string]any{"provider": "none", "allow_public_without_check": false})
	out, refusal := endpointSet(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "public"})
	if out != nil || !strings.Contains(refusal, "needs a bot check") {
		t.Fatalf("a public binding with no bot check: want refused, got %v %q", out, refusal)
	}
	putBotCheck(t, admin, map[string]any{"allow_public_without_check": true})
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "auth_mode": "public"})
	if got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+name, nil, ""); got.status != http.StatusOK {
		t.Fatalf("the public endpoint with the override: %d %s", got.status, got.body)
	}
}
