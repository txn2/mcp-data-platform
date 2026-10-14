//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1921: the portal's Catalog section reads and curates the platform
// catalog. Each criterion runs as the dev stack's ordinary signed-in person
// (the collaborator persona, which grants fetch and apply_knowledge, and is
// not an administrator) through the routes the portal's pages call, and reads
// the result back through those routes and through fetch, the tool an agent
// reads the same catalog with.
//
// Wire forms: the aspect write's value is untyped JSON that takes the
// aspect's own shape, so it is sent as a string (description), an array of
// strings (tags, glossary_terms) and null (clearing); a definition is created
// with a body of strings. fetch takes one parameter, reference, a JSON
// string.

// issue1921Dataset declares a dataset with two columns on the dev Trino
// connection, named uniquely so runs never share one, and removes the
// definitions the test creates when it ends.
func issue1921Dataset(t *testing.T) (catalog.Ref, *sql.DB) {
	t.Helper()
	store, db := issue1917Store(t)
	ref := issue1917Dataset(t, db)
	if _, err := store.PutDataset(context.Background(), catalog.DatasetInput{
		Ref: ref, Type: catalog.DatasetTable,
		Columns: []catalog.ColumnInput{{Name: "order_id", DataType: "bigint"}, {Name: "amount", DataType: "double"}},
	}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	return ref, db
}

// issue1921Write writes one aspect through the portal route and fails on any
// status but 200.
func issue1921Write(t *testing.T, c *client, ref, aspect string, value any) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodPut, "/api/v1/portal/catalog/aspect",
		jsonBody(t, map[string]any{"reference": ref, "aspect": aspect, "value": value}))
	if status != http.StatusOK {
		t.Fatalf("PUT aspect %s on %s: %d %v", aspect, ref, status, out)
	}
	return out
}

// issue1921Get reads one portal catalog route and fails on any status but 200.
func issue1921Get(t *testing.T, c *client, path string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d %v", path, status, out)
	}
	return out
}

// issue1921Column returns one column of a dataset as a read returned it.
func issue1921Column(t *testing.T, dataset map[string]any, name string) map[string]any {
	t.Helper()
	cols, _ := dataset["columns"].([]any)
	for _, raw := range cols {
		col, _ := raw.(map[string]any)
		if col["name"] == name {
			return col
		}
	}
	t.Fatalf("column %s not in %v", name, dataset["columns"])
	return nil
}

// TestIssue1921_FindEditAndSeeHistory: a signed-in person finds a table by
// name, edits its description and a column's tags, sees both on the table and
// in its history, and rolls the description back.
func TestIssue1921_FindEditAndSeeHistory(t *testing.T) {
	ref, _ := issue1921Dataset(t)
	c := connectAs(t, devOwnerAPIKey)
	name := ref.Path[len(ref.Path)-1]

	found := issue1921Get(t, c, "/api/v1/portal/catalog/datasets?q="+url.QueryEscape(name))
	results, _ := found["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["reference"] != ref.String() {
		t.Fatalf("search for %s found %v, want exactly %s", name, results, ref)
	}

	dsPath := "/api/v1/portal/catalog/dataset?ref=" + url.QueryEscape(ref.String())
	if ds := issue1921Get(t, c, dsPath); ds["writable"] != true {
		t.Fatalf("the collaborator persona grants apply_knowledge, but the dataset reads writable=%v", ds["writable"])
	}

	issue1921Write(t, c, ref.String(), "description", "Orders placed on the storefront.")
	col := catalog.ColumnRef(ref, "amount").String()
	issue1921Write(t, c, col, "tags", []string{"currency", "finance"})

	ds := issue1921Get(t, c, dsPath)
	if a, _ := ds["aspects"].(map[string]any); a["description"] != "Orders placed on the storefront." {
		t.Fatalf("description after the edit: %v", ds["aspects"])
	}
	if a, _ := issue1921Column(t, ds, "amount")["aspects"].(map[string]any); !reflect.DeepEqual(a["tags"], []any{"currency", "finance"}) {
		t.Fatalf("column tags after the edit: %v", a)
	}

	history := issue1921Get(t, c, "/api/v1/portal/catalog/history?ref="+url.QueryEscape(ref.String()))
	changes, _ := history["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("history of %s: %v, want the one description change", ref, changes)
	}
	change, _ := changes[0].(map[string]any)
	if change["aspect"] != "description" || change["written_by"] != devOwnerEmail || change["value"] != "Orders placed on the storefront." {
		t.Fatalf("history entry: %v", change)
	}
	colHistory := issue1921Get(t, c, "/api/v1/portal/catalog/history?ref="+url.QueryEscape(col))
	if ch, _ := colHistory["changes"].([]any); len(ch) != 1 {
		t.Fatalf("column history: %v, want the one tags change", ch)
	}

	status, out := c.rest(http.MethodPost, "/api/v1/portal/catalog/rollback", jsonBody(t, map[string]any{
		"reference": ref.String(), "aspect": "description", "change_id": change["id"],
	}))
	if status != http.StatusOK || out["reverts"] != change["id"] {
		t.Fatalf("rollback: %d %v", status, out)
	}
	if a, _ := issue1921Get(t, c, dsPath)["aspects"].(map[string]any); a["description"] != nil {
		t.Fatalf("description after the rollback: %v, want none", a["description"])
	}
}

// TestIssue1921_ClearsAnAspectWithNull: a null value clears an aspect, the
// third form the untyped value admits.
func TestIssue1921_ClearsAnAspectWithNull(t *testing.T) {
	ref, _ := issue1921Dataset(t)
	c := connectAs(t, devOwnerAPIKey)
	issue1921Write(t, c, ref.String(), "tags", []string{"pii"})
	issue1921Write(t, c, ref.String(), "tags", nil)
	ds := issue1921Get(t, c, "/api/v1/portal/catalog/dataset?ref="+url.QueryEscape(ref.String()))
	if a, _ := ds["aspects"].(map[string]any); a["tags"] != nil {
		t.Fatalf("tags after writing null: %v", a["tags"])
	}
}

// TestIssue1921_GlossaryTermFromThePortalReachesFetch: a glossary term created
// in the portal and applied to a column is what fetch returns for the column's
// dataset and for the term.
func TestIssue1921_GlossaryTermFromThePortalReachesFetch(t *testing.T) {
	ref, db := issue1921Dataset(t)
	c := connectAs(t, devOwnerAPIKey)

	status, def := c.rest(http.MethodPost, "/api/v1/portal/catalog/definitions", jsonBody(t, map[string]any{
		"kind": "glossary_term", "name": "Issue 1921 " + ref.Path[len(ref.Path)-1], "description": "Money received for an order.",
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating the term: %d %v", status, def)
	}
	term, _ := def["reference"].(string)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM catalog_entities WHERE reference = $1", term)
	})

	col := catalog.ColumnRef(ref, "amount").String()
	issue1921Write(t, c, col, "glossary_terms", []string{term})

	content := issue1917Fetch(t, c, ref.String())
	if a, _ := issue1921Column(t, content, "amount")["aspects"].(map[string]any); !reflect.DeepEqual(a["glossary_terms"], []any{term}) {
		t.Fatalf("fetch of %s: column amount carries %v, want [%s]", ref, a["glossary_terms"], term)
	}

	termDoc := issue1917Fetch(t, c, term)
	if termDoc["description"] != "Money received for an order." {
		t.Fatalf("fetch of %s: %v", term, termDoc)
	}
	carriers, _ := termDoc["carriers"].([]any)
	if len(carriers) != 1 || carriers[0].(map[string]any)["reference"] != col {
		t.Fatalf("fetch of %s: carriers %v, want the column %s", term, carriers, col)
	}

	view := issue1921Get(t, c, "/api/v1/portal/catalog/definition?ref="+url.QueryEscape(term))
	if uses, _ := view["uses"].([]any); len(uses) != 1 || uses[0].(map[string]any)["column"] != "amount" {
		t.Fatalf("the term's page lists %v, want the amount column", view["uses"])
	}

	// A term a column carries is not deleted out from under it.
	status, out := c.rest(http.MethodDelete, "/api/v1/portal/catalog/definition?ref="+url.QueryEscape(term), nil)
	if status != http.StatusConflict {
		t.Fatalf("deleting a term in use: %d %v, want 409", status, out)
	}
}

// TestIssue1921_CurationIsHeldToTheConnectionsAPersonaReaches: a persona that
// reaches one connection curates that connection's datasets and is refused on
// another's, through the portal route and through apply_knowledge alike, so
// neither surface changes more than the other.
func TestIssue1921_CurationIsHeldToTheConnectionsAPersonaReaches(t *testing.T) {
	ref, _ := issue1921Dataset(t) // on the dev Trino connection "acme"
	admin := connect(t)

	elsewhere := connectAs(t, member1918(t, admin, "acme-scratch"))
	status, out := elsewhere.rest(http.MethodPut, "/api/v1/portal/catalog/aspect",
		jsonBody(t, map[string]any{"reference": ref.String(), "aspect": "tags", "value": []string{"x"}}))
	if status != http.StatusNotFound {
		t.Fatalf("the portal wrote a dataset the persona does not reach: %d %v", status, out)
	}
	res, text, err := elsewhere.callRaw("apply_knowledge", map[string]any{
		"action": "apply", "entity_urn": ref.String(), "confirm": true,
		"changes": []map[string]any{{"change_type": "add_tag", "target": "", "detail": "x"}},
	})
	if err != nil {
		t.Fatalf("apply_knowledge: transport error: %v", err)
	}
	if !res.IsError || !strings.Contains(text, "on a connection your persona reaches") {
		t.Fatalf("apply_knowledge wrote a dataset the persona does not reach: %s", text)
	}

	reaching := connectAs(t, member1918(t, admin, "acme"))
	issue1921Write(t, reaching, ref.String(), "tags", []string{"portal"})
	out = reaching.call("apply_knowledge", map[string]any{
		"action": "apply", "entity_urn": ref.String(), "confirm": true,
		"changes": []map[string]any{{"change_type": "add_tag", "target": "", "detail": "mcp"}},
	})
	if _, ok := out["changeset_id"].(string); !ok {
		t.Fatalf("apply_knowledge refused a dataset the persona reaches: %v", out)
	}
	ds := issue1921Get(t, admin, "/api/v1/portal/catalog/dataset?ref="+url.QueryEscape(ref.String()))
	if a, _ := ds["aspects"].(map[string]any); !reflect.DeepEqual(a["tags"], []any{"portal", "mcp"}) {
		t.Fatalf("tags after both writes: %v", a)
	}
}
