//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1923: the one-time import of a DataHub into the platform catalog,
// and the rewrite of the urn:li: references platform tables hold.
//
// The criterion creates two tables in Trino's memory catalog, scans them into
// the catalog, and writes their curation into the dev stack's DataHub: a live
// record of each table, and a record DataHub has marked removed that holds a
// field the live record lacks. It captures a memory and promotes a knowledge
// page that cite the table by its DataHub URN, starts the import through the
// admin route the Import tab uses, and reads the result back through fetch.
//
// Wire forms: the import route's body is a JSON object whose url and token
// are strings, its one form. fetch's reference, memory_capture's entity_urns
// (an array of strings) and apply_knowledge's page object are each sent in
// the one form their schemas admit. The DataHub is the dev stack's, at
// DATAHUB_ENDPOINT (http://localhost:8080 when unset).

// datahub1923 is the dev stack's DataHub GMS.
func datahub1923() string {
	if u := os.Getenv("DATAHUB_ENDPOINT"); u != "" {
		parsed, err := url.Parse(u)
		if err == nil {
			return parsed.Scheme + "://" + parsed.Host
		}
	}
	return "http://localhost:8080"
}

// dhPost writes entities of one type into DataHub through its OpenAPI.
func dhPost(t *testing.T, kind string, entities ...map[string]any) {
	t.Helper()
	body, err := json.Marshal(entities)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		datahub1923()+"/openapi/v3/entity/"+kind+"?async=false", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("writing %s into DataHub: %v", kind, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("writing %s into DataHub: status %d", kind, resp.StatusCode)
	}
}

// dhDelete removes an entity from DataHub, for cleanup.
func dhDelete(kind, urn string) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		datahub1923()+"/openapi/v3/entity/"+kind+"/"+url.PathEscape(urn), http.NoBody)
	if err != nil {
		return
	}
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// dhGraphQL runs a query against DataHub's GraphQL and decodes data.
func dhGraphQL(t *testing.T, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		datahub1923()+"/api/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("querying DataHub: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding DataHub's answer: %v", err)
	}
	data, _ := out["data"].(map[string]any)
	return data
}

// fixture1923 is the curation the criterion writes into DataHub.
type fixture1923 struct {
	schema, orders, customers, removed string
	node, term, domain, tag, money     string
}

func newFixture1923(schema string) fixture1923 {
	key := strings.ReplaceAll(schema, "_", "-")
	return fixture1923{
		schema:    schema,
		orders:    "urn:li:dataset:(urn:li:dataPlatform:trino,memory." + schema + ".orders,PROD)",
		customers: "urn:li:dataset:(urn:li:dataPlatform:trino,memory." + schema + ".customers,PROD)",
		removed:   "urn:li:dataset:(urn:li:dataPlatform:trino,memory." + schema + ".orders,DEV)",
		node:      "urn:li:glossaryNode:" + key + "-finance",
		term:      "urn:li:glossaryTerm:" + key + "-net-revenue",
		domain:    "urn:li:domain:" + key + "-sales",
		tag:       "urn:li:tag:" + key + "-retail",
		money:     "urn:li:tag:" + key + "-money",
	}
}

// seed writes the fixture into DataHub and removes it when the criterion ends.
func (f fixture1923) seed(t *testing.T) {
	t.Helper()
	stamp := map[string]any{"time": 0, "actor": "urn:li:corpuser:datahub"}
	dhPost(t, "glossarynode", map[string]any{"urn": f.node,
		"glossaryNodeInfo": map[string]any{"value": map[string]any{"name": "Finance " + f.schema, "definition": "Money terms."}}})
	dhPost(t, "glossaryterm", map[string]any{"urn": f.term,
		"glossaryTermInfo": map[string]any{"value": map[string]any{
			"name": "Net revenue " + f.schema, "definition": "Gross sales minus returns.", "termSource": "INTERNAL", "parentNode": f.node}}})
	dhPost(t, "domain", map[string]any{"urn": f.domain,
		"domainProperties": map[string]any{"value": map[string]any{"name": "Sales " + f.schema, "description": "Selling."}}})
	dhPost(t, "tag",
		map[string]any{"urn": f.tag, "tagProperties": map[string]any{"value": map[string]any{"name": strings.TrimPrefix(f.tag, "urn:li:tag:"), "description": "Retail line."}}},
		map[string]any{"urn": f.money, "tagProperties": map[string]any{"value": map[string]any{"name": strings.TrimPrefix(f.money, "urn:li:tag:")}}})
	dhPost(t, "dataset",
		map[string]any{"urn": f.orders,
			"datasetProperties":         map[string]any{"value": map[string]any{"name": "orders", "description": "ingested"}},
			"editableDatasetProperties": map[string]any{"value": map[string]any{"description": "Orders placed online."}},
			"globalTags":                map[string]any{"value": map[string]any{"tags": []any{map[string]any{"tag": f.tag}}}},
			"glossaryTerms":             map[string]any{"value": map[string]any{"terms": []any{map[string]any{"urn": f.term}}, "auditStamp": stamp}},
			"domains":                   map[string]any{"value": map[string]any{"domains": []any{f.domain}}},
			"ownership": map[string]any{"value": map[string]any{
				"owners": []any{map[string]any{"owner": "urn:li:corpuser:data-eng", "type": "DATAOWNER"}}, "lastModified": stamp}},
			"institutionalMemory": map[string]any{"value": map[string]any{"elements": []any{
				map[string]any{"url": "https://wiki.example.com/orders", "description": "Runbook", "createStamp": stamp}}}},
			"editableSchemaMetadata": map[string]any{"value": map[string]any{"created": stamp, "editableSchemaFieldInfo": []any{
				map[string]any{"fieldPath": "amount", "description": "Total in USD.",
					"globalTags": map[string]any{"tags": []any{map[string]any{"tag": f.money}}}}}}},
			"upstreamLineage": map[string]any{"value": map[string]any{"upstreams": []any{
				map[string]any{"dataset": f.customers, "type": "TRANSFORMED", "auditStamp": stamp}}}},
		},
		map[string]any{"urn": f.customers,
			"datasetProperties": map[string]any{"value": map[string]any{"name": "customers", "description": "Customers."}}},
		map[string]any{"urn": f.removed,
			"datasetProperties": map[string]any{"value": map[string]any{
				"name": "orders", "description": "A description the live record replaces.",
				"customProperties": map[string]any{"owning_team": "finance"}}},
			"status": map[string]any{"value": map[string]any{"removed": true}}},
	)
	t.Cleanup(func() {
		for _, urn := range []string{f.orders, f.customers, f.removed} {
			dhDelete("dataset", urn)
		}
		dhDelete("glossaryterm", f.term)
		dhDelete("glossarynode", f.node)
		dhDelete("domain", f.domain)
		dhDelete("tag", f.tag)
		dhDelete("tag", f.money)
	})
}

// indexed waits until DataHub's search lists the removed record and its graph
// holds the lineage edge: DataHub writes its index after the write returns.
func (f fixture1923) indexed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		removed := dhGraphQL(t, `{ searchAcrossEntities(input: { types: [DATASET], query: "*", start: 0, count: 1000,
			orFilters: [{ and: [{ field: "removed", values: ["true"] }] }] }) { searchResults { entity { urn } } } }`)
		up := dhGraphQL(t, `{ dataset(urn: "`+f.orders+`") { lineage(input: { direction: UPSTREAM, start: 0, count: 10 }) { relationships { entity { urn } } } } }`)
		if strings.Contains(fmt.Sprint(removed), f.removed) && strings.Contains(fmt.Sprint(up), f.customers) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("DataHub did not index the fixture within two minutes")
}

// importTimeout1923 bounds how long the criterion waits for the import.
const importTimeout1923 = 5 * time.Minute

// import1923 starts an import of the dev DataHub and returns the run when it
// finishes.
func import1923(t *testing.T, c *client) map[string]any {
	t.Helper()
	status, started := c.rest(http.MethodPost, "/api/v1/admin/catalog/imports",
		jsonBody(t, map[string]any{"url": datahub1923(), "token": "acceptance"}))
	if status != http.StatusAccepted {
		t.Fatalf("starting the import: status %d, %v", status, started)
	}
	id, _ := started["id"].(string)
	deadline := time.Now().Add(importTimeout1923)
	for time.Now().Before(deadline) {
		_, body := c.rest(http.MethodGet, "/api/v1/admin/catalog/imports", nil)
		runs, _ := body["imports"].([]any)
		for _, r := range runs {
			run, _ := r.(map[string]any)
			if run["id"] == id && run["finished_at"] != nil {
				if msg, _ := run["error"].(string); msg != "" {
					t.Fatalf("the import failed: %s", msg)
				}
				return run
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the import %s did not finish within %s", id, importTimeout1923)
	return nil
}

// TestIssue1923_ImportCarriesDataHubCuration: the import produces catalog
// entities and aspects matching DataHub's, a field only a removed record
// holds lands on the live dataset of its path, and the platform's stored
// references to the DataHub entities are rewritten into catalog references
// that resolve through fetch.
func TestIssue1923_ImportCarriesDataHubCuration(t *testing.T) {
	c := connectFor(t, scanTimeout+importTimeout1923+4*time.Minute)
	schema := schema1918(t, c, "1923", "orders", "customers")
	exec1918(c, "CREATE TABLE memory."+schema+".orders (id bigint, amount double)")
	exec1918(c, "CREATE TABLE memory."+schema+".customers (id bigint)")
	scanOf1918(t, c, "trino", conn1918)

	f := newFixture1923(schema)
	f.seed(t)
	f.indexed(t)

	// What the platform stores before the import names the table by its
	// DataHub URN.
	captured := c.call("memory_capture", map[string]any{
		"type": "schema_entity", "category": "business_context",
		"content":     "Acceptance 1923: orders " + f.orders + " counts each order once.",
		"entity_urns": []any{f.orders},
	})
	memID, _ := captured["id"].(string)
	if memID == "" {
		t.Fatalf("memory_capture returned no id: %v", captured)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("memory_manage", map[string]any{"command": "forget", "id": memID}) })
	page := c.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug": "acceptance-1923-" + schema, "title": "Acceptance 1923 orders",
			"summary": "Written by the #1923 acceptance run.", "force_new": true,
			"body":       "Orders are in " + f.orders + " and revenue means " + f.term + ".",
			"references": []any{f.orders},
		},
	})
	pageID, _ := page["page_id"].(string)
	if cs, _ := page["changeset_id"].(string); cs != "" {
		rollback1696(t, c, cs)
	}

	run := import1923(t, c)
	report, _ := run["report"].(map[string]any)
	t.Logf("import report: datasets %v, definitions %v, aspects written %v, lineage edges %v, rewrite %v",
		report["datasets"], report["definitions"], report["aspects_written"], report["lineage_edges"], report["rewrite"])

	// The catalog holds DataHub's curation, with a field only the removed
	// record had filled in.
	_, db := issue1917Store(t)
	ordersRef := catalog.DatasetRef("trino", conn1918, "memory", schema, "orders")
	a := issue1920Aspects(t, c, ordersRef, "")
	if a["description"] != "Orders placed online." {
		t.Errorf("description %v, want DataHub's edited description", a["description"])
	}
	if !reflect.DeepEqual(a["custom_properties"], map[string]any{"owning_team": "finance"}) {
		t.Errorf("custom_properties %v, want the removed record's", a["custom_properties"])
	}
	if !reflect.DeepEqual(a["tags"], []any{strings.TrimPrefix(f.tag, "urn:li:tag:")}) {
		t.Errorf("tags %v", a["tags"])
	}
	if !reflect.DeepEqual(a["owners"], []any{map[string]any{"owner": "data-eng", "type": "dataowner"}}) {
		t.Errorf("owners %v", a["owners"])
	}
	if !reflect.DeepEqual(a["documentation_links"], []any{map[string]any{"url": "https://wiki.example.com/orders", "description": "Runbook"}}) {
		t.Errorf("documentation_links %v", a["documentation_links"])
	}
	terms, _ := a["glossary_terms"].([]any)
	if len(terms) != 1 {
		t.Fatalf("glossary_terms %v", a["glossary_terms"])
	}
	term := issue1917Document(t, c, terms[0].(string))
	if term["title"] != "Net revenue "+schema || term["body"] != "Gross sales minus returns." {
		t.Errorf("the imported glossary term: %v", term)
	}
	domain := issue1917Document(t, c, a["domain"].(string))
	if domain["title"] != "Sales "+schema {
		t.Errorf("the imported domain: %v", domain)
	}
	amount := issue1920Aspects(t, c, ordersRef, "amount")
	if amount["description"] != "Total in USD." ||
		!reflect.DeepEqual(amount["tags"], []any{strings.TrimPrefix(f.money, "urn:li:tag:")}) {
		t.Errorf("the amount column: %v", amount)
	}
	content := issue1917Fetch(t, c, ordersRef.String())
	if !strings.Contains(fmt.Sprint(content["upstream"]), "memory."+schema+".customers") {
		t.Errorf("the lineage edge from customers was not imported: %v", content["upstream"])
	}

	// The memory and the page now cite catalog references, each of which
	// fetch resolves.
	var urns, memContent string
	if err := db.QueryRow(`SELECT entity_urns::text, content FROM memory_records WHERE id = $1`, memID).Scan(&urns, &memContent); err != nil {
		t.Fatalf("reading the memory: %v", err)
	}
	if strings.Contains(urns+memContent, "urn:li:") {
		t.Errorf("the memory still holds a DataHub URN: %s / %s", urns, memContent)
	}
	var cited []string
	_ = json.Unmarshal([]byte(urns), &cited)
	for _, ref := range cited {
		issue1917Document(t, c, ref)
	}
	if pageID != "" {
		var body string
		if err := db.QueryRow(`SELECT body FROM portal_knowledge_pages WHERE id = $1`, pageID).Scan(&body); err != nil {
			t.Fatalf("reading the page: %v", err)
		}
		if strings.Contains(body, "urn:li:") {
			t.Errorf("the page body still holds a DataHub URN: %s", body)
		}
		rows, err := db.Query(`SELECT target_type, entity_urn FROM knowledge_page_entity_refs WHERE page_id = $1 AND entity_urn IS NOT NULL`, pageID)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var typ, ref string
			if err := rows.Scan(&typ, &ref); err != nil {
				t.Fatal(err)
			}
			if typ != "catalog" {
				t.Errorf("a page citation is still %s %s", typ, ref)
				continue
			}
			issue1917Document(t, c, ref)
		}
	}

	// Every DataHub URN a rewritten column still holds is one the run
	// listed as having no catalog entity.
	listed := map[string]bool{}
	skipped, _ := report["skipped"].([]any)
	for _, s := range skipped {
		entry, _ := s.(map[string]any)
		if where, _ := entry["where"].(string); where != "" {
			listed[where] = true
		}
	}
	rewrite, _ := report["rewrite"].(map[string]any)
	if n, _ := rewrite["unmapped"].(float64); int(n) <= len(skipped) {
		for _, q := range []struct{ column, query string }{
			{"memory_records.entity_urns", `SELECT id FROM memory_records WHERE entity_urns::text LIKE '%urn:li:%'`},
			{"knowledge_changesets.target_urn", `SELECT id FROM knowledge_changesets WHERE target_urn LIKE 'urn:li:%'`},
			{"portal_knowledge_pages.body", `SELECT id FROM portal_knowledge_pages WHERE body LIKE '%urn:li:%'`},
		} {
			rows, err := db.Query(q.query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				if !listed[q.column+" "+id] {
					t.Errorf("%s %s still holds a DataHub URN the report does not list", q.column, id)
				}
			}
			_ = rows.Close()
		}
	}
}
