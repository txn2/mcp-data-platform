// Package contentviewer embeds the public share viewer's frontend build:
// the code-split JavaScript chunks produced from ui/src/content-viewer-entry.tsx
// and the stylesheet compiled against them. The viewer renders every content
// type with the same React renderers as the authenticated portal.
//
// The JavaScript is a chunk graph rather than a single bundle, and it is
// served as files instead of being inlined into the share page. A share of an
// 850-byte markdown document used to carry the whole renderer — the JSX
// transformer, CodeMirror, the diagram engine — because the build format
// collapsed every lazy() boundary back into one file (#1355). Now the page
// references the entry chunk and the browser fetches only what the asset's
// family needs, and caches it for the next share it opens.
//
// The stylesheet stays inline: it is small once compiled against the viewer's
// own bundle, and inlining it keeps the page from blocking first paint on a
// second round trip.
package contentviewer

import (
	"embed"
	"encoding/json"
	"io/fs"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// AssetPathPrefix is the URL prefix the viewer's chunks are served under. It
// matches the `base` in ui/vite.content-viewer.config.ts: vite bakes that
// value into the chunk-loading helper, so the two cannot drift without the
// dynamic imports 404ing.
const AssetPathPrefix = "/portal/view/_assets/"

// CSS is the viewer's stylesheet, inlined into the share page.
// Empty when the frontend has not been built (e.g. CI's embed-clean).
var CSS string

// entryFile is the hashed filename of the entry chunk, e.g.
// "content-viewer-entry-C8tFPYTK.js". Empty when the bundle is not built.
var entryFile string

func init() {
	CSS, entryFile = loadBundle(distFS)
}

// EntryURL returns the absolute path the share page should load the viewer
// from, or "" when no bundle is embedded. A page rendered without a bundle
// still serves its metadata and its download link; only the rendered preview
// is missing, which is the same degradation the un-built bundle produced
// before.
func EntryURL() string {
	return viewerURLFor(entryFile)
}

// viewerURLFor is EntryURL over an explicit filename, so both answers are
// reachable from a test whatever the checkout has built.
func viewerURLFor(entry string) string {
	if entry == "" {
		return ""
	}
	return AssetPathPrefix + entry
}

// viteManifest is the subset of vite's manifest.json this package reads: the
// entry chunk's emitted filename. Chunk-to-chunk edges are resolved by the
// browser from the imports rollup writes into the chunks themselves, so they
// are not needed here.
type viteManifest map[string]struct {
	File    string `json:"file"`
	IsEntry bool   `json:"isEntry"`
}

// loadBundle reads the stylesheet and resolves the entry chunk's hashed name
// from vite's manifest. Anything missing yields empty strings rather than an
// error: a clean checkout has no bundle, and the server has to start anyway.
func loadBundle(fsys fs.FS) (css, entry string) {
	if data, err := fs.ReadFile(fsys, "dist/content-viewer.css"); err == nil {
		css = string(data)
	}
	data, err := fs.ReadFile(fsys, "dist/.vite/manifest.json")
	if err != nil {
		return css, ""
	}
	var m viteManifest
	if json.Unmarshal(data, &m) != nil {
		return css, ""
	}
	for _, e := range m {
		if e.IsEntry {
			entry = e.File
			break
		}
	}
	// The chunks are served from one flat directory, so an entry naming a
	// subpath is a build that no longer matches this loader. And a manifest
	// naming a file the build did not emit would serve a 404 to every viewer,
	// so the name is only accepted once the file is confirmed.
	if entry == "" || strings.ContainsAny(entry, `/\`) {
		return css, ""
	}
	if _, err := fs.Stat(fsys, "dist/"+entry); err != nil {
		return css, ""
	}
	return css, entry
}
