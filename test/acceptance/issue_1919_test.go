//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1919: the platform catalog is the semantic layer. Each criterion
// catalogs a memory-catalog table with a scan, writes curation through the
// store the platform runs on, and reads it back through the tools an agent
// uses: trino_describe_table's enrichment and search.
//
// Wire forms: trino_describe_table's `connection`, `catalog`, `schema` and
// `table` and search's `intent` are typed string and `sources` an array of
// strings, each sent in that one form.

// indexTimeout bounds how long a criterion waits for the catalog index to
// read a change. The dev stack checks every 15 seconds.
const indexTimeout = 3 * time.Minute

// curated1919 catalogs a table and writes a description, a tag, a glossary
// term and an owner on it, returning the table and the term's name.
func curated1919(t *testing.T, c *client, description string) (schema, termName string) {
	t.Helper()
	schema = schema1918(t, c, "1919", "orders")
	exec1918(c, "CREATE TABLE memory."+schema+".orders (id bigint, amount double)")
	scan1918(t, c)

	store, db := issue1917Store(t)
	ctx := t.Context()
	termName = "Acceptance revenue " + schema
	term, err := store.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: termName})
	if err != nil {
		t.Fatalf("PutDefinition: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM catalog_entities WHERE id = $1", term.ID) })
	ref, err := catalog.ParseRef(ref1918(schema, "orders"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[catalog.AspectName]string{
		catalog.AspectDescription:   fmt.Sprintf("%q", description),
		catalog.AspectTags:          `["acceptance-1919"]`,
		catalog.AspectGlossaryTerms: fmt.Sprintf("[%q]", term.Reference),
		catalog.AspectOwners:        `[{"owner":"owner-1919@example.com","type":"technical_owner"}]`,
	} {
		if _, err := store.WriteAspect(ctx, ref, name, json.RawMessage(value), "acceptance@example.com"); err != nil {
			t.Fatalf("WriteAspect %s: %v", name, err)
		}
	}
	return schema, termName
}

// TestIssue1919_DescribeCarriesTheCatalogsCuration: trino_describe_table
// through a real client returns the catalog's description, tags, terms and
// owners in its enrichment.
func TestIssue1919_DescribeCarriesTheCatalogsCuration(t *testing.T) {
	c := connectFor(t, scanTimeout+time.Minute)
	description := "Storefront orders, curated for acceptance 1919."
	schema, termName := curated1919(t, c, description)

	res, text, err := c.callRaw("trino_describe_table", map[string]any{
		"connection": conn1918, "catalog": "memory", "schema": schema, "table": "orders",
		"purpose": "Acceptance: reading a table's catalog context.",
	})
	if err != nil || res.IsError {
		t.Fatalf("trino_describe_table: %v %s", err, text)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{description, "acceptance-1919", termName, "owner-1919@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("the enrichment does not carry %q:\n%s", want, got)
		}
	}
}

// TestIssue1919_SearchReturnsAChangedDescription: a description changed in the
// catalog is returned by search on the next index pass, with no manual
// reindex.
func TestIssue1919_SearchReturnsAChangedDescription(t *testing.T) {
	c := connectFor(t, scanTimeout+indexTimeout+time.Minute)
	word := fmt.Sprintf("quillback%d", time.Now().UnixNano())
	schema, _ := curated1919(t, c, "Orders whose settlement code is "+word+".")
	ref := ref1918(schema, "orders")

	deadline := time.Now().Add(indexTimeout)
	for time.Now().Before(deadline) {
		out := c.call("search", map[string]any{
			"intent": "settlement code " + word, "sources": []any{"catalog"},
			"purpose": "Acceptance: finding a dataset by its changed description.",
		})
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), ref) {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("search did not return %s within %s of its description changing", ref, indexTimeout)
}
