//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2005: a catalog spec supplied as Swagger 2.0 is converted to the
// OpenAPI 3 the gateway reads, on every save, and reports what it was
// converted from. These criteria save Swagger 2.0 documents through the admin
// API into a catalog, point an api connection at the dev stack's api-test
// fixture through it, and find and call the operation through the MCP tools.
// The documents carry `basePath: /v1` and no host, the shape swaggo emits, so a
// call that reaches the fixture's /v1/whoami proves the base path was kept.
//
// Wire forms: the admin route's `content` is a string, sent as YAML and as
// JSON (the two forms of the document it admits), and the Swagger version as a
// quoted "2.0" and as the bare number YAML reads 2.0 as. api_discover's and
// api_invoke_endpoint's parameters here are typed strings, sent as such.

const issue2005Purpose = "Acceptance #2005: a Swagger 2.0 catalog spec is served."

// issue2005Documents is the one operation as each form the content admits.
func issue2005Documents(operation string) map[string]string {
	return map[string]string{
		"yaml-quoted": fmt.Sprintf(`swagger: "2.0"
info:
  title: api-test identity
  version: "1.0"
basePath: /v1
paths:
  /whoami:
    get:
      operationId: %s
      summary: Who the caller is
      responses:
        200:
          description: the caller
`, operation),
		"yaml-number": fmt.Sprintf(`swagger: 2.0
info: {title: api-test identity, version: "1.0"}
basePath: /v1
paths:
  /whoami:
    get:
      operationId: %s
      responses:
        200: {description: the caller}
`, operation),
		"json": fmt.Sprintf(`{"swagger": "2.0", "info": {"title": "api-test identity", "version": "1.0"}, `+
			`"basePath": "/v1", "paths": {"/whoami": {"get": {"operationId": %q, `+
			`"responses": {"200": {"description": "the caller"}}}}}}`, operation),
	}
}

func TestIssue2005_ASwagger2SpecIsConvertedAndCalledThroughTheGateway(t *testing.T) {
	c := connect(t)
	for form, document := range issue2005Documents("whoAmI2005") {
		t.Run(form, func(t *testing.T) {
			stamp := time.Now().UnixNano()
			catalogID := fmt.Sprintf("acc-2005-%s-%d", form, stamp)
			if code := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
				"id": catalogID, "name": catalogID, "display_name": "api-test (" + form + ")",
				"description": "The api-test fixture, described in Swagger 2.0.",
			}); code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("creating catalog %s: HTTP %d", catalogID, code)
			}
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })

			status, body := c.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/identity",
				jsonBody(t, map[string]any{"source_kind": "inline", "content": document}))
			if status != http.StatusOK && status != http.StatusNoContent {
				t.Fatalf("saving the Swagger 2.0 spec: HTTP %d %v", status, body)
			}
			if body["converted_from"] != "swagger 2.0" {
				t.Errorf("the saved spec reports converted_from %v, want \"swagger 2.0\"", body["converted_from"])
			}

			name := catalogID
			if code := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
				"config": map[string]any{
					"base_url": apiTestFixtureURL(), "auth_mode": "api_key", "credential": issue2005FixtureKey(),
					"api_key_placement": "header", "api_key_header": "X-API-Key",
					"connection_name": name, "catalog_id": catalogID,
					"connect_timeout": "5s", "call_timeout": "20s", "trust_level": "untrusted",
				},
				"description": "api-test fixture through a Swagger 2.0 catalog, #2005 acceptance.",
			}); code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("registering connection %s: HTTP %d", name, code)
			}
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
			issue1736AwaitConnection(t, c, name)

			ops := map[string]map[string]any{}
			collectOperations(c.call("api_discover", map[string]any{"connection": name, "purpose": issue2005Purpose}), ops)
			if _, ok := ops["whoAmI2005"]; !ok {
				t.Fatalf("api_discover did not list the Swagger 2.0 operation; it listed %v", keysOfOps(ops))
			}

			out := c.call("api_invoke_endpoint", map[string]any{
				"connection": name, "operation_id": "whoAmI2005", "purpose": issue2005Purpose,
			})
			if code, _ := out["status"].(float64); code != http.StatusOK {
				t.Errorf("calling the operation answered %v, want 200 from /v1/whoami: %v", out["status"], firstRunes(fmt.Sprint(out), 600))
			}
		})
	}
}

// issue2005FixtureKey is the api-test fixture's development key, as dev/start.sh
// registers it.
func issue2005FixtureKey() string {
	return "apitest-dev-key-2024"
}
