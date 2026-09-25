//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1875: an agent saved an HTML report whose body linked a CSV asset as
// <a href="mcp:asset:<id>">, passed no references, and the save succeeded
// silently. The link goes nowhere: a reference loads a file's bytes, it cannot
// be followed, and nothing reported the undeclared reference either.
//
// What these hold, against the running platform: save_asset, manage_asset
// update and manage_asset patch refuse a body that uses a reference as a link
// target (an HTML <a href> and a Markdown [text](ref) link), naming it and
// writing nothing; the same three calls save a body that loads an undeclared
// reference and return it as undeclared_references; and both tools' references
// descriptions say a reference loads content and is not a link.
//
// Wire forms: save_asset's `content`, `name` and `content_type` and
// manage_asset's `action`, `asset_id` and `content` are typed string and sent
// as strings; `references` is typed array of strings and sent as an array;
// patch `edits` is typed array of objects and sent as one; `dry_run` is typed
// boolean and sent as one. Each is the one form its schema admits.

// save1875 saves one asset and returns the tool's result and text, leaving the
// verdict to the caller.
func save1875(t *testing.T, c *client, name, contentType, content string) (bool, string) {
	t.Helper()
	res, text, err := c.callRaw("save_asset", map[string]any{
		"name": name, "content_type": contentType, "content": content,
	})
	if err != nil {
		t.Fatalf("save_asset: transport error: %v", err)
	}
	return res.IsError, text
}

// dataAsset1875 saves the CSV the report points at and returns its reference.
func dataAsset1875(t *testing.T, c *client, stamp string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name": "acc-1875-data-" + stamp, "content_type": "text/csv", "content": "region,total\nwest,4\n",
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	deleteAfter1875(t, c, id)
	return "mcp:asset:" + id
}

func deleteAfter1875(t *testing.T, c *client, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
}

// assetsNamed1875 counts the caller's assets named exactly name. The list
// action takes no name filter, so the listing is read and matched here.
func assetsNamed1875(t *testing.T, c *client, name string) int {
	t.Helper()
	out := c.call("manage_asset", map[string]any{"action": "list", "limit": 200})
	assets, _ := out["assets"].([]any)
	n := 0
	for _, a := range assets {
		if row, _ := a.(map[string]any); row["name"] == name {
			n++
		}
	}
	return n
}

// currentVersion1875 reads an asset's current version.
func currentVersion1875(t *testing.T, c *client, id string) float64 {
	t.Helper()
	out := c.call("manage_asset", map[string]any{"action": "get", "asset_id": id})
	if asset, ok := out["asset"].(map[string]any); ok {
		out = asset
	}
	v, ok := out["current_version"].(float64)
	if !ok {
		t.Fatalf("get returned no current_version: %v", out)
	}
	return v
}

// assertLinkRefusal checks a refusal names the reference and says why.
func assertLinkRefusal(t *testing.T, isError bool, text, ref string) {
	t.Helper()
	if !isError {
		t.Fatalf("the write was accepted; want a refusal: %s", text)
	}
	for _, want := range []string{ref, "links between assets are not supported", "name it in text"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q: %s", want, text)
		}
	}
}

// TestIssue1875_ASaveUsingAReferenceAsALinkIsRefused is the ticket's
// reproduction, in both link forms: refused, naming the reference, and no
// asset left behind.
func TestIssue1875_ASaveUsingAReferenceAsALinkIsRefused(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ref := dataAsset1875(t, c, stamp)

	for form, body := range map[string]struct{ contentType, content string }{
		"html":     {"text/html", `<html><body><p>The data: <a href="` + ref + `">regional totals</a></p></body></html>`},
		"markdown": {"text/markdown", "# Report\n\nThe data: [regional totals](" + ref + ")\n"},
	} {
		t.Run(form, func(t *testing.T) {
			name := "acc-1875-link-" + form + "-" + stamp
			isError, text := save1875(t, c, name, body.contentType, body.content)
			if !isError {
				// A save that should have been refused was written; remove it
				// so a failing run leaves no fixture behind.
				var saved struct {
					AssetID string `json:"asset_id"`
				}
				if json.Unmarshal([]byte(text), &saved) == nil && saved.AssetID != "" {
					deleteAfter1875(t, c, saved.AssetID)
				}
			}
			assertLinkRefusal(t, isError, text, ref)
			if n := assetsNamed1875(t, c, name); n != 0 {
				t.Errorf("a refused save left %d asset(s) named %s", n, name)
			}
		})
	}
}

// TestIssue1875_AnUpdateOrPatchUsingAReferenceAsALinkIsRefused holds the same
// refusal on manage_asset update and patch, a patch's dry run included, with
// no version written.
func TestIssue1875_AnUpdateOrPatchUsingAReferenceAsALinkIsRefused(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ref := dataAsset1875(t, c, stamp)
	out := c.call("save_asset", map[string]any{
		"name": "acc-1875-report-" + stamp, "content_type": "text/markdown", "content": "# Report\n\nTotals below.\n",
	})
	id, _ := out["asset_id"].(string)
	deleteAfter1875(t, c, id)
	before := currentVersion1875(t, c, id)

	res, text, err := c.callRaw("manage_asset", map[string]any{
		"action": "update", "asset_id": id, "content": "# Report\n\n[regional totals](" + ref + ")\n",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	assertLinkRefusal(t, res.IsError, text, ref)

	for _, dryRun := range []bool{true, false} {
		res, text, err = c.callRaw("manage_asset", map[string]any{
			"action": "patch", "asset_id": id, "dry_run": dryRun,
			"edits": []any{map[string]any{
				"op": "replace", "find": "Totals below.", "replace": `<a href="` + ref + `">Totals</a>`,
			}},
		})
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		assertLinkRefusal(t, res.IsError, text, ref)
	}

	if after := currentVersion1875(t, c, id); after != before {
		t.Errorf("current_version moved from %v to %v; a refused write writes nothing", before, after)
	}
}

// TestIssue1875_AnUndeclaredLoadIsReported: a body that loads a reference it
// does not declare is saved, and the result names the reference, on save,
// update and patch, judged against the references in effect after the call.
func TestIssue1875_AnUndeclaredLoadIsReported(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	ref := dataAsset1875(t, c, stamp)
	img := `<img src="` + ref + `" alt="chart">`

	saved := c.call("save_asset", map[string]any{
		"name": "acc-1875-undeclared-" + stamp, "content_type": "text/html",
		"content": "<html><body><h1>Q4</h1>" + img + "</body></html>",
	})
	id, _ := saved["asset_id"].(string)
	deleteAfter1875(t, c, id)
	assertUndeclared1875(t, "save_asset", saved, ref)

	updated := c.call("manage_asset", map[string]any{
		"action": "update", "asset_id": id, "content": "<html><body><h1>Q4 final</h1>" + img + "</body></html>",
	})
	assertUndeclared1875(t, "update", updated, ref)

	patched := c.call("manage_asset", map[string]any{
		"action": "patch", "asset_id": id,
		"edits": []any{map[string]any{"op": "replace", "find": "Q4 final", "replace": "Q4 closed"}},
	})
	assertUndeclared1875(t, "patch", patched, ref)

	declared := c.call("manage_asset", map[string]any{
		"action": "patch", "asset_id": id, "references": []any{ref},
		"edits": []any{map[string]any{"op": "replace", "find": "Q4 closed", "replace": "Q4"}},
	})
	if _, present := declared["undeclared_references"]; present {
		t.Errorf("a patch declaring the reference still reports it: %v", declared)
	}
}

func assertUndeclared1875(t *testing.T, call string, out map[string]any, ref string) {
	t.Helper()
	list, _ := out["undeclared_references"].([]any)
	if len(list) != 1 || list[0] != ref {
		t.Errorf("%s: undeclared_references = %v; want [%s]: %v", call, out["undeclared_references"], ref, out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "served as written and resolve to nothing") {
		t.Errorf("%s: the message does not say what an undeclared reference does: %q", call, msg)
	}
}

// TestIssue1875_TheReferencesDescriptionsSayALoadIsNotALink reads both tools'
// references parameter as a client lists it.
func TestIssue1875_TheReferencesDescriptionsSayALoadIsNotALink(t *testing.T) {
	c := connect(t)
	seen := map[string]bool{}
	for _, tool := range c.tools() {
		if tool.Name != "save_asset" && tool.Name != "manage_asset" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: schema: %v", tool.Name, err)
		}
		desc := schema.Properties["references"].Description
		for _, want := range []string{"loads another file's content into this document", "it is not a link"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s references description does not say %q: %s", tool.Name, want, desc)
			}
		}
		seen[tool.Name] = true
	}
	if !seen["save_asset"] || !seen["manage_asset"] {
		t.Fatalf("tools/list did not return both tools: %v", seen)
	}
}
