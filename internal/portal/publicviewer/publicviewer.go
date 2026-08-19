// Package publicviewer holds the embedded assets of the portal's public share
// pages: the two HTML templates, their Content-Security-Policy, and the default
// brand mark.
//
// These are the parts of the public viewer that are pure content — no request
// handling, no storage access, no share gating. Keeping them here means a
// change to the viewer's markup or its CSP is reviewed on its own, separate
// from the access-control code in pkg/portal that decides who may reach it.
package publicviewer

import (
	"embed"
	"html/template"
)

//go:embed templates/public_viewer.html templates/public_collection_viewer.html
var templateFS embed.FS

// AssetTemplate renders the single-asset public viewer page. It is also used
// for a collection item opened in the collection viewer's iframe.
var AssetTemplate = template.Must(template.ParseFS(templateFS, "templates/public_viewer.html"))

// CollectionTemplate renders the public collection viewer page.
var CollectionTemplate = template.Must(template.ParseFS(templateFS, "templates/public_collection_viewer.html"))

// DefaultLogoSVG is the platform logo used in the public viewer header when no
// brand logo is configured. It matches the platform-info app's default icon.
//
//nolint:lll // SVG markup
const DefaultLogoSVG = `<svg viewBox="0 0 40 40" fill="none" xmlns="http://www.w3.org/2000/svg">` +
	`<circle cx="20" cy="20" r="4.5" fill="currentColor" opacity=".95"/>` +
	`<circle cx="6"  cy="11" r="3"   fill="currentColor" opacity=".65"/>` +
	`<circle cx="34" cy="11" r="3"   fill="currentColor" opacity=".65"/>` +
	`<circle cx="6"  cy="29" r="3"   fill="currentColor" opacity=".45"/>` +
	`<circle cx="34" cy="29" r="3"   fill="currentColor" opacity=".45"/>` +
	`<circle cx="20" cy="4"  r="2.2" fill="currentColor" opacity=".55"/>` +
	`<circle cx="20" cy="36" r="2.2" fill="currentColor" opacity=".35"/>` +
	`<line x1="20" y1="20" x2="6"  y2="11" stroke="currentColor" stroke-width="1.4" opacity=".3"/>` +
	`<line x1="20" y1="20" x2="34" y2="11" stroke="currentColor" stroke-width="1.4" opacity=".3"/>` +
	`<line x1="20" y1="20" x2="6"  y2="29" stroke="currentColor" stroke-width="1.4" opacity=".22"/>` +
	`<line x1="20" y1="20" x2="34" y2="29" stroke="currentColor" stroke-width="1.4" opacity=".22"/>` +
	`<line x1="20" y1="20" x2="20" y2="4"  stroke="currentColor" stroke-width="1.4" opacity=".28"/>` +
	`<line x1="20" y1="20" x2="20" y2="36" stroke="currentColor" stroke-width="1.4" opacity=".18"/>` +
	`</svg>`

// baseCSP is the policy both viewers share.
//
// It governs two documents at once, which is what bounds how far it narrows:
// the viewer page, and the untrusted HTML and JSX assets it renders in blob:
// URL iframes, which inherit the creating document's policy. Each remaining
// permissive source is required by one of them:
//
//   - script-src 'unsafe-inline' — both documents carry inline script. The
//     page's theme, expiry and modal handlers are inline
//     (templates/public_viewer.html), and a stored HTML asset's own <script>
//     blocks are the artifact itself. A per-response nonce would cover the
//     page and blank every HTML asset, because the inherited policy would
//     then reject script the server never saw. What isolates an artifact is
//     the frame, not this directive: the renderers set
//     sandbox="allow-scripts" without allow-same-origin, so artifact script
//     runs in an opaque origin with no reach into the viewer's origin or
//     storage.
//   - script-src 'self' — the content-viewer bundle is loaded from
//     /portal/view/_assets/ as a module rather than inlined, and its chunks
//     are fetched by the same directive as it renders each family (#1355).
//     On an https deployment `https:` already covered this; 'self' is what
//     makes a plaintext deployment work too. It grants the server's own
//     origin, which is where the page itself came from, and resolves to
//     nothing inside an artifact frame, whose origin is opaque.
//   - script-src https: — assets legitimately load third-party script. The
//     JSX renderer resolves react, react-dom, recharts and lucide-react from
//     esm.sh through an import map, and stored HTML artifacts reference CDN
//     libraries directly. Plain http: is not permitted: an https page blocks
//     it as mixed content anyway, so allowing it only widened the policy for
//     plaintext deployments.
//   - script-src blob: — worker-src falls back through child-src to this
//     directive, so dropping blob: would refuse an artifact its own web
//     worker while buying nothing: a blob URL can only be minted by script
//     that is already running, which 'unsafe-inline' has already permitted.
//   - style-src 'unsafe-inline', img-src, font-src — the page and its assets
//     style themselves inline and reference images and webfonts from
//     arbitrary hosts. All three are passive.
//   - media-src and object-src 'self' — audio and video stream from the
//     same-origin raw content endpoint, and PDFs render through an <object>
//     pointed at it (ui/src/components/renderers/MediaRenderer.tsx).
//   - connect-src https: — no viewer path issues a request this directive
//     governs; the page hands content URLs to elements instead. It is here
//     for artifacts that call an API. 'self' rides along for the page's own
//     origin, and resolves to nothing inside an artifact frame, whose origin
//     is opaque.
//
// 'unsafe-eval' is deliberately absent: Sucrase transforms JSX in the parent
// page and the iframe runs the result as a module, so no viewer path
// evaluates source at runtime.
//
//nolint:lll // CSP directives are necessarily long
const baseCSP = "default-src 'none'; " +
	"script-src 'self' 'unsafe-inline' blob: https:; " +
	"style-src 'unsafe-inline' https:; " +
	"img-src * data: blob:; " +
	"media-src 'self' blob: data:; " +
	"object-src 'self'; " +
	"font-src * data:; " +
	"connect-src 'self' https:;"

// AssetCSP returns the Content-Security-Policy for the single-asset viewer.
func AssetCSP() string {
	return "frame-src blob: data: 'self'; " + baseCSP
}

// CollectionCSP returns the Content-Security-Policy for the collection viewer.
// It adds 'self' to frame-src so the per-item asset viewer iframe, served from
// the same origin, can load.
func CollectionCSP() string {
	return "frame-src 'self' blob: data:; " + baseCSP
}
