//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #1277: the graphql connection kind. Every criterion here runs
// through the real MCP surface against a real GraphQL server the repository
// runs itself: DataHub's GMS, at /api/graphql, which the platform already
// integrates with. No fixture stands in for the upstream.
//
// Namespace descent is proved by the hand-authored namespaced schema in the
// unit suite (internal/gqlschema/testdata), not here: DataHub's schema is
// flat, and no vendor's schema is committed to this repository.
//
// Wire forms: graphql_query's `variables` is typed ["object", "string"] and
// admits the object itself and a string holding that object's JSON. Both are
// sent as literal tools/call params and asserted to produce the same answer.
// `paginate` is an object only; `query`, `operation_name` and `connection`
// are strings only.

const (
	issue1277DiscoverTool = "graphql_discover"
	issue1277QueryTool    = "graphql_query"
	issue1277Purpose      = "Acceptance for #1277: the graphql connection kind reaches a real GraphQL endpoint through the platform."
)

// issue1277Endpoint is the GraphQL endpoint the suite runs against: the
// DataHub GMS the repository's own e2e environment brings up. Overridable so
// the suite can be pointed at another deployment's DataHub.
func issue1277Endpoint() string {
	if v := os.Getenv("DATAHUB_GRAPHQL_URL"); v != "" {
		return v
	}
	return "http://localhost:8085/api/graphql"
}

// requireGraphQLUpstream fails, rather than skipping, when no GraphQL server
// answers: a gate that skips is a gate that was not run.
func requireGraphQLUpstream(t *testing.T) {
	t.Helper()
	body := strings.NewReader(`{"query":"{ __typename }"}`)
	req, err := http.NewRequest(http.MethodPost, issue1277Endpoint(), body)
	if err != nil {
		t.Fatalf("building the probe: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("no GraphQL server answers at %s (%v). Start DataHub: `datahub docker quickstart`, or set DATAHUB_GRAPHQL_URL",
			issue1277Endpoint(), err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s answered HTTP %d to a trivial document", issue1277Endpoint(), res.StatusCode)
	}
}

// issue1277Connect registers one graphql connection and returns its name.
// Registration is what triggers the introspection, so a connection that
// comes back from this has a schema.
func issue1277Connect(t *testing.T, c *client, label string, extra map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-1277-%s-%d", label, time.Now().UnixNano())
	cfg := map[string]any{
		"endpoint_url":    issue1277Endpoint(),
		"connection_name": name,
		"connect_timeout": "10s",
		"call_timeout":    "30s",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+name, map[string]any{
		"config":      cfg,
		"description": "Acceptance 1277: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register the %s connection: HTTP %d", label, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/graphql/"+name, http.NoBody)
	})
	return name
}

// issue1277Discover calls graphql_discover.
func issue1277Discover(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	args["purpose"] = issue1277Purpose
	return c.call(issue1277DiscoverTool, args)
}

// issue1277Refuse calls a tool expecting a refusal and returns its text.
func issue1277Refuse(t *testing.T, c *client, tool string, args map[string]any) string {
	t.Helper()
	args["purpose"] = issue1277Purpose
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s: transport error: %v", tool, err)
	}
	if !res.IsError {
		t.Fatalf("%s was not refused: %s", tool, text)
	}
	return text
}

// issue1277Operations returns the operation ids graphql_discover lists.
func issue1277Operations(t *testing.T, out map[string]any) []string {
	t.Helper()
	rows, _ := out["operations"].([]any)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		op, _ := row.(map[string]any)
		if id, _ := op["operation_id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestIssue1277_ConnectionIntrospectsOnRegisterAndListsItsOperations is the
// first criterion: a connection registered through the admin API introspects
// its endpoint, and graphql_discover lists what that schema exposes.
func TestIssue1277_ConnectionIntrospectsOnRegisterAndListsItsOperations(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "discover", nil)

	status, schema := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the schema state: HTTP %d %v", status, schema)
	}
	if cause, _ := schema["error"].(string); cause != "" {
		t.Fatalf("the connection holds no schema: %s", cause)
	}
	if source, _ := schema["source"].(string); source != "introspection" {
		t.Errorf("source = %q; registration introspects the endpoint", source)
	}
	count, _ := schema["operation_count"].(float64)
	if count < 10 {
		t.Fatalf("operation_count = %v; a metadata service's schema exposes more than that", count)
	}
	if hash, _ := schema["schema_hash"].(string); hash == "" {
		t.Error("the stored schema is not identified by a hash")
	}

	out := issue1277Discover(t, c, map[string]any{"connection": name, "limit": 200})
	if level, _ := out["level"].(string); level != "operations" {
		t.Fatalf("level = %q", level)
	}
	ids := issue1277Operations(t, out)
	if len(ids) == 0 {
		t.Fatalf("no operations listed: %v", out)
	}
	var queries, mutations int
	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, "query:"):
			queries++
		case strings.HasPrefix(id, "mutation:"):
			mutations++
		default:
			t.Errorf("%q carries no kind prefix", id)
		}
	}
	if queries == 0 || mutations == 0 {
		t.Errorf("listed %d queries and %d mutations; the schema has both", queries, mutations)
	}
	if out["schema_hash"] == nil || out["schema_fetched_at"] == nil {
		t.Error("the answer does not say which schema version produced it")
	}
}

// TestIssue1277_SearchSurfacesTheConnectionsOperations proves the operations
// reach the universal search tool's endpoints group, which is what an agent
// that does not yet know the connection exists finds them through.
func TestIssue1277_SearchSurfacesTheConnectionsOperations(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "search", nil)

	out := c.call("search", map[string]any{
		"intent": "dataset", "sources": []any{"endpoints"}, "limit": 50,
		"purpose": issue1277Purpose,
	})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal search result: %v", err)
	}
	if !strings.Contains(string(raw), name) {
		t.Fatalf("the graphql connection's operations are absent from the endpoints group:\n%s", raw)
	}
}

// TestIssue1277_DiscoverRendersASkeletonThatRuns is the criterion the
// skeleton exists for: what graphql_discover hands back validates against the
// stored schema and executes against the endpoint on the first try.
func TestIssue1277_DiscoverRendersASkeletonThatRuns(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "skeleton", nil)

	out := issue1277Discover(t, c, map[string]any{"connection": name, "operation_id": issue1277ReadOperation(t, c, name)})
	if level, _ := out["level"].(string); level != "operation" {
		t.Fatalf("level = %q: %v", level, out)
	}
	op, _ := out["operation"].(map[string]any)
	if op == nil {
		t.Fatalf("no operation returned: %v", out)
	}
	skeleton, _ := op["skeleton"].(string)
	if skeleton == "" {
		t.Fatal("no skeleton")
	}
	if shape, _ := op["return_shape"].([]any); len(shape) == 0 {
		t.Error("no return shape")
	}
	variables, _ := op["variables"].(string)
	var stub map[string]any
	if err := json.Unmarshal([]byte(variables), &stub); err != nil {
		t.Fatalf("the variables stub is not a JSON object: %v\n%s", err, variables)
	}

	// The skeleton runs. Its stub carries placeholders rather than real
	// values, so the endpoint may answer with an empty result or with its
	// own errors; what this asserts is that the platform sent it, which
	// means it parsed, validated and passed the depth cap.
	result := c.call(issue1277QueryTool, map[string]any{
		"connection": name, "query": skeleton, "variables": stub,
		"purpose": issue1277Purpose,
	})
	if status, _ := result["status"].(float64); status != http.StatusOK {
		t.Errorf("the skeleton did not reach the endpoint: status %v", status)
	}
}

// issue1277ReadOperation picks an operation that reads one entity by
// identifier, which every metadata service exposes and which a skeleton can
// be run with placeholder arguments. Chosen from what the connection
// actually lists rather than hard-coded, so the suite does not pin a
// vendor's schema.
func issue1277ReadOperation(t *testing.T, c *client, connection string) string {
	t.Helper()
	out := issue1277Discover(t, c, map[string]any{"connection": connection, "query": "dataset", "limit": 20})
	for _, id := range issue1277Operations(t, out) {
		if id == "query:dataset" {
			return id
		}
	}
	t.Fatalf("the connection lists no query:dataset operation: %v", issue1277Operations(t, out))
	return ""
}

// TestIssue1277_AValidDocumentReturnsData runs a document written by hand
// against the endpoint and reads its answer.
func TestIssue1277_AValidDocumentReturnsData(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "valid", nil)

	out := c.call(issue1277QueryTool, map[string]any{
		"connection": name,
		"query":      `query Health { appConfig { appVersion } }`,
		"purpose":    issue1277Purpose,
	})
	if out["upstream_error"] == true {
		t.Fatalf("the endpoint refused a valid document: %v", out["errors"])
	}
	if kind, _ := out["kind"].(string); kind != "QUERY" {
		t.Errorf("kind = %q", kind)
	}
	if opName, _ := out["operation_name"].(string); opName != "Health" {
		t.Errorf("operation_name = %q", opName)
	}
	data, _ := out["data"].(map[string]any)
	if data == nil {
		t.Fatalf("no data: %v", out)
	}
	if _, ok := data["appConfig"]; !ok {
		t.Errorf("data = %v; want the field the document selected", data)
	}
	ops, _ := out["operations"].([]any)
	if len(ops) != 1 || ops[0] != "appConfig" {
		t.Errorf("operations = %v; want the operation the document invoked", ops)
	}
}

// TestIssue1277_VariablesReachTheEndpointInEveryFormTheSchemaAdmits is the
// wire-form criterion. The schema types `variables` as ["object", "string"],
// so both must produce the same answer: a client that stringifies structured
// arguments sends the second.
func TestIssue1277_VariablesReachTheEndpointInEveryFormTheSchemaAdmits(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "wireforms", nil)

	const document = `query Read($urn: String!) { dataset(urn: $urn) { urn } }`
	const urn = "urn:li:dataset:(urn:li:dataPlatform:acceptance,issue1277,PROD)"

	asObject := c.call(issue1277QueryTool, map[string]any{
		"connection": name, "query": document,
		"variables": map[string]any{"urn": urn},
		"purpose":   issue1277Purpose,
	})
	asString := c.call(issue1277QueryTool, map[string]any{
		"connection": name, "query": document,
		"variables": `{"urn": "` + urn + `"}`,
		"purpose":   issue1277Purpose,
	})

	objectData, _ := json.Marshal(asObject["data"])
	stringData, _ := json.Marshal(asString["data"])
	if string(objectData) != string(stringData) {
		t.Fatalf("the two forms produced different answers:\n object: %s\n string: %s", objectData, stringData)
	}
	if asObject["upstream_error"] != asString["upstream_error"] {
		t.Errorf("the two forms were classified differently: %v vs %v",
			asObject["upstream_error"], asString["upstream_error"])
	}
	// A variable that did not reach the endpoint would be a null-argument
	// error rather than a lookup that found nothing.
	if asObject["upstream_error"] == true {
		t.Fatalf("the object form was refused: %v", asObject["errors"])
	}
}

// TestIssue1277_StrictValidationRefusesAnUnknownFieldAndWarnPassesItThrough
// covers both halves of schema_validation on the same document.
func TestIssue1277_StrictValidationRefusesAnUnknownFieldAndWarnPassesItThrough(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	strict := issue1277Connect(t, c, "strict", nil)
	warn := issue1277Connect(t, c, "warn", map[string]any{"schema_validation": "warn"})

	const document = `{ appConfig { noSuchFieldOnAppConfig } }`

	refusal := issue1277Refuse(t, c, issue1277QueryTool, map[string]any{
		"connection": strict, "query": document,
	})
	if !strings.Contains(refusal, strict) {
		t.Errorf("the refusal does not name the connection: %s", refusal)
	}
	if !strings.Contains(refusal, "noSuchFieldOnAppConfig") {
		t.Errorf("the refusal does not name the field: %s", refusal)
	}
	if !strings.Contains(refusal, "graphql_discover") {
		t.Errorf("the refusal does not say where the schema is: %s", refusal)
	}

	out := c.call(issue1277QueryTool, map[string]any{
		"connection": warn, "query": document, "purpose": issue1277Purpose,
	})
	warnings, _ := out["validation_warnings"].([]any)
	if len(warnings) == 0 {
		t.Errorf("warn mode reported no violations: %v", out)
	}
	if status, _ := out["status"].(float64); status == 0 {
		t.Errorf("warn mode did not send the document: %v", out)
	}
}

// TestIssue1277_ADocumentIsRefusedBeforeItIsSent covers the four refusals a
// caller can hit without the endpoint being involved at all.
func TestIssue1277_ADocumentIsRefusedBeforeItIsSent(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "refusals", map[string]any{"max_query_depth": 2})

	cases := []struct{ name, document, want string }{
		{
			name:     "two operations and no name",
			document: `query A { appConfig { appVersion } } query B { appConfig { appVersion } }`,
			want:     "operation_name is required",
		},
		{
			name:     "deeper than the connection allows",
			document: `{ appConfig { authConfig { tokenAuthEnabled } } }`,
			want:     "max_query_depth",
		},
		{
			name:     "reading the schema rather than the data",
			document: `{ __schema { types { name } } }`,
			want:     "graphql_discover",
		},
		{
			name:     "a subscription",
			document: `subscription S { anything }`,
			want:     "subscriptions are not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refusal := issue1277Refuse(t, c, issue1277QueryTool, map[string]any{
				"connection": name, "query": tc.document,
			})
			if !strings.Contains(refusal, tc.want) {
				t.Errorf("refusal = %q; want it to say %q", refusal, tc.want)
			}
		})
	}
}

// TestIssue1277_AnErrorsArrayInAnHTTP200IsAnUpstreamError is the
// classification criterion: a GraphQL failure arrives inside a 200, and the
// platform records it as a failure with the partial data preserved.
func TestIssue1277_AnErrorsArrayInAnHTTP200IsAnUpstreamError(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "errors", map[string]any{"schema_validation": "warn"})

	// A document the schema admits but the resolver refuses: an
	// unparseable URN. warn mode is what lets it reach the endpoint.
	out := c.call(issue1277QueryTool, map[string]any{
		"connection": name,
		"query":      `query Bad { dataset(urn: "not-a-urn") { urn } }`,
		"purpose":    issue1277Purpose,
	})
	if status, _ := out["status"].(float64); status != http.StatusOK {
		t.Fatalf("status = %v; this criterion is about a failure inside a 200", status)
	}
	if out["upstream_error"] != true {
		t.Fatalf("a 200 carrying errors was recorded as a success: %v", out)
	}
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("the upstream's errors were not passed through: %v", out)
	}
	first, _ := errs[0].(map[string]any)
	if msg, _ := first["message"].(string); msg == "" {
		t.Errorf("the upstream's own message was dropped: %v", first)
	}
	if _, ok := out["data"]; !ok {
		t.Error("partial data was discarded")
	}

	// The call catalog records it as a graphql call that failed, which is
	// the reason classification happens on the body at all.
	record := issue1277WaitForCallRecord(t, c, name)
	if record == nil {
		t.Fatal("the call was not cataloged")
	}
	if kind, _ := record["kind"].(string); kind != "graphql" {
		t.Errorf("kind = %q", kind)
	}
	if method, _ := record["method"].(string); method != "QUERY" {
		t.Errorf("method = %q; the operation kind is what says the call only read", method)
	}
	if statement, _ := record["statement"].(string); !strings.Contains(statement, "dataset") {
		t.Errorf("statement = %q; the document is what a reader re-runs", statement)
	}
}

// issue1277WaitForCallRecord polls the call catalog for this connection's
// record. The audit pipeline writes it on the drain goroutine, so the record
// arrives shortly after the call rather than with it.
func issue1277WaitForCallRecord(t *testing.T, c *client, connection string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		status, body := c.rest(http.MethodGet, "/api/v1/admin/calls?connection="+connection+"&per_page=25", http.NoBody)
		if status == http.StatusOK {
			records, _ := body["data"].([]any)
			for _, r := range records {
				record, _ := r.(map[string]any)
				if conn, _ := record["connection"].(string); conn == connection {
					return record
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

// TestIssue1277_PaginateWalksTheEndpointsPagesIntoOneAnswer proves the walk
// against the upstream's own paging. DataHub's scroll API carries a bare next
// cursor rather than a Relay pageInfo, which is what next_cursor_path is for.
func TestIssue1277_PaginateWalksTheEndpointsPagesIntoOneAnswer(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "paginate", nil)

	const document = `query Scroll($scrollId: String) {
	  scrollAcrossEntities(input: {query: "*", count: 1, scrollId: $scrollId}) {
	    nextScrollId
	    searchResults { entity { urn } }
	  }
	}`

	out := c.call(issue1277QueryTool, map[string]any{
		"connection": name, "query": document,
		"paginate": map[string]any{
			"items":            "scrollAcrossEntities.searchResults",
			"cursor_variable":  "scrollId",
			"next_cursor_path": "scrollAcrossEntities.nextScrollId",
			"max_pages":        3,
		},
		"purpose": issue1277Purpose,
	})
	if out["upstream_error"] == true {
		t.Fatalf("the walk's first page was refused: %v", out["errors"])
	}
	pagination, _ := out["pagination"].(map[string]any)
	if pagination == nil {
		t.Fatalf("no pagination report: %v", out)
	}
	pages, _ := pagination["pages_fetched"].(float64)
	merged, _ := pagination["items_merged"].(float64)
	if pages < 2 {
		t.Fatalf("pages_fetched = %v; a page size of 1 over a seeded catalog walks more than one", pages)
	}
	if merged < pages {
		t.Errorf("items_merged = %v over %v pages; each page carried one row", merged, pages)
	}
	if stopped, _ := pagination["stopped_by"].(string); stopped != "end" && stopped != "max_pages" {
		t.Errorf("stopped_by = %q", stopped)
	}
	// The merged rows replace the first page's array in place, so the
	// caller reads one page's shape holding every row.
	data, _ := out["data"].(map[string]any)
	scroll, _ := data["scrollAcrossEntities"].(map[string]any)
	results, _ := scroll["searchResults"].([]any)
	if float64(len(results)) != merged {
		t.Errorf("the merged array holds %d rows; the report says %v", len(results), merged)
	}
}

// TestIssue1277_AReadOnlyConnectionRefusesMutationsForEveryPersona covers the
// connection-level rule, which needs no persona at all.
func TestIssue1277_AReadOnlyConnectionRefusesMutationsForEveryPersona(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "readonly", map[string]any{"read_only": true})

	refusal := issue1277Refuse(t, c, issue1277QueryTool, map[string]any{
		"connection": name,
		"query":      `mutation { updateDescription(input: {resourceUrn: "urn:li:dataset:(urn:li:dataPlatform:x,y,PROD)", description: "x"}) }`,
	})
	if !strings.Contains(refusal, "read_only") {
		t.Errorf("refusal = %q", refusal)
	}
	// A query on the same connection still runs.
	c.call(issue1277QueryTool, map[string]any{
		"connection": name, "query": `{ appConfig { appVersion } }`, "purpose": issue1277Purpose,
	})
}

// TestIssue1277_APersonaThatDeniesMutationsCannotRunOrSeeThem is the
// persona-level criterion. The rules name the METHOD and no paths: a path
// glob would not do it, because `*` does not cross a separator and `**` is
// not recursive.
func TestIssue1277_APersonaThatDeniesMutationsCannotRunOrSeeThem(t *testing.T) {
	requireGraphQLUpstream(t)
	admin := connect(t)
	name := issue1277Connect(t, admin, "persona", nil)

	restore := issue1277DenyMutations(t, admin, "collaborator", name)
	t.Cleanup(restore)

	// The person the rule is about runs the checks, not the administrator.
	person := connectAs(t, devOwnerAPIKey)

	refusal := issue1277Refuse(t, person, issue1277QueryTool, map[string]any{
		"connection": name,
		"query":      `mutation { updateDescription(input: {resourceUrn: "urn:li:dataset:(urn:li:dataPlatform:x,y,PROD)", description: "x"}) }`,
	})
	if !strings.Contains(refusal, "MUTATION") {
		t.Errorf("the refusal does not name what was denied: %s", refusal)
	}

	// And a denied operation is absent from discovery rather than refused
	// by it: a discovery surface is not a map of what the caller cannot do.
	out := issue1277Discover(t, person, map[string]any{"connection": name, "limit": 200})
	for _, id := range issue1277Operations(t, out) {
		if strings.HasPrefix(id, "mutation:") {
			t.Errorf("%s is listed to a persona that cannot run it", id)
		}
	}
	if len(issue1277Operations(t, out)) == 0 {
		t.Error("every operation was hidden; only the mutations were denied")
	}

	// And absent from the federated search too.
	search := person.call("search", map[string]any{
		"intent": "update description", "sources": []any{"endpoints"}, "limit": 50,
		"purpose": issue1277Purpose,
	})
	raw, err := json.Marshal(search)
	if err != nil {
		t.Fatalf("marshal search result: %v", err)
	}
	if strings.Contains(string(raw), `"mutation:`) {
		t.Errorf("a mutation reached a federated search for a persona that cannot run it:\n%s", raw)
	}

	// A mixed document is refused when any one of its operations is denied,
	// rather than being sent with the denied part stripped.
	mixed := issue1277Refuse(t, person, issue1277QueryTool, map[string]any{
		"connection": name,
		"query": `mutation Mixed {
		  updateDescription(input: {resourceUrn: "urn:li:dataset:(urn:li:dataPlatform:x,y,PROD)", description: "x"})
		}`,
	})
	if mixed == "" {
		t.Error("want a refusal")
	}
}

// issue1277DenyMutations makes a persona read-only on one connection and
// returns the restore function. The rules name only this suite's
// connection, so nothing else the persona reaches changes while they are in
// force.
func issue1277DenyMutations(t *testing.T, c *client, personaName, connection string) func() {
	t.Helper()
	status, before := c.rest(http.MethodGet, "/api/v1/admin/personas/"+personaName, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading persona %s: HTTP %d %v", personaName, status, before)
	}
	body := issue1277PersonaRequest(before)
	original := issue1277PersonaRequest(before)
	// One allow rule naming the QUERY method is the read-only persona.
	// Rules are a narrowing: once any of them names the connection, a
	// matching allow is required, so a mutation is denied by matching
	// none. The explicit deny beside it states the intent and is what a
	// refusal names.
	body["api_routes"] = []any{
		map[string]any{"connection": connection, "methods": []any{"QUERY"}},
		map[string]any{"connection": connection, "methods": []any{"MUTATION"}, "action": "deny"},
	}
	if code := c.restJSON(http.MethodPut, "/api/v1/admin/personas/"+personaName, body); code != http.StatusOK {
		t.Fatalf("adding the deny rule: HTTP %d", code)
	}
	return func() {
		c.restJSON(http.MethodPut, "/api/v1/admin/personas/"+personaName, original)
	}
}

// issue1277PersonaRequest projects a persona as the admin API reports it onto
// the shape its update route takes.
func issue1277PersonaRequest(p map[string]any) map[string]any {
	out := map[string]any{
		"name":         p["name"],
		"display_name": p["display_name"],
		"description":  p["description"],
		"roles":        p["roles"],
		"allow_tools":  p["allow_tools"],
		"deny_tools":   p["deny_tools"],
		"priority":     p["priority"],
	}
	if v, ok := p["allow_connections"]; ok {
		out["allow_connections"] = v
	}
	if v, ok := p["deny_connections"]; ok {
		out["deny_connections"] = v
	}
	if v, ok := p["api_routes"]; ok {
		out["api_routes"] = v
	}
	for _, key := range []string{"description_prefix", "description_override", "agent_instructions_suffix", "agent_instructions_override"} {
		if v, ok := p[key]; ok {
			out[key] = v
		}
	}
	if out["display_name"] == nil || out["display_name"] == "" {
		out["display_name"] = out["name"]
	}
	return out
}

// TestIssue1277_AnOperatorSuppliesASchemaWhenTheEndpointWillNotAnswer covers
// the upload path, which is what an endpoint with introspection disabled
// leaves an operator.
func TestIssue1277_AnOperatorSuppliesASchemaWhenTheEndpointWillNotAnswer(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "upload", nil)

	const sdl = `
	schema { query: Query }
	type Query {
	  "Read one thing."
	  thing(id: ID!): Thing
	}
	type Thing { id: ID!, label: String }
	`
	status, _ := c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema",
		strings.NewReader(sdl))
	if status != http.StatusOK {
		t.Fatalf("uploading a schema: HTTP %d", status)
	}

	_, schema := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
	if source, _ := schema["source"].(string); source != "upload" {
		t.Errorf("source = %q; want upload", source)
	}
	if count, _ := schema["operation_count"].(float64); count != 1 {
		t.Errorf("operation_count = %v; the uploaded schema exposes one operation", count)
	}

	out := issue1277Discover(t, c, map[string]any{"connection": name})
	if ids := issue1277Operations(t, out); len(ids) != 1 || ids[0] != "query:thing" {
		t.Errorf("operations = %v; discovery serves the uploaded schema", ids)
	}

	// A malformed upload is the operator's input, and is refused as one.
	code, _ := c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema",
		strings.NewReader("type Query {"))
	if code != http.StatusBadRequest {
		t.Errorf("a malformed schema was answered with HTTP %d", code)
	}
}

// TestIssue1277_ARefreshRereadsTheEndpoint puts the connection back on its own
// schema after the upload path replaced it.
func TestIssue1277_ARefreshRereadsTheEndpoint(t *testing.T) {
	requireGraphQLUpstream(t)
	c := connect(t)
	name := issue1277Connect(t, c, "refresh", nil)

	_, before := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/graphql/"+name+"/schema", http.NoBody)
	beforeHash, _ := before["schema_hash"].(string)

	status, after := c.rest(http.MethodPost,
		"/api/v1/admin/connection-instances/graphql/"+name+"/refresh-schema", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("refreshing: HTTP %d %v", status, after)
	}
	if hash, _ := after["schema_hash"].(string); hash != beforeHash {
		t.Errorf("the endpoint's schema changed between two reads: %q then %q", beforeHash, hash)
	}
	if source, _ := after["source"].(string); source != "introspection" {
		t.Errorf("source = %q", source)
	}
}
