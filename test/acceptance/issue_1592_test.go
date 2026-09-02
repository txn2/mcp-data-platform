//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1592: API-connection discovery was three tools that answered one
// question at three depths (api_list_specs, api_list_endpoints,
// api_get_endpoint_schema). It is one tool, api_discover, whose depth the
// arguments select: a bare call on a multi-spec catalog returns the specs, spec
// or query returns the matching operations, operation_id returns one
// operation's schema, and every response names the argument that goes one
// level deeper. The three names are retired, not aliased.
//
// Every criterion runs through the real surface against the dev stack (make
// dev). The api-test fixture (api-test-fixture, one spec, 17 operations) and
// the built-in platform-admin connection (one spec) are the single-spec
// connections; the multi-spec catalog is built here through the admin REST
// API from two specs that both describe the api-test fixture, and a
// catalog-less connection to the same fixture is registered the same way.

const (
	issue1592Tool        = "api_discover"
	issue1592FixtureConn = "api-test-fixture"
	issue1592AdminConn   = "platform-admin"
	issue1592FixtureURL  = "http://localhost:9282"
	issue1592FixtureKey  = "apitest-dev-key-2024"
	issue1592Purpose     = "Acceptance for #1592: api_discover replaces the three discovery tools."
)

// issue1592Retired is the surface #1592 retired.
var issue1592Retired = []string{"api_list_specs", "api_list_endpoints", "api_get_endpoint_schema"}

// issue1592IdentitySpec and issue1592EchoSpec are the two component specs of
// the multi-spec catalog. Both define an operation with the id "ping", so the
// operation level's disambiguation is executed too.
const issue1592IdentitySpec = `{
  "openapi": "3.0.3",
  "info": {"title": "Fixture identity", "version": "1"},
  "paths": {
    "/v1/whoami": {"get": {"operationId": "whoami", "summary": "Who the fixture thinks the caller is", "responses": {"200": {"description": "ok"}}}},
    "/v1/headers": {"get": {"operationId": "ping", "summary": "Echo the request headers", "responses": {"200": {"description": "ok"}}}}
  }
}`

const issue1592EchoSpec = `{
  "openapi": "3.0.3",
  "info": {"title": "Fixture echo", "version": "1"},
  "paths": {
    "/v1/echo": {"post": {"operationId": "echo", "summary": "Echo the request body", "requestBody": {"content": {"application/json": {"schema": {"type": "object"}}}}, "responses": {"200": {"description": "ok"}}}},
    "/v1/lorem": {"get": {"operationId": "ping", "summary": "Lorem ipsum text", "responses": {"200": {"description": "ok"}}}}
  }
}`

// requireAPIDiscover fails, rather than skips, when the running platform
// registers no api_discover: the dev stack always carries an API connection.
func requireAPIDiscover(t *testing.T, c *client) {
	t.Helper()
	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == issue1592Tool {
			return
		}
	}
	t.Fatalf("the running platform registers no %s; the dev stack (make dev) carries an API connection", issue1592Tool)
}

// discover issues one api_discover call and returns its result. Discovery
// carries no purpose argument: the schema is closed, and purpose is asked
// of the tools that reach data, which discovery does not.
func (c *client) discover(args map[string]any) map[string]any {
	c.t.Helper()
	return c.call(issue1592Tool, args)
}

// discoverErr issues one api_discover call expected to be refused and returns
// the refusal text.
func (c *client) discoverErr(args map[string]any) string {
	c.t.Helper()
	res, text, err := c.callRaw(issue1592Tool, args)
	if err != nil {
		c.t.Fatalf("%s: transport error: %v", issue1592Tool, err)
	}
	if !res.IsError {
		c.t.Fatalf("%s %v: expected a refusal, got %s", issue1592Tool, args, text)
	}
	return text
}

// restJSON issues an admin REST call with a JSON body and returns the status.
func (c *client) restJSON(method, path string, body any) int {
	c.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		c.t.Fatalf("marshal %s %s: %v", method, path, err)
	}
	status, _ := c.rest(method, path, bytes.NewReader(raw))
	return status
}

// fixtureConnectionConfig is the api-test fixture as dev/start.sh registers
// it, with or without a catalog.
func fixtureConnectionConfig(name, catalogID string) map[string]any {
	cfg := map[string]any{
		"base_url": issue1592FixtureURL, "auth_mode": "api_key", "credential": issue1592FixtureKey,
		"api_key_placement": "header", "api_key_header": "X-API-Key", "connection_name": name,
		"connect_timeout": "5s", "call_timeout": "10s", "trust_level": "untrusted",
	}
	if catalogID != "" {
		cfg["catalog_id"] = catalogID
	}
	return cfg
}

// createMultiSpecConnection builds a two-spec catalog and a connection over
// it through the admin REST API, registering their removal.
func createMultiSpecConnection(t *testing.T, c *client) (connection string) {
	t.Helper()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	catalogID := "acc-1592-" + stamp
	connection = "acc-1592-multi-" + stamp

	if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 1592",
		"description": "Two specs over the api-test fixture for the #1592 acceptance run.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create catalog: HTTP %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
	for name, content := range map[string]string{"identity": issue1592IdentitySpec, "echo": issue1592EchoSpec} {
		if status := c.restJSON(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/"+name, map[string]any{
			"source_kind": "inline", "content": content,
		}); status != http.StatusCreated && status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("upsert spec %s: HTTP %d", name, status)
		}
	}
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+connection, map[string]any{
		"config":      fixtureConnectionConfig(connection, catalogID),
		"description": "Acceptance 1592: a two-spec catalog over the api-test fixture.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection: HTTP %d", status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+connection, http.NoBody)
	})
	return connection
}

// createBareConnection registers a connection to the fixture with no catalog.
func createBareConnection(t *testing.T, c *client) (connection string) {
	t.Helper()
	connection = fmt.Sprintf("acc-1592-bare-%d", time.Now().UnixNano())
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+connection, map[string]any{
		"config":      fixtureConnectionConfig(connection, ""),
		"description": "Acceptance 1592: the api-test fixture with no catalog.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection: HTTP %d", status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+connection, http.NoBody)
	})
	return connection
}

// operationIDs reads the operation ids out of an operations-level result.
func operationIDs(t *testing.T, out map[string]any) []string {
	t.Helper()
	ops, _ := out["operations"].([]any)
	ids := make([]string, 0, len(ops))
	for _, op := range ops {
		m, _ := op.(map[string]any)
		id, _ := m["operation_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// Criterion 5: the tool list carries api_discover and none of the three, the
// API toolkit registers exactly three tools, and a retired name answers
// nothing.
func TestIssue1592_OneDiscoveryToolReplacesThree(t *testing.T) {
	c := connect(t)
	requireAPIDiscover(t, c)

	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var apiTools []string
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "api_") {
			apiTools = append(apiTools, tool.Name)
		}
	}
	want := map[string]bool{"api_discover": true, "api_invoke_endpoint": true, "api_export": true}
	if len(apiTools) != len(want) {
		t.Fatalf("the API toolkit should register exactly %d tools, got %v", len(want), apiTools)
	}
	for _, name := range apiTools {
		if !want[name] {
			t.Errorf("unexpected API tool %q registered", name)
		}
	}
	for _, name := range issue1592Retired {
		res, text, err := c.callRaw(name, map[string]any{"connection": issue1592FixtureConn})
		if err == nil && !res.IsError {
			t.Errorf("retired tool %s still answers: %s", name, text)
		}
	}
}

// Criterion 1: each level is one call, and the walk goes specs, operations,
// schema, invoke on a real multi-spec connection.
func TestIssue1592_WalkSpecsToOperationsToSchemaToInvoke(t *testing.T) {
	c := connect(t)
	requireAPIDiscover(t, c)
	conn := createMultiSpecConnection(t, c)

	specs := c.discover(map[string]any{"connection": conn})
	if specs["level"] != "specs" {
		t.Fatalf("a bare call on a multi-spec catalog is the specs level: %v", specs)
	}
	specList, _ := specs["specs"].([]any)
	if len(specList) != 2 || specs["operations"] != nil {
		t.Fatalf("expected two spec summaries and no operations: %v", specs)
	}
	if next, _ := specs["next"].(string); !strings.Contains(next, "spec=<name>") {
		t.Errorf("the specs level should name spec as the next argument: %q", next)
	}

	ops := c.discover(map[string]any{"connection": conn, "spec": "identity"})
	if ops["level"] != "operations" || ops["specs"] != nil {
		t.Fatalf("spec selects the operations level: %v", ops)
	}
	ids := operationIDs(t, ops)
	if len(ids) != 2 || !strings.Contains(strings.Join(ids, ","), "whoami") {
		t.Fatalf("expected the identity spec's two operations, got %v", ids)
	}
	if next, _ := ops["next"].(string); !strings.Contains(next, "operation_id=<id>") {
		t.Errorf("the operations level should name operation_id as the next argument: %q", next)
	}

	across := c.discover(map[string]any{"connection": conn, "query": "echo", "limit": 10})
	if across["level"] != "operations" {
		t.Fatalf("a query with no spec is the operations level across every spec: %v", across)
	}
	if ids := operationIDs(t, across); len(ids) == 0 || !strings.Contains(strings.Join(ids, ","), "echo") {
		t.Fatalf("query should rank the echo operation across specs, got %v", ids)
	}
	// A semantic request on a connection registered moments ago: its
	// vectors are written by the embed job after registration, so this is
	// where the documented fallback to lexical, with its note, executes at
	// the real surface when the job has not caught up yet.
	semantic := c.discover(map[string]any{"connection": conn, "query": "echo the request body", "ranking": "semantic"})
	if ids := operationIDs(t, semantic); len(ids) == 0 || !strings.Contains(strings.Join(ids, ","), "echo") {
		t.Fatalf("semantic ranking should still list the echo operation: %v", semantic)
	}
	if note, _ := semantic["note"].(string); strings.Contains(note, "fell back to lexical") {
		t.Logf("semantic on a just-registered connection fell back to lexical with the documented note: %q", note)
	} else {
		t.Logf("semantic on a just-registered connection ranked semantically (its catalog was already indexed)")
	}

	schema := c.discover(map[string]any{"connection": conn, "operation_id": "whoami"})
	if schema["level"] != "operation" {
		t.Fatalf("operation_id selects the operation level: %v", schema)
	}
	op, _ := schema["operation"].(map[string]any)
	if op["method"] != "GET" || op["path"] != "/v1/whoami" || op["spec"] != "identity" {
		t.Fatalf("expected GET /v1/whoami from the identity spec: %v", op)
	}
	next, _ := schema["next"].(string)
	for _, want := range []string{"api_invoke_endpoint", `operation_id="whoami"`, "path_params"} {
		if !strings.Contains(next, want) {
			t.Errorf("the operation level's next should carry %q: %q", want, next)
		}
	}

	invoked := c.call("api_invoke_endpoint", map[string]any{
		"connection": conn, "operation_id": "whoami", "purpose": issue1592Purpose,
	})
	if number(t, invoked, "status") != http.StatusOK {
		t.Fatalf("invoking the discovered operation: %v", invoked)
	}

	// An id both specs define is refused naming the candidates, and spec
	// resolves it.
	refusal := c.discoverErr(map[string]any{"connection": conn, "operation_id": "ping"})
	if !strings.Contains(refusal, "ambiguous") || !strings.Contains(refusal, "echo, identity") {
		t.Errorf("an ambiguous id should list both candidate specs: %s", refusal)
	}
	resolved := c.discover(map[string]any{"connection": conn, "operation_id": "ping", "spec": "echo"})
	op, _ = resolved["operation"].(map[string]any)
	if op["path"] != "/v1/lorem" {
		t.Errorf("spec should disambiguate ping to the echo spec's operation: %v", op)
	}
	if next, _ := resolved["next"].(string); !strings.Contains(next, `spec="echo"`) {
		t.Errorf("a spec the caller needed is repeated for invoke: %q", next)
	}

	// A spec the catalog does not have is refused naming the ones it has.
	refusal = c.discoverErr(map[string]any{"connection": conn, "spec": "billing"})
	if !strings.Contains(refusal, "echo, identity") {
		t.Errorf("an unknown spec should be refused naming the catalog's specs: %s", refusal)
	}
}

// Criterion 2: a single-spec connection needs no spec-selection step: a bare
// call returns operations.
func TestIssue1592_SingleSpecBareCallListsOperations(t *testing.T) {
	c := connect(t)
	requireAPIDiscover(t, c)

	for _, conn := range []string{issue1592FixtureConn, issue1592AdminConn} {
		out := c.discover(map[string]any{"connection": conn, "limit": 500})
		if out["level"] != "operations" || out["specs"] != nil {
			t.Fatalf("%s: a bare call on a single-spec catalog is the operations level: level=%v specs=%v", conn, out["level"], out["specs"])
		}
		if ids := operationIDs(t, out); len(ids) == 0 {
			t.Fatalf("%s: expected operations, got none: %v", conn, out)
		}
	}
	out := c.discover(map[string]any{"connection": issue1592FixtureConn, "limit": 500})
	if ids := operationIDs(t, out); len(ids) < 14 || !strings.Contains(strings.Join(ids, ","), "whoami") {
		t.Errorf("%s serves at least the fixture's 14 documented operations, got %d: %v", issue1592FixtureConn, len(ids), ids)
	}
}

// Criterion 3: the ranking modes of the old api_list_endpoints are preserved,
// including the fallback to lexical with its note.
func TestIssue1592_RankingModesArePreserved(t *testing.T) {
	c := connect(t)
	requireAPIDiscover(t, c)

	lexical := c.discover(map[string]any{"connection": issue1592FixtureConn, "query": "echo", "ranking": "lexical"})
	ids := operationIDs(t, lexical)
	if len(ids) == 0 {
		t.Fatalf("lexical ranking should match the echo operations: %v", lexical)
	}
	for _, id := range ids {
		if !strings.Contains(strings.ToLower(id), "echo") {
			t.Errorf("lexical AND match returned %q for query echo", id)
		}
	}
	if note, _ := lexical["note"].(string); strings.Contains(note, "fell back") {
		t.Errorf("explicit lexical never falls back: %q", note)
	}

	for _, mode := range []string{"semantic", "hybrid"} {
		out := c.discover(map[string]any{"connection": issue1592FixtureConn, "query": "echo the request body", "ranking": mode})
		if out["level"] != "operations" || len(operationIDs(t, out)) == 0 {
			t.Fatalf("%s ranking should list operations: %v", mode, out)
		}
		note, _ := out["note"].(string)
		if strings.Contains(note, "fell back to lexical") {
			t.Logf("%s: fell back to lexical with the documented note: %q", mode, note)
		} else {
			t.Logf("%s: ranked semantically (the fixture's catalog is indexed)", mode)
		}
	}

	refusal := c.discoverErr(map[string]any{"connection": issue1592FixtureConn, "query": "echo", "ranking": "weird"})
	if !strings.Contains(refusal, "ranking") {
		t.Errorf("an unknown ranking mode is refused naming the argument: %s", refusal)
	}
}

// Criterion 4: a connection with no catalog answers with the note telling the
// caller to invoke by method and path, and that call works.
func TestIssue1592_NoCatalogAnswersWithTheInvokeNote(t *testing.T) {
	c := connect(t)
	requireAPIDiscover(t, c)
	conn := createBareConnection(t, c)

	for _, args := range []map[string]any{
		{"connection": conn},
		{"connection": conn, "query": "whoami"},
	} {
		out := c.discover(args)
		note, _ := out["note"].(string)
		if !strings.Contains(note, "no catalog configured") || !strings.Contains(note, "api_invoke_endpoint with method+path") {
			t.Fatalf("%v: expected the no-catalog note, got %v", args, out)
		}
		if out["operations"] != nil || out["specs"] != nil {
			t.Errorf("%v: nothing to list on a catalog-less connection: %v", args, out)
		}
	}
	refusal := c.discoverErr(map[string]any{"connection": conn, "operation_id": "whoami"})
	if !strings.Contains(refusal, "no catalog configured") {
		t.Errorf("an operation_id cannot resolve without a catalog and says so: %s", refusal)
	}

	invoked := c.call("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/v1/whoami", "purpose": issue1592Purpose,
	})
	if number(t, invoked, "status") != http.StatusOK {
		t.Fatalf("the note's advice should work: %v", invoked)
	}
}

// platform_find_tools returns api_discover for a discovery task.
func TestIssue1592_FindToolsReturnsDiscover(t *testing.T) {
	c := connect(t)
	requireAPIDiscover(t, c)
	out := c.call("platform_find_tools", map[string]any{"query": "discover the operations of an API connection", "limit": 5})
	tools, _ := out["tools"].([]any)
	for _, tool := range tools {
		m, _ := tool.(map[string]any)
		if m["name"] == issue1592Tool {
			return
		}
	}
	t.Fatalf("platform_find_tools did not return %s: %v", issue1592Tool, out)
}
