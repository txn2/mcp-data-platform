package script

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// The rules for the three fields that explain a script to a person: its display
// name, its description, and the category it is filed under (#1369).
//
// A managed script is complex logic that outlives the conversation that
// produced it, so its description is a DOCUMENT rather than a caption: markdown
// an author writes at whatever length the automation actually needs explaining
// at. That is why the bounds here are not the ones an asset or a resource puts
// on its own description. Those are caption bounds — 2000 characters, about half
// a page — and capping a script description there would cap the thing this
// documentation exists to hold.
const (
	// MaxDisplayNameLen bounds a script's display name, in runes, at the limit
	// its siblings use (resource.MaxDisplayNameLen). Unlike the description
	// there is no reading on which a label wants to be long: it is what the
	// listing, the page header and a search result print, and every one of them
	// truncates. An empty display name is allowed and falls back to the name an
	// agent calls the script by (Title).
	MaxDisplayNameLen = 200

	// MaxDescriptionBytes is the ceiling a description is refused above, and it
	// is a structural limit rather than an editorial one: it exists to protect
	// the row, the version history, and the search vector.
	//
	// The vector is the binding constraint. script_fts is composed from the
	// description together with the title, the category, the tags and the
	// parameter contract, and it is built into a GIN index expression
	// (migration 000102), so to_tsvector runs on every write. PostgreSQL
	// refuses a tsvector input over 1 MiB, and an index expression that raises
	// makes the row unwritable rather than merely unfindable — the description
	// would take the script down with it. 64 KiB leaves the composed document
	// an order of magnitude inside that limit even with every other indexed
	// field at its own maximum, and it is roughly twenty pages of markdown,
	// which is far past any honest documentation of one script.
	MaxDescriptionBytes = 64 * 1024

	// longDescriptionBytes is where a description stops being a document about
	// one script and starts being a document that wants a home of its own. It
	// blocks nothing: crossing it produces the advisory DescriptionNotice
	// returns, exactly as knowledgepage.SplitSuggestion signals an oversized
	// page without ever refusing the write, and it is set at the same ~16 KiB.
	longDescriptionBytes = 16 * 1024

	// maxCategoryLen is the category bound, matching resource.MaxPathSegmentLen.
	// It is the pattern below that actually enforces it; the constant names the
	// number the error message quotes.
	maxCategoryLen = 31
)

// categoryPattern is the category shape, identical to the one resources and
// insights use: a lowercase slug. A category is an axis a listing filters on
// and a facet a reader scans, so it has to be one value written one way —
// "Sales Reports", "sales-reports" and "sales_reports" filing as three
// categories is the failure a free-text field has.
var categoryPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// validateDisplayName bounds the display name. Empty is allowed: a script whose
// author never wrote one is displayed under the name an agent calls it by.
func validateDisplayName(name string) error {
	if utf8.RuneCountInString(name) > MaxDisplayNameLen {
		return fmt.Errorf("display_name must be at most %d characters", MaxDisplayNameLen)
	}
	return nil
}

// validateDescription bounds the description at what the storage and the search
// vector can carry. Everything below that ceiling is accepted, including the
// long ones: see DescriptionNotice for the advisory that fires first.
func validateDescription(desc string) error {
	if len(desc) > MaxDescriptionBytes {
		return fmt.Errorf("description is %d bytes, over the %d-byte limit", len(desc), MaxDescriptionBytes)
	}
	return nil
}

// validateCategory checks the category slug. Empty is allowed: a category is
// how an author files a script, and most scripts are never filed.
func validateCategory(category string) error {
	if category == "" {
		return nil
	}
	if !categoryPattern.MatchString(category) {
		return fmt.Errorf(
			"category must be at most %d characters of lowercase letters, digits, and hyphens, starting with a letter, got %q",
			maxCategoryLen, category)
	}
	return nil
}

// DescriptionNotice is the non-blocking signal that a description has outgrown
// the script it documents, and empty when it has not.
//
// It is advisory by construction, which is the whole point of it. The platform
// wants long descriptions — that is what makes a stored automation
// understandable months later by somebody who did not write it — so the only
// hard refusal is the structural one (MaxDescriptionBytes), and everything
// short of it succeeds. This says what the author might do instead; it never
// decides for them.
//
// The signal is size alone, unlike the knowledge page's, which also counts
// headings. A page with twelve top-level sections is covering several topics
// and wants splitting; a script description with twelve sections is still about
// one script, and refusing to distinguish the two would put a nudge in front of
// an author who is documenting thoroughly and correctly.
func DescriptionNotice(desc string) string {
	if len(desc) < longDescriptionBytes {
		return ""
	}
	return fmt.Sprintf(
		"this description is %d bytes, which is long enough to be a document in its own right; "+
			"consider moving the background to a knowledge page and leaving the description to what "+
			"this script does, takes and produces", len(desc))
}
