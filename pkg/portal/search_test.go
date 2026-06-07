package portal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssetIndexText(t *testing.T) {
	tests := []struct {
		name        string
		aName       string
		description string
		tags        []string
		want        string
	}{
		{"all fields", "Q4 Dashboard", "revenue by region", []string{"sales", "q4"}, "Q4 Dashboard\nrevenue by region\nsales q4"},
		{"name only", "Just A Name", "", nil, "Just A Name"},
		{"skips empty desc", "Name", "", []string{"tag"}, "Name\ntag"},
		{"skips empty tags", "Name", "Desc", []string{}, "Name\nDesc"},
		{"all empty", "", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, AssetIndexText(tt.aName, tt.description, tt.tags))
		})
	}
}

func TestCollectionIndexText(t *testing.T) {
	assert.Equal(t, "Coll\ndesc\nsec text",
		CollectionIndexText("Coll", "desc", "sec text"))
	assert.Equal(t, "Coll", CollectionIndexText("Coll", "", ""))
	assert.Equal(t, "", CollectionIndexText("", "", ""))
}

func TestSectionsText(t *testing.T) {
	sections := []CollectionSection{
		{Title: "Overview", Description: "the summary"},
		{Title: "Details", Description: ""},
		{Title: "", Description: "trailing note"},
	}
	assert.Equal(t, "Overview the summary Details trailing note", SectionsText(sections))
	assert.Equal(t, "", SectionsText(nil))
}

func TestClampSearchLimit(t *testing.T) {
	assert.Equal(t, DefaultSearchLimit, clampSearchLimit(0))
	assert.Equal(t, DefaultSearchLimit, clampSearchLimit(-5))
	assert.Equal(t, DefaultSearchLimit, clampSearchLimit(maxSearchLimit+1))
	assert.Equal(t, 5, clampSearchLimit(5))
	assert.Equal(t, maxSearchLimit, clampSearchLimit(maxSearchLimit))
}

func TestSearchQueryEffectiveLimit(t *testing.T) {
	assert.Equal(t, DefaultSearchLimit, AssetSearchQuery{Limit: 0}.EffectiveLimit())
	assert.Equal(t, 7, AssetSearchQuery{Limit: 7}.EffectiveLimit())
	assert.Equal(t, DefaultSearchLimit, CollectionSearchQuery{Limit: 0}.EffectiveLimit())
	assert.Equal(t, 9, CollectionSearchQuery{Limit: 9}.EffectiveLimit())
}

func TestFuseHybridScore(t *testing.T) {
	// cosine 1.0 mapped to semantic 1.0, lexical match -> 0.6*1 + 0.4*1 = 1.0
	assert.InDelta(t, 1.0, fuseHybridScore(1.0, true), 1e-9)
	// cosine 1.0, no lexical match -> 0.6*1 + 0.4*0 = 0.6
	assert.InDelta(t, 0.6, fuseHybridScore(1.0, false), 1e-9)
	// cosine 0.0 maps to semantic 0.5; with lexical match -> 0.6*0.5 + 0.4 = 0.7
	assert.InDelta(t, 0.7, fuseHybridScore(0.0, true), 1e-9)
}
