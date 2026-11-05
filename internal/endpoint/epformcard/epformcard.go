// Package epformcard is what the tile and PDF renderers draw in place of a
// form an asset embeds (#2431).
package epformcard

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/headless"
)

// A tile and a PDF are drawn with no reader to fill a form in, and a form's
// page fetches its spec from its endpoint, which runs the form's script
// (#2431). So the renderers draw an embedded form as a card naming it and
// never load the page: drawing an asset runs no script and makes no request
// for the form. The card follows the reader's color scheme, which the
// renderer emulates for a dark tile.

// endpointName is an endpoint's name as a binding allows it.
var endpointName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// EmbeddedPage reports the endpoint u is the embedded form page of: a path
// of epform.PagePrefix and one endpoint name. On the rendered page's own
// origin that is the platform's form page whatever its query: a tile's
// document names it there, rewritten with no query. Elsewhere it must carry
// the embed marker the platform writes into a served asset, which is how a
// printed document names it, at the deployment's public address; the marker
// is the platform's alone, so another site's /forms/ page is left to load.
func EmbeddedPage(u *url.URL, own bool) (string, bool) {
	if u == nil || (!own && !u.Query().Has(epform.EmbedParam)) {
		return "", false
	}
	name, ok := strings.CutPrefix(u.Path, epform.PagePrefix)
	if !ok || !endpointName.MatchString(name) {
		return "", false
	}
	return name, true
}

// RenderedCard is what a renderer draws in place of an embedded form page,
// for headless.Page.Substitute: the card when u is one, and false otherwise.
func RenderedCard(u *url.URL, own bool) (headless.File, bool) {
	name, ok := EmbeddedPage(u, own)
	if !ok {
		return headless.File{}, false
	}
	return headless.File{ContentType: "text/html; charset=utf-8", Body: Card(name)}, true
}

// Card is the page drawn in place of the form an endpoint serves.
func Card(endpoint string) []byte {
	return []byte(`<!doctype html><html><head><meta charset="utf-8"><title>Form</title><style>` + cardStyle + `</style></head>` +
		`<body><div class="card" role="img" aria-label="Form: ` + html.EscapeString(endpoint) + `">` +
		`<div class="label">Form</div><div class="name">` + html.EscapeString(endpoint) + `</div>` +
		`<div class="field"></div><div class="field short"></div><div class="button"></div>` +
		`</div></body></html>`)
}

// cardStyle is the card's stylesheet, light and dark.
const cardStyle = `:root{--bg:#ffffff;--fg:#1f2937;--muted:#6b7280;--line:#e5e7eb;--field:#f3f4f6;--accent:#2563eb}
@media (prefers-color-scheme: dark){:root{--bg:#111827;--fg:#f3f4f6;--muted:#9ca3af;--line:#374151;--field:#1f2937;--accent:#60a5fa}}
html,body{margin:0;height:100%;background:var(--bg);color:var(--fg);font:14px/1.4 system-ui,-apple-system,"Segoe UI",sans-serif}
.card{box-sizing:border-box;height:100%;padding:20px;border:1px solid var(--line);border-radius:10px;display:flex;flex-direction:column;gap:10px}
.label{font-size:12px;text-transform:uppercase;letter-spacing:.06em;color:var(--muted)}
.name{font-size:16px;font-weight:600;overflow-wrap:anywhere}
.field{height:28px;border-radius:6px;background:var(--field);border:1px solid var(--line)}
.field.short{width:60%}
.button{width:96px;height:30px;border-radius:6px;background:var(--accent)}`
