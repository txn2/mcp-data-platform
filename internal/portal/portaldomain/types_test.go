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

func TestParseEmail(t *testing.T) {
	cases := map[string]struct {
		input string
		want  string
	}{
		"bare address":           {"user@example.com", "user@example.com"},
		"display name form":      {"Example User <User@Example.com>", "user@example.com"},
		"quoted display name":    {`"Doe, Jane" <jane.doe@example.com>`, "jane.doe@example.com"},
		"angle brackets only":    {"<user@example.com>", "user@example.com"},
		"surrounding whitespace": {"  user@example.com  ", "user@example.com"},
		"uppercase bare address": {"USER@EXAMPLE.COM", "user@example.com"},
		"display name with plus": {"Ops <ops+alerts@example.com>", "ops+alerts@example.com"},
		"subdomain":              {"user@mail.example.com", "user@mail.example.com"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseEmail(tc.input)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	for name, input := range map[string]string{
		"empty":                        "",
		"display name only":            "Example User",
		"unroutable host":              "user@localhost",
		"no local part":                "@example.com",
		"two addresses":                "a@example.com, b@example.com",
		"unclosed bracket":             "Example User <user@example.com",
		"empty angle pair":             "<>",
		"over length limit":            strings.Repeat("a", 250) + "@example.com",
		"missing domain dot":           "Example User <user@localhost>",
		"spaces inside angle brackets": "Example User < user@example.com >",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			got, err := ParseEmail(input)
			assert.Error(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestValidateShareMessage(t *testing.T) {
	for name, msg := range map[string]string{
		"empty":                "",
		"plain sentence":       "Here's the Q3 revenue breakdown you asked about.",
		"comparison operators": "margin > 40% and churn < 2%",
		"punctuation":          "Review by Friday -- numbers are final (finally!).",
		"multiline":            "Two things:\n1. the totals\n2. the footnote",
		"at the length cap":    strings.Repeat("a", MaxShareMessageLength),
		"bare domain mention":  "the numbers came from the finance export",
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			assert.NoError(t, ValidateShareMessage(msg))
		})
	}

	for name, msg := range map[string]string{
		"anchor tag":       `see <a href="https://x.io">here</a>`,
		"bold tag":         "<b>urgent</b>",
		"closing tag":      "urgent</b>",
		"spaced tag":       "< script >alert(1)",
		"doctype":          "<!DOCTYPE html>",
		"http url":         "details at http://x.io/report",
		"https url":        "details at https://x.io/report",
		"www host":         "go to www.x.io",
		"javascript uri":   "javascript:alert(1)",
		"data uri":         "data:text/html;base64,PHNjcmlwdD4=",
		"mailto uri":       "mailto:someone@example.com",
		"over length":      strings.Repeat("a", MaxShareMessageLength+1),
		"uppercase scheme": "HTTPS://X.IO",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			assert.Error(t, ValidateShareMessage(msg))
		})
	}
}

// TestIsLegacyThumbnailKey pins the names a re-capture supersedes. The hidden
// spellings must not match: deleting the key an asset row points at would take
// the thumbnail away instead of clearing the directory.
func TestIsLegacyThumbnailKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"portal/u1/a1/thumbnail.png", true},
		{"portal/u1/a1/thumbnail_dark.png", true},
		{"thumbnail.png", true},
		{"portal/u1/a1/.thumbnail.png", false},
		{"portal/u1/a1/.thumbnail_dark.png", false},
		{"portal/u1/a1/content.csv", false},
		{"portal/u1/a1/my_thumbnail.png", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, IsLegacyThumbnailKey(tt.key), tt.key)
	}
}
