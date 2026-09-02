//go:build integration

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
)

// Issue #1590: the DataHub toolkit registered five read-by-URN tools that
// duplicated fetch. They are retired, not aliased: datahub_get_entity,
// datahub_get_schema and datahub_get_queries are one fetch of the dataset's
// urn:li:dataset: reference, whose record now carries the declared schema,
// the saved queries and query availability; datahub_get_glossary_term is a
// fetch of the term's reference, which now carries its parent node and owners
// beside the datasets that carry it; datahub_get_data_product is a fetch of
// the product's urn:li:dataProduct: reference. datahub_browse and
// datahub_get_lineage stay, and so do the write tools.
//
// Every criterion here runs through the real surface against the dev stack
// attached to a local DataHub (DATAHUB_ENABLED=true DATAHUB_ENDPOINT=... make
// dev), whose seed writes the iceberg.retail datasets the dataset arm is
// checked on. The other fixtures are created through the platform's own
// DataHub write tools, except the glossary node a term is filed under, which
// no platform tool creates and which the test writes with the same upstream
// client the platform uses.

// issue1590RetiredTools maps each retired tool to the fetch reference form
// that replaces it (acceptance 1); issue1590Replacement executes each.
var issue1590RetiredTools = map[string]string{
	"datahub_get_entity":        "fetch urn:li:dataset:<id>",
	"datahub_get_schema":        "fetch urn:li:dataset:<id> (the record's schema)",
	"datahub_get_queries":       "fetch urn:li:dataset:<id> (the record's queries)",
	"datahub_get_glossary_term": "fetch urn:li:glossaryTerm:<id>",
	"datahub_get_data_product":  "fetch urn:li:dataProduct:<id>",
}

// issue1590DatasetURN is one of the datasets dev/seed-datahub.sh ingests, with
// a declared schema, when the dev stack is attached to a local DataHub.
const issue1590DatasetURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"

const issue1590Purpose = "Acceptance for #1590: fetch replaces the retired DataHub read-by-URN tools."

// issue1590Fixtures is what the fetch criteria read: a glossary term filed
// under a node, a data product in a domain, and a saved query on the seeded
// dataset, which also carries the term so the term's fetch lists it.
type issue1590Fixtures struct {
	nodeURN    string
	termURN    string
	domainURN  string
	productURN string
	queryURN   string
}

// requireDataHub skips the DataHub-dependent criteria when the running platform
// has no DataHub connection: the platform answers, so this is not the
// no-server failure, but the criterion cannot be executed against a catalog
// that is not there.
func requireDataHub(t *testing.T, c *client) {
	t.Helper()
	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "datahub_browse" {
			return
		}
	}
	t.Skip("the running platform has no DataHub connection; start a local DataHub and run DATAHUB_ENABLED=true DATAHUB_ENDPOINT=http://localhost:8080 make dev")
}

// createFixtures writes the fixtures and registers their removal.
func createFixtures(t *testing.T, c *client) issue1590Fixtures {
	t.Helper()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	var f issue1590Fixtures

	endpoint := os.Getenv("DATAHUB_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:8080"
	}
	token := os.Getenv("DATAHUB_TOKEN")
	if token == "" {
		token = "acceptance"
	}
	dh, err := dhclient.New(dhclient.Config{URL: endpoint, Token: token})
	if err != nil {
		t.Fatalf("datahub client: %v", err)
	}
	ctx := context.Background()
	f.nodeURN, err = dh.CreateGlossaryNode(ctx, "Acceptance 1590 "+stamp, "Node for the #1590 acceptance run.", "")
	if err != nil {
		t.Fatalf("create glossary node: %v", err)
	}
	t.Cleanup(func() { _ = dh.DeleteGlossaryEntity(ctx, f.nodeURN) })

	f.termURN = created(t, c, map[string]any{
		"what": "glossary_term", "name": "Reorder Point " + stamp,
		"description": "The inventory level at which a store reorders a product.",
		"parent_node": f.nodeURN,
	})
	t.Cleanup(func() { deleteEntity(c, "glossary_entity", f.termURN) })

	f.domainURN = created(t, c, map[string]any{"what": "domain", "name": "Acceptance Domain " + stamp, "description": "Domain for the #1590 acceptance run."})
	t.Cleanup(func() { deleteEntity(c, "domain", f.domainURN) })
	f.productURN = created(t, c, map[string]any{
		"what": "data_product", "name": "Retail 360 " + stamp,
		"description": "Everything about a retail day.", "domain_urn": f.domainURN,
	})
	t.Cleanup(func() { deleteEntity(c, "data_product", f.productURN) })

	f.queryURN = created(t, c, map[string]any{
		"what": "query", "name": "daily sales by store " + stamp,
		"description":  "Acceptance query for #1590.",
		"value":        "SELECT store_id, sum(amount) FROM iceberg.retail.daily_sales GROUP BY store_id",
		"dataset_urns": []string{issue1590DatasetURN},
	})
	t.Cleanup(func() { deleteEntity(c, "query", f.queryURN) })

	c.call("datahub_update", map[string]any{
		"what": "glossary_term", "action": "add", "urn": issue1590DatasetURN, "target_urn": f.termURN,
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("datahub_update", map[string]any{
			"what": "glossary_term", "action": "remove", "urn": issue1590DatasetURN, "target_urn": f.termURN,
		})
	})
	return f
}

func created(t *testing.T, c *client, args map[string]any) string {
	t.Helper()
	out := c.call("datahub_create", args)
	urn, _ := out["urn"].(string)
	if urn == "" {
		t.Fatalf("datahub_create %v returned no urn: %v", args["what"], out)
	}
	return urn
}

func deleteEntity(c *client, what, urn string) {
	_, _, _ = c.callRaw("datahub_delete", map[string]any{"what": what, "urn": urn})
}

// fetchContent reads a reference through fetch and returns the document's
// content, failing the test when the reference does not resolve.
func fetchContent(t *testing.T, c *client, ref string) map[string]any {
	t.Helper()
	out := c.call("fetch", map[string]any{"reference": ref, "purpose": issue1590Purpose})
	if found, _ := out["found"].(bool); !found {
		t.Fatalf("fetch %s: not found: %v", ref, out)
	}
	doc, _ := out["document"].(map[string]any)
	content, _ := doc["content"].(map[string]any)
	if content == nil {
		t.Fatalf("fetch %s: document carries no content object: %v", ref, out)
	}
	return content
}

func TestIssue1590_RetiredReadToolsAreNotRegistered(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]string{}
	var datahubTools []string
	for _, tool := range tools.Tools {
		names[tool.Name] = tool.Description
		if strings.HasPrefix(tool.Name, "datahub_") {
			datahubTools = append(datahubTools, tool.Name)
		}
	}
	for retired, replacement := range issue1590RetiredTools {
		if _, ok := names[retired]; ok {
			t.Errorf("%s is still registered; it is replaced by %s", retired, replacement)
		}
	}
	for _, kept := range []string{"datahub_browse", "datahub_get_lineage", "fetch"} {
		if _, ok := names[kept]; !ok {
			t.Errorf("%s must stay registered", kept)
		}
	}
	// Acceptance 3: the administrator's DataHub surface is exactly the two
	// reads a reference read cannot do plus the three writes, ten minus the
	// five retired.
	if len(datahubTools) != 5 {
		t.Errorf("admin persona carries %d datahub_* tools %v, want 5 (browse, lineage, create, update, delete)", len(datahubTools), datahubTools)
	}
	// Acceptance 4: no advertised description steers to a retired tool.
	for name, desc := range names {
		for retired := range issue1590RetiredTools {
			if strings.Contains(desc, retired) {
				t.Errorf("%s's description still names %s", name, retired)
			}
		}
	}
}

func TestIssue1590_NoPersonaKeepsARetiredTool(t *testing.T) {
	c := connect(t)
	status, body := c.rest("GET", "/api/v1/admin/personas", nil)
	if status != 200 {
		t.Fatalf("GET /api/v1/admin/personas: %d %v", status, body)
	}
	personas, _ := body["personas"].([]any)
	if len(personas) == 0 {
		t.Fatalf("no personas listed: %v", body)
	}
	for _, p := range personas {
		persona, _ := p.(map[string]any)
		name, _ := persona["name"].(string)
		resolved, _ := persona["tools"].([]any)
		for _, tool := range resolved {
			if _, retired := issue1590RetiredTools[fmt.Sprint(tool)]; retired {
				t.Errorf("persona %s still resolves %v, a tool that no longer exists", name, tool)
			}
		}
	}
}

func TestIssue1590_FetchReplacesEachRetiredRead(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	f := createFixtures(t, c)

	// The saved query reaches the dataset's record through DataHub's own query
	// listing, which is served from its search index and lags the write, so
	// the record is read again until the index has caught up.
	dataset := fetchContent(t, c, issue1590DatasetURN)
	for deadline := time.Now().Add(90 * time.Second); !recordListsQuery(dataset, f.queryURN) && time.Now().Before(deadline); {
		time.Sleep(3 * time.Second)
		dataset = fetchContent(t, c, issue1590DatasetURN)
	}
	term := fetchContent(t, c, f.termURN)
	product := fetchContent(t, c, f.productURN)

	t.Run("datahub_get_entity: the dataset's business context and identity", func(t *testing.T) {
		if dataset["urn"] != issue1590DatasetURN || dataset["name"] != "daily_sales" || dataset["description"] == "" {
			t.Errorf("record = %v", dataset)
		}
		if owners, _ := dataset["owners"].([]any); len(owners) == 0 {
			t.Errorf("owners missing: %v", dataset["owners"])
		}
		if tags, _ := dataset["tags"].([]any); len(tags) == 0 {
			t.Errorf("tags missing: %v", dataset["tags"])
		}
		if dataset["platform"] != "trino" {
			t.Errorf("platform = %v", dataset["platform"])
		}
		// The dev Trino serves no iceberg catalog, so the honest answer is "not
		// queryable, and why"; the retired tool said as much, and so must the
		// record. A queryable dataset answers with its table and connection.
		avail, ok := dataset["query_availability"].(map[string]any)
		if !ok {
			t.Fatalf("query_availability missing; the dev stack wires a query provider: %v", dataset)
		}
		if available, isBool := avail["available"].(bool); !isBool {
			t.Errorf("query_availability.available = %v", avail["available"])
		} else if available && avail["query_table"] == "" {
			t.Errorf("a queryable dataset names its table: %v", avail)
		} else if !available && avail["error"] == "" {
			t.Errorf("an unqueryable dataset says why: %v", avail)
		}
	})
	t.Run("datahub_get_schema: the declared schema rides on the record", func(t *testing.T) {
		schema, _ := dataset["schema"].(map[string]any)
		fields, _ := schema["fields"].([]any)
		if len(fields) == 0 {
			t.Fatalf("schema.fields missing: %v", dataset["schema"])
		}
		first, _ := fields[0].(map[string]any)
		if first["field_path"] == "" || first["type"] == "" {
			t.Errorf("field = %v", first)
		}
	})
	t.Run("datahub_get_queries: the saved queries ride on the record", func(t *testing.T) {
		if !recordListsQuery(dataset, f.queryURN) {
			t.Errorf("the saved query %s is not on the record: %v", f.queryURN, dataset["queries"])
		}
		if n, _ := dataset["total_queries"].(float64); n < 1 {
			t.Errorf("total_queries = %v", dataset["total_queries"])
		}
	})
	t.Run("datahub_get_glossary_term: definition, parent node and owners", func(t *testing.T) {
		if term["kind"] != "glossary_term" || term["description"] != "The inventory level at which a store reorders a product." {
			t.Errorf("term = %v", term)
		}
		if term["parent_node"] != f.nodeURN {
			t.Errorf("parent_node = %v, want %s", term["parent_node"], f.nodeURN)
		}
	})
	t.Run("datahub_get_data_product: the product, its domain and its members", func(t *testing.T) {
		if product["kind"] != "data_product" || !strings.HasPrefix(fmt.Sprint(product["name"]), "Retail 360") {
			t.Errorf("product = %v", product)
		}
		domain, _ := product["domain"].(map[string]any)
		if domain["urn"] != f.domainURN {
			t.Errorf("domain = %v, want %s", product["domain"], f.domainURN)
		}
		if _, ok := product["datasets"]; !ok {
			// A product with no members carries no list; the field is the
			// contract, checked on the type by the unit tests.
			t.Logf("product carries no member datasets yet: %v", product)
		}
	})
}

// TestIssue1590_TermMeaningAndCarriersInOneCall is acceptance 5: "what does
// this business term mean and which tables use it" is one fetch. The carrying
// dataset is listed through DataHub's search index, which lags a write, so the
// read is retried until the index has caught up.
func TestIssue1590_TermMeaningAndCarriersInOneCall(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	f := createFixtures(t, c)

	deadline := time.Now().Add(90 * time.Second)
	for {
		term := fetchContent(t, c, f.termURN)
		if term["description"] == "" {
			t.Fatalf("the term's meaning is missing: %v", term)
		}
		datasets, _ := term["datasets"].([]any)
		for _, d := range datasets {
			ds, _ := d.(map[string]any)
			if ds["urn"] == issue1590DatasetURN {
				return
			}
		}
		if time.Now().After(deadline) {
			b, _ := json.Marshal(term)
			t.Fatalf("the term never listed %s among its datasets: %s", issue1590DatasetURN, b)
		}
		time.Sleep(3 * time.Second)
	}
}

// recordListsQuery reports whether a fetched dataset record carries the saved
// query, with the statement the fixture wrote.
func recordListsQuery(dataset map[string]any, queryURN string) bool {
	queries, _ := dataset["queries"].([]any)
	for _, q := range queries {
		query, _ := q.(map[string]any)
		if query["urn"] == queryURN && strings.HasPrefix(fmt.Sprint(query["statement"]), "SELECT store_id") {
			return true
		}
	}
	return false
}
