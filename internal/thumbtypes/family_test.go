package thumbtypes

import (
	"slices"
	"testing"
)

func TestFamilyPredicates(t *testing.T) {
	for _, tc := range []struct {
		ct                 string
		themeable, docLike bool
	}{
		{"text/html; charset=utf-8", true, true},
		{"text/jsx", true, true},
		{"text/markdown", true, false},
		{"TEXT/CSV", true, false},
		{"application/x-ndjson", true, false},
		{"image/svg+xml", false, false},
		{"application/xml", true, false},
		{"application/vnd.acme+json", true, false},
		{"image/png", false, false},
		{"application/pdf", false, false},
	} {
		if got := IsThemeable(tc.ct); got != tc.themeable {
			t.Errorf("IsThemeable(%q) = %v", tc.ct, got)
		}
		if got := DrawnAsDocument(tc.ct); got != tc.docLike {
			t.Errorf("DrawnAsDocument(%q) = %v", tc.ct, got)
		}
	}
}

// TestThemeableShadowsAgreeWithFirstMatch holds the SQL form of the rule to the
// Go one. The store asks "a themeable fragment and no shadow"; that equals
// "the first fragment is themeable" only while every shadow precedes every
// themeable fragment. A reordering of Capturable that breaks it fails here
// instead of letting the store and the renderer disagree about what is owed.
func TestThemeableShadowsAgreeWithFirstMatch(t *testing.T) {
	shadows := ThemeableShadows()
	firstThemeable := -1
	for i, f := range Capturable {
		if isThemeableFamily(f) {
			firstThemeable = i
			break
		}
	}
	for i, f := range Capturable {
		if !isThemeableFamily(f) && i > firstThemeable {
			for j := firstThemeable; j < len(Capturable); j++ {
				if isThemeableFamily(Capturable[j]) && j > i {
					t.Fatalf("non-themeable %q sits between themeable fragments; ThemeableShadows no longer states the rule exactly", f)
				}
			}
		}
	}
	for _, ct := range []string{
		"image/svg+xml", "text/html", "text/jsx", "application/xml", "text/markdown",
		"application/json", "text/csv", "image/png", "text/plain; charset=utf-8", "text/x-python",
	} {
		sqlForm := containsAny(ct, Themeable) && !containsAny(ct, shadows)
		if sqlForm != IsThemeable(ct) {
			t.Errorf("%q: the SQL form says themeable=%v, first-match says %v", ct, sqlForm, IsThemeable(ct))
		}
	}
}

// TestSourceLimitRaisesOnlyForPDF. The bound exists because the renderer holds
// the whole document; a PDF is the one family where that cost is not what its
// size suggests, because only page one is decoded (#1794). Every other family
// stays where it was, which is the half of the rule a change here would break
// silently.
func TestSourceLimitRaisesOnlyForPDF(t *testing.T) {
	for _, ct := range []string{"application/pdf", "APPLICATION/PDF", "application/x-pdf"} {
		if got := SourceLimit(ct); got != LargeSourceLimit {
			t.Errorf("SourceLimit(%q) = %d, want %d", ct, got, LargeSourceLimit)
		}
	}
	for _, ct := range []string{
		"text/html", "image/png", "image/svg+xml", "text/markdown",
		"application/json", "text/csv", "text/plain", "application/zip",
	} {
		if got := SourceLimit(ct); got != DefaultSourceLimit {
			t.Errorf("SourceLimit(%q) = %d, want %d", ct, got, DefaultSourceLimit)
		}
	}
}

// TestSourceLimitExprNamesTheCallersColumnsAndPlaceholder. Two stores render
// this into statements written in different placeholder styles, and each binds
// the large families to its own number.
func TestSourceLimitExprNamesTheCallersColumnsAndPlaceholder(t *testing.T) {
	want := "size_bytes <= CASE WHEN mime_type ILIKE ANY($3) THEN 33554432::bigint ELSE 1048576::bigint END"
	if got := SourceLimitExpr("size_bytes", "mime_type", "$3"); got != want {
		t.Errorf("SourceLimitExpr = %q, want %q", got, want)
	}
	want = "size_bytes <= CASE WHEN content_type ILIKE ANY(?) THEN 33554432::bigint ELSE 1048576::bigint END"
	if got := SourceLimitExpr("size_bytes", "content_type", "?"); got != want {
		t.Errorf("SourceLimitExpr = %q, want %q", got, want)
	}
}

// TestEveryLargeSourceFamilyIsCapturable. A family with its own bound that
// nothing draws is a bound on nothing, and would read as a family that gets a
// tile.
func TestEveryLargeSourceFamilyIsCapturable(t *testing.T) {
	for _, f := range LargeSourceFamilies {
		if !slices.Contains(Capturable, f) {
			t.Errorf("%q has its own source bound but is not capturable", f)
		}
	}
}
