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

	// Verify category lists valid values in description (no enum constraint)
	category, ok := props["category"].(map[string]any)
	require.True(t, ok, "should have category property")
	_, hasEnum := category["enum"]
	assert.False(t, hasEnum, "category should not have enum — valid values belong in description only")
	catDesc, ok := category["description"].(string)
	require.True(t, ok, "category should have description")
	assert.Contains(t, catDesc, "correction")
	assert.Contains(t, catDesc, "business_context")

	// Verify confidence lists valid values in description
	conf, ok := props["confidence"].(map[string]any)
	require.True(t, ok, "should have confidence property")
	_, hasEnum = conf["enum"]
	assert.False(t, hasEnum, "confidence should not have enum")
	confDesc, ok := conf["description"].(string)
	require.True(t, ok, "confidence should have description")
	assert.Contains(t, confDesc, "high")
	assert.Contains(t, confDesc, "medium")
	assert.Contains(t, confDesc, "low")

	// Verify source lists valid values in description
	src, ok := props["source"].(map[string]any)
	require.True(t, ok, "should have source property")
	_, hasEnum = src["enum"]
	assert.False(t, hasEnum, "source should not have enum")
	srcDesc, ok := src["description"].(string)
	require.True(t, ok, "source should have description")
	assert.Contains(t, srcDesc, "user")
	assert.Contains(t, srcDesc, "agent_discovery")
	assert.Contains(t, srcDesc, "enrichment_gap")

	// Verify suggested_actions items have action_type with valid values in description
	sa, ok := props["suggested_actions"].(map[string]any)
	require.True(t, ok, "should have suggested_actions property")
	items, ok := sa["items"].(map[string]any)
	require.True(t, ok, "suggested_actions should have items")
	itemProps, ok := items["properties"].(map[string]any)
	require.True(t, ok, "items should have properties")
	actionType, ok := itemProps["action_type"].(map[string]any)
	require.True(t, ok, "should have action_type property")
	_, hasEnum = actionType["enum"]
	assert.False(t, hasEnum, "action_type should not have enum")
	atDesc, ok := actionType["description"].(string)
	require.True(t, ok, "action_type should have description")
	assert.Contains(t, atDesc, "update_description")
	assert.Contains(t, atDesc, "add_tag")
	assert.Contains(t, atDesc, "remove_tag")
	assert.Contains(t, atDesc, "flag_quality_issue")
}

func TestApplyKnowledgeSchema_Valid(t *testing.T) {
	var schema map[string]any
	err := json.Unmarshal(applyKnowledgeSchema, &schema)
	require.NoError(t, err)

	assert.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema should have properties")

	// Verify action lists valid values in description (no enum constraint)
	action, ok := props["action"].(map[string]any)
	require.True(t, ok, "should have action property")
	_, hasEnum := action["enum"]
	assert.False(t, hasEnum, "action should not have enum — valid values belong in description only")
	actionDesc, ok := action["description"].(string)
	require.True(t, ok, "action should have description")
	assert.Contains(t, actionDesc, "apply")
	assert.Contains(t, actionDesc, "synthesize")
	assert.Contains(t, actionDesc, "bulk_review")

	// Verify changes items have change_type with valid values in description
	changes, ok := props["changes"].(map[string]any)
	require.True(t, ok, "should have changes property")
	items, ok := changes["items"].(map[string]any)
	require.True(t, ok, "changes should have items")
	itemProps, ok := items["properties"].(map[string]any)
	require.True(t, ok, "items should have properties")
	changeType, ok := itemProps["change_type"].(map[string]any)
	require.True(t, ok, "should have change_type property")
	_, hasEnum = changeType["enum"]
	assert.False(t, hasEnum, "change_type should not have enum")
	ctDesc, ok := changeType["description"].(string)
	require.True(t, ok, "change_type should have description")
	assert.Contains(t, ctDesc, "update_description")
	assert.Contains(t, ctDesc, "remove_tag")
	assert.Contains(t, ctDesc, "flag_quality_issue")

	// Verify target field has column: documentation
	target, ok := itemProps["target"].(map[string]any)
	require.True(t, ok, "should have target property")
	targetDesc, ok := target["description"].(string)
	require.True(t, ok, "target should have description")
	assert.Contains(t, targetDesc, "column:")
}
