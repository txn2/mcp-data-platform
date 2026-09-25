package thumbtypes

import (
	"slices"
	"strings"
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
// Go one. The store asks "a themeable pattern and no shadow"; that equals
// "the first pattern is themeable" only while every shadow precedes every
// themeable pattern. A reordering of Capturable that breaks it fails here
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
					t.Fatalf("non-themeable %q sits between themeable patterns; ThemeableShadows no longer states the rule exactly", f)
				}
			}
		}
	}
	for _, ct := range []string{
		"image/svg+xml", "text/html", "text/jsx", "application/xml", "text/markdown",
		"application/json", "text/csv", "image/png", "text/plain; charset=utf-8", "text/x-python",
		"application/xhtml+xml", "application/atom+xml", XLSX,
	} {
		sqlForm := matchesAny(ct, Themeable) && !matchesAny(ct, shadows)
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

func matchesAny(contentType string, patterns []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return matches(contentType, p) })
}

// The Office and OpenDocument formats and the archives are zip containers whose
// type names contain "xml" or a family word. Matched anywhere in the type, the
// workbook was offered as XML and drawn as the text of its zip bytes (#1882).
const XLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

var neverDrawn = []string{
	XLSX,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.template",
	"application/vnd.ms-excel.sheet.macroenabled.12",
	"application/vnd.oasis.opendocument.text",
	"application/vnd.oasis.opendocument.spreadsheet",
	"application/vnd.oasis.opendocument.presentation",
	"application/zip", "application/gzip", "application/x-tar",
	"application/vnd.apache.parquet", "application/octet-stream",
	"image/tiff", "image/heic", "audio/mpeg", "video/mp4",
	// Contain a family word without being that family.
	"application/x-javascript-bundle+zip", "application/x-sqlite3", "application/vnd.acme.jsonish",
	// Not a media type at all: nothing to classify.
	"", "not a media type",
}

func TestContainersAndBinariesAreNeverDrawn(t *testing.T) {
	for _, ct := range neverDrawn {
		if f := family(ct); f != "" {
			t.Errorf("%q is offered for a tile as %q; nothing can draw it", ct, f)
		}
		if IsThemeable(ct) || DrawnAsDocument(ct) || DrawnFromHead(ct) {
			t.Errorf("%q answers as a drawn family", ct)
		}
		if matchesAny(ct, Capturable) {
			t.Errorf("%q matches the pattern list the stores send as SQL", ct)
		}
	}
}

// The XML family is still drawn: by its registered types, and by any dialect's
// structured suffix, with or without parameters. SVG and XHTML end in "+xml"
// too and keep their own families, which is what the order decides.
func TestXMLIsDrawnByTypeAndSuffix(t *testing.T) {
	for ct, want := range map[string]string{
		"application/xml":                         "application/xml",
		"text/xml; charset=utf-8":                 "application/xml",
		"application/atom+xml":                    "%+xml",
		"application/rss+xml; charset=utf-8":      "%+xml",
		"image/svg+xml":                           "image/svg+xml",
		"application/svg+xml":                     "image/svg+xml",
		"application/xhtml+xml":                   "application/xhtml+xml",
		"application/vnd.acme.report+json":        "%+json",
		"application/problem+json; charset=utf-8": "application/json",
		"text/x-yaml":                             "application/yaml",
		"text/plain; charset=utf-8":               "text/%",
		"text/x-go":                               "text/%",
		"text/csv":                                "text/csv",
	} {
		if got := family(ct); got != want {
			t.Errorf("family(%q) = %q, want %q", ct, got, want)
		}
	}
}

// ilike is PostgreSQL's ILIKE for the patterns Patterns writes: '%' is any run
// of characters and case is ignored.
func ilike(value, pattern string) bool {
	v, parts := strings.ToLower(value), strings.Split(strings.ToLower(pattern), "%")
	if !strings.HasPrefix(v, parts[0]) {
		return false
	}
	v = v[len(parts[0]):]
	last := len(parts) - 1
	if last == 0 {
		return v == ""
	}
	for _, part := range parts[1:last] {
		i := strings.Index(v, part)
		if i < 0 {
			return false
		}
		v = v[i+len(part):]
	}
	return strings.HasSuffix(v, parts[last])
}

func ilikeAny(value string, patterns []string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return ilike(value, p) })
}

// TestSQLAndGoClassifyAlike. The stores ask the rule of a column as ILIKE over
// Patterns; the worker and the tile page ask it of one value. Every type here --
// canonical, an alias spelling, with parameters, and the containers that must
// not match -- gets the same three answers both ways.
func TestSQLAndGoClassifyAlike(t *testing.T) {
	for _, p := range Patterns(Capturable) {
		if strings.ContainsAny(p, `_\`) {
			t.Errorf("pattern %q uses '_' or a backslash, which ILIKE reads as more than a character", p)
		}
	}
	corpus := slices.Concat(neverDrawn, []string{
		"text/csv", "text/csv; header=present", "application/csv", "text/tsv", "text/tab-separated-values",
		"text/html", "text/html; charset=utf-8", "text/jsx", "text/babel", "application/xhtml+xml",
		"text/markdown", "text/x-markdown", "application/json", "text/json", "application/ld+json",
		"application/x-ndjson", "application/jsonl", "application/vnd.acme+json",
		"application/yaml", "text/yaml", "application/xml", "text/xml", "application/atom+xml",
		"application/sql", "text/x-sql", "text/x-python", "text/javascript", "application/javascript",
		"text/css", "text/plain", "text/plain; charset=utf-8", "text/calendar", "TEXT/X-GO; charset=utf-8",
		"image/svg+xml", "application/svg+xml",
		"application/pdf", "application/x-pdf", "image/png", "image/jpg", "image/x-icon",
	})
	for _, ct := range corpus {
		if sql, goForm := ilikeAny(ct, Patterns(Capturable)), family(ct) != ""; sql != goForm {
			t.Errorf("%q: SQL offers it=%v, Go classifies it=%v", ct, sql, goForm)
		}
		sqlThemeable := ilikeAny(ct, Patterns(Themeable)) && !ilikeAny(ct, Patterns(ThemeableShadows()))
		if sqlThemeable != IsThemeable(ct) {
			t.Errorf("%q: SQL themeable=%v, Go themeable=%v", ct, sqlThemeable, IsThemeable(ct))
		}
		if sql, goForm := ilikeAny(ct, Patterns(LargeSourceFamilies)), SourceLimit(ct) == LargeSourceLimit; sql != goForm {
			t.Errorf("%q: SQL raises the bound=%v, Go=%v", ct, sql, goForm)
		}
	}
}
