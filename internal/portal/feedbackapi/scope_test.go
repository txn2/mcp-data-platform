package feedbackapi

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// scopeFromFilter and validThreadTarget are portal handler helpers (they gate
// request scoping and target validation), so their test stays in the portal
// package even though the Thread types now live in pkg/portal/threads.
func TestScopeFromFilterAndValidTarget(t *testing.T) {
	for _, tt := range []struct {
		f    threads.ThreadFilter
		want string
		ok   bool
	}{
		{threads.ThreadFilter{TargetType: portaldomain.TargetTypeStandalone}, portaldomain.TargetTypeStandalone, true},
		{threads.ThreadFilter{AssetID: "a"}, portaldomain.TargetTypeAsset, true},
		{threads.ThreadFilter{CollectionID: "c"}, portaldomain.TargetTypeCollection, true},
		{threads.ThreadFilter{PromptID: "p"}, portaldomain.TargetTypePrompt, true},
		{threads.ThreadFilter{KnowledgePageID: "kp"}, portaldomain.TargetTypeKnowledgePage, true},
		{threads.ThreadFilter{}, "", false},
		{threads.ThreadFilter{AssetID: "a", CollectionID: "c"}, "", false},
	} {
		got, ok := scopeFromFilter(tt.f)
		assert.Equal(t, tt.ok, ok)
		if ok {
			assert.Equal(t, tt.want, got)
		}
	}

	assert.True(t, validThreadTarget(portaldomain.TargetTypeStandalone, "", "", "", ""))
	assert.True(t, validThreadTarget(portaldomain.TargetTypeAsset, "a", "", "", ""))
	assert.True(t, validThreadTarget(portaldomain.TargetTypeCollection, "", "c", "", ""))
	assert.True(t, validThreadTarget(portaldomain.TargetTypePrompt, "", "", "p", ""))
	assert.True(t, validThreadTarget(portaldomain.TargetTypeKnowledgePage, "", "", "", "kp"))
	assert.False(t, validThreadTarget(portaldomain.TargetTypeAsset, "", "", "", ""))
	assert.False(t, validThreadTarget(portaldomain.TargetTypeStandalone, "a", "", "", ""))
	assert.False(t, validThreadTarget(portaldomain.TargetTypeKnowledgePage, "a", "", "", ""))
	// More than one object id set is invalid for any single-target type.
	assert.False(t, validThreadTarget(portaldomain.TargetTypeAsset, "a", "c", "", ""))
	assert.False(t, validThreadTarget(portaldomain.TargetTypeKnowledgePage, "a", "", "", "kp"))
	assert.False(t, validThreadTarget("bogus", "", "", "", ""))
}

// validAppendEventType is a portal handler helper (it gates which event types a
// reply may carry), so its test stays in the portal package.
func TestValidAppendEventType(t *testing.T) {
	assert.True(t, validAppendEventType(threads.EventTypeComment))
	assert.True(t, validAppendEventType(threads.EventTypeRating))
	assert.False(t, validAppendEventType(threads.EventTypeResolution))
}
