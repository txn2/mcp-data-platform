//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2358: a connection test that cannot reach its upstream
// answers with the address it tried and never the credential the request
// carried in its URL. An api connection sends its api_key as a query
// parameter to a port nothing listens on, and its test is read.
//
// Wire forms: the admin routes take one JSON object; the test is a POST with
// no body.

func TestIssue2358_AFailedConnectionTestNamesNoQueryCredential(t *testing.T) {
	c := connect(t)
	name := "acc-2358-" + strings.ToLower(unique1579())
	key := "k3y-2358-" + strings.ToLower(unique1579())
	path := "/api/v1/admin/connection-instances/api/" + name
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"description": "Acceptance #2358: a connection pointed at nothing, its key in the query.",
		"config": map[string]any{
			"base_url": "http://127.0.0.1:9", "connect_timeout": "2s", "call_timeout": "5s",
			"auth_mode": "api_key", "credential": key, "api_key_placement": "query", "api_key_param": "key",
		},
	}))
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, path, http.NoBody) })
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("PUT answered %d: %v", status, body)
	}

	testStatus, result := c.rest(http.MethodPost, path+"/test", http.NoBody)
	if testStatus != http.StatusServiceUnavailable {
		t.Fatalf("testing a connection that cannot be reached answered %d, want 503: %v", testStatus, result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), key) {
		t.Fatalf("the test's answer carries the key: %s", raw)
	}
	if errText, _ := result["error"].(string); !strings.Contains(errText, "key=REDACTED") {
		t.Fatalf("the error does not name the parameter with its value replaced: %q", errText)
	}
}
