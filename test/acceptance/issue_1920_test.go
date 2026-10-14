//go:build integration

package acceptance

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Issue #1920: apply_knowledge writes catalog curation to the platform catalog
// and rolls it back through the catalog's history, with no DataHub involved.
// Each criterion applies through the tool an agent calls, reads the result
// back through fetch, and rolls the changeset back through apply_knowledge.
//
// Wire forms: apply_knowledge's schema types every parameter this ticket
// touches: action, entity_urn, sink, tag_urn and changeset_id are strings,
// confirm is a boolean and changes is an array of objects whose fields are all
// strings, so each admits one JSON form and is sent in it. entity_urn carries
// two reference forms inside that string, an mcp:dataset: reference and a
// DataHub dataset URN, and both are sent. set_structured_property's detail is
// read as a JSON array, a bare number or plain text, and all three are sent.

// issue1920Dataset declares a dataset with two columns on the dev Trino
// connection, named uniquely so runs never share one.
func issue1920Dataset(t *testing.T) catalog.Ref {
	t.Helper()
	store, db := issue1917Store(t)
	ref := issue1917Dataset(t, db)
	if _, err := store.PutDataset(context.Background(), catalog.DatasetInput{
		Ref: ref, Type: catalog.DatasetTable,
		Columns: []catalog.ColumnInput{{Name: "order_id", DataType: "bigint"}, {Name: "amount", DataType: "double"}},
	}); err != nil {
		t.Fatalf("PutDataset: %v", err)
	}
	return ref
}

// issue1920Apply applies changes to entity and returns the response.
func issue1920Apply(t *testing.T, c *client, entity string, changes ...map[string]any) map[string]any {
	t.Helper()
	out := c.call("apply_knowledge", map[string]any{
		"action": "apply", "entity_urn": entity, "changes": changes, "confirm": true,
	})
	if _, ok := out["changeset_id"].(string); !ok {
		t.Fatalf("apply returned no changeset: %v", out)
	}
	return out
}

// issue1920Rollback rolls a changeset back through the tool.
func issue1920Rollback(t *testing.T, c *client, id string) {
	t.Helper()
	out := c.call("apply_knowledge", map[string]any{"action": "rollback", "changeset_id": id, "confirm": true})
	if out["changeset_id"] != id {
		t.Fatalf("rollback of %s: %v", id, out)
	}
}

// issue1920Aspects fetches the curation on a dataset, or on one of its
// columns when column is set.
func issue1920Aspects(t *testing.T, c *client, ref catalog.Ref, column string) map[string]any {
	t.Helper()
	content := issue1917Fetch(t, c, ref.String())
	if column == "" {
		a, _ := content["aspects"].(map[string]any)
		return a
	}
	cols, _ := content["columns"].([]any)
	for _, raw := range cols {
		col, _ := raw.(map[string]any)
		if col["name"] == column {
			a, _ := col["aspects"].(map[string]any)
			return a
		}
	}
	t.Fatalf("column %s not in %v", column, content["columns"])
	return nil
}

// issue1920Case is one change type: what is applied, and what the fetched
// curation holds afterwards.
type issue1920Case struct {
	name    string
	column  string
	changes []map[string]any
	check   func(a map[string]any) bool
}

// issue1920Change is one entry of an apply's changes.
func issue1920Change(changeType, target, detail string) map[string]any {
	return map[string]any{"change_type": changeType, "target": target, "detail": detail}
}

var issue1920Cases = []issue1920Case{
	{name: "update_description", changes: []map[string]any{issue1920Change("update_description", "", "Orders placed online.")},
		check: func(a map[string]any) bool { return a["description"] == "Orders placed online." }},
	{name: "column update_description", column: "amount", changes: []map[string]any{issue1920Change("update_description", "column:amount", "Total in USD.")},
		check: func(a map[string]any) bool { return a["description"] == "Total in USD." }},
	{name: "batch of column descriptions", column: "order_id", changes: []map[string]any{
		issue1920Change("update_description", "column:order_id", "Order key."), issue1920Change("update_description", "column:amount", "Total."),
	}, check: func(a map[string]any) bool { return a["description"] == "Order key." }},
	{name: "add_tag", changes: []map[string]any{issue1920Change("add_tag", "", "pii"), issue1920Change("add_tag", "", "urn:li:tag:finance")},
		check: func(a map[string]any) bool { return reflect.DeepEqual(a["tags"], []any{"pii", "finance"}) }},
	{name: "column add_tag", column: "amount", changes: []map[string]any{issue1920Change("add_tag", "column:amount", "currency")},
		check: func(a map[string]any) bool { return reflect.DeepEqual(a["tags"], []any{"currency"}) }},
	{name: "add_glossary_term", changes: []map[string]any{issue1920Change("add_glossary_term", "", "Issue 1920 Net Revenue")},
		check: func(a map[string]any) bool {
			terms, _ := a["glossary_terms"].([]any)
			return len(terms) == 1 && strings.HasPrefix(terms[0].(string), "mcp:glossary_term:")
		}},
	{name: "flag_quality_issue", changes: []map[string]any{issue1920Change("flag_quality_issue", "", "amount is null for refunds")},
		check: func(a map[string]any) bool {
			incidents, _ := a["incidents"].([]any)
			return reflect.DeepEqual(a["tags"], []any{"QualityIssue"}) && len(incidents) == 1
		}},
	{name: "add_documentation", changes: []map[string]any{issue1920Change("add_documentation", "https://example.com/orders", "Runbook")},
		check: func(a map[string]any) bool {
			return reflect.DeepEqual(a["documentation_links"], []any{map[string]any{"url": "https://example.com/orders", "description": "Runbook"}})
		}},
	{name: "add_curated_query", changes: []map[string]any{{
		"change_type": "add_curated_query", "target": "", "detail": "Daily orders",
		"query_sql": "SELECT count(*) FROM orders", "query_description": "Orders per day",
	}}, check: func(a map[string]any) bool {
		return reflect.DeepEqual(a["curated_queries"], []any{map[string]any{
			"name": "Daily orders", "sql": "SELECT count(*) FROM orders", "description": "Orders per day"}})
	}},
	{name: "set_structured_property", changes: []map[string]any{
		issue1920Change("set_structured_property", "retention_days", "[90, \"days\"]"),
		issue1920Change("set_structured_property", "urn:li:structuredProperty:tier", "1"),
		issue1920Change("set_structured_property", "owner_team", "finance"),
	}, check: func(a map[string]any) bool {
		return reflect.DeepEqual(a["structured_properties"], map[string]any{
			"retention_days": []any{float64(90), "days"}, "tier": []any{float64(1)}, "owner_team": []any{"finance"}})
	}},
	{name: "raise_incident", changes: []map[string]any{issue1920Change("raise_incident", "Late load", "The 06:00 load missed.")},
		check: func(a map[string]any) bool {
			incidents, _ := a["incidents"].([]any)
			first, _ := incidents[0].(map[string]any)
			return len(incidents) == 1 && first["title"] == "Late load" && first["state"] == "active"
		}},
	{name: "set_custom_property", changes: []map[string]any{issue1920Change("set_custom_property", "refresh", "hourly")},
		check: func(a map[string]any) bool {
			return reflect.DeepEqual(a["custom_properties"], map[string]any{"refresh": "hourly"})
		}},
	{name: "add_context_document", changes: []map[string]any{{
		"change_type": "add_context_document", "target": "Loading", "detail": "Loaded nightly.", "query_description": "operations",
	}}, check: func(a map[string]any) bool {
		docs, _ := a["context_documents"].([]any)
		first, _ := docs[0].(map[string]any)
		return len(docs) == 1 && first["title"] == "Loading" && first["content"] == "Loaded nightly."
	}},
}

// TestIssue1920_EveryChangeTypeAppliesFetchesAndRollsBack: each change type
// is applied by apply_knowledge, read back with fetch, then rolled back,
// leaving the curation as it was.
func TestIssue1920_EveryChangeTypeAppliesFetchesAndRollsBack(t *testing.T) {
	c := connect(t)
	_, db := issue1917Store(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(),
			"DELETE FROM catalog_entities WHERE kind = 'glossary_term' AND slug = 'issue-1920-net-revenue'")
	})
	for _, tc := range issue1920Cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := issue1920Dataset(t)
			before := issue1920Aspects(t, c, ref, tc.column)
			out := issue1920Apply(t, c, ref.String(), tc.changes...)
			if out["revertible"] != true || out["entity_urn"] != ref.String() {
				t.Fatalf("apply response: %v", out)
			}
			if got := issue1920Aspects(t, c, ref, tc.column); !tc.check(got) {
				t.Fatalf("fetched after apply: %v", got)
			}
			issue1920Rollback(t, c, out["changeset_id"].(string))
			if got := issue1920Aspects(t, c, ref, tc.column); !reflect.DeepEqual(got, before) {
				t.Fatalf("after rollback %v, before apply %v", got, before)
			}
		})
	}
}

// TestIssue1920_ChangesOnCreatedEntries: resolve_incident, the removals and
// update_context_document act on what an earlier apply created, by the ids it
// reported, and roll back too.
func TestIssue1920_ChangesOnCreatedEntries(t *testing.T) {
	c := connect(t)
	ref := issue1920Dataset(t)
	created := issue1920Apply(t, c, ref.String(),
		issue1920Change("raise_incident", "Late load", "missed"),
		issue1920Change("add_context_document", "Loading", "v1"),
		issue1920Change("add_tag", "", "stale"),
		issue1920Change("set_custom_property", "refresh", "hourly"),
		issue1920Change("set_structured_property", "tier", "1"))
	ids, _ := created["created_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("created_ids: %v", created)
	}
	incident, doc := ids[0].(string), ids[1].(string)
	before := issue1920Aspects(t, c, ref, "")

	out := issue1920Apply(t, c, ref.String(),
		issue1920Change("resolve_incident", incident, "Backfilled."),
		map[string]any{"change_type": "update_context_document", "target": doc, "detail": "v2", "query_sql": "Loading (nightly)"},
		issue1920Change("remove_tag", "", "stale"),
		issue1920Change("remove_custom_property", "refresh", ""),
		issue1920Change("remove_structured_property", "tier", ""))
	a := issue1920Aspects(t, c, ref, "")
	incidents, _ := a["incidents"].([]any)
	first, _ := incidents[0].(map[string]any)
	docs, _ := a["context_documents"].([]any)
	d, _ := docs[0].(map[string]any)
	if first["state"] != "resolved" || first["resolution"] != "Backfilled." || d["content"] != "v2" ||
		d["title"] != "Loading (nightly)" || a["tags"] != nil || a["custom_properties"] != nil || a["structured_properties"] != nil {
		t.Fatalf("fetched after the edits: %v", a)
	}
	issue1920Rollback(t, c, out["changeset_id"].(string))
	if got := issue1920Aspects(t, c, ref, ""); !reflect.DeepEqual(got, before) {
		t.Fatalf("after rollback %v, want %v", got, before)
	}

	removed := issue1920Apply(t, c, ref.String(), issue1920Change("remove_context_document", doc, ""))
	if a := issue1920Aspects(t, c, ref, ""); a["context_documents"] != nil {
		t.Fatalf("document not removed: %v", a)
	}
	issue1920Rollback(t, c, removed["changeset_id"].(string))
	if a := issue1920Aspects(t, c, ref, ""); a["context_documents"] == nil {
		t.Fatalf("document not restored: %v", a)
	}
}

// TestIssue1920_DataHubURNAndTagForms: a DataHub dataset URN is read as the
// catalog dataset with the same path, the changeset is recorded under the
// catalog reference, and delete_tag and bulk_untag take a tag off every
// entity carrying it and roll back.
func TestIssue1920_DataHubURNAndTagForms(t *testing.T) {
	c := connect(t)
	ref := issue1920Dataset(t)
	urn := "urn:li:dataset:(urn:li:dataPlatform:trino," + ref.PathString() + ",PROD)"
	tag := "issue1920-" + ref.Path[2]

	out := issue1920Apply(t, c, urn, issue1920Change("add_tag", "", tag), issue1920Change("add_tag", "column:amount", tag))
	if out["entity_urn"] != ref.String() {
		t.Fatalf("changeset not recorded under the catalog reference: %v", out)
	}
	listed := c.call("apply_knowledge", map[string]any{"action": "list_changesets", "entity_urn": urn})
	if n, _ := listed["total"].(float64); n < 1 {
		t.Fatalf("list_changesets by DataHub URN: %v", listed)
	}

	deleted := issue1920Apply(t, c, "mcp:tag:"+tag, issue1920Change("delete_tag", "", ""))
	if a := issue1920Aspects(t, c, ref, "amount"); a["tags"] != nil {
		t.Fatalf("delete_tag left the column tagged: %v", a)
	}
	issue1920Rollback(t, c, deleted["changeset_id"].(string))
	if a := issue1920Aspects(t, c, ref, ""); !reflect.DeepEqual(a["tags"], []any{tag}) {
		t.Fatalf("rollback of delete_tag: %v", a)
	}

	untag := c.call("apply_knowledge", map[string]any{"action": "bulk_untag", "tag_urn": "urn:li:tag:" + tag, "confirm": true})
	if untag["entities_untagged"] != float64(2) || untag["revertible"] != true {
		t.Fatalf("bulk_untag: %v", untag)
	}
	issue1920Rollback(t, c, untag["changeset_id"].(string))
	if a := issue1920Aspects(t, c, ref, "amount"); !reflect.DeepEqual(a["tags"], []any{tag}) {
		t.Fatalf("rollback of bulk_untag: %v", a)
	}
}

// TestIssue1920_RollbackRefusedAfterANewerWrite: a rollback is refused, and
// reverts nothing, when an aspect the changeset wrote has been written again.
func TestIssue1920_RollbackRefusedAfterANewerWrite(t *testing.T) {
	c := connect(t)
	ref := issue1920Dataset(t)
	older := issue1920Apply(t, c, ref.String(), issue1920Change("update_description", "", "first"), issue1920Change("add_tag", "", "a"))
	issue1920Apply(t, c, ref.String(), issue1920Change("add_tag", "", "b"))

	res, text, err := c.callRaw("apply_knowledge", map[string]any{
		"action": "rollback", "changeset_id": older["changeset_id"], "confirm": true,
	})
	if err != nil || !res.IsError || !strings.Contains(text, "rollback blocked") || !strings.Contains(text, "tags of "+ref.String()) {
		t.Fatalf("rollback after a newer write: err=%v text=%s", err, text)
	}
	a := issue1920Aspects(t, c, ref, "")
	if a["description"] != "first" || !reflect.DeepEqual(a["tags"], []any{"a", "b"}) {
		t.Fatalf("the refused rollback changed the dataset: %v", a)
	}
}
