package portaldomain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Type tests ---

func TestAssetFilterEffectiveLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{"default", 0, DefaultLimit},
		{"negative", -1, DefaultLimit},
		{"small", 10, 10},
		{"max", MaxLimit, MaxLimit},
		{"over_max", MaxLimit + 1, MaxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := AssetFilter{Limit: tt.limit}
			assert.Equal(t, tt.expected, f.EffectiveLimit())
		})
	}
}

// --- Validation tests ---

func TestValidateAssetName(t *testing.T) {
	assert.Error(t, ValidateAssetName(""))
	assert.NoError(t, ValidateAssetName("valid name"))

	longName := make([]byte, MaxNameLength+1)
	for i := range longName {
		longName[i] = 'a'
	}
	assert.Error(t, ValidateAssetName(string(longName)))
}

func TestValidateContentType(t *testing.T) {
	assert.Error(t, ValidateContentType(""))
	assert.NoError(t, ValidateContentType("text/html"))
	assert.NoError(t, ValidateContentType("text/xml"), "an alias of an accepted family is accepted")

	err := ValidateContentType("application/xhtml+xml")
	require.Error(t, err, "the scriptable document family must not be storable")
	assert.Contains(t, err.Error(), "text/markdown", "the error names what would be accepted")

	assert.Error(t, ValidateContentType("application/pdf"), "a binary family cannot arrive as a string")
	assert.Error(t, ValidateContentType("text/x-shellscript"), "text/* is not a wildcard")
}

func TestValidateContentTypeChange(t *testing.T) {
	assert.NoError(t, ValidateContentTypeChange("text/markdown", "text/html"),
		"a change onto the allowlist is accepted")
	assert.NoError(t, ValidateContentTypeChange("text/x-log", "text/x-log"),
		"an asset keeps its own type however it was stored")
	assert.NoError(t, ValidateContentTypeChange("TEXT/X-LOG; charset=utf-8", "text/x-log"),
		"the comparison is over normalized types")
	assert.Error(t, ValidateContentTypeChange("text/x-log", "application/xhtml+xml"),
		"a change off the allowlist is refused")
}

func TestValidateTags(t *testing.T) {
	assert.NoError(t, ValidateTags(nil))
	assert.NoError(t, ValidateTags([]string{"a", "b"}))

	tooMany := make([]string, MaxTags+1)
	assert.Error(t, ValidateTags(tooMany))

	longTag := make([]byte, MaxTagLength+1)
	for i := range longTag {
		longTag[i] = 'a'
	}
	assert.Error(t, ValidateTags([]string{string(longTag)}))
}

func TestValidateDescription(t *testing.T) {
	assert.NoError(t, ValidateDescription(""))
	assert.NoError(t, ValidateDescription("valid"))

	longDesc := make([]byte, MaxDescriptionLength+1)
	for i := range longDesc {
		longDesc[i] = 'a'
	}
	assert.Error(t, ValidateDescription(string(longDesc)))
}

func TestValidateChangeSummary(t *testing.T) {
	assert.NoError(t, ValidateChangeSummary(""))
	assert.NoError(t, ValidateChangeSummary("Fixed typo"))
	assert.NoError(t, ValidateChangeSummary(strings.Repeat("a", MaxChangeSummaryLength)))

	longSummary := strings.Repeat("a", MaxChangeSummaryLength+1)
	assert.Error(t, ValidateChangeSummary(longSummary))
}

func TestValidateNoticeText(t *testing.T) {
	assert.NoError(t, ValidateNoticeText(""))
	assert.NoError(t, ValidateNoticeText("Custom notice"))
	assert.NoError(t, ValidateNoticeText(strings.Repeat("a", MaxNoticeTextLength)))

	longText := strings.Repeat("a", MaxNoticeTextLength+1)
	assert.Error(t, ValidateNoticeText(longText))
}

func TestValidateEmail(t *testing.T) {
	assert.NoError(t, ValidateEmail("user@example.com"))
	assert.NoError(t, ValidateEmail("a@b.co"))
	assert.Error(t, ValidateEmail(""))               // empty
	assert.Error(t, ValidateEmail("noatsign"))       // no @
	assert.Error(t, ValidateEmail("@example.com"))   // no local part
	assert.Error(t, ValidateEmail("user@"))          // no domain
	assert.Error(t, ValidateEmail("user@localhost")) // no dot in domain

	longEmail := strings.Repeat("a", 250) + "@b.co"
	assert.Error(t, ValidateEmail(longEmail)) // exceeds 254
}
