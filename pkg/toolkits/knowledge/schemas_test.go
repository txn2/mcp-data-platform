package knowledge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureInsightSchema_Valid(t *testing.T) {
	var schema map[string]any
	err := json.Unmarshal(captureInsightSchema, &schema)
	require.NoError(t, err)

	assert.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema should have properties")

	// Verify category enum
	category, ok := props["category"].(map[string]any)
	require.True(t, ok, "should have category property")
	catEnum, ok := category["enum"].([]any)
	require.True(t, ok, "category should have enum")
	assert.Len(t, catEnum, 6, "category should have 6 enum values")
	assert.Contains(t, catEnum, "correction")
	assert.Contains(t, catEnum, "business_context")

	// Verify confidence enum
	conf, ok := props["confidence"].(map[string]any)
	require.True(t, ok, "should have confidence property")
	confEnum, ok := conf["enum"].([]any)
	require.True(t, ok, "confidence should have enum")
	assert.Len(t, confEnum, 3, "confidence should have 3 enum values")

	// Verify source enum
	src, ok := props["source"].(map[string]any)
	require.True(t, ok, "should have source property")
	srcEnum, ok := src["enum"].([]any)
	require.True(t, ok, "source should have enum")
	assert.Len(t, srcEnum, 3, "source should have 3 enum values")

	// Verify suggested_actions items have action_type enum
	sa, ok := props["suggested_actions"].(map[string]any)
	require.True(t, ok, "should have suggested_actions property")
	items, ok := sa["items"].(map[string]any)
	require.True(t, ok, "suggested_actions should have items")
	itemProps, ok := items["properties"].(map[string]any)
	require.True(t, ok, "items should have properties")
	actionType, ok := itemProps["action_type"].(map[string]any)
	require.True(t, ok, "should have action_type property")
	atEnum, ok := actionType["enum"].([]any)
	require.True(t, ok, "action_type should have enum")
	assert.Len(t, atEnum, 5, "action_type should have 5 enum values")
}

func TestApplyKnowledgeSchema_Valid(t *testing.T) {
	var schema map[string]any
	err := json.Unmarshal(applyKnowledgeSchema, &schema)
	require.NoError(t, err)

	assert.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema should have properties")

	// Verify action enum
	action, ok := props["action"].(map[string]any)
	require.True(t, ok, "should have action property")
	actionEnum, ok := action["enum"].([]any)
	require.True(t, ok, "action should have enum")
	assert.Len(t, actionEnum, 6, "action should have 6 enum values")
	assert.Contains(t, actionEnum, "apply")
	assert.Contains(t, actionEnum, "synthesize")

	// Verify changes items have change_type enum
	changes, ok := props["changes"].(map[string]any)
	require.True(t, ok, "should have changes property")
	items, ok := changes["items"].(map[string]any)
	require.True(t, ok, "changes should have items")
	itemProps, ok := items["properties"].(map[string]any)
	require.True(t, ok, "items should have properties")
	changeType, ok := itemProps["change_type"].(map[string]any)
	require.True(t, ok, "should have change_type property")
	ctEnum, ok := changeType["enum"].([]any)
	require.True(t, ok, "change_type should have enum")
	assert.Len(t, ctEnum, 5, "change_type should have 5 enum values")

	// Verify target field has column: documentation
	target, ok := itemProps["target"].(map[string]any)
	require.True(t, ok, "should have target property")
	targetDesc, ok := target["description"].(string)
	require.True(t, ok, "target should have description")
	assert.Contains(t, targetDesc, "column:")
}
