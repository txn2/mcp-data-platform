//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Issue #1832: an empty list reaches a caller as [], never null. These
// criteria read a collection with an empty section, and one with no sections,
// through manage_asset on a real MCP client and through the portal REST route
// the collection page reads, and run a managed script that files a report into
// the empty section.
//
// Wire forms: manage_asset's action and collection_id are typed strings, name
// a string, and sections an array of objects, so each admits one JSON form
// and is sent as a literal tools/call parameter of it. run_script's name is a
// string, args an object and wait_seconds an integer. The REST route takes no
// body.

// collection1832 creates a collection owned by c, with the sections given,
// and returns its id.
func collection1832(t *testing.T, c *client, name string, sections []any) string {
	t.Helper()
	args := map[string]any{"action": "create_collection", "name": name, "description": "Acceptance #1832."}
	if sections != nil {
		args["sections"] = sections
	}
	out := c.call("manage_asset", args)
	id, _ := out["collection_id"].(string)
	if id == "" {
		t.Fatalf("create_collection returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": id})
	})
	return id
}

// rawField1832 is the JSON text of one key of a JSON object, so a criterion
// can tell [] from null, which a decoded value cannot.
func rawField1832(t *testing.T, body []byte, key string) string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, body)
	}
	return string(obj[key])
}

// sectionItems1832 is the JSON text of each section's items.
func sectionItems1832(t *testing.T, sectionsRaw string) []string {
	t.Helper()
	var sections []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(sectionsRaw), &sections); err != nil {
		t.Fatalf("sections is not a list: %v\n%s", err, sectionsRaw)
	}
	out := make([]string, 0, len(sections))
	for _, s := range sections {
		out = append(out, string(s["items"]))
	}
	return out
}

func TestIssue1832_AnEmptySectionAndNoSectionsAreEmptyListsOverMCPAndREST(t *testing.T) {
	c := connect(t)
	withEmpty := collection1832(t, c, "acceptance-1832-empty-section-"+unique1579(),
		[]any{map[string]any{"title": "Monthly reports", "items": []any{}}})
	none := collection1832(t, c, "acceptance-1832-no-sections-"+unique1579(), nil)

	for _, tc := range []struct {
		id, want string
	}{
		{withEmpty, `[[]]`},
		{none, ``},
	} {
		_, text, err := c.callRaw("manage_asset", map[string]any{"action": "get_collection", "collection_id": tc.id})
		if err != nil {
			t.Fatalf("get_collection: %v", err)
		}
		sections := rawField1832(t, []byte(text), "sections")
		if tc.want == `` {
			if sections != `[]` {
				t.Errorf("MCP: a collection with no sections must carry \"sections\": [], got %s", sections)
			}
		} else if items := sectionItems1832(t, sections); len(items) != 1 || items[0] != `[]` {
			t.Errorf("MCP: an empty section must carry \"items\": [], got %v", items)
		}

		status, body := c.restText("/api/v1/portal/collections/" + tc.id)
		if status != http.StatusOK {
			t.Fatalf("GET collection answered %d: %s", status, body)
		}
		sections = rawField1832(t, []byte(body), "sections")
		if tc.want == `` {
			if sections != `[]` {
				t.Errorf("REST: a collection with no sections must carry \"sections\": [], got %s", sections)
			}
		} else if items := sectionItems1832(t, sections); len(items) != 1 || items[0] != `[]` {
			t.Errorf("REST: an empty section must carry \"items\": [], got %v", items)
		}
	}
}

// file1832 is a script that reads a collection and files an asset into its
// first section, carrying the items each section already holds.
const file1832 = `def main():
    """Files the report into the collection's first section."""
    coll = platform.call("manage_asset", {"action": "get_collection", "collection_id": run.params["collection_id"]})
    sections = []
    for sec in coll["sections"]:
        items = [{"asset_id": it["asset_id"]} for it in sec.get("items", [])]
        if not sections:
            items.append({"asset_id": run.params["asset_id"]})
        sections.append({"title": sec["title"], "items": items})
    platform.call("manage_asset", {"action": "set_sections", "collection_id": run.params["collection_id"], "sections": sections})
    platform.result({"sections": len(sections)})
`

func TestIssue1832_AScriptFilesIntoAnEmptySectionOnItsFirstRun(t *testing.T) {
	c := connect(t)
	id := unique1579()
	coll := collection1832(t, c, "acceptance-1832-filing-"+id,
		[]any{map[string]any{"title": "Monthly reports", "items": []any{}}})
	saved := c.call("save_asset", map[string]any{
		"name": "acceptance-1832-report-" + id, "content": "# September\n", "content_type": "text/markdown",
		"description": "Acceptance #1832: the report a script files.",
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })

	name := "acceptance-1832-file-" + id
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	c.seedPreGate(t, map[string]any{
		"command": "create", "name": name, "source": file1832,
		"description": "Acceptance #1832: files a report into a collection's empty section.",
		"params": []any{
			map[string]any{"name": "collection_id", "type": "string", "required": true, "description": "The collection."},
			map[string]any{"name": "asset_id", "type": "string", "required": true, "description": "The report."},
		},
	})

	run := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"collection_id": coll, "asset_id": assetID}, "wait_seconds": 120,
	})
	if run["status"] != "succeeded" {
		t.Fatalf("the script's first run did not succeed: %v", run)
	}
	_, text, err := c.callRaw("manage_asset", map[string]any{"action": "get_collection", "collection_id": coll})
	if err != nil || !strings.Contains(text, assetID) {
		t.Errorf("the report was not filed into the section: %v %s", err, text)
	}
}
