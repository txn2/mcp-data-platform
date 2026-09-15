//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1745: a `graphql` connection's schema was stored per connection and
// keyed by connection name. It could not be registered from a URL, two
// connections against one endpoint each stored, hashed and embedded their own
// copy, and the schema was not an inventoried object: `list_connections`
// reported neither catalog nor operation count for it, and there was no admin
// listing of GraphQL schemas at all.
//
// What these hold, against the running platform: a catalog spec entry whose
// spec_format is graphql accepts SDL and is refused an introspection result;
// its operations are listed and described through the catalog routes an
// OpenAPI spec uses; a graphql connection naming that catalog serves
// graphql_discover and graphql_query from the catalog's schema WITHOUT
// reaching its endpoint for it, reports source "catalog" beside the hash and
// read time, and refuses an upload by naming the catalog; two connections on
// one catalog report one schema hash; list_connections reports the catalog and
// the operation count; an edit to the catalog reaches the connections serving
// it; and an `api` connection whose catalog holds the schema keeps serving its
// OpenAPI specs rather than failing on SDL it cannot parse.
//
// Wire forms: graphql_query's and graphql_export's `variables` is typed
// ["object", "string"]; the query criterion sends BOTH forms as literal
// tools/call params against the catalog-backed connection
// (TestIssue1745_ACatalogBackedConnectionRunsADocument). Every other parameter
// these send — `connection`, `query`, `operation_id`, `purpose`, and the admin
// bodies' `content`, `source_kind`, `spec_format`, `catalog_id` — is typed
// string and admits one form.

const issue1745Purpose = "Acceptance for #1745: a graphql connection takes its schema from an API catalog."

// issue1745SDL is the schema the catalog holds. It is deliberately NOT the
// endpoint's own schema: every criterion that reads it proves the catalog was
// the source, because introspecting the endpoint would return something else.
const issue1745SDL = `
"The root query type."
type Query {
  acceptance: AcceptanceQueries
}

"Operations this acceptance schema exposes."
type AcceptanceQueries {
  "Echo a string back, for the #1745 acceptance run."
  echo(message: String!): String
  "Look one widget up by id."
  widget(id: ID!): Widget
}

"A widget."
type Widget {
  id: ID!
  name: String
}
`

// issue1745Catalog creates a catalog holding issue1745SDL as a graphql spec
// and returns its id.
func issue1745Catalog(t *testing.T, c *client, label string) string {
	t.Helper()
	catalogID := fmt.Sprintf("acc-1745-%s-%d", label, time.Now().UnixNano())
	if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 1745",
		"description": "A GraphQL schema as a catalogued spec, for the #1745 acceptance run.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create catalog: HTTP %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
	issue1745PutSchema(t, c, catalogID, issue1745SDL)
	return catalogID
}

// issue1745PutSchema writes the catalog's one graphql spec entry.
func issue1745PutSchema(t *testing.T, c *client, catalogID, sdl string) {
	t.Helper()
	status := c.restJSON(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/schema", map[string]any{
		"source_kind": "inline", "spec_format": "graphql", "content": sdl,
	})
	if status != http.StatusCreated && status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("upsert the graphql spec: HTTP %d", status)
	}
}

// issue1745Register saves a graphql connection naming catalogID. Its endpoint
// is the repository's real GraphQL endpoint, whose schema is nothing like
// issue1745SDL, so a connection answering with the catalog's operations proves
// the endpoint was not introspected for them.
func issue1745Register(t *testing.T, c *client, label, catalogID string) string {
	t.Helper()
	name := fmt.Sprintf("acc-1745-%s-%d", label, time.Now().UnixNano())
	config := map[string]any{
		"endpoint_url":    issue1277Endpoint(),
		"connection_name": name,
		"connect_timeout": "10s",
		"call_timeout":    "30s",
	}
	if catalogID != "" {
		config["catalog_id"] = catalogID
	}
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+name, map[string]any{
		"config": config, "description": "Acceptance 1745: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the %s connection: HTTP %d", label, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody)
	})
	return name
}

// issue1745Schema reads a connection's schema state through the admin route.
func issue1745Schema(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet,
		"/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET .../schema for %s: HTTP %d %v", name, status, body)
	}
	return body
}

// TestIssue1745_ACatalogHoldsAGraphQLSchemaAndListsItsOperations is the
// inventory the ticket asks for: the schema is a catalogued spec, browsed
// operation by operation through the routes an OpenAPI spec is browsed by.
func TestIssue1745_ACatalogHoldsAGraphQLSchemaAndListsItsOperations(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "inventory")

	status, body := c.rest(http.MethodGet,
		"/api/v1/admin/api-catalogs/"+catalogID+"/specs/schema/operations", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("listing the catalogued schema's operations: HTTP %d %v", status, body)
	}
	raw, err := json.Marshal(body["operations"])
	if err != nil {
		t.Fatalf("re-marshalling the operations: %v", err)
	}
	var ops []struct {
		OperationID string `json:"operation_id"`
		Method      string `json:"method"`
		Path        string `json:"path"`
		Summary     string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &ops); err != nil {
		t.Fatalf("decoding the operations: %v\n%s", err, raw)
	}
	if len(ops) != 2 {
		t.Fatalf("the catalogued schema lists %d operations; want the two it exposes: %v", len(ops), ops)
	}
	found := map[string]bool{}
	for _, op := range ops {
		found[op.OperationID] = true
		if op.Method != "QUERY" {
			t.Errorf("%s reports method %q; want the GraphQL operation kind", op.OperationID, op.Method)
		}
	}
	if !found["query:acceptance.echo"] || !found["query:acceptance.widget"] {
		t.Errorf("the dotted operation ids a connection invokes by are missing: %v", found)
	}

	// The detail route answers what graphql_discover answers at its
	// operation level, so a page and a tool call describe one operation
	// identically.
	status, detail := c.rest(http.MethodGet,
		"/api/v1/admin/api-catalogs/"+catalogID+"/specs/schema/operations/query:acceptance.echo", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("describing one catalogued operation: HTTP %d %v", status, detail)
	}
	// The operations pane renders either format, so the fields it reads for
	// an OpenAPI operation carry the GraphQL equivalent and what has no
	// equivalent travels beside them.
	if method, _ := detail["method"].(string); method != "QUERY" {
		t.Errorf("the pane would render method %q for a query", method)
	}
	if path, _ := detail["path"].(string); path != "/acceptance/echo" {
		t.Errorf("the pane would render path %q", path)
	}
	if skeleton, _ := detail["graphql_skeleton"].(string); !strings.Contains(skeleton, "echo") {
		t.Errorf("the operation detail carries no document that calls it: %v", detail["graphql_skeleton"])
	}
	args, _ := detail["graphql_arguments"].([]any)
	if len(args) != 1 {
		t.Errorf("the operation's arguments are missing from the detail: %v", detail["graphql_arguments"])
	}
}

// TestIssue1745_ACatalogRefusesSomethingThatIsNotSDL holds the write-time
// contract: what a catalog stores is a document an operator can read back and
// diff, so an introspection result is refused rather than accepted.
func TestIssue1745_ACatalogRefusesSomethingThatIsNotSDL(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "refusal")

	status, body := c.rest(http.MethodPut,
		"/api/v1/admin/api-catalogs/"+catalogID+"/specs/introspected",
		jsonBody(t, map[string]any{
			"source_kind": "inline", "spec_format": "graphql",
			"content": `{"__schema":{"queryType":{"name":"Query"}}}`,
		}))
	if status < 400 {
		t.Fatalf("an introspection result was accepted as a catalogued schema: HTTP %d %v", status, body)
	}
}

// TestIssue1745_AConnectionAnswersFromTheCatalogNotItsEndpoint is the ticket's
// centre. The connection's endpoint is a real GraphQL server whose schema is
// nothing like the catalog's, so answering with the catalog's operations is
// the proof that the endpoint was not introspected for them.
func TestIssue1745_AConnectionAnswersFromTheCatalogNotItsEndpoint(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "source")
	name := issue1745Register(t, c, "source", catalogID)

	out := c.call("graphql_discover", map[string]any{
		"connection": name,
		"query":      "echo a string",
		"purpose":    issue1745Purpose,
	})
	raw, err := json.Marshal(out["operations"])
	if err != nil {
		t.Fatalf("re-marshalling the operations: %v", err)
	}
	if !strings.Contains(string(raw), "query:acceptance.echo") {
		t.Fatalf("graphql_discover did not answer from the catalog's schema: %s", raw)
	}

	// The connection still says which version answered and where it came
	// from, which is what makes a catalogued schema diagnosable.
	if hash, _ := out["schema_hash"].(string); hash == "" {
		t.Error("graphql_discover reports no schema_hash for a catalogued schema")
	}
	if fetched, _ := out["schema_fetched_at"].(string); fetched == "" {
		t.Error("graphql_discover reports no schema_fetched_at for a catalogued schema")
	}

	state := issue1745Schema(t, c, name)
	if source, _ := state["source"].(string); source != "catalog" {
		t.Errorf("the schema state reports source %q; want the catalog it came from: %v", source, state)
	}
	if failure, _ := state["error"].(string); failure != "" {
		t.Errorf("a read failure was recorded for a schema read from the catalog: %q", failure)
	}
	if count, _ := state["operation_count"].(float64); int(count) != 2 {
		t.Errorf("the schema state reports %v operations; want the catalog's two: %v", state["operation_count"], state)
	}
}

// TestIssue1745_ACatalogBackedConnectionRunsADocument holds that the catalogued
// schema is the one a document is validated against, in both JSON forms the
// `variables` parameter admits.
func TestIssue1745_ACatalogBackedConnectionRunsADocument(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "query")
	name := issue1745Register(t, c, "query", catalogID)

	// A document the CATALOG's schema does not admit is refused under
	// strict validation, which is the observable proof that the catalogued
	// schema is what validation runs against.
	for _, form := range []struct {
		label     string
		variables any
	}{
		{"object", map[string]any{"message": "hello"}},
		{"string", `{"message":"hello"}`},
	} {
		t.Run(form.label, func(t *testing.T) {
			res, text, err := c.callRaw("graphql_query", map[string]any{
				"connection": name,
				"query":      "query Nope($message: String!) { acceptance { nosuchfield(message: $message) } }",
				"variables":  form.variables,
				"purpose":    issue1745Purpose,
			})
			if err != nil {
				t.Fatalf("graphql_query: transport error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("a document the catalogued schema does not admit was sent: %s", text)
			}
			if !strings.Contains(text, "nosuchfield") {
				t.Errorf("the refusal does not name what the schema rejected: %s", text)
			}
		})
	}
}

// TestIssue1745_AnUploadIsRefusedByNamingTheCatalog holds that the catalog is
// the one place the schema is edited: an upload would be replaced on the next
// read and would differ from what every other connection on the catalog serves.
func TestIssue1745_AnUploadIsRefusedByNamingTheCatalog(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "upload")
	name := issue1745Register(t, c, "upload", catalogID)

	status, body := c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema",
		strings.NewReader("type Query { unrelated: String }"))
	if status < 400 {
		t.Fatalf("a schema was uploaded onto a connection whose catalog owns it: HTTP %d %v", status, body)
	}
	if !strings.Contains(fmt.Sprint(body), catalogID) {
		t.Errorf("the refusal does not say where to make the edit: %v", body)
	}
}

// TestIssue1745_TwoConnectionsOnOneCatalogServeOneSchema is the sharing the
// ticket exists for: read-only and read-write against one endpoint store,
// hash and embed the schema once rather than once each.
func TestIssue1745_TwoConnectionsOnOneCatalogServeOneSchema(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "shared")
	readWrite := issue1745Register(t, c, "rw", catalogID)
	readOnly := issue1745Register(t, c, "ro", catalogID)

	first := issue1745Schema(t, c, readWrite)
	second := issue1745Schema(t, c, readOnly)
	firstHash, _ := first["schema_hash"].(string)
	secondHash, _ := second["schema_hash"].(string)
	if firstHash == "" {
		t.Fatalf("the first connection holds no schema, so this criterion proves nothing: %v", first)
	}
	if firstHash != secondHash {
		t.Errorf("two connections on one catalog hold different schemas: %q and %q", firstHash, secondHash)
	}
}

// TestIssue1745_ListConnectionsReportsTheCatalogAndItsSurface holds the
// inventory half a caller sees: a GraphQL endpoint's surface is reported the
// way a REST one's is, without calling graphql_discover against it.
func TestIssue1745_ListConnectionsReportsTheCatalogAndItsSurface(t *testing.T) {
	// Registered on one replica and listed through the other, which is what
	// #1757 made answerable: the enumeration reports the connections the
	// store holds, so the catalog and its operation count are facts about
	// the connection rather than about the replica that answered.
	c, listing := connectReplicaPair(t)
	catalogID := issue1745Catalog(t, c, "listed")
	name := issue1745Register(t, c, "listed", catalogID)

	out := listing.call("list_connections", map[string]any{"purpose": issue1745Purpose})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("re-marshalling list_connections: %v", err)
	}
	var listed struct {
		Connections []struct {
			Name           string `json:"name"`
			CatalogID      string `json:"catalog_id"`
			OperationCount int    `json:"operation_count"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decoding list_connections: %v\n%s", err, raw)
	}
	for _, conn := range listed.Connections {
		if conn.Name != name {
			continue
		}
		if conn.CatalogID != catalogID {
			t.Errorf("list_connections reports catalog_id %q for %s; want %q", conn.CatalogID, name, catalogID)
		}
		if conn.OperationCount != 2 {
			t.Errorf("list_connections reports %d operations for %s; want the catalog's two", conn.OperationCount, name)
		}
		return
	}
	t.Fatalf("list_connections does not report %s at all: %s", name, raw)
}

// TestIssue1745_AnEditToTheCatalogReachesTheConnectionsServingIt is what a
// catalogued schema buys over a pasted one: the edit is made in one place and
// every connection on it picks the change up.
func TestIssue1745_AnEditToTheCatalogReachesTheConnectionsServingIt(t *testing.T) {
	c := connect(t)
	catalogID := issue1745Catalog(t, c, "edited")
	name := issue1745Register(t, c, "edited", catalogID)
	before, _ := issue1745Schema(t, c, name)["schema_hash"].(string)
	if before == "" {
		t.Fatalf("the connection holds no schema, so this criterion proves nothing")
	}

	issue1745PutSchema(t, c, catalogID, issue1745SDL+"\ntype Extra { id: ID! }\n")

	state := issue1745Schema(t, c, name)
	after, _ := state["schema_hash"].(string)
	if after == before {
		t.Errorf("an edit to the catalog did not reach the connection serving it: %v", state)
	}
}

// TestIssue1745_AnAPIConnectionKeepsItsOpenAPISpecsBesideASchema holds the
// mixed catalog: a GraphQL spec is SDL, which no OpenAPI reader parses, so the
// api gateway must recognize it by format rather than fail on it.
func TestIssue1745_AnAPIConnectionKeepsItsOpenAPISpecsBesideASchema(t *testing.T) {
	c := connect(t)
	catalogID := issue1746Catalog(t, c)
	issue1745PutSchema(t, c, catalogID, issue1745SDL)

	name := fmt.Sprintf("acc-1745-mixed-%d", time.Now().UnixNano())
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config":      fixtureConnectionConfig(name, catalogID),
		"description": "Acceptance 1745: an api connection on a catalog that also holds a schema.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering the api connection: HTTP %d", status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})

	out := c.call("api_discover", map[string]any{
		"connection": name,
		"query":      "identity",
		"purpose":    issue1745Purpose,
	})
	ops, _ := out["operations"].([]any)
	if len(ops) == 0 {
		t.Fatalf("a catalog holding a GraphQL schema cost the api connection its OpenAPI operations: %v", out)
	}
}
