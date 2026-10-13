package portal

import (
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManageResourceIndexReachesTheWriter is #2111 at the tool: the writer's
// choice travels with a create and a replacement, and a create says where it
// came from for the operator's rules.
func TestManageResourceIndexReachesTheWriter(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.Index = "none"
	decodeResourceOutput(t, callResource(t, tk, in))
	assert.Equal(t, "none", w.created.Index)
	assert.Equal(t, "agent", string(w.created.Origin))

	replace := manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "a,b\n", Index: "metadata",
	}
	decodeResourceOutput(t, callResource(t, tk, replace))
	assert.Equal(t, "metadata", w.replaced.Index)
}

func TestManageResourceRefusesAnIndexItCannotRead(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.Index = "partial"
	result := callResource(t, tk, in)
	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "index must be one of")
	assert.Empty(t, w.created.Filename, "nothing reaches the writer")
}

func TestManageResourceUpdate(t *testing.T) {
	t.Run("changes only what it names", func(t *testing.T) {
		tk, w := resourceToolkit(t)
		out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
			Action: resourceActionUpdate, Reference: "mcp:resource:res1", Index: "none",
		}))
		assert.Equal(t, "res1", w.gotID)
		require.NotNil(t, w.updated.Index)
		assert.Equal(t, "none", *w.updated.Index)
		assert.Nil(t, w.updated.DisplayName)
		assert.Nil(t, w.updated.Description)
		assert.Nil(t, w.updated.Tags)
		assert.Equal(t, "none", out.IndexPolicy)
		assert.Contains(t, out.Message, "list its folder")
	})
	t.Run("names and tags", func(t *testing.T) {
		tk, w := resourceToolkit(t)
		out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
			Action: resourceActionUpdate, Reference: "mcp:resource:res1",
			DisplayName: "NLTV daily", Description: "One station day", Tags: []string{"nltv"},
		}))
		require.NotNil(t, w.updated.DisplayName)
		assert.Equal(t, "NLTV daily", *w.updated.DisplayName)
		assert.Equal(t, "One station day", *w.updated.Description)
		assert.Equal(t, []string{"nltv"}, w.updated.Tags)
		assert.Contains(t, out.Message, "contents")
	})
	t.Run("metadata says what search reads", func(t *testing.T) {
		tk, _ := resourceToolkit(t)
		out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
			Action: resourceActionUpdate, Reference: "mcp:resource:res1", Index: "metadata",
		}))
		assert.Contains(t, out.Message, "name, description, folder, filename and tags")
	})
	refusals := map[string]manageResourceInput{
		"nothing to change": {Action: resourceActionUpdate, Reference: "mcp:resource:res1"},
		"no reference":      {Action: resourceActionUpdate, Index: "none"},
		"bad index":         {Action: resourceActionUpdate, Reference: "mcp:resource:res1", Index: "most"},
	}
	for name, in := range refusals {
		t.Run(name, func(t *testing.T) {
			tk, _ := resourceToolkit(t)
			assert.True(t, callResource(t, tk, in).IsError)
		})
	}
	t.Run("a writer refusal is reported", func(t *testing.T) {
		tk, w := resourceToolkit(t)
		w.updateErr = errors.New("you cannot change a file in the global scope")
		result := callResource(t, tk, manageResourceInput{
			Action: resourceActionUpdate, Reference: "mcp:resource:res1", Index: "none",
		})
		require.True(t, result.IsError)
		assert.Contains(t, errText(t, result), "global scope")
	})
}

// TestManageResourceDescribesAFolder: an update naming a path and no
// reference sets what the folder holds, which its search entry is matched on.
func TestManageResourceDescribesAFolder(t *testing.T) {
	tk, w := resourceToolkit(t)
	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionUpdate, Path: "pipelines/nltv", Scope: "global",
		Description: "NLTV station daily reach and frequency archive",
	})
	require.False(t, result.IsError, errText(t, result))
	assert.Equal(t, "pipelines/nltv", w.describedAddr.Path)
	assert.Equal(t, "global", w.describedAddr.Scope)
	assert.Equal(t, "NLTV station daily reach and frequency archive", w.described)

	tk, _ = resourceToolkit(t)
	decodeOK := callResource(t, tk, manageResourceInput{Action: resourceActionUpdate, Path: "x", Description: "y"})
	require.False(t, decodeOK.IsError)
	assert.Contains(t, errTextOrBody(decodeOK), `"scope": "user"`, "the caller's own library by default")

	tk, _ = resourceToolkit(t)
	assert.True(t, callResource(t, tk, manageResourceInput{Action: resourceActionUpdate, Path: "x"}).IsError,
		"a folder update with no description changes nothing")

	tk, w = resourceToolkit(t)
	w.describeErr = errors.New("there is no folder")
	assert.True(t, callResource(t, tk, manageResourceInput{Action: resourceActionUpdate, Path: "x", Description: "y"}).IsError)
}

func errTextOrBody(r *mcp.CallToolResult) string {
	if len(r.Content) == 0 {
		return ""
	}
	if tc, ok := r.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
