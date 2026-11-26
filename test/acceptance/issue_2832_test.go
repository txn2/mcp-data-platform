//go:build integration

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Issue #2832: help listed a parameter under a command only when its
// description named the command after "for" or opened with it, so the
// manage_asset parameters written "(patch action)" or "(update and patch
// actions)" were listed under no command: patch's example was {"action":
// "patch"} with no parameters, and locate, get_content and diff listed none.
// A parenthesis opening with the commands now names them, and the content
// actions' descriptions say asset_id and edits are required for them.
//
// Wire forms: manage_asset takes action as a string ("help"); search takes
// intent and purpose as strings and sources as a list of strings. No
// parameter touched admits a second form.

// help2832 is manage_asset's help answer by action.
func help2832(t *testing.T, c *client) map[string]help2826 {
	t.Helper()
	out := c.call("manage_asset", map[string]any{"action": "help"})
	raw, _ := json.Marshal(out["commands"])
	var cmds []help2826
	if err := json.Unmarshal(raw, &cmds); err != nil || len(cmds) == 0 {
		t.Fatalf("help answered no commands: %v %v", err, out)
	}
	by := map[string]help2826{}
	for _, c := range cmds {
		by[c.Command] = c
	}
	return by
}

// TestIssue2832_EachContentActionListsItsParameters: patch, locate,
// get_content, outline, stats and diff list the parameters their descriptions
// name, and update lists the ones written "(update action)".
func TestIssue2832_EachContentActionListsItsParameters(t *testing.T) {
	by := help2832(t, connect(t))
	for cmd, want := range map[string][]string{
		"patch":       {"asset_id", "edits", "base_version", "dry_run", "change_summary"},
		"locate":      {"asset_id", "find", "pattern", "section", "selector", "context_bytes"},
		"get_content": {"asset_id", "line_start", "line_end", "section", "selector"},
		"outline":     {"asset_id"},
		"stats":       {"asset_id"},
		"diff":        {"asset_id", "from_version", "to_version"},
		"update":      {"asset_id", "change_summary", "max_versions", "status", "add_tags", "remove_tags"},
		"verify":      {"asset_id", "note", "expires_at"},
	} {
		var listed []string
		for _, p := range by[cmd].Parameters {
			listed = append(listed, p.Name)
		}
		for _, name := range want {
			if !slices.Contains(listed, name) {
				t.Errorf("%s does not list %s: %v", cmd, name, listed)
			}
		}
	}
}

// TestIssue2832_PatchsExampleCanBeSentAsWritten: patch's example carries the
// asset and the edits, and nothing patch does not need.
func TestIssue2832_PatchsExampleCanBeSentAsWritten(t *testing.T) {
	by := help2832(t, connect(t))
	for cmd, keys := range map[string][]string{
		"patch":  {"action", "asset_id", "edits"},
		"locate": {"action", "asset_id"},
		"diff":   {"action", "asset_id"},
		"verify": {"action", "asset_id"},
	} {
		ex := by[cmd].Example
		if len(ex) != len(keys) {
			t.Errorf("%s example = %s, want exactly %v", cmd, asJSON(ex), keys)
			continue
		}
		for _, k := range keys {
			if _, ok := ex[k]; !ok {
				t.Errorf("%s example = %s, lacks %s", cmd, asJSON(ex), k)
			}
		}
	}
}

// TestIssue2832_SearchsExampleForAPatchCarriesTheEdits: the same reader feeds
// search's tools source, so the example it gives for patching an asset holds
// the asset and the edits.
func TestIssue2832_SearchsExampleForAPatchCarriesTheEdits(t *testing.T) {
	text := issue2809ToolHit(t, connect(t), "patch the text of a saved asset with anchored edits", "manage_asset")
	if text == "" {
		t.Fatal("search did not return manage_asset for patching an asset")
	}
	for _, want := range []string{`"action":"patch"`, `"asset_id"`, `"edits"`} {
		if !strings.Contains(text, want) {
			t.Errorf("manage_asset's hit lacks %s: %s", want, text)
		}
	}
}
