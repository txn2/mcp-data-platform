//go:build integration

package acceptance

import (
	"encoding/json"
	"testing"
)

// Issue #2826: help on a tool whose parameter descriptions name their actions
// in parentheses ("Asset ID (required for get, update, ...)") gave every
// action one example of five parameters and an empty parameter list. Each
// action now lists the parameters its descriptions name and its example holds
// only what that action needs.
//
// Wire forms: manage_asset takes action as a string ("help"); help takes no
// other argument, and no parameter touched admits a second form.

// help2826 is one action of manage_asset's help answer.
type help2826 struct {
	Command    string                     `json:"command"`
	Parameters []struct{ Name string }    `json:"parameters"`
	Example    map[string]json.RawMessage `json:"example"`
}

// TestIssue2826_EachActionListsItsOwnParametersAndExample: manage_asset
// action=help through the real client.
func TestIssue2826_EachActionListsItsOwnParametersAndExample(t *testing.T) {
	c := connect(t)
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
	want := map[string][]string{
		"list":           {"action"},
		"get":            {"action", "asset_id"},
		"revert":         {"action", "asset_id", "version"},
		"revoke_share":   {"action", "share_id"},
		"get_collection": {"action", "collection_id"},
	}
	for cmd, keys := range want {
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
	for _, cmd := range []string{"get", "revert", "revoke_share", "update"} {
		if len(by[cmd].Parameters) == 0 {
			t.Errorf("%s lists no parameters: %+v", cmd, by[cmd])
		}
	}
	for cmd, h := range by {
		if _, ok := h.Example["expires_in"]; ok {
			t.Errorf("%s's example carries expires_in, which only a public share takes", cmd)
		}
	}
}
