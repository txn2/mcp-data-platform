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
// The viewer renders every content family client-side. HTML and JSX assets run
// inside blob: URL iframes, which inherit this policy, so it has to permit the
// external resources those documents legitimately reference. media-src carries
// the audio and video families, which stream from the same-origin raw content
// endpoint rather than being embedded in the page.
//
//nolint:lll // CSP directives are necessarily long
const baseCSP = "default-src 'none'; " +
	"script-src 'unsafe-eval' 'unsafe-inline' blob: https: http:; " +
	"style-src 'unsafe-inline' https:; " +
	"img-src * data: blob:; " +
	"media-src 'self' blob: data:; " +
	"object-src 'self'; " +
	"font-src * data:; " +
	"connect-src 'self' https: http:;"

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
