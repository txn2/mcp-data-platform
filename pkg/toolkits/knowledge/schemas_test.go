package knowledge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
