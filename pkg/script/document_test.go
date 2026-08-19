package script

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidate_BoundsTheDocumentationFields proves the three fields that
// explain a script are bounded on the whole record, which is where every
// mutation surface checks them (#1369).
func TestValidate_BoundsTheDocumentationFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Script)
		wantErr string
	}{
		{
			name:   "a display name at the limit is accepted",
			mutate: func(s *Script) { s.DisplayName = strings.Repeat("a", MaxDisplayNameLen) },
		},
		{
			name:    "a display name over the limit is refused",
			mutate:  func(s *Script) { s.DisplayName = strings.Repeat("a", MaxDisplayNameLen+1) },
			wantErr: "display_name must be at most 200 characters",
		},
		{
			name: "the display-name bound counts runes, not bytes",
			// 200 multi-byte runes is 600 bytes and must still be accepted: the
			// limit is what a header can print, and a header prints characters.
			mutate: func(s *Script) { s.DisplayName = strings.Repeat("é", MaxDisplayNameLen) },
		},
		{
			name:   "an empty display name is accepted",
			mutate: func(s *Script) { s.DisplayName = "" },
		},
		{
			name: "a description far past the caption bound is accepted",
			// The point of the ticket: an asset's 2000-character description
			// bound would cap the documentation this field exists to hold.
			mutate: func(s *Script) { s.Description = strings.Repeat("x", 40_000) },
		},
		{
			name:    "a description over the structural ceiling is refused",
			mutate:  func(s *Script) { s.Description = strings.Repeat("x", MaxDescriptionBytes+1) },
			wantErr: "over the 65536-byte limit",
		},
		{
			name:   "a category slug is accepted",
			mutate: func(s *Script) { s.Category = "sales-reporting" },
		},
		{
			name:   "no category is accepted",
			mutate: func(s *Script) { s.Category = "" },
		},
		{
			name:    "an uppercase category is refused",
			mutate:  func(s *Script) { s.Category = "Sales" },
			wantErr: "category must be at most 31 characters",
		},
		{
			name:    "a category with an underscore is refused",
			mutate:  func(s *Script) { s.Category = "sales_reporting" },
			wantErr: "category must be at most 31 characters",
		},
		{
			name:    "a category starting with a digit is refused",
			mutate:  func(s *Script) { s.Category = "1sales" },
			wantErr: "category must be at most 31 characters",
		},
		{
			name:    "a category over the length is refused",
			mutate:  func(s *Script) { s.Category = strings.Repeat("a", 32) },
			wantErr: "category must be at most 31 characters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &Script{
				Name: "daily-sales", Scope: ScopePersonal,
				Source: "print(1)", Status: StatusDraft,
			}
			tt.mutate(sc)

			err := sc.Validate()

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestDescriptionNotice proves the long-description signal is advisory: it
// fires only well above what any script needs, and the write that produced it
// has already succeeded by the time anyone reads it.
func TestDescriptionNotice(t *testing.T) {
	assert.Empty(t, DescriptionNotice(""), "an undocumented script is not nagged")
	assert.Empty(t, DescriptionNotice(strings.Repeat("x", 2_000)),
		"a description past every sibling's caption bound is still an ordinary description")
	assert.Empty(t, DescriptionNotice(strings.Repeat("x", longDescriptionBytes-1)))

	notice := DescriptionNotice(strings.Repeat("x", longDescriptionBytes))
	assert.Contains(t, notice, "knowledge page")
	assert.Contains(t, notice, "16384 bytes")

	// And the advisory is not a refusal: the same description validates.
	sc := &Script{
		Name: "daily-sales", Scope: ScopePersonal, Source: "print(1)", Status: StatusDraft,
		Description: strings.Repeat("x", longDescriptionBytes),
	}
	require.NoError(t, sc.Validate())
}

// TestSnapshotChanged_CarriesTheCategory proves filing a script produces a
// version. The four documentation fields are versioned together, so a reader of
// the history can see what the script claimed to be at the time a run ran.
func TestSnapshotChanged_CarriesTheCategory(t *testing.T) {
	before := &Script{Name: "daily", Source: "print(1)"}
	after := *before
	after.Category = "reporting"

	assert.True(t, SnapshotChanged(before, &after))
	assert.False(t, RequiresReview(before, &after),
		"filing a script is not a change to what it does, so it must not go to review")
}

// TestRequiresReview_DocumentingAnApprovedScriptIsNotAReview is the ticket's
// governance claim, proved against the gate rather than asserted in prose: an
// owner documenting a script the platform is executing does not send that
// script back to a reviewer, and the version being executed is untouched.
func TestRequiresReview_DocumentingAnApprovedScriptIsNotAReview(t *testing.T) {
	before := &Script{
		Name: "daily", Source: "print(1)", ApprovedVersionID: "sver_1",
		DisplayName: "Daily", Description: "old", Category: "old-cat", Tags: []string{"a"},
	}
	after := *before
	after.DisplayName = "Daily Sales"
	after.Description = "## What it produces\n\nA CSV."
	after.Category = "reporting"
	after.Tags = []string{"sales"}

	assert.False(t, RequiresReview(before, &after))
	assert.True(t, SnapshotChanged(before, &after), "the change is still captured as a version")
	assert.Equal(t, "sver_1", after.ApprovedVersionID, "the executing version is untouched")
}

// TestIndexText_CarriesTheCategory proves the category reaches the text a
// script is embedded on and shown as, alongside its tags. The lexical arm reads
// it through script_fts; this is the semantic arm's half of the same corpus.
func TestIndexText_CarriesTheCategory(t *testing.T) {
	sc := &Script{
		Name: "daily", DisplayName: "Daily Sales", Category: "reporting",
		Tags: []string{"sales"}, Description: "By region.",
	}

	text := IndexText(sc)

	assert.Contains(t, text, "reporting")
	assert.Contains(t, text, "sales")

	// A script nobody has filed pads the text with nothing, so an uncategorized
	// and untagged script does not embed a blank line.
	bare := IndexText(&Script{Name: "daily"})
	assert.NotContains(t, bare, "\n\n")
}
