package portal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// scopeFromFilter and validThreadTarget are portal handler helpers (they gate
// request scoping and target validation), so their test stays in the portal
// package even though the Thread types now live in pkg/portal/threads.
func TestScopeFromFilterAndValidTarget(t *testing.T) {
	for _, tt := range []struct {
		f    ThreadFilter
		want string
		ok   bool
	}{
		{ThreadFilter{TargetType: targetTypeStandalone}, targetTypeStandalone, true},
		{ThreadFilter{AssetID: "a"}, targetTypeAsset, true},
		{ThreadFilter{CollectionID: "c"}, targetTypeCollection, true},
		{ThreadFilter{PromptID: "p"}, targetTypePrompt, true},
		{ThreadFilter{KnowledgePageID: "kp"}, targetTypeKnowledgePage, true},
		{ThreadFilter{}, "", false},
		{ThreadFilter{AssetID: "a", CollectionID: "c"}, "", false},
	} {
		got, ok := scopeFromFilter(tt.f)
		assert.Equal(t, tt.ok, ok)
		if ok {
			assert.Equal(t, tt.want, got)
		}
	}

	assert.True(t, validThreadTarget(targetTypeStandalone, "", "", "", ""))
	assert.True(t, validThreadTarget(targetTypeAsset, "a", "", "", ""))
	assert.True(t, validThreadTarget(targetTypeCollection, "", "c", "", ""))
	assert.True(t, validThreadTarget(targetTypePrompt, "", "", "p", ""))
	assert.True(t, validThreadTarget(targetTypeKnowledgePage, "", "", "", "kp"))
	assert.False(t, validThreadTarget(targetTypeAsset, "", "", "", ""))
	assert.False(t, validThreadTarget(targetTypeStandalone, "a", "", "", ""))
	assert.False(t, validThreadTarget(targetTypeKnowledgePage, "a", "", "", ""))
	// More than one object id set is invalid for any single-target type.
	assert.False(t, validThreadTarget(targetTypeAsset, "a", "c", "", ""))
	assert.False(t, validThreadTarget(targetTypeKnowledgePage, "a", "", "", "kp"))
	assert.False(t, validThreadTarget("bogus", "", "", "", ""))
}

// validAppendEventType is a portal handler helper (it gates which event types a
// reply may carry), so its test stays in the portal package.
func TestValidAppendEventType(t *testing.T) {
	assert.True(t, validAppendEventType(EventTypeComment))
	assert.True(t, validAppendEventType(EventTypeRating))
	assert.False(t, validAppendEventType(EventTypeResolution))
}
