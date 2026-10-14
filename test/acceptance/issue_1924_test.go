//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	catalogpg "github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1924: the datahub_* tools are gone, with no aliases, and every read
// they served is answered by search and fetch over the platform catalog. One
// criterion per read: the entity, its schema, its lineage, a glossary term,
// search, and browse (a path, a glossary node, a connection). The catalog is
// written through the store the platform runs on, against the dev database,
// and read back through a real MCP client.
//
// Wire forms: fetch takes one parameter, reference, a JSON string; search's
// intent is a string and sources an array of strings. Each admits one form and
// is sent in it.

// issue1924Fixture is a glossary node with a term under it, and a dataset with
// columns, a description, the term on a column and an upstream edge.
type issue1924Fixture struct {
	dataset  catalog.Ref
	upstream string
	node     *catalog.Definition
	term     *catalog.Definition
}

// issue1924Setup writes the fixture and removes it when the test ends.
func issue1924Setup(t *testing.T) issue1924Fixture {
	t.Helper()
	ctx := context.Background()
	store, db := issue1917Store(t)
	ref := issue1917Dataset(t, db)
	suffix := ref.Path[len(ref.Path)-1]
	f := issue1924Fixture{dataset: ref, upstream: catalog.DatasetRef("trino", "acme", "acme", "raw", suffix+"_src").String()}

	if _, err := store.PutDataset(ctx, catalog.DatasetInput{
		Ref: ref, Type: catalog.DatasetTable,
		Columns: []catalog.ColumnInput{{Name: "order_id", DataType: "bigint"}, {Name: "amount", DataType: "double"}},
	}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	f.node = issue1924Definition(t, store, db, catalog.DefinitionInput{Kind: catalog.KindGlossaryNode, Name: "Issue 1924 node " + suffix})
	f.term = issue1924Definition(t, store, db, catalog.DefinitionInput{
		Kind: catalog.KindGlossaryTerm, Name: "Issue 1924 term " + suffix, Parent: f.node.Reference,
	})
	for _, w := range []struct {
		ref    catalog.Ref
		aspect catalog.AspectName
		value  string
	}{
		{ref, catalog.AspectDescription, `"Orders placed on the storefront."`},
		{catalog.ColumnRef(ref, "amount"), catalog.AspectGlossaryTerms, `["` + f.term.Reference + `"]`},
		{catalog.KeyRef(catalog.KindGlossaryTerm, f.term.ID), catalog.AspectDescription, `"Money received for an order."`},
	} {
		if _, err := store.WriteAspect(ctx, w.ref, w.aspect, json.RawMessage(w.value), "acceptance@example.com"); err != nil {
			t.Fatalf("WriteAspect %s on %s: %v", w.aspect, w.ref, err)
		}
	}
	if err := store.AddEdges(ctx, []catalog.Edge{{
		Source: f.upstream, Target: ref.String(), WriterKind: catalog.WriterSQL,
		WrittenBy: "acceptance@example.com", WrittenAt: time.Now(),
	}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(),
			"DELETE FROM catalog_lineage WHERE target_ref = $1", ref.String())
	})
	return f
}

// issue1924Definition creates a definition and removes it when the test ends,
// children before their node.
func issue1924Definition(t *testing.T, store *catalogpg.Store, db *sql.DB, in catalog.DefinitionInput) *catalog.Definition {
	t.Helper()
	def, err := store.PutDefinition(context.Background(), in)
	if err != nil {
		t.Fatalf("PutDefinition %s: %v", in.Name, err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM catalog_entities WHERE reference = $1", def.Reference)
	})
	return def
}

// TestIssue1924_NoDataHubToolIsListed: tools/list carries no datahub_ tool,
// and the two tools that replace them are there.
func TestIssue1924_NoDataHubToolIsListed(t *testing.T) {
	c := connect(t)
	names := map[string]bool{}
	for _, tool := range c.tools() {
		names[tool.Name] = true
		if strings.HasPrefix(tool.Name, "datahub_") {
			t.Errorf("tools/list still carries %s", tool.Name)
		}
	}
	if !names["search"] || !names["fetch"] {
		t.Fatalf("tools/list lacks search or fetch: %v", names)
	}
}

// TestIssue1924_FetchReadsTheEntity: fetch on a dataset reference returns its
// curation, the read datahub_get_entity served.
func TestIssue1924_FetchReadsTheEntity(t *testing.T) {
	f := issue1924Setup(t)
	content := issue1917Fetch(t, connect(t), f.dataset.String())
	if a, _ := content["aspects"].(map[string]any); a["description"] != "Orders placed on the storefront." {
		t.Fatalf("fetch of %s: aspects %v", f.dataset, content["aspects"])
	}
}

// TestIssue1924_FetchReadsTheSchema: the same document carries the columns
// with their types and their own curation, the read datahub_get_schema served.
func TestIssue1924_FetchReadsTheSchema(t *testing.T) {
	f := issue1924Setup(t)
	content := issue1917Fetch(t, connect(t), f.dataset.String())
	types := map[string]any{}
	var amountTerms any
	cols, _ := content["columns"].([]any)
	for _, raw := range cols {
		col, _ := raw.(map[string]any)
		types[fmt.Sprint(col["name"])] = col["data_type"]
		if col["name"] == "amount" {
			a, _ := col["aspects"].(map[string]any)
			amountTerms = a["glossary_terms"]
		}
	}
	if types["order_id"] != "bigint" || types["amount"] != "double" {
		t.Fatalf("fetch of %s: column types %v", f.dataset, types)
	}
	if terms, _ := amountTerms.([]any); len(terms) != 1 || terms[0] != f.term.Reference {
		t.Fatalf("column amount carries %v, want [%s]", amountTerms, f.term.Reference)
	}
}

// TestIssue1924_FetchReadsTheLineage: the dataset document carries the edges
// upstream of it, the read datahub_get_lineage served.
func TestIssue1924_FetchReadsTheLineage(t *testing.T) {
	f := issue1924Setup(t)
	content := issue1917Fetch(t, connect(t), f.dataset.String())
	up, _ := content["upstream"].([]any)
	if len(up) != 1 || up[0].(map[string]any)["source"] != f.upstream {
		t.Fatalf("fetch of %s: upstream %v, want the edge from %s", f.dataset, up, f.upstream)
	}
}

// TestIssue1924_FetchReadsAGlossaryTerm: fetch on a term returns its
// definition, its node and the columns carrying it, the read
// datahub_get_glossary_term served. Its slug reads the same term.
func TestIssue1924_FetchReadsAGlossaryTerm(t *testing.T) {
	f := issue1924Setup(t)
	c := connect(t)
	for _, ref := range []string{f.term.Reference, catalog.KeyRef(catalog.KindGlossaryTerm, f.term.Slug).String()} {
		doc := issue1917Fetch(t, c, ref)
		if doc["name"] != f.term.Name || doc["description"] != "Money received for an order." || doc["parent"] != f.node.Reference {
			t.Fatalf("fetch of %s: %v", ref, doc)
		}
		carriers, _ := doc["carriers"].([]any)
		if len(carriers) != 1 || carriers[0].(map[string]any)["reference"] != catalog.ColumnRef(f.dataset, "amount").String() {
			t.Fatalf("fetch of %s: carriers %v", ref, carriers)
		}
	}
}

// TestIssue1924_SearchFindsTheTerm: search on the term's name returns its
// reference, the read datahub_search served for definitions.
func TestIssue1924_SearchFindsTheTerm(t *testing.T) {
	f := issue1924Setup(t)
	out := connect(t).call("search", map[string]any{
		"intent":  f.term.Name,
		"sources": []any{"governance"},
		"purpose": "Acceptance: finding a glossary term by name.",
	})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), f.term.Reference) {
		t.Fatalf("search for %q did not return %s:\n%s", f.term.Name, f.term.Reference, raw)
	}
}

// TestIssue1924_FetchBrowses: fetch on a path above datasets lists what is
// under it, on a glossary node its entries, and on a connection the top of its
// catalog, the reads datahub_browse served.
func TestIssue1924_FetchBrowses(t *testing.T) {
	f := issue1924Setup(t)
	c := connect(t)

	schema := catalog.DatasetRef("trino", "acme", f.dataset.Path[:len(f.dataset.Path)-1]...).String()
	if !issue1924Lists(issue1917Fetch(t, c, schema)["entries"], f.dataset.String()) {
		t.Fatalf("fetch of %s does not list %s", schema, f.dataset)
	}
	if !issue1924Lists(issue1917Fetch(t, c, f.node.Reference)["children"], f.term.Reference) {
		t.Fatalf("fetch of %s does not list %s", f.node.Reference, f.term.Reference)
	}
	top := catalog.DatasetRef("trino", "acme", f.dataset.Path[0]).String()
	if !issue1924Lists(issue1917Fetch(t, c, "mcp:connection:(trino,acme)")["catalog"], top) {
		t.Fatalf("fetch of the acme connection does not list %s", top)
	}
}

// issue1924Lists reports whether a listing holds an entry with the reference.
func issue1924Lists(listing any, ref string) bool {
	entries, _ := listing.([]any)
	for _, raw := range entries {
		if e, _ := raw.(map[string]any); e["reference"] == ref {
			return true
		}
	}
	return false
}

// TestIssue1924_DataHubURNIsNotAReference: a DataHub URN dereferences to
// nothing; the catalog's own references are the only form it reads.
func TestIssue1924_DataHubURNIsNotAReference(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("fetch", map[string]any{
		"reference": "urn:li:dataset:(urn:li:dataPlatform:trino,acme.retail.orders,PROD)",
		"purpose":   "Acceptance: a DataHub URN is not a reference.",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !res.IsError && strings.Contains(text, `"found":true`) {
		t.Fatalf("fetch of a DataHub URN found a document: %s", text)
	}
}
