package portaldomain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionFilterEffectiveLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"unset", 0, DefaultLimit},
		{"negative", -5, DefaultLimit},
		{"in range", 25, 25},
		{"at the cap", MaxLimit, MaxLimit},
		{"over the cap", MaxLimit + 1, MaxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &CollectionFilter{Limit: tt.limit}
			assert.Equal(t, tt.want, f.EffectiveLimit())
		})
	}
}

func TestValidateCollectionName(t *testing.T) {
	require.NoError(t, ValidateCollectionName("Q4 Review"))
	require.Error(t, ValidateCollectionName(""), "a nameless collection is not addressable")
	require.Error(t, ValidateCollectionName(strings.Repeat("x", MaxNameLength+1)))
}

func TestValidateCollectionDescription(t *testing.T) {
	require.NoError(t, ValidateCollectionDescription(""))
	require.NoError(t, ValidateCollectionDescription(strings.Repeat("x", MaxCollectionDescriptionLength)))
	require.Error(t, ValidateCollectionDescription(strings.Repeat("x", MaxCollectionDescriptionLength+1)))
}

func TestValidateSectionTitleAndDescription(t *testing.T) {
	require.NoError(t, ValidateSectionTitle(strings.Repeat("x", MaxSectionTitleLength)))
	require.Error(t, ValidateSectionTitle(strings.Repeat("x", MaxSectionTitleLength+1)))

	require.NoError(t, ValidateSectionDescription(strings.Repeat("x", MaxSectionDescriptionLength)))
	require.Error(t, ValidateSectionDescription(strings.Repeat("x", MaxSectionDescriptionLength+1)))
}

// TestValidateSections walks each way a section set is rejected, and reports
// which section failed: the message is what the REST caller sees.
func TestValidateSections(t *testing.T) {
	require.NoError(t, ValidateSections(nil))
	require.NoError(t, ValidateSections([]CollectionSection{{Title: "Overview"}}))

	err := ValidateSections(make([]CollectionSection, MaxSections+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many sections")

	err = ValidateSections([]CollectionSection{{Title: "ok"}, {Title: strings.Repeat("x", MaxSectionTitleLength+1)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "section 1", "the failing section must be named")

	err = ValidateSections([]CollectionSection{{Description: strings.Repeat("x", MaxSectionDescriptionLength+1)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "section 0")

	err = ValidateSections([]CollectionSection{{Title: "big", Items: make([]CollectionItem, MaxItemsPerSection+1)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many items")
}

func TestValidSharePermission(t *testing.T) {
	assert.True(t, ValidSharePermission(string(PermissionViewer)))
	assert.True(t, ValidSharePermission(string(PermissionEditor)))
	assert.False(t, ValidSharePermission("owner"))
	assert.False(t, ValidSharePermission(""))
}

func TestContentTypeHelpers(t *testing.T) {
	assert.Equal(t, ".html", ExtensionForContentType("text/html"))

	// A specific declaration is honored; a generic one is replaced by the type
	// detected from the bytes, which never resolves to an active type.
	assert.Equal(t, "text/markdown", ResolveContentType("text/markdown", []byte("# heading")))
	assert.Equal(t, "text/plain", ResolveContentType("application/octet-stream", []byte("just words")))
}
