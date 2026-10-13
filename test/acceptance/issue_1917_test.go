//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	catalogpg "github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1917: the platform's own catalog store and its mcp: references. No
// tool writes the catalog yet (the connection scan and apply_knowledge do, in
// #1918 and #1920), so each criterion writes through the store the platform
// itself runs on, against the dev database, and reads back through fetch, the
// tool an agent dereferences a reference with.
//
// fetch takes one parameter, reference, a JSON string; that is the only form
// its schema admits.

// issue1917Store opens the catalog store on the dev database and removes what
// the test wrote when it ends.
func issue1917Store(t *testing.T) (*catalogpg.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return catalogpg.New(db), db
}

// issue1917Dataset is a dataset on the dev Trino connection, named uniquely so
// runs never share one.
func issue1917Dataset(t *testing.T, db *sql.DB) catalog.Ref {
	t.Helper()
	ref := catalog.DatasetRef("trino", "acme", "acme", "retail",
		fmt.Sprintf("issue1917_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(),
			"DELETE FROM catalog_entities WHERE reference = $1", ref.String())
	})
	return ref
}

// issue1917Document dereferences a reference through the fetch tool and
// returns the document it found.
func issue1917Document(t *testing.T, c *client, ref string) map[string]any {
	t.Helper()
	out := c.call("fetch", map[string]any{
		"reference": ref,
		"purpose":   "Reading a catalog entry the acceptance suite wrote for #1917.",
	})
	doc, ok := out["document"].(map[string]any)
	if out["found"] != true || !ok {
		t.Fatalf("fetch %s found no document: %v", ref, out)
	}
	return doc
}

// issue1917Fetch returns the content of the document fetch finds.
func issue1917Fetch(t *testing.T, c *client, ref string) map[string]any {
	t.Helper()
	doc := issue1917Document(t, c, ref)
	content, ok := doc["content"].(map[string]any)
	if !ok {
		t.Fatalf("fetch %s returned no content: %v", ref, doc)
	}
	return content
}

var issue1917Aspects = map[catalog.AspectName]string{
	catalog.AspectDescription:          `"Orders placed on the storefront."`,
	catalog.AspectTags:                 `["pii","finance"]`,
	catalog.AspectGlossaryTerms:        `["mcp:glossary_term:revenue"]`,
	catalog.AspectOwners:               `[{"owner":"ana@example.com","type":"technical_owner"}]`,
	catalog.AspectDomain:               `"mcp:domain:sales"`,
	catalog.AspectCustomProperties:     `{"refresh":"hourly"}`,
	catalog.AspectStructuredProperties: `{"retention_days":[90]}`,
	catalog.AspectDocumentationLinks:   `[{"url":"https://example.com/orders","description":"Runbook"}]`,
	catalog.AspectCuratedQueries:       `[{"name":"daily","sql":"SELECT count(*) FROM orders","description":"per day"}]`,
	catalog.AspectIncidents:            `[{"id":"i1","type":"operational","title":"late load","state":"active"}]`,
	catalog.AspectContextDocuments:     `[{"id":"d1","title":"Loading","content":"Loaded at 02:00 UTC","content_type":"text/markdown"}]`,
}

// TestIssue1917_DatasetReadsBackThroughFetch: a dataset written to the store
// with columns and every aspect reads back identically through fetch on its
// mcp: reference.
func TestIssue1917_DatasetReadsBackThroughFetch(t *testing.T) {
	ctx := context.Background()
	store, db := issue1917Store(t)
	ref := issue1917Dataset(t, db)

	if _, err := store.PutDataset(ctx, catalog.DatasetInput{
		Ref: ref, Type: catalog.DatasetTable, SchemaResourceID: "res-1917",
		Columns: []catalog.ColumnInput{
			{Name: "order_id", DataType: "bigint"},
			{Name: "amount", DataType: "decimal(12,2)", Nullable: true},
		},
	}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	for name, v := range issue1917Aspects {
		if _, err := store.WriteAspect(ctx, ref, name, json.RawMessage(v), "acceptance@example.com"); err != nil {
			t.Fatalf("WriteAspect %s: %v", name, err)
		}
	}
	amount := catalog.ColumnRef(ref, "amount")
	if _, err := store.WriteAspect(ctx, amount, catalog.AspectTags, json.RawMessage(`["currency"]`), "a"); err != nil {
		t.Fatalf("WriteAspect column: %v", err)
	}

	c := connect(t)
	content := issue1917Fetch(t, c, ref.String())

	if content["reference"] != ref.String() || content["type"] != "table" || content["schema_resource_id"] != "res-1917" {
		t.Fatalf("dataset identity did not read back: %v", content)
	}
	aspects, _ := content["aspects"].(map[string]any)
	for name, v := range issue1917Aspects {
		var want any
		_ = json.Unmarshal([]byte(v), &want)
		if !reflect.DeepEqual(aspects[string(name)], want) {
			t.Errorf("aspect %s: fetched %v, wrote %v", name, aspects[string(name)], want)
		}
	}
	cols, _ := content["columns"].([]any)
	if len(cols) != 2 {
		t.Fatalf("columns: %v", content["columns"])
	}
	first, _ := cols[0].(map[string]any)
	second, _ := cols[1].(map[string]any)
	if first["name"] != "order_id" || first["data_type"] != "bigint" || first["nullable"] != false {
		t.Errorf("first column: %v", first)
	}
	colAspects, _ := second["aspects"].(map[string]any)
	if second["name"] != "amount" || !reflect.DeepEqual(colAspects["tags"], []any{"currency"}) {
		t.Errorf("second column: %v", second)
	}

	// The column is its own reference.
	col := issue1917Document(t, c, amount.String())
	if col["reference"] != amount.String() || col["title"] != ref.Path[2]+".amount" {
		t.Errorf("column fetch: %v", col)
	}
}

// TestIssue1917_DefinitionByIDAndSlug: a glossary term reads back through
// fetch by the reference it is stored under and by its slug.
func TestIssue1917_DefinitionByIDAndSlug(t *testing.T) {
	ctx := context.Background()
	store, db := issue1917Store(t)
	slug := fmt.Sprintf("net-revenue-1917-%d", time.Now().UnixNano())
	term, err := store.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Slug: slug, Name: "Net revenue"})
	if err != nil {
		t.Fatalf("PutDefinition: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM catalog_entities WHERE id = $1", term.ID)
	})
	if _, err := store.WriteAspect(ctx, catalog.KeyRef(catalog.KindGlossaryTerm, slug), catalog.AspectDescription,
		json.RawMessage(`"Revenue after refunds."`), "a"); err != nil {
		t.Fatalf("WriteAspect: %v", err)
	}

	c := connect(t)
	for _, ref := range []string{term.Reference, "mcp:glossary_term:" + slug} {
		out := issue1917Document(t, c, ref)
		if out["reference"] != term.Reference || out["body"] != "Revenue after refunds." {
			t.Errorf("fetch %s: %v", ref, out)
		}
	}
}

// TestIssue1917_HistoryAndRollback: writing an aspect twice keeps both
// versions in history, and rolling back restores the earlier value, which
// fetch then returns.
func TestIssue1917_HistoryAndRollback(t *testing.T) {
	ctx := context.Background()
	store, db := issue1917Store(t)
	ref := issue1917Dataset(t, db)
	if _, err := store.PutDataset(ctx, catalog.DatasetInput{Ref: ref, Type: catalog.DatasetView}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	if _, err := store.WriteAspect(ctx, ref, catalog.AspectDescription, json.RawMessage(`"first"`), "ana"); err != nil {
		t.Fatal(err)
	}
	second, err := store.WriteAspect(ctx, ref, catalog.AspectDescription, json.RawMessage(`"second"`), "ben")
	if err != nil {
		t.Fatal(err)
	}
	hist, err := store.History(ctx, ref, catalog.AspectDescription)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: %v %v", hist, err)
	}

	c := connect(t)
	if got := issue1917Fetch(t, c, ref.String()); got["aspects"].(map[string]any)["description"] != "second" {
		t.Fatalf("before rollback: %v", got["aspects"])
	}
	if _, err := store.Rollback(ctx, second.ID, "admin"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := issue1917Fetch(t, c, ref.String()); got["aspects"].(map[string]any)["description"] != "first" {
		t.Fatalf("after rollback: %v", got["aspects"])
	}
	hist, _ = store.History(ctx, ref, catalog.AspectDescription)
	if len(hist) != 3 || hist[0].Reverts != second.ID {
		t.Fatalf("the rollback is not recorded as a change: %v", hist)
	}
}
