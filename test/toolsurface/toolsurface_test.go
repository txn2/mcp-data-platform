package toolsurface

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conforming is a tools/list that agrees with both tables.
func conforming() []*mcp.Tool {
	tools := make([]*mcp.Tool, 0, len(Annotations))
	for name, want := range Annotations {
		ann := &mcp.ToolAnnotations{ReadOnlyHint: want.ReadOnly, IdempotentHint: want.ReadOnly}
		if !want.ReadOnly {
			ann.DestructiveHint = new(want.Destructive)
		}
		tool := &mcp.Tool{Name: name, Annotations: ann}
		if verbs, ok := ActionEnums[name]; ok {
			enum := make([]any, len(verbs))
			for i, v := range verbs {
				enum[i] = v
			}
			tool.InputSchema = map[string]any{"type": "object", "properties": map[string]any{
				VerbArgOf(name): map[string]any{"type": "string", "enum": enum},
			}}
		}
		tools = append(tools, tool)
	}
	return tools
}

func find(tools []*mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	return nil
}

func TestMismatchesOfAConformingListing(t *testing.T) {
	tools := append(conforming(), &mcp.Tool{Name: "upstream__anything"})
	assert.Empty(t, Mismatches(tools), "a gateway tool is the upstream's to annotate")
	assert.Len(t, Native(tools), len(Annotations))
}

// Each way a listing can drift from the tables is named.
func TestMismatchesNameEachDrift(t *testing.T) {
	cases := map[string]struct {
		edit func([]*mcp.Tool) []*mcp.Tool
		want string
	}{
		"verb added": {func(ts []*mcp.Tool) []*mcp.Tool {
			schema, ok := find(ts, "notify").InputSchema.(map[string]any)
			require.True(t, ok)
			props, ok := schema["properties"].(map[string]any)
			require.True(t, ok)
			props["action"] = map[string]any{"enum": []any{"list", "send", "publish", "recall"}}
			return ts
		}, "notify action enum"},
		"destructive flipped": {func(ts []*mcp.Tool) []*mcp.Tool {
			find(ts, "manage_feedback").Annotations.DestructiveHint = new(false)
			return ts
		}, "manage_feedback advertises destructiveHint: false, want true"},
		"no destructive hint": {func(ts []*mcp.Tool) []*mcp.Tool {
			find(ts, "save_asset").Annotations.DestructiveHint = nil
			return ts
		}, "save_asset writes and advertises no destructiveHint"},
		"write claims read": {func(ts []*mcp.Tool) []*mcp.Tool {
			find(ts, "save_asset").Annotations.ReadOnlyHint = true
			return ts
		}, "save_asset can modify state"},
		"read not read-only": {func(ts []*mcp.Tool) []*mcp.Tool {
			find(ts, "search").Annotations.ReadOnlyHint = false
			return ts
		}, "search reads and does not advertise readOnlyHint"},
		"no annotations": {func(ts []*mcp.Tool) []*mcp.Tool {
			find(ts, "fetch").Annotations = nil
			return ts
		}, "fetch is listed with no annotations object"},
		"unclassified tool": {func(ts []*mcp.Tool) []*mcp.Tool {
			return append(ts, &mcp.Tool{Name: "brand_new", Annotations: &mcp.ToolAnnotations{}})
		}, "brand_new is listed and Annotations does not say"},
		"action tool missing": {func(ts []*mcp.Tool) []*mcp.Tool {
			out := ts[:0]
			for _, tool := range ts {
				if tool.Name != "s3_object" {
					out = append(out, tool)
				}
			}
			return out
		}, "s3_object is in ActionEnums and is not listed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := Mismatches(c.edit(conforming()))
			require.Len(t, got, 1, "%v", got)
			assert.Contains(t, got[0], c.want)
		})
	}
}

func TestEnumAndDestructiveReaders(t *testing.T) {
	_, err := Enum(func() {}, "action")
	require.Error(t, err)
	_, err = Enum([]any{1}, "action")
	require.Error(t, err)
	got, err := Enum(map[string]any{}, "action")
	require.NoError(t, err)
	assert.Empty(t, got)

	_, present := Destructive(&mcp.Tool{})
	assert.False(t, present)
	assert.Equal(t, "command", VerbArgOf("memory_manage"))
	assert.Equal(t, "action", VerbArgOf("notify"))
}
