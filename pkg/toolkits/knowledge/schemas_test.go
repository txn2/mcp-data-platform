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

// TestApplyKnowledgeSchema_InsightIDsCoversApply guards #725: the apply handler
// links the changeset to insight_ids and marks those insights applied, but that
// only happens if the agent passes insight_ids on apply. The param description
// once framed insight_ids as approve/reject-only, so agents never passed it on
// apply and insights_marked_applied stayed 0. The description must tell the agent
// insight_ids applies to the apply action too.
func TestApplyKnowledgeSchema_InsightIDsCoversApply(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(applyKnowledgeSchema, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema should have properties")
	insightIDs, ok := props["insight_ids"].(map[string]any)
	require.True(t, ok, "should have insight_ids property")
	desc, ok := insightIDs["description"].(string)
	require.True(t, ok, "insight_ids should have description")
	assert.Contains(t, desc, "apply", "insight_ids description must cover the apply action (#725)")
	assert.Contains(t, desc, "marked applied", "description must explain that apply marks the source insights applied")
}

// TestKnowledgeApplyPrompt_GuidesInsightIDsOnApply guards that the reviewer
// guidance prompt tells the holder to pass insight_ids on apply (not only to
// vaguely "mark insights applied"), which is what closes the loop (#725).
func TestKnowledgeApplyPrompt_GuidesInsightIDsOnApply(t *testing.T) {
	assert.Contains(t, knowledgeApplyPrompt, "insight_ids on the apply call",
		"apply guidance must tell the reviewer to pass insight_ids on apply (#725)")
}
