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
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// A content type is classified by its canonical media type -- parameters
// removed, case folded, an alias settled on the type it spells
// (pkg/contenttype.Normalize) -- compared whole against the types each family
// names. Two entries are wider than one type: "%+json" is every type ending in
// the structured suffix "+json", and "text/%" is every type under text/. The
// stores ask the same of a column in SQL, through Patterns.
//
// The rule was a list of fragments matched anywhere in the type, and every
// Office Open XML type contains "xml" ("openxmlformats", "spreadsheetml"): an
// Excel workbook was offered as XML and drawn as the text of its zip bytes
// (#1882).
//
// The groups below are the families; each list is in the order its entries
// are tried within Capturable.
var (
	// svgTypes and pdfTypes lead Capturable: they are the families ahead of a
	// themeable one that are not themeable themselves; see ThemeableShadows.
	// image/svg+xml also ends in "+xml", which is why SVG comes first.
	svgTypes = []string{"image/svg+xml"}

	// PDF is drawn by the tile page itself rather than by a viewer renderer:
	// the viewer hands a PDF to the browser's own plugin, which headless-shell
	// does not ship, and page one is a raster the tile page produces with
	// pdf.js instead (#1794).
	pdfTypes = []string{"application/pdf", "application/x-pdf"}

	// documentTypes lay themselves out at page size. XHTML is here, ahead of
	// the "+xml" suffix, because a browser draws it as the document it is.
	documentTypes = []string{"text/html", "application/xhtml+xml", "text/jsx"}

	markdownTypes = []string{"text/markdown"}

	tableTypes = []string{"text/csv", "text/tab-separated-values"}

	// Both JSON families, newline-delimited included, and every vendor dialect
	// by its structured suffix; which of the two is drawn is a question only
	// the capturer asks.
	jsonTypes = []string{"application/json", "application/x-ndjson", "%+json"}

	// The code families and every other text/ type: the viewer renders every
	// one of them, the last as plain text, and a browser that can render it can
	// draw a tile of it; they were absent, and each kept a content-type icon
	// forever (#1754). An XML dialect is named by its "+xml" suffix, after SVG
	// and XHTML have been tried, and "text/%" comes after every text/ type a
	// family above names.
	codeTypes = []string{
		"application/yaml", "application/xml", "%+xml",
		"application/sql", "text/x-python", "text/javascript", "text/css", "text/%",
	}

	// The raster families are named one by one rather than as "image/": a
	// capture DOWNSCALES a raster image by decoding it in the browser, and
	// TIFF, HEIC and PSD are images a browser cannot decode at all. Offering
	// one is offering work that fails every time, and the pending query is an
	// ORDER BY ... LIMIT window, so a bulk upload of them would fill it and
	// starve the documents behind them of a capture they could actually
	// complete. These are the families every current browser decodes.
	rasterTypes = []string{
		"image/png", "image/jpeg", "image/gif", "image/webp",
		"image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon",
	}
)

// Capturable names every content type a browser can draw into a tile, in the
// order the entries are tried: a type belongs to the family of the FIRST entry
// it matches.
//
// Everything else -- spreadsheets, word-processing documents, presentations,
// archives, binaries -- has no renderer, keeps its content-type icon, and is
// never offered for capture.
var Capturable = slices.Concat(svgTypes, pdfTypes, documentTypes, markdownTypes, tableTypes,
	jsonTypes, codeTypes, rasterTypes)

// Themeable are the families drawn once per color scheme.
//
// The families the portal lays out on its own surface are drawn on the
// scheme's background. HTML and JSX are drawn with the renderer emulating the
// scheme, which is what a document's own prefers-color-scheme rules answer to
// and what the viewer's frame does for a reader in that scheme: a dashboard
// with a dark stylesheet opened dark and had a white card (#1789). SVG, PDF
// and a raster image are drawn as stored and serve the one image in both
// modes, so reading their empty dark key as "pending" would offer them
// forever.
//
// In Capturable's order, which is what the parity test compares.
var Themeable = slices.Concat(documentTypes, markdownTypes, tableTypes, jsonTypes, codeTypes)

// DefaultSourceLimit is the largest document a tile is drawn from. A tile is
// drawn by loading the whole document into the renderer beside the platform,
// whose memory is sized for documents, not archives; above it a file keeps its
// content-type icon (#1351).
const DefaultSourceLimit = 1 << 20 // 1 MB

// LargeSourceLimit is the bound the families in LargeSourceFamilies are held
// to instead.
const LargeSourceLimit = 32 << 20 // 32 MB

// LargeSourceFamilies are the families whose source bound is LargeSourceLimit
// rather than DefaultSourceLimit.
//
// What the default bound protects against is the renderer holding a whole
// document. A family is here when its tile costs less of that than the file's
// size suggests, because the tile is drawn from a part of the file rather than
// from all of it.
//
// PDF: the tile page decodes page one and nothing else, and the default bound
// would leave the feature looking broken on the documents it exists for --
// one letter page scanned at 300dpi measures about 2 MB, twice the default, so
// most scanned PDFs would keep an icon (#1794). A 26 MB, 12-page scan drew in
// 2.7s with the renderer at 291 MiB.
//
// CSV and TSV: the tile is the header row and the first rows, so the worker
// hands the renderer the head of the file and nothing else (HeadDrawnFamilies).
// A CSV is the family most likely to be large -- it is what an export
// produces, what a script lands on a schedule, and what a person uploads to
// register as a table -- and at the default bound the files people upload most
// kept an icon while the small ones tiled (#1802).
//
// The bound is still the most the worker reads from the object store for a
// tile, which is why a family here has one at all. Every other family is held
// to DefaultSourceLimit, because every other family is laid out in full to be
// drawn.
var LargeSourceFamilies = slices.Concat(pdfTypes, tableTypes)

// HeadDrawnFamilies are the families whose tile is drawn from the head of the
// document rather than from the whole of it.
//
// A table's tile is its header row and its first rows -- the tile page keeps
// ten of them -- so the rest of the document is parsed, copied through a
// JavaScript string and discarded. The worker cuts the file at a record
// boundary before the tile page is built, which is what makes the raised bound
// above safe for these two (#1802).
//
// PDF is NOT here although only its first page is drawn: its bytes travel to
// the tile page by URL and pdf.js reads the pages it needs itself, so there is
// nothing for the worker to cut.
var HeadDrawnFamilies = tableTypes

// DrawnFromHead reports whether a tile of contentType is drawn from the head
// of the document, so the worker may hand the tile page a prefix of it.
//
// The family is the FIRST Capturable entry the type matches, as it is
// everywhere else here.
func DrawnFromHead(contentType string) bool {
	return slices.Contains(HeadDrawnFamilies, family(contentType))
}

// SourceLimit is the largest document of contentType's family a tile is drawn
// from.
//
// The family is the FIRST Capturable entry the type matches, as it is
// everywhere else here, so a bound is raised for the family a type is actually
// drawn as rather than for any entry it would also match.
func SourceLimit(contentType string) int64 {
	if slices.Contains(LargeSourceFamilies, family(contentType)) {
		return LargeSourceLimit
	}
	return DefaultSourceLimit
}

// SourceLimitExpr is the same bound as a SQL predicate, over the column
// holding a row's stored size and the column holding its content type.
// familiesPlaceholder is where the caller binds Patterns(LargeSourceFamilies), in the placeholder style its own statement is written in ("?" for
// a builder that renumbers, "$3" for a hand-numbered statement).
//
// The two limits are written into the expression rather than bound, so the
// whole rule is one fragment: a caller that had to append them as arguments
// could append them in the wrong order and still compile.
func SourceLimitExpr(sizeCol, typeCol, familiesPlaceholder string) string {
	return fmt.Sprintf("%s <= CASE WHEN %s ILIKE ANY(%s) THEN %d::bigint ELSE %d::bigint END",
		sizeCol, typeCol, familiesPlaceholder, LargeSourceLimit, DefaultSourceLimit)
}

// Patterns are the SQL ILIKE patterns that ask of a column what matches asks of
// one value: every spelling of each type (contenttype.Spellings), bare and with
// parameters, and each structured suffix likewise. A type is stored canonical,
// lowercase and parameter-free when the platform writes it, so the other forms
// cover rows written before it did; ILIKE ignores case either way.
func Patterns(entries []string) []string {
	var patterns []string
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e, wildcard):
			// A prefix already admits whatever follows it, parameters included.
			patterns = append(patterns, e)
		case strings.HasPrefix(e, wildcard):
			patterns = append(patterns, e, e+";%")
		default:
			for _, s := range contenttype.Spellings(e) {
				patterns = append(patterns, s, s+";%")
			}
		}
	}
	return patterns
}

// IsThemeable reports whether contentType is drawn on a forced background and
// so has a tile per color scheme.
//
// A content type belongs to the family of the FIRST Capturable entry it
// matches, which is how Capturable's order resolves overlaps: image/svg+xml
// matches both "image/svg+xml" and "%+xml", is an SVG, and is drawn as stored.
// Asking only "does it match a themeable entry" would call it themeable, and a
// store asking that would owe every SVG a dark tile nothing ever draws.
func IsThemeable(contentType string) bool {
	return isThemeableFamily(family(contentType))
}

// ThemeableShadows are the entries that are not themeable and come before
// the themeable ones in Capturable's order. A content type is themeable
// exactly when it matches a themeable entry and none of these, which is the
// form a store's SQL asks it in. That is exact only while every such entry
// precedes all the themeable ones; a test holds the order to it.
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

// wildcard marks an entry wider than one type: leading, a structured suffix;
// trailing, a type prefix. It is also SQL ILIKE's any-run character, which is
// why Patterns can pass such an entry through.
const wildcard = "%"

// family is the first Capturable entry contentType matches, or "".
func family(contentType string) string {
	for _, e := range Capturable {
		if matches(contentType, e) {
			return e
		}
	}
	return ""
}

// matches reports whether contentType's canonical type is the one entry
// names, ends in the structured suffix an entry written "%+suffix" names, or
// starts with the prefix an entry written "prefix/%" names.
func matches(contentType, entry string) bool {
	canonical := contenttype.Normalize(contentType)
	if canonical == "" {
		return false
	}
	if suffix, ok := strings.CutPrefix(entry, wildcard); ok {
		return strings.HasSuffix(canonical, suffix)
	}
	if prefix, ok := strings.CutSuffix(entry, wildcard); ok {
		return strings.HasPrefix(canonical, prefix)
	}
	return canonical == entry
}

func isThemeableFamily(entry string) bool {
	return slices.Contains(Themeable, entry)
}

// DrawnAsDocument reports whether contentType is a document that lays itself
// out at page size -- HTML and JSX -- rather than a family the portal lays out
// on its own tile-sized surface. The two are drawn at different geometries.
func DrawnAsDocument(contentType string) bool {
	return slices.Contains(documentTypes, family(contentType))
}
