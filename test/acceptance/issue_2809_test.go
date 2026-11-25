//go:build integration

package acceptance

import (
	"slices"
	"strings"
	"testing"
)

// Issue #2809: search answers "which tool does this". Its tools source ranks
// the platform's own tools against the intent, filtered by the persona
// predicate tools/list applies, and each hit carries an example call; the
// separate tool-finding tool is gone.
//
// Wire forms: search's intent and purpose are strings and sources a list of
// strings, each sent in that one form; tools/list takes no arguments. No
// parameter touched admits a second form.

// issue2809ToolHit is the text of search's hit naming tool, "" when none.
func issue2809ToolHit(t *testing.T, c *client, intent, tool string) string {
	t.Helper()
	out := c.call("search", map[string]any{"intent": intent, "sources": []string{"tools"}, "purpose": "Acceptance #2809: which tool fits this task."})
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		hits, _ := group["hits"].([]any)
		for _, h := range hits {
			hit, _ := h.(map[string]any)
			if hit["ref"] == tool {
				text, _ := hit["text"].(string)
				return text
			}
		}
	}
	return ""
}

// TestIssue2809_SearchNamesTheToolForAnAction: "publish a static site" returns
// manage_site for the administrator, and not for the analyst, whose persona
// may not call it.
func TestIssue2809_SearchNamesTheToolForAnAction(t *testing.T) {
	admin := connect(t)
	if !slices.Contains(admin.searchToolNames("publish a static site"), "manage_site") {
		t.Errorf("search's tools for the administrator lack manage_site: %v", admin.searchToolNames("publish a static site"))
	}
	analyst := connectAs(t, "acme-analyst-key")
	if slices.Contains(analyst.searchToolNames("publish a static site"), "manage_site") {
		t.Error("search offered manage_site to a persona that cannot call it")
	}
}

// TestIssue2809_TheHitCarriesTheActionThatListsSharedAssets: "assets shared
// with me" returns manage_asset with an example call listing them.
func TestIssue2809_TheHitCarriesTheActionThatListsSharedAssets(t *testing.T) {
	text := issue2809ToolHit(t, connect(t), "assets shared with me", "manage_asset")
	if text == "" {
		t.Fatal("search did not return manage_asset for assets shared with me")
	}
	for _, want := range []string{`"action":"list"`, `"scope":"shared"`} {
		if !strings.Contains(text, want) {
			t.Errorf("manage_asset's hit lacks %s: %s", want, text)
		}
	}
}

// TestIssue2809_NoToolFindingTool: tools/list holds no platform_find_tools.
func TestIssue2809_NoToolFindingTool(t *testing.T) {
	for _, tool := range connect(t).tools() {
		if tool.Name == "platform_find_tools" {
			t.Fatal("tools/list still holds platform_find_tools")
		}
	}
}
