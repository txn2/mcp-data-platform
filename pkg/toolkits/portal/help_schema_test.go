package portal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/toolhelp"
)

// TestHelpReadsManageAssetsSchema: help on the real manage_asset schema lists
// each action's own parameters and puts in each example only what that action
// needs (#2826). Its descriptions name actions in parentheses, "(required for
// get, update, ...)", which help used to miss, giving every action one example
// of five parameters.
func TestHelpReadsManageAssetsSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(manageAssetSchema, &schema); err != nil {
		t.Fatal(err)
	}
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		t.Fatal("help reached the tool")
		return &mcp.CallToolResult{}, nil
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "manage_asset", Arguments: json.RawMessage(`{"action":"help"}`)}}
	res, err := toolhelp.HelpMiddleware(func(context.Context, string) map[string]any { return schema })(leaf)(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Commands []struct {
			Command    string
			Parameters []struct{ Name string }
			Example    map[string]any
		}
	}
	raw, _ := res.(*mcp.CallToolResult).StructuredContent.(json.RawMessage)
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	byCmd := map[string]map[string]any{}
	params := map[string]int{}
	for _, c := range h.Commands {
		byCmd[c.Command] = c.Example
		params[c.Command] = len(c.Parameters)
	}
	want := map[string][]string{
		"list":         {"action"},
		"get":          {"action", "asset_id"},
		"revert":       {"action", "asset_id", "version"},
		"revoke_share": {"action", "share_id"},
		"share":        {"action", "asset_id"},
	}
	for cmd, keys := range want {
		ex := byCmd[cmd]
		if len(ex) != len(keys) {
			t.Errorf("%s example = %v, want keys %v", cmd, ex, keys)
		}
		for _, k := range keys {
			if _, ok := ex[k]; !ok {
				t.Errorf("%s example = %v, lacks %s", cmd, ex, k)
			}
		}
	}
	for _, cmd := range []string{"get", "revert", "revoke_share", "get_collection"} {
		if params[cmd] == 0 {
			t.Errorf("%s lists no parameters", cmd)
		}
	}
	for cmd, ex := range byCmd {
		if _, ok := ex["expires_in"]; ok {
			t.Errorf("%s example carries expires_in, which only a public share takes: %v", cmd, ex)
		}
	}
}

// TestExampleForSharedAssetsListsThem: search's tools source answers "assets
// shared with me" with manage_asset action=list scope=shared (#2809); a
// parameter that mentions search in passing does not pull the example to it
// (#2826).
func TestExampleForSharedAssetsListsThem(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(manageAssetSchema, &schema); err != nil {
		t.Fatal(err)
	}
	ex := toolhelp.Example(schema, "assets shared with me")
	if ex["action"] != "list" || ex["scope"] != "shared" {
		t.Errorf("example = %v, want action list scope shared", ex)
	}
}
