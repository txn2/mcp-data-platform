package portaldomain

import (
	"reflect"
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

// The refresh queue matches recorded keys against the superseded spelling in
// SQL, so the name it compares to has to be the same one IsLegacyThumbnailKey
// recognizes here -- a queue asking about a filename this package no longer
// writes would offer every thumbnailed asset for capture, or none (#1431).
func TestLegacyThumbnailFilenameFor(t *testing.T) {
	for _, variant := range []string{ThumbnailVariantLight, ThumbnailVariantDark} {
		name := LegacyThumbnailFilenameFor(variant)
		assert.True(t, IsLegacyThumbnailKey("portal/u1/a1/"+name), variant)
		assert.False(t, IsLegacyThumbnailKey(DeriveThumbnailKeyVariant("portal/u1/a1/content.csv", variant)),
			"the current spelling is not the legacy one")
	}
	assert.Equal(t, "thumbnail.png", LegacyThumbnailFilenameFor("light"))
	assert.Equal(t, "thumbnail_dark.png", LegacyThumbnailFilenameFor("dark"))
	assert.Equal(t, "thumbnail.png", LegacyThumbnailFilenameFor("sepia"),
		"an unknown variant takes the light filename, as every other variant helper does")
}

// --- AssetUpdate.IsThumbnailOnly (#1466) ---

// The store reads this to decide whether an update is a change to the asset or
// a capture the platform asked for, and only the former moves updated_at.
func TestAssetUpdateIsThumbnailOnly(t *testing.T) {
	tests := []struct {
		name   string
		update AssetUpdate
		want   bool
	}{
		{
			name:   "a light capture stamps its key and version",
			update: AssetUpdate{ThumbnailS3Key: new("k/.thumbnail.png"), ThumbnailVersion: new(3)},
			want:   true,
		},
		{
			name:   "a dark capture stamps its own key and version",
			update: AssetUpdate{ThumbnailDarkS3Key: new("k/.thumbnail_dark.png"), ThumbnailDarkVersion: new(3)},
			want:   true,
		},
		{
			// The version write that blanks a stale pointer is still the
			// platform maintaining its own state.
			name:   "clearing a pointer",
			update: AssetUpdate{ThumbnailS3Key: new("")},
			want:   true,
		},
		{"a rename", AssetUpdate{Name: new("New")}, false},
		{"a description edit", AssetUpdate{Description: new("why it exists")}, false},
		{"a tag change", AssetUpdate{Tags: []string{"q4"}}, false},
		{"clearing every tag", AssetUpdate{Tags: []string{}}, false},
		{
			name:   "replaced content",
			update: AssetUpdate{ContentType: "text/csv", S3Key: "k/v2/content.csv", SizeBytes: 90, HasContent: true},
			want:   false,
		},
		{
			// Content that came out empty still replaced what was there.
			name:   "replaced content that is empty",
			update: AssetUpdate{HasContent: true},
			want:   false,
		},
		{"a retention cap", AssetUpdate{MaxVersions: new(10)}, false},
		{"clearing the retention cap", AssetUpdate{ClearMaxVersions: true}, false},
		{
			// A content write carries the blanked pointers with it. What the
			// person did is still the write.
			name:   "a content write that blanks the pointers",
			update: AssetUpdate{HasContent: true, ThumbnailS3Key: new(""), ThumbnailDarkS3Key: new("")},
			want:   false,
		},
		{
			name:   "an update with nothing set is not a capture",
			update: AssetUpdate{},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.update.IsThumbnailOnly())
		})
	}
}

// A field added to AssetUpdate and left unclassified would be carried by an
// update that hasAuthoredField does not see, and such an update reaches the
// store looking exactly like a thumbnail capture: written, and silently not
// counted as a change. Placing every field is what keeps that from happening
// quietly, so this fails until the new one is named on one side or the other.
func TestAssetUpdateFieldsAreClassified(t *testing.T) {
	thumbnail := map[string]bool{
		"ThumbnailS3Key":       true,
		"ThumbnailDarkS3Key":   true,
		"ThumbnailVersion":     true,
		"ThumbnailDarkVersion": true,
	}
	authored := map[string]bool{
		"Name":        true,
		"Description": true,
		"Tags":        true,
		"ContentType": true,
		"S3Key":       true,
		// SizeBytes is written only as part of a content replacement, which
		// HasContent is the signal for -- a size on its own sets no column.
		"SizeBytes":        true,
		"HasContent":       true,
		"MaxVersions":      true,
		"ClearMaxVersions": true,
	}

	for _, f := range reflect.VisibleFields(reflect.TypeFor[AssetUpdate]()) {
		assert.True(t, thumbnail[f.Name] != authored[f.Name],
			"AssetUpdate.%s is not classified: name it in exactly one of "+
				"hasThumbnailField or hasAuthoredField (and in this test's maps)", f.Name)
	}
}
