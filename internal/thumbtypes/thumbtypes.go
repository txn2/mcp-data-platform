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
	"html", "jsx", "svg", "markdown", "csv", "json", "text/plain",
	"image/png", "image/jpeg", "image/gif", "image/webp",
	"image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon",
}

// Themeable are the families drawn on a forced background, which are captured
// twice, once per color scheme. HTML, JSX, SVG and a raster image carry their
// own colors: they store a single image and serve it in both modes, so reading
// their empty dark key as "pending" would offer them forever.
var Themeable = []string{"markdown", "csv", "json", "text/plain"}

// ILikePatterns wraps content-type fragments as SQL ILIKE patterns, which is
// how the substring test the browser applies is asked of a column.
func ILikePatterns(fragments []string) []string {
	patterns := make([]string, 0, len(fragments))
	for _, f := range fragments {
		patterns = append(patterns, "%"+f+"%")
	}
	return patterns
}
