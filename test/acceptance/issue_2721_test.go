//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2721: a JSON spec is read as JSON, not through the loader's YAML
// parser that allocated two hundred times a large spec's size at startup. A
// spec in either form, its references included, still serves its operations
// through an api connection, and the platform's own admin API, the largest
// spec a deployment holds, still lists its operations.
//
// Wire forms: the spec is saved with PUT
// /api/v1/admin/api-catalogs/{id}/specs/{name} and a JSON object whose
// `content` is a string holding JSON in one case and YAML in the other, the
// two document forms the content admits; api_discover's `connection` and
// `purpose` and api_invoke_endpoint's `operation_id` are strings.

const issue2721Purpose = "Acceptance #2721: a spec read as JSON serves its operations."

// issue2721Documents is one OpenAPI 3 document in each form, its response
// described through a component reference.
func issue2721Documents(op string) map[string]string {
	return map[string]string{
		"json": `{"openapi":"3.0.3","info":{"title":"api-test","version":"1"},"paths":{"/v1/whoami":{"get":{"operationId":"` + op +
			`","responses":{"200":{"description":"who","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Who"}}}}}}}},` +
			`"components":{"schemas":{"Who":{"type":"object","properties":{"key":{"type":"string"}}}}}}`,
		"yaml": "openapi: 3.0.3\ninfo:\n  title: api-test\n  version: '1'\npaths:\n  /v1/whoami:\n    get:\n      operationId: " + op +
			"\n      responses:\n        '200':\n          description: who\n          content:\n            application/json:\n" +
			"              schema:\n                $ref: '#/components/schemas/Who'\ncomponents:\n  schemas:\n    Who:\n      type: object\n" +
			"      properties:\n        key:\n          type: string\n",
	}
}

// TestIssue2721_ASpecInEitherFormServesItsOperations: each form is saved,
// served through a connection, listed by api_discover and called.
func TestIssue2721_ASpecInEitherFormServesItsOperations(t *testing.T) {
	c := connect(t)
	for form, document := range issue2721Documents("whoAmI2721") {
		t.Run(form, func(t *testing.T) {
			catalogID := fmt.Sprintf("acc-2721-%s-%d", form, time.Now().UnixNano())
			if code := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
				"id": catalogID, "name": catalogID, "display_name": "api-test (" + form + ")",
				"description": "The api-test fixture, #2721 acceptance.",
			}); code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("creating catalog %s: HTTP %d", catalogID, code)
			}
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
			status, body := c.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/identity",
				jsonBody(t, map[string]any{"source_kind": "inline", "content": document}))
			if status != http.StatusOK && status != http.StatusNoContent {
				t.Fatalf("saving the %s spec: HTTP %d %v", form, status, body)
			}

			name := catalogID
			if code := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
				"config": map[string]any{
					"base_url": apiTestFixtureURL(), "auth_mode": "api_key", "credential": issue2005FixtureKey(),
					"api_key_placement": "header", "api_key_header": "X-API-Key",
					"connection_name": name, "catalog_id": catalogID,
					"connect_timeout": "5s", "call_timeout": "20s", "trust_level": "untrusted",
				},
				"description": "api-test fixture, #2721 acceptance.",
			}); code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("registering connection %s: HTTP %d", name, code)
			}
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
			issue1736AwaitConnection(t, c, name)

			ops := map[string]map[string]any{}
			collectOperations(c.call("api_discover", map[string]any{"connection": name, "purpose": issue2721Purpose}), ops)
			if _, ok := ops["whoAmI2721"]; !ok {
				t.Fatalf("api_discover did not list the %s spec's operation; it listed %v", form, keysOfOps(ops))
			}
			out := c.call("api_invoke_endpoint", map[string]any{"connection": name, "operation_id": "whoAmI2721", "purpose": issue2721Purpose})
			if code, _ := out["status"].(float64); code != http.StatusOK {
				t.Errorf("calling the operation answered %v, want 200: %v", out["status"], firstRunes(fmt.Sprint(out), 600))
			}
		})
	}
}

// TestIssue2721_ThePlatformsOwnAPIListsItsOperations: the platform-admin
// connection, served from the converted Swagger 2.0 document the platform
// embeds, lists the admin operations.
func TestIssue2721_ThePlatformsOwnAPIListsItsOperations(t *testing.T) {
	c := connect(t)
	ops := map[string]map[string]any{}
	collectOperations(c.call("api_discover", map[string]any{"connection": "platform-admin", "query": "list personas", "purpose": issue2721Purpose}), ops)
	if len(ops) == 0 {
		t.Fatal("api_discover on platform-admin listed no operation")
	}
}
