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

// TestSourceLimitRaisesForTheFamiliesDrawnFromPartOfTheFile. The bound exists
// because the renderer holds the whole document; it rises for a family whose
// tile is drawn from part of the file -- page one of a PDF (#1794), the first
// rows of a table (#1802). Every other family stays where it was, which is the
// half of the rule a change here would break silently.
func TestSourceLimitRaisesForTheFamiliesDrawnFromPartOfTheFile(t *testing.T) {
	for _, ct := range []string{
		"application/pdf", "APPLICATION/PDF", "application/x-pdf",
		"text/csv", "TEXT/CSV; charset=utf-8", "text/tab-separated-values",
	} {
		if got := SourceLimit(ct); got != LargeSourceLimit {
			t.Errorf("SourceLimit(%q) = %d, want %d", ct, got, LargeSourceLimit)
		}
	}
	for _, ct := range []string{
		"text/html", "image/png", "image/svg+xml", "text/markdown",
		"application/json", "text/plain", "application/zip",
	} {
		if got := SourceLimit(ct); got != DefaultSourceLimit {
			t.Errorf("SourceLimit(%q) = %d, want %d", ct, got, DefaultSourceLimit)
		}
	}
}

// TestDrawnFromHead. The worker hands the tile page a prefix of a document in
// this family and the whole of every other, so a family that answers yes here
// and is laid out in full would be drawn from a document with its tail cut
// off.
func TestDrawnFromHead(t *testing.T) {
	for _, ct := range []string{"text/csv", "TEXT/CSV", "text/tab-separated-values"} {
		if !DrawnFromHead(ct) {
			t.Errorf("DrawnFromHead(%q) = false, want true", ct)
		}
	}
	for _, ct := range []string{
		"application/pdf", "text/html", "text/markdown", "application/json",
		"text/plain", "image/png", "application/zip",
	} {
		if DrawnFromHead(ct) {
			t.Errorf("DrawnFromHead(%q) = true, want false", ct)
		}
	}
}

// TestEveryHeadDrawnFamilyIsHeldToTheRaisedBound. The head cut is what makes
// the raised bound safe: a family drawn from its head but held to the default
// bound gains nothing from the cut, and one past the default bound without a
// cut hands the renderer a document the bound exists to refuse.
func TestEveryHeadDrawnFamilyIsHeldToTheRaisedBound(t *testing.T) {
	for _, f := range HeadDrawnFamilies {
		if !slices.Contains(Capturable, f) {
			t.Errorf("%q is drawn from its head but is not capturable", f)
		}
		if !slices.Contains(LargeSourceFamilies, f) {
			t.Errorf("%q is drawn from its head but is held to the default source bound", f)
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
