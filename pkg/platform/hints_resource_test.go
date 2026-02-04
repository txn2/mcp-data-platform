package platform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

func TestBuildHintsResourceResult(t *testing.T) {
	// Create platform with hint manager
	p := &Platform{
		hintManager: tuning.NewHintManager(),
	}

	// Set some hints
	p.hintManager.SetHints(map[string]string{
		"test_tool":      "This is a test hint",
		"another_tool":   "Another hint",
		"datahub_search": "Search for datasets",
	})

	// Build resource result
	result, err := p.buildHintsResourceResult()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Contents, 1)

	// Verify content
	content := result.Contents[0]
	assert.Equal(t, hintsResourceURI, content.URI)
	assert.Equal(t, "application/json", content.MIMEType)

	// Parse JSON and verify hints
	var hints map[string]string
	err = json.Unmarshal([]byte(content.Text), &hints)
	require.NoError(t, err)
	assert.Equal(t, "This is a test hint", hints["test_tool"])
	assert.Equal(t, "Another hint", hints["another_tool"])
	assert.Equal(t, "Search for datasets", hints["datahub_search"])
}

func TestHintsResourceURI(t *testing.T) {
	assert.Equal(t, "hints://operational", hintsResourceURI)
}
