package promptlayer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer/promptschema"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleShowPrompts_ReturnsPresentationOnlyPayload(t *testing.T) {
	h, _ := newTestHandle()

	res, _, err := h.handleShowPrompts(context.Background(), showPromptsInput{})
	require.NoError(t, err)
	require.False(t, res.IsError)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out))

	// It renders a UI and carries no prompt data, which is what keeps it useless
	// to an agent as a data source.
	assert.Equal(t, true, out["shown"])
	assert.Contains(t, out, "message")
	assert.Contains(t, out, "hint")
	assert.NotContains(t, out, "prompts", "show_prompts must not return prompt data")
	assert.NotContains(t, out, "count")
}

func TestHandleShowPrompts_EchoesSearch(t *testing.T) {
	h, _ := newTestHandle()

	res, _, err := h.handleShowPrompts(context.Background(), showPromptsInput{Search: "sales"})
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out))
	assert.Equal(t, "sales", out["search"])
}

func TestRegisterShowPromptsTool(_ *testing.T) {
	h, _ := newTestHandle()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)

	// Registers on a DB-backed handle.
	h.RegisterShowPromptsTool(server)

	// No-op on a nil store (no-DB deployment): must not panic and must not
	// register anything.
	nilStore := &Handle{}
	nilStore.RegisterShowPromptsTool(server)
}

func TestShowPromptsSchemaIsAnObjectWithSearch(t *testing.T) {
	schema, ok := showPromptsSchema().(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema[promptschema.KeyType])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "search")
}

func TestShowPromptsToolNameIsStable(t *testing.T) {
	// The composition root binds the prompt-browser app to this exact name.
	assert.Equal(t, "show_prompts", ToolNameShowPrompts)
}
