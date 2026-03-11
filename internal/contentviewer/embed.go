// Package contentviewer embeds the standalone content viewer JS/CSS bundle
// built from ui/src/content-viewer-entry.tsx. This bundle is used by the
// public viewer to render all content types client-side using the same
// React renderers as the authenticated portal.
package contentviewer

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// JS is the content viewer IIFE bundle that renders content client-side.
// Empty when the bundle has not been built (e.g., CI embed-clean).
// Built by: make frontend-build-content-viewer.
var JS string

// CSS is the Tailwind + theme stylesheet for the content viewer.
// Empty when the bundle has not been built.
var CSS string

func init() {
	JS, CSS = loadBundles(distFS)
}

// loadBundles reads the JS and CSS bundles from the given filesystem.
// Returns empty strings for any file that does not exist.
func loadBundles(fsys fs.FS) (js, css string) {
	if data, err := fs.ReadFile(fsys, "dist/content-viewer.js"); err == nil {
		js = string(data)
	}
	if data, err := fs.ReadFile(fsys, "dist/content-viewer.css"); err == nil {
		css = string(data)
	}
	return js, css
}
