//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2649: an MCP connection is stored under one name (the one
// its page is reached by) and its calls carry another (its connection_name,
// which prefixes its tools). A tool-call handler matches the name the calls
// carry: handler_set naming the stored name is refused naming the other, the
// connection's page lists and tests under either name with the one calls
// carry, and a policy bound there refuses the live call the test panel says it
// refuses.
//
// Wire forms: PUT /api/v1/admin/connection-instances/mcp/{name} takes a JSON
// object; manage_script handler_set takes kind "policy", connection a string,
// operations a JSON array of strings, everyone and approve JSON booleans; GET
// /api/v1/portal/connections/{name}/handlers takes no body; POST
// .../handlers/test takes tool a string and arguments a JSON object; the
// proxied echo tool takes message and purpose as strings.

// purpose2649 is the purpose every proxied call states.
const purpose2649 = "Acceptance #2649: a policy bound to the name an MCP connection's calls carry."

// mcpConnection2649 adds a gateway connection to the mcp-test fixture stored
// as instance whose calls carry bound, removed after the test.
func mcpConnection2649(t *testing.T, c *client) (instance, bound string) {
	t.Helper()
	stamp := strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	instance, bound = "acc-2649-server-"+stamp, "acc2649"+stamp
	path := "/api/v1/admin/connection-instances/mcp/" + instance
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"endpoint": mcpTestFixture1967, "auth_mode": "api_key", "credential": fixtureKey1967(),
			"connection_name": bound, "connect_timeout": "5s", "call_timeout": "10s",
		},
		"description": "Acceptance #2649: a connection stored under one name whose calls carry another.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("PUT %s: HTTP %d: %v", path, status, body)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, text, err := c.callRaw(bound+"__echo", map[string]any{"message": "ready", "purpose": purpose2649})
		if err == nil && !res.IsError {
			return instance, bound
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s__echo never answered after the connection was added: %v %s", bound, err, text)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestIssue2649_TheStoredNameIsRefusedNamingTheOne: a policy bound to the
// connection's stored name is refused, naming the name its calls carry.
func TestIssue2649_TheStoredNameIsRefusedNamingTheOne(t *testing.T) {
	admin := connect(t)
	instance, bound := mcpConnection2649(t, admin)
	name := handlerNonce("acc-2649-deny")
	saveHandlerScript(t, admin, name, denyPolicy, "decide")
	_, refusal := admin.handlerSetRaw(name, "policy", map[string]any{
		"connection": instance, "operations": []string{"*echo"}, "everyone": true, "approve": true,
	})
	if !strings.Contains(refusal, `name "`+bound+`"`) {
		t.Errorf("binding the stored name %s was not refused naming %s: %s", instance, bound, refusal)
	}
}

// TestIssue2649_ThePageListsTestsAndEnforcesOneName: bound to the name calls
// carry, the policy is listed on the page reached by the stored name, which
// answers that name; the page's test says the call is denied; the live call
// is denied.
func TestIssue2649_ThePageListsTestsAndEnforcesOneName(t *testing.T) {
	admin := connect(t)
	instance, bound := mcpConnection2649(t, admin)
	name := handlerNonce("acc-2649-deny")
	saveHandlerScript(t, admin, name, denyPolicy, "decide")
	h := bindHandler(t, admin, name, "", "policy", map[string]any{"connection": bound, "operations": []string{"*echo"}})

	for _, page := range []string{instance, bound} {
		status, list := admin.rest(http.MethodGet, "/api/v1/portal/connections/"+url.PathEscape(page)+"/handlers", http.NoBody)
		if status != http.StatusOK || list["connection"] != bound {
			t.Fatalf("the page reached as %s: HTTP %d connection %v", page, status, list["connection"])
		}
		found := false
		for _, row := range listOf(list, "data") {
			found = found || row["handler"] == idOf(h)
		}
		if !found {
			t.Errorf("the page reached as %s does not list the policy: %v", page, list["data"])
		}
	}

	status, tested := admin.rest(http.MethodPost, "/api/v1/portal/connections/"+url.PathEscape(instance)+"/handlers/test",
		jsonBody(t, map[string]any{"tool": bound + "__echo", "arguments": map[string]any{"message": "hi"}}))
	if status != http.StatusOK || tested["outcome"] != "denied" {
		t.Errorf("the test panel: HTTP %d %v", status, tested)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		res, text, err := admin.callRaw(bound+"__echo", map[string]any{"message": "hi", "purpose": purpose2649})
		if err == nil && res.IsError && strings.Contains(text, "Nothing runs on Fridays.") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the live call was not denied by the policy the test panel found: %v %s", err, text)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
