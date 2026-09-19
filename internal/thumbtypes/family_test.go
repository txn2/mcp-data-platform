package thumbtypes

import "testing"

func TestFamilyPredicates(t *testing.T) {
	for _, tc := range []struct {
		ct                 string
		themeable, docLike bool
	}{
		{"text/html; charset=utf-8", false, true},
		{"text/jsx", false, true},
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
