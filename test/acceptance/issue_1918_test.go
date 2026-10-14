//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1918: the platform scans its own connections into the catalog. Each
// criterion creates the tables it is about in Trino's memory catalog through
// trino_execute on the writable dev connection, asks for a scan through the
// admin route the Catalog page uses, and reads the result through fetch.
//
// Wire forms: fetch's `reference`, trino_execute's `connection` and `sql`,
// trino_browse's and trino_describe_table's `connection`, `catalog`, `schema`
// and `table`, and manage_table's `action`, `reference`, `connection` and
// `table_name` are typed string, and each is sent in that one form. The
// catalog settings body is JSON with `scan_interval` a string and
// `exclusions` an array of objects, its one form.
//
// The schema-document criterion reads the stored API operation's dataset
// through fetch and follows the mcp:schema_document: reference it lists, the
// same string parameter, as an administrator and as two personas created for
// it: one allowed the API connection, one allowed only another connection. It
// writes a page citing the document with apply_knowledge, whose `page` is a
// JSON object with `references` an array of strings (its one form), and
// resolves the citation with POST /api/v1/portal/knowledge-pages/refs/resolve,
// whose `urns` is an array of strings (its one form).

// conn1918 is the writable dev Trino connection the criteria create tables on.
const conn1918 = scratchConnection

// scanTimeout bounds how long a criterion waits for the scan it asked for. A
// request made while another replica scans is run after that scan, so the
// wait covers two; a connection whose host does not answer holds each scan
// for its client's two-minute retry budget.
const scanTimeout = 6 * time.Minute

// exec1918 runs a statement on the writable connection.
func exec1918(c *client, sql string) {
	c.call("trino_execute", map[string]any{
		"connection": conn1918, "sql": sql,
		"purpose": "Acceptance: arranging the tables a catalog criterion is about.",
	})
}

// schema1918 creates a memory-catalog schema for one criterion and removes it,
// with every table in it, when the criterion ends.
func schema1918(t *testing.T, c *client, name string, tables ...string) string {
	t.Helper()
	schema := fmt.Sprintf("acc%s_%d", name, time.Now().UnixNano())
	exec1918(c, "CREATE SCHEMA memory."+schema)
	t.Cleanup(func() {
		admin := connect(t)
		for _, tbl := range tables {
			_, _, _ = admin.callRaw("trino_execute", map[string]any{
				"connection": conn1918, "sql": "DROP TABLE IF EXISTS memory." + schema + "." + tbl,
				"purpose": "Acceptance: removing a criterion's tables.",
			})
		}
		_, _, _ = admin.callRaw("trino_execute", map[string]any{
			"connection": conn1918, "sql": "DROP SCHEMA IF EXISTS memory." + schema,
			"purpose": "Acceptance: removing a criterion's schema.",
		})
	})
	return schema
}

// ref1918 is the catalog reference of a memory-catalog table on the writable
// connection.
func ref1918(schema, table string) string {
	return catalog.DatasetRef("trino", conn1918, "memory", schema, table).String()
}

// scan1918 asks for a scan and waits for one of the writable connection that
// started after the request to finish.
func scan1918(t *testing.T, c *client) map[string]any {
	t.Helper()
	return scanOf1918(t, c, "trino", conn1918)
}

// scanOf1918 asks for a scan and waits for one of the given connection that
// started after the request to finish.
func scanOf1918(t *testing.T, c *client, kind, conn string) map[string]any {
	t.Helper()
	// Only a scan of the connection that starts after the request is taken:
	// one already under way may have read it before the caller's change.
	requested := time.Now()
	if status, body := c.rest(http.MethodPost, "/api/v1/admin/catalog/scans", nil); status != http.StatusAccepted {
		t.Fatalf("requesting a scan: status %d, %v", status, body)
	}
	deadline := time.Now().Add(scanTimeout)
	for time.Now().Before(deadline) {
		_, body := c.rest(http.MethodGet, "/api/v1/admin/catalog/scans", nil)
		scans, _ := body["scans"].([]any)
		for _, s := range scans {
			scan, _ := s.(map[string]any)
			if scan["connection_kind"] != kind || scan["connection"] != conn {
				continue
			}
			started, err := time.Parse(time.RFC3339Nano, fmt.Sprint(scan["started_at"]))
			if err == nil && started.After(requested) {
				if msg, _ := scan["error"].(string); msg != "" {
					t.Fatalf("the scan of %s failed: %s", conn, msg)
				}
				return scan
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("no scan of %s finished within %s of the request", conn, scanTimeout)
	return nil
}

// structured1918 calls a tool and returns its structured content.
func structured1918(t *testing.T, c *client, tool string, args map[string]any) map[string]any {
	t.Helper()
	res, text, err := c.callRaw(tool, args)
	if err != nil || res.IsError {
		t.Fatalf("%s: %v %s", tool, err, text)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s structured content: %v", tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s structured content is not an object: %s", tool, raw)
	}
	return out
}

// found1918 reports whether fetch finds a reference.
func found1918(c *client, ref string) bool {
	out := c.call("fetch", map[string]any{"reference": ref, "purpose": "Acceptance: checking a catalog entry."})
	return out["found"] == true
}

// TestIssue1918_EveryBrowsedTableIsCataloged: after a scan, every table
// trino_browse lists is a catalog dataset with its columns and types.
func TestIssue1918_EveryBrowsedTableIsCataloged(t *testing.T) {
	c := connectFor(t, scanTimeout+time.Minute)
	schema := schema1918(t, c, "1918browse", "orders", "users")
	exec1918(c, "CREATE TABLE memory."+schema+".orders (id bigint, name varchar, amount double)")
	exec1918(c, "CREATE TABLE memory."+schema+".users (id integer, joined date)")
	scan1918(t, c)

	browsed := structured1918(t, c, "trino_browse", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema,
		"purpose": "Acceptance: listing the tables a scan should have cataloged.",
	})
	items, _ := browsed["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("trino_browse listed %v, want the two tables created", items)
	}
	for _, item := range items {
		table := fmt.Sprint(item)
		described := structured1918(t, c, "trino_describe_table", map[string]any{
			"connection": conn1918, "catalog": "memory", "schema": schema, "table": table,
			"purpose": "Acceptance: the columns the catalog should hold.",
		})
		content := issue1917Fetch(t, c, ref1918(schema, table))
		got, _ := content["columns"].([]any)
		want, _ := described["columns"].([]any)
		if len(got) != len(want) {
			t.Fatalf("%s: cataloged %d columns, Trino describes %d", table, len(got), len(want))
		}
		for i := range want {
			w, _ := want[i].(map[string]any)
			g, _ := got[i].(map[string]any)
			if g["name"] != w["name"] || g["data_type"] != w["type"] {
				t.Errorf("%s column %d: cataloged %v %v, Trino describes %v %v", table, i, g["name"], g["data_type"], w["name"], w["type"])
			}
		}
	}
}

// TestIssue1918_ANewTableIsCatalogedAndADroppedOneMarkedRemoved: a table
// created after a scan is cataloged by the next one; a dropped table is marked
// removed and its curation is still readable.
func TestIssue1918_ANewTableIsCatalogedAndADroppedOneMarkedRemoved(t *testing.T) {
	c := connectFor(t, 2*scanTimeout+time.Minute)
	schema := schema1918(t, c, "1918life", "kept", "added")
	exec1918(c, "CREATE TABLE memory."+schema+".kept (id bigint)")
	scan1918(t, c)
	if found1918(c, ref1918(schema, "added")) {
		t.Fatal("a table that does not exist yet is cataloged")
	}

	exec1918(c, "CREATE TABLE memory."+schema+".added (id bigint)")
	scan1918(t, c)
	if !found1918(c, ref1918(schema, "added")) {
		t.Fatal("a table created after a scan is not cataloged by the next one")
	}

	store, _ := issue1917Store(t)
	ref, err := catalog.ParseRef(ref1918(schema, "added"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteAspect(t.Context(), ref, catalog.AspectDescription,
		json.RawMessage(`"Curation that outlives the table."`), "acceptance@example.com"); err != nil {
		t.Fatalf("writing curation: %v", err)
	}
	exec1918(c, "DROP TABLE memory."+schema+".added")
	scan1918(t, c)

	content := issue1917Fetch(t, c, ref1918(schema, "added"))
	if content["removed_at"] == nil {
		t.Errorf("a dropped table is not marked removed: %v", content)
	}
	aspects, _ := content["aspects"].(map[string]any)
	if aspects["description"] != "Curation that outlives the table." {
		t.Errorf("a removed dataset's curation is not readable: %v", aspects)
	}
	if kept := issue1917Fetch(t, c, ref1918(schema, "kept")); kept["removed_at"] != nil {
		t.Errorf("a table that still exists is marked removed: %v", kept)
	}
}

// TestIssue1918_RegisteringATableCatalogsItBeforeTheCallReturns: the
// registration call's own answer is followed at once by a fetch that finds the
// table, with no scan between them.
func TestIssue1918_RegisteringATableCatalogsItBeforeTheCallReturns(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	upload := c.call("manage_resource", map[string]any{
		"action": "create", "display_name": "Acceptance 1918 " + stamp,
		"description": "Acceptance #1918: a CSV registered as a table.",
		"filename":    "acc-1918-" + stamp + ".csv", "path": "acceptance/issue-1918",
		"content_type": "text/csv", "content": "region,units\nnorth,10\n",
	})
	reference, _ := upload["reference"].(string)
	resourceID, _ := upload["resource_id"].(string)
	if reference == "" {
		t.Fatalf("manage_resource create: %v", upload)
	}
	t.Cleanup(func() { _, _ = connect(t).rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })

	registered := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": "acc_1918_" + stamp,
	})
	regID, _ := registered["registration_id"].(string)
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": regID})
	})
	queryTable, _ := registered["query_table"].(string)
	parts := strings.Split(queryTable, ".")
	if len(parts) != 3 {
		t.Fatalf("query_table %q is not catalog.schema.table", queryTable)
	}
	ref := catalog.DatasetRef("trino", scratchResourceConnection, parts...).String()

	content := issue1917Fetch(t, c, ref)
	if content["type"] != string(catalog.DatasetRegisteredTable) {
		t.Errorf("type = %v, want registered_table", content["type"])
	}
	cols, _ := content["columns"].([]any)
	if len(cols) != 2 {
		t.Errorf("columns = %v, want region and units", cols)
	}
	upstream, _ := content["upstream"].([]any)
	var fromFile bool
	for _, h := range upstream {
		hop, _ := h.(map[string]any)
		if hop["source"] == "mcp:resource:"+resourceID {
			fromFile = true
		}
	}
	if !fromFile {
		t.Errorf("the table has no lineage from the file it reads: %v", upstream)
	}
}

// TestIssue1918_AnExcludedSchemaIsNotCataloged: an operator's exclusion keeps
// a schema's tables out of the catalog.
func TestIssue1918_AnExcludedSchemaIsNotCataloged(t *testing.T) {
	c := connectFor(t, scanTimeout+time.Minute)
	status, original := c.rest(http.MethodGet, "/api/v1/admin/catalog/settings", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the catalog settings: %d %v", status, original)
	}
	schema := schema1918(t, c, "1918excl", "hidden")
	exec1918(c, "CREATE TABLE memory."+schema+".hidden (id bigint)")

	exclusions, _ := original["exclusions"].([]any)
	withExclusion := append(append([]any{}, exclusions...), map[string]any{
		"connection_kind": "trino", "connection": conn1918, "entries": []any{"memory." + schema},
	})
	put := func(body map[string]any) {
		status, out := c.rest(http.MethodPut, "/api/v1/admin/catalog/settings", jsonBody(t, body))
		if status != http.StatusOK {
			t.Fatalf("saving the catalog settings: %d %v", status, out)
		}
	}
	put(map[string]any{"scan_interval": original["scan_interval"], "exclusions": withExclusion})
	t.Cleanup(func() { put(map[string]any{"scan_interval": original["scan_interval"], "exclusions": exclusions}) })

	scan1918(t, c)
	if found1918(c, ref1918(schema, "hidden")) {
		t.Error("a table in an excluded schema is cataloged")
	}
}

// TestIssue1918_ASchemaDocumentIsKeptAndLinked: a scan keeps an API
// connection's OpenAPI spec in the catalog and links it from each operation's
// dataset. fetch on the dataset lists the document; fetch on the document
// returns the spec to a persona that may use the connection and nothing to one
// that may not, and a knowledge page citing it lists the citation only to the
// first. The admin route downloads it, a second scan of the unchanged spec
// records no version, and no file is written to the resource library.
func TestIssue1918_ASchemaDocumentIsKeptAndLinked(t *testing.T) {
	const apiConn = "api-test-fixture"
	c := connectFor(t, 2*scanTimeout+time.Minute)
	scanOf1918(t, c, "api", apiConn)

	_, db := issue1917Store(t)
	var ref string
	err := db.QueryRow(`SELECT reference FROM catalog_entities
		WHERE kind = 'dataset' AND connection_kind = 'api' AND connection_name = $1 AND removed_at IS NULL
		ORDER BY reference LIMIT 1`, apiConn).Scan(&ref)
	if err != nil {
		t.Fatalf("no cataloged operation of %s: %v", apiConn, err)
	}

	doc := issue1917Document(t, c, ref)
	refs, _ := doc["references"].([]any)
	if len(refs) != 1 {
		t.Fatalf("the operation %s lists no schema document: %v", ref, doc)
	}
	first, _ := refs[0].(map[string]any)
	docRef, _ := first["reference"].(string)
	parsed, err := catalog.ParseRef(docRef)
	if err != nil || parsed.Kind != catalog.KindSchemaDocument || parsed.Connection != apiConn || first["type"] != "catalog" {
		t.Fatalf("schema document reference: %v (%v)", first, err)
	}

	readsSpec := func(reader *client) bool {
		out := reader.call("fetch", map[string]any{"reference": docRef, "purpose": "Acceptance: reading a kept schema document."})
		body, _ := out["document"].(map[string]any)
		return out["found"] == true && strings.Contains(fmt.Sprint(body["body"]), "openapi")
	}
	allowed := connectAs(t, member1918(t, c, apiConn))
	denied := connectAs(t, member1918(t, c, scratchConnection))
	if !readsSpec(c) {
		t.Fatalf("fetch %s did not return the spec to an administrator", docRef)
	}
	if !readsSpec(allowed) {
		t.Fatalf("fetch %s did not return the spec to a persona allowed %s", docRef, apiConn)
	}
	if readsSpec(denied) {
		t.Fatalf("fetch %s returned the spec to a persona not allowed %s", docRef, apiConn)
	}
	citedOnAPage1918(t, c, allowed, denied, docRef)

	path := "/api/v1/admin/catalog/documents/api/" + apiConn + "/" + parsed.Key
	status, body := c.restText(path)
	if status != http.StatusOK || !strings.Contains(body, "openapi") {
		t.Fatalf("GET %s answered %d: %.200s", path, status, body)
	}

	version := func() int {
		var v int
		if err := db.QueryRow(`SELECT version FROM catalog_documents
			WHERE connection_kind = 'api' AND connection_name = $1 AND filename = $2`, apiConn, parsed.Key).Scan(&v); err != nil {
			t.Fatalf("reading the document's version: %v", err)
		}
		return v
	}
	before := version()
	scanOf1918(t, c, "api", apiConn)
	if after := version(); after != before {
		t.Fatalf("a scan of an unchanged spec recorded a version: %d before, %d after", before, after)
	}

	var files int
	if err := db.QueryRow(`SELECT count(*) FROM resources WHERE path LIKE 'catalog/%'`).Scan(&files); err != nil {
		t.Fatalf("counting resource library files: %v", err)
	}
	if files != 0 {
		t.Fatalf("the scan wrote %d schema documents to the resource library", files)
	}
}

// citedOnAPage1918 writes a knowledge page citing docRef and checks that the
// citation is listed, by fetch and by the portal, for the persona allowed its
// connection and withheld from the one that is not.
func citedOnAPage1918(t *testing.T, admin, allowed, denied *client, docRef string) {
	t.Helper()
	page := admin.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug": fmt.Sprintf("acceptance-1918-%d", time.Now().UnixNano()), "title": "Acceptance 1918 spec",
			"summary": "Written by the #1918 acceptance run.", "force_new": true,
			"body":       "The fixture's operations are described by " + docRef + ".",
			"references": []any{docRef},
		},
	})
	pageID, _ := page["page_id"].(string)
	if pageID == "" {
		t.Fatalf("apply_knowledge wrote no page: %v", page)
	}
	if cs, _ := page["changeset_id"].(string); cs != "" {
		rollback1696(t, admin, cs)
	}
	cites := func(reader *client) (bool, float64) {
		out := reader.call("fetch", map[string]any{"reference": "mcp:knowledge_page:" + pageID, "purpose": "Acceptance: reading a page that cites a schema document."})
		doc, _ := out["document"].(map[string]any)
		refs, _ := doc["references"].([]any)
		for _, r := range refs {
			if m, _ := r.(map[string]any); m["reference"] == docRef {
				return true, 0
			}
		}
		withheld, _ := doc["references_withheld"].(float64)
		return false, withheld
	}
	if listed, _ := cites(allowed); !listed {
		t.Errorf("the page's citation of %s is not listed for a persona allowed its connection", docRef)
	}
	if listed, withheld := cites(denied); listed || withheld < 1 {
		t.Errorf("the page's citation of %s for a persona not allowed its connection: listed %v, withheld %v", docRef, listed, withheld)
	}
	resolve := func(reader *client) bool {
		status, out := reader.rest(http.MethodPost, "/api/v1/portal/knowledge-pages/refs/resolve",
			jsonBody(t, map[string]any{"urns": []any{docRef}}))
		refs, _ := out["refs"].([]any)
		if status != http.StatusOK || len(refs) != 1 {
			t.Fatalf("resolving %s answered %d %v", docRef, status, out)
		}
		m, _ := refs[0].(map[string]any)
		return m["accessible"] == true
	}
	if !resolve(allowed) || resolve(denied) {
		t.Errorf("the portal resolves %s for the allowed persona %v and the denied one %v", docRef, resolve(allowed), resolve(denied))
	}
}

// member1918 creates a database persona allowed only the named connection, and
// an API key reaching it, both removed at cleanup.
func member1918(t *testing.T, admin *client, connection string) string {
	t.Helper()
	suffix := time.Now().UnixNano()
	name := fmt.Sprintf("acc-1918-%d", suffix)
	role := fmt.Sprintf("acc_1918_%d", suffix)
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Acceptance 1918 member", "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{connection},
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating persona %s answered %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })
	status, out = admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []any{role}, "description": "Acceptance #1918 member",
	}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating key %s answered %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	key, _ := out["key"].(string)
	if key == "" {
		t.Fatalf("key %s was created without a secret: %v", name, out)
	}
	return key
}
