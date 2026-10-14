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

// Issue #2124: a deployment upgrading with DataHub still in its configuration
// starts, and the platform imports that DataHub into its catalog by itself,
// after a catalog scan, once.
//
// The dev stack runs as such a deployment: DATAHUB_ENABLED=true puts the dev
// DataHub (DATAHUB_ENDPOINT, the quickstart's GMS) in toolkits.datahub. The
// criterion writes curation into that DataHub, stores what an earlier release
// stored (a memory naming a table by its DataHub URN, and a knowledge page
// citing it as a DataHub entity), clears the import's run log so the stack is
// a deployment that has never imported, and asks for a catalog scan: the scan
// a deployment runs at startup. Nothing asks for the import.
//
// Wire forms: POST /api/v1/admin/catalog/imports takes a JSON object whose url
// and token are strings, or no body, or an empty object, which read the
// configured DataHub; the criteria send no body, {}, and the object.
// memory_capture's entity_urns is an array of strings, its one form.

// datahub2124 is the dev DataHub the stack imports.
func datahub2124() string {
	if u := os.Getenv("DATAHUB_ENDPOINT"); u != "" {
		if parsed, err := url.Parse(u); err == nil {
			return parsed.Scheme + "://" + parsed.Host
		}
	}
	return "http://localhost:18080"
}

// dhPost2124 writes entities of one type into DataHub through its OpenAPI.
func dhPost2124(t *testing.T, kind string, entities ...map[string]any) {
	t.Helper()
	body, err := json.Marshal(entities)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		datahub2124()+"/openapi/v3/entity/"+kind+"?async=false", bytes.NewReader(body))
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

// dhDelete2124 removes an entity from DataHub, for cleanup.
func dhDelete2124(kind, urn string) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		datahub2124()+"/openapi/v3/entity/"+kind+"/"+url.PathEscape(urn), http.NoBody)
	if err != nil {
		return
	}
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// dhSearch2124 reports whether DataHub's search lists urn, which it does once
// it has indexed the write.
func dhSearch2124(t *testing.T, urn string) bool {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": `{ searchAcrossEntities(input: { types: [DATASET], query: "*", start: 0, count: 1000 }) { searchResults { entity { urn } } } }`})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, datahub2124()+"/api/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("querying DataHub: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return strings.Contains(out.String(), urn)
}

// fixture2124 is the curation the criterion writes into DataHub.
type fixture2124 struct {
	schema, orders, customers, term, tag string
}

func newFixture2124(schema string) fixture2124 {
	key := strings.ReplaceAll(schema, "_", "-")
	return fixture2124{
		schema:    schema,
		orders:    "urn:li:dataset:(urn:li:dataPlatform:trino,memory." + schema + ".orders,PROD)",
		customers: "urn:li:dataset:(urn:li:dataPlatform:trino,memory." + schema + ".customers,PROD)",
		term:      "urn:li:glossaryTerm:" + key + "-net-revenue",
		tag:       "urn:li:tag:" + key + "-retail",
	}
}

// seed writes the fixture into DataHub, waits for DataHub to index it, and
// removes it when the criterion ends.
func (f fixture2124) seed(t *testing.T) {
	t.Helper()
	stamp := map[string]any{"time": 0, "actor": "urn:li:corpuser:datahub"}
	dhPost2124(t, "glossaryterm", map[string]any{"urn": f.term,
		"glossaryTermInfo": map[string]any{"value": map[string]any{
			"name": "Net revenue " + f.schema, "definition": "Gross sales minus returns.", "termSource": "INTERNAL"}}})
	dhPost2124(t, "tag", map[string]any{"urn": f.tag,
		"tagProperties": map[string]any{"value": map[string]any{"name": strings.TrimPrefix(f.tag, "urn:li:tag:")}}})
	dhPost2124(t, "dataset",
		map[string]any{"urn": f.orders,
			"datasetProperties":         map[string]any{"value": map[string]any{"name": "orders", "description": "ingested"}},
			"editableDatasetProperties": map[string]any{"value": map[string]any{"description": "Orders placed online."}},
			"globalTags":                map[string]any{"value": map[string]any{"tags": []any{map[string]any{"tag": f.tag}}}},
			"glossaryTerms":             map[string]any{"value": map[string]any{"terms": []any{map[string]any{"urn": f.term}}, "auditStamp": stamp}},
			"upstreamLineage": map[string]any{"value": map[string]any{"upstreams": []any{
				map[string]any{"dataset": f.customers, "type": "TRANSFORMED", "auditStamp": stamp}}}},
		},
		map[string]any{"urn": f.customers,
			"datasetProperties": map[string]any{"value": map[string]any{"name": "customers", "description": "Customers."}}},
	)
	t.Cleanup(func() {
		dhDelete2124("dataset", f.orders)
		dhDelete2124("dataset", f.customers)
		dhDelete2124("glossaryterm", f.term)
		dhDelete2124("tag", f.tag)
	})
	deadline := time.Now().Add(2 * time.Minute)
	for !dhSearch2124(t, f.orders) {
		if time.Now().After(deadline) {
			t.Fatal("DataHub did not index the fixture within two minutes")
		}
		time.Sleep(2 * time.Second)
	}
}

// importTimeout2124 bounds how long a criterion waits for an import.
const importTimeout2124 = 5 * time.Minute

// imports2124 reads the import listing.
func imports2124(t *testing.T, c *client) (runs []map[string]any, configured string) {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/admin/catalog/imports", nil)
	if status != http.StatusOK {
		t.Fatalf("listing the imports: status %d, %v", status, body)
	}
	list, _ := body["imports"].([]any)
	for _, r := range list {
		run, _ := r.(map[string]any)
		runs = append(runs, run)
	}
	configured, _ = body["configured"].(string)
	return runs, configured
}

// finished2124 waits until a run match accepts has finished, and returns it.
func finished2124(t *testing.T, c *client, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(importTimeout2124)
	for time.Now().Before(deadline) {
		runs, _ := imports2124(t, c)
		for _, run := range runs {
			if match(run) && run["finished_at"] != nil {
				return run
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("no matching import finished within %s", importTimeout2124)
	return nil
}

// start2124 starts an import with body and returns its id.
func start2124(t *testing.T, c *client, body *bytes.Reader) string {
	t.Helper()
	var status int
	var started map[string]any
	if body == nil {
		status, started = c.rest(http.MethodPost, "/api/v1/admin/catalog/imports", nil)
	} else {
		status, started = c.rest(http.MethodPost, "/api/v1/admin/catalog/imports", body)
	}
	if status != http.StatusAccepted {
		t.Fatalf("starting the import: status %d, %v", status, started)
	}
	id, _ := started["id"].(string)
	return id
}

// TestIssue2124_UpgradeImportsTheConfiguredDataHub: with DataHub in its
// configuration, the platform starts, and after a catalog scan, with no
// request, imports that DataHub: the catalog carries its curation, a memory's
// DataHub URN and a knowledge page's DataHub citation become catalog
// references, and the run is recorded as the platform's.
func TestIssue2124_UpgradeImportsTheConfiguredDataHub(t *testing.T) {
	c := connectFor(t, scanTimeout+2*importTimeout2124+4*time.Minute)
	_, configured := imports2124(t, c)
	if configured != datahub2124() {
		t.Fatalf("the stack names DataHub %q, want %q: start it with DATAHUB_ENABLED=true", configured, datahub2124())
	}
	schema := schema1918(t, c, "2124", "orders", "customers")
	exec1918(c, "CREATE TABLE memory."+schema+".orders (id bigint, amount double)")
	exec1918(c, "CREATE TABLE memory."+schema+".customers (id bigint)")
	f := newFixture2124(schema)
	f.seed(t)

	// What an earlier release stored: a memory naming the table by its
	// DataHub URN, and a page citing it as a DataHub entity.
	captured := c.call("memory_capture", map[string]any{
		"type": "schema_entity", "category": "business_context",
		"content":     "Acceptance 2124: orders " + f.orders + " counts each order once.",
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
			"slug": "acceptance-2124-" + schema, "title": "Acceptance 2124 orders",
			"summary": "Written by the #2124 acceptance run.", "force_new": true,
			"body": "Orders are counted once.",
		},
	})
	pageID, _ := page["page_id"].(string)
	if pageID == "" {
		t.Fatalf("apply_knowledge wrote no page: %v", page)
	}
	if cs, _ := page["changeset_id"].(string); cs != "" {
		t.Cleanup(func() { rollback1696(t, c, cs) })
	}
	_, db := issue1917Store(t)
	refID := fmt.Sprintf("acc2124-%d", time.Now().UnixNano())
	if _, err := db.Exec(`INSERT INTO knowledge_page_entity_refs (id, page_id, target_type, entity_urn, source)
		VALUES ($1, $2, 'datahub', $3, 'promoted')`, refID, pageID, f.orders); err != nil {
		t.Fatalf("storing an earlier release's citation: %v", err)
	}

	// The tables are cataloged first. Scans hold one lock, so every scan that
	// finishes after this one started after it, and holds them: the
	// deployment's tables were there before it upgraded.
	scanOf1918(t, c, "trino", conn1918)

	// A deployment that has never imported, at the scan it runs on startup.
	if _, err := db.Exec(`DELETE FROM catalog_imports`); err != nil {
		t.Fatalf("clearing the import's run log: %v", err)
	}
	scanOf1918(t, c, "trino", conn1918)
	run := finished2124(t, c, func(r map[string]any) bool { return r["started_by"] == "platform (upgrade from DataHub)" })
	if msg, _ := run["error"].(string); msg != "" {
		t.Fatalf("the upgrade import failed: %s", msg)
	}
	if run["source_url"] != datahub2124() {
		t.Errorf("the upgrade imported %v, want the configured %s", run["source_url"], datahub2124())
	}
	report, _ := run["report"].(map[string]any)
	t.Logf("upgrade import report: datasets %v, definitions %v, aspects written %v, lineage edges %v, rewrite %v",
		report["datasets"], report["definitions"], report["aspects_written"], report["lineage_edges"], report["rewrite"])

	ordersRef := catalog.DatasetRef("trino", conn1918, "memory", schema, "orders")
	a := issue1920Aspects(t, c, ordersRef, "")
	if a["description"] != "Orders placed online." {
		t.Errorf("description %v, want DataHub's edited description", a["description"])
	}
	if !reflect.DeepEqual(a["tags"], []any{strings.TrimPrefix(f.tag, "urn:li:tag:")}) {
		t.Errorf("tags %v", a["tags"])
	}
	terms, _ := a["glossary_terms"].([]any)
	if len(terms) != 1 {
		t.Fatalf("glossary_terms %v", a["glossary_terms"])
	}
	if term := issue1917Document(t, c, terms[0].(string)); term["title"] != "Net revenue "+schema {
		t.Errorf("the imported glossary term: %v", term)
	}
	if content := issue1917Fetch(t, c, ordersRef.String()); !strings.Contains(fmt.Sprint(content["upstream"]), "memory."+schema+".customers") {
		t.Errorf("the lineage edge from customers was not imported: %v", content["upstream"])
	}

	// A DataHub URN names no connection, and every Trino connection that
	// catalogs the path took the curation; a stored URN becomes the first of
	// them by connection name.
	var want string
	if err := db.QueryRow(`SELECT reference FROM catalog_entities WHERE kind = 'dataset' AND connection_kind = 'trino'
		AND path = $1 ORDER BY connection_name LIMIT 1`, "memory."+schema+".orders").Scan(&want); err != nil {
		t.Fatalf("reading the cataloged orders tables: %v", err)
	}
	var urns string
	if err := db.QueryRow(`SELECT entity_urns::text FROM memory_records WHERE id = $1`, memID).Scan(&urns); err != nil {
		t.Fatalf("reading the memory: %v", err)
	}
	if urns != `["`+want+`"]` {
		t.Errorf("the memory's DataHub URN became %s, want %s", urns, want)
	}
	var typ, ref string
	if err := db.QueryRow(`SELECT target_type, entity_urn FROM knowledge_page_entity_refs WHERE id = $1`, refID).Scan(&typ, &ref); err != nil {
		t.Fatalf("reading the page citation: %v", err)
	}
	if typ != "catalog" || ref != want {
		t.Errorf("the page citation is %s %s, want catalog %s", typ, ref, want)
	}
	wantRef, err := catalog.ParseRef(want)
	if err != nil {
		t.Fatalf("parsing %s: %v", want, err)
	}
	if a := issue1920Aspects(t, c, wantRef, ""); a["description"] != "Orders placed online." {
		t.Errorf("the dataset the citation names has description %v", a["description"])
	}
}

// TestIssue2124_ImportsOnceAndAgainOnRequest: a scan after an import of the
// configured DataHub finished starts none, an administrator's request with no
// body or an empty object imports it again, and one naming a DataHub imports
// that one.
func TestIssue2124_ImportsOnceAndAgainOnRequest(t *testing.T) {
	c := connectFor(t, scanTimeout+3*importTimeout2124+2*time.Minute)
	_, configured := imports2124(t, c)
	if configured != datahub2124() {
		t.Fatalf("the stack names DataHub %q, want %q: start it with DATAHUB_ENABLED=true", configured, datahub2124())
	}
	finished2124(t, c, func(r map[string]any) bool { return r["source_url"] == configured && r["error"] == nil })
	before, _ := imports2124(t, c)
	scanOf1918(t, c, "trino", conn1918)
	after, _ := imports2124(t, c)
	if len(after) != len(before) {
		t.Errorf("a scan after a finished import started another: %d runs, then %d", len(before), len(after))
	}

	for _, body := range []*bytes.Reader{nil, bytes.NewReader([]byte(`{}`))} {
		id := start2124(t, c, body)
		run := finished2124(t, c, func(r map[string]any) bool { return r["id"] == id })
		if run["source_url"] != configured || run["started_by"] == "platform (upgrade from DataHub)" {
			t.Errorf("a request with no DataHub imported %v as %v, want the configured one by the administrator",
				run["source_url"], run["started_by"])
		}
		if msg, _ := run["error"].(string); msg != "" {
			t.Errorf("the requested import failed: %s", msg)
		}
	}
	named, _ := json.Marshal(map[string]any{"url": datahub2124() + "/api/graphql", "token": "acceptance"})
	id := start2124(t, c, bytes.NewReader(named))
	run := finished2124(t, c, func(r map[string]any) bool { return r["id"] == id })
	if run["source_url"] != datahub2124()+"/api/graphql" {
		t.Errorf("the named import read %v", run["source_url"])
	}
	if msg, _ := run["error"].(string); msg != "" {
		t.Errorf("the named import failed: %s", msg)
	}
}

// TestIssue2124_NoDataHubToolsRun: the DataHub in the configuration is only
// imported; no datahub_ tool is listed.
func TestIssue2124_NoDataHubToolsRun(t *testing.T) {
	c := connect(t)
	tools := c.tools()
	if len(tools) == 0 {
		t.Fatal("tools/list carried no tools")
	}
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, "datahub_") {
			t.Errorf("%s is listed", tool.Name)
		}
	}
}
