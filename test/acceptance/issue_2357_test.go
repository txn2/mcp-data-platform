//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2357: an mcp connection whose credential names a stored
// secret never returns the secret's value in a forwarded tool result. A
// connection to the mcp-test fixture is created with credential
// {{secret:<name>}}, and the fixture's headers tool, which answers with the
// request headers it received, is called through it.
//
// Wire forms: the admin routes take one JSON object; the gateway tool takes
// no arguments beyond purpose, a string.

func TestIssue2357_AnMCPToolResultCarriesNoFilledSecret(t *testing.T) {
	admin := connect(t)
	stamp := strings.ToLower(unique1579())
	conn := "acc-2357-" + stamp
	secret := "acc2357_" + stamp
	if status := admin.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secret, map[string]any{
		"description": "Acceptance #2357", "value": fixtureKey1967(), "allow_connections": []string{conn}, "allow_personas": []string{},
	}); status != http.StatusCreated {
		t.Fatalf("creating the secret answered %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/secrets/"+secret, http.NoBody) })

	path := "/api/v1/admin/connection-instances/mcp/" + conn
	t.Cleanup(func() { _, _ = admin.rest(http.MethodDelete, path, http.NoBody) })
	status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"endpoint": mcpTestFixture1967, "auth_mode": "api_key", "credential": "{{secret:" + secret + "}}",
			"connection_name": conn, "connect_timeout": "5s", "call_timeout": "10s",
		},
		"description": "Acceptance #2357",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("PUT %s: HTTP %d: %v", path, status, body)
	}

	c := connect(t)
	res, text, err := c.callRaw(conn+"__headers", map[string]any{"purpose": "Acceptance #2357: the fixture echoes its request headers."})
	if err != nil {
		t.Fatalf("calling %s__headers: %v", conn, err)
	}
	if res.IsError {
		t.Fatalf("%s__headers failed: %s", conn, text)
	}
	if strings.Contains(text, fixtureKey1967()) {
		t.Fatalf("the tool result holds the stored secret's value:\n%s", text)
	}
	if !strings.Contains(text, "[REDACTED:"+secret+"]") {
		t.Fatalf("the echoed credential is not shown as the secret's name:\n%s", text)
	}
}
