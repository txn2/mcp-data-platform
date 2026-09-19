// Package thumbtypes names the content families a browser can rasterize into a
// tile, and which of them are drawn on a forced background and so need a second
// capture for dark mode.
//
// It exists because the rule was written out four times -- once per store, once
// for the browser gate, once for the capturer's dispatch -- and the four copies
// stopped agreeing: a JSX resource was never offered although the capturer
// renders JSX, and an image asset was never offered although the capturer
// downscales images (#1568). This is the one Go definition; the one TypeScript
// definition is ui/src/lib/thumbnailSupport.ts, and TestGoAndBrowserAgree holds
// the two to each other.
//
// A thumbnail is a property of a piece of content rather than of a kind, so
// nothing here knows whether the row it is asked about is a portal asset or a
// managed resource.
package thumbtypes

import (
	"slices"
	"strings"
)

// Capturable are the content families a browser can draw into a tile, as
// fragments of the media type rather than exact types: a stored type carries
// parameters and vendor prefixes ("text/markdown; charset=utf-8",
// "application/vnd.acme+json"), and every spelling of a family contains its
// fragment.
//
// "json" covers both JSON families at once, newline-delimited included
// ("application/x-ndjson", "application/jsonl"); which of the two is drawn is a
// question only the capturer asks. "text/plain" is spelled in full because the
// bare word is a substring of "text/html", "text/csv" and "text/markdown", each
// of which is drawn differently.
//
// The code families -- YAML, XML, SQL, Python, JavaScript, CSS -- and TSV are
// here because the viewer renders every one of them and a browser that can
// render it can draw a tile of it; they were absent, and each kept a
// content-type icon forever (#1754). Order carries the two overlaps: "svg"
// takes image/svg+xml before "xml" is reached, and "jsx" takes text/jsx before
// "javascript" is. SVG is first because it is the one family ahead of a
// themeable one that is not itself themeable; see ThemeableShadows.
//
// The raster families are named one by one rather than as "image/", which is
// what a bare prefix would have cost: a capture DOWNSCALES a raster image by
// decoding it in the browser, and TIFF, HEIC and PSD are images a browser
// cannot decode at all. Offering one is offering work that fails every time,
// and the pending query is an ORDER BY ... LIMIT window, so a bulk upload of
// them would fill it and starve the documents behind them of a capture they
// could actually complete -- the same failure the PDF exclusion exists to
// prevent. These eight are the families every current browser decodes.
//
// Everything else -- PDF, spreadsheets, archives, binaries -- has no renderer,
// keeps its content-type icon, and is never offered for capture.
var Capturable = []string{
	"svg", "html", "jsx", "markdown", "csv", "tab-separated", "json",
	"yaml", "xml", "sql", "python", "javascript", "css", "text/plain",
	"image/png", "image/jpeg", "image/gif", "image/webp",
	"image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon",
}

// Themeable are the families drawn once per color scheme.
//
// The families the portal lays out on its own surface are drawn on the
// scheme's background. HTML and JSX are drawn with the renderer emulating the
// scheme, which is what a document's own prefers-color-scheme rules answer to
// and what the viewer's frame does for a reader in that scheme: a dashboard
// with a dark stylesheet opened dark and had a white card (#1789). SVG and a
// raster image are drawn as stored and serve the one image in both modes, so
// reading their empty dark key as "pending" would offer them forever.
//
// In Capturable's order, which is what the parity test compares.
var Themeable = []string{
	"html", "jsx", "markdown", "csv", "tab-separated", "json",
	"yaml", "xml", "sql", "python", "javascript", "css", "text/plain",
}

// ILikePatterns wraps content-type fragments as SQL ILIKE patterns, which is
// how the substring test the browser applies is asked of a column.
func ILikePatterns(fragments []string) []string {
	patterns := make([]string, 0, len(fragments))
	for _, f := range fragments {
		patterns = append(patterns, "%"+f+"%")
	}
	return patterns
}

// IsThemeable reports whether contentType is drawn on a forced background and
// so has a tile per color scheme.
//
// A content type belongs to the family of the FIRST Capturable fragment it
// contains, which is how Capturable's order resolves overlaps: image/svg+xml
// contains both "svg" and "xml", is an SVG, and is drawn as stored. Asking
// only "does it contain a themeable fragment" would call it themeable, and a
// store asking that would owe every SVG a dark tile nothing ever draws.
func IsThemeable(contentType string) bool {
	return isThemeableFamily(family(contentType))
}

// ThemeableShadows are the fragments that are not themeable and come before
// the themeable ones in Capturable's order. A content type is themeable
// exactly when it contains a themeable fragment and none of these, which is
// the form a store's SQL asks it in. That is exact only while every such
// fragment precedes all the themeable ones; a test holds the order to it.
func ThemeableShadows() []string {
	var out []string
	for _, f := range Capturable {
		if isThemeableFamily(f) {
			break
		}
		out = append(out, f)
	}
	return out
}

// family is the first Capturable fragment contentType contains, or "".
func family(contentType string) string {
	ct := strings.ToLower(contentType)
	for _, f := range Capturable {
		if strings.Contains(ct, f) {
			return f
		}
	}
	return ""
}

func isThemeableFamily(fragment string) bool {
	return slices.Contains(Themeable, fragment)
}

// DrawnAsDocument reports whether contentType is a document that lays itself
// out at page size -- HTML and JSX -- rather than a family the portal lays out
// on its own tile-sized surface. The two are drawn at different geometries.
func DrawnAsDocument(contentType string) bool {
	return containsAny(contentType, []string{"html", "jsx"})
}

func containsAny(contentType string, fragments []string) bool {
	ct := strings.ToLower(contentType)
	for _, f := range fragments {
		if strings.Contains(ct, f) {
			return true
		}
	}
	return false
}
