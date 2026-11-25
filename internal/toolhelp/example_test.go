package toolhelp

import "testing"

func assetSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []any{"get", "list", "search", "delete"}},
			"scope":    map[string]any{"type": "string", "enum": []any{"mine", "shared", "all"}, "description": "Which assets list and search cover."},
			"asset_id": map[string]any{"type": "string", "description": "get, delete: the asset. Required for get and delete."},
		},
	}
}

func TestExample(t *testing.T) {
	ex := Example(assetSchema(), "assets shared with me")
	if ex["action"] != "list" || ex["scope"] != "shared" {
		t.Errorf("shared assets = %v, want action list scope shared", ex)
	}
	if _, ok := ex["asset_id"]; ok {
		t.Errorf("list carries get's required asset_id: %v", ex)
	}
	del := Example(assetSchema(), "delete an asset")
	if del["action"] != "delete" || del["asset_id"] != "<string>" {
		t.Errorf("delete = %v, want action delete with asset_id", del)
	}
	plain := Example(map[string]any{
		"required":   []any{"sql"},
		"properties": map[string]any{"sql": map[string]any{"type": "string"}, "format": map[string]any{"type": "string", "enum": []any{"json", "csv"}}},
	}, "rows as csv")
	if plain["sql"] != "<string>" || plain["format"] != "csv" {
		t.Errorf("plain = %v", plain)
	}
	if Example(nil, "x") != nil {
		t.Error("a missing schema gave an example")
	}
}

func TestIntentWordsAndMentions(t *testing.T) {
	if got := intentWords("A to-do: list my_items, OK?"); len(got) != 2 || got[0] != "list" || got[1] != "my_items" {
		t.Errorf("intentWords = %v", got)
	}
	if !mentions("list and search cover", "search") || mentions("research", "search") {
		t.Error("mentions misread a word boundary")
	}
	if countMatches([]string{"list", "zzz"}, "list them") != 1 {
		t.Error("countMatches miscounted")
	}
	if len(asMap("x")) != 0 {
		t.Error("asMap of a non-object is not empty")
	}
}
