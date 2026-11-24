//go:build integration

package acceptance

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Acceptance for #2778: a compiled entry is split at its dynamic imports.
// Each lazily loaded module is a chunk under /_chunks/ named by a hash of its
// content and loaded when first used; chunks are served immutable and entries
// are not; a page left on an earlier release still loads a chunk the current
// one no longer holds; splitting false compiles one file; and the tile
// renderer draws a split application.
//
// Wire forms: manage_site takes command, site and note as strings, base as a
// number, and files as a list of {path, content} objects whose content is
// utf-8 text; _build.json's splitting is a boolean. A request to a site is an
// HTTP GET naming the hostname in Host. No tool parameter changed its schema.

const lazyApp2778 = `const pages = {
  a: () => import("./pages/A.tsx"),
  b: () => import("./pages/B.tsx"),
};
export async function show(name: "a" | "b"): Promise<string> {
  return (await pages[name]()).default;
}
document.title = "split-app-2778";
`

func split2778(build, b string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": build},
		{"path": "app.tsx", "content": lazyApp2778},
		{"path": "pages/A.tsx", "content": `export default "page-a-body-2778";`},
		{"path": "pages/B.tsx", "content": `export default "` + b + `";`},
		{"path": "index.html", "content": `<!doctype html><script type="module" src="/app.js"></script>`},
	}
}

var chunkRef2778 = regexp.MustCompile(`_chunks/([A-Za-z]+)-[A-Za-z0-9]+\.js`)

// chunks2778 names each chunk app.js loads, by the module it was made from.
func chunks2778(js string) map[string]string {
	out := map[string]string{}
	for _, m := range chunkRef2778.FindAllStringSubmatch(js, -1) {
		out[m[1]] = "/" + m[0]
	}
	return out
}

// Criteria 1, 2, 4 and 5: app.js holds neither page and names one chunk for
// each; a chunk is immutable and the entry is not; changing only B leaves A's
// chunk byte for byte and gives B's chunk a new name, app.js differing only by
// that name; and release 1's B chunk still answers after release 2.
func TestIssue2778_LazyPagesAreChunksLoadedOnDemand(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, split2778(`{"entries":["app.tsx"]}`, "page-b-body-2778"))
	js := s.served2774(t, "/app.js", "split-app-2778")
	if strings.Contains(js.body, "page-a-body-2778") || strings.Contains(js.body, "page-b-body-2778") {
		t.Fatalf("app.js holds a lazy page:\n%s", js.body)
	}
	if cc := js.header.Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("the entry is cached for good (%s); it is how a release picks up new chunk names", cc)
	}
	first := chunks2778(js.body)
	if first["A"] == "" || first["B"] == "" {
		t.Fatalf("app.js does not name a chunk for each page: %v\n%s", first, js.body)
	}
	a := onHost(t, s.preview("stage"), http.MethodGet, first["A"], nil, "")
	if a.status != http.StatusOK || !strings.Contains(a.body, "page-a-body-2778") || strings.Contains(a.body, "page-b-body-2778") {
		t.Fatalf("A's chunk answered %d: %s", a.status, a.body)
	}
	if cc := a.header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("a chunk is served with %q", cc)
	}

	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "change B", "base": 1,
		"files": []map[string]any{{"path": "pages/B.tsx", "content": `export default "page-b-changed-2778";`}}})
	s.promote2592(t)
	js2 := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.preview("stage"), http.MethodGet, "/app.js", nil, "")
		return r, r.status == http.StatusOK && !strings.Contains(r.body, first["B"])
	})
	second := chunks2778(js2.body)
	if second["A"] != first["A"] {
		t.Errorf("A's chunk was renamed though A did not change: %s then %s", first["A"], second["A"])
	}
	if second["B"] == first["B"] {
		t.Errorf("B's chunk kept its name after B changed: %s", second["B"])
	}
	if strings.ReplaceAll(js2.body, second["B"], first["B"]) != js.body {
		t.Errorf("app.js changed by more than B's chunk name")
	}
	a2 := onHost(t, s.preview("stage"), http.MethodGet, first["A"], nil, "")
	if a2.body != a.body {
		t.Errorf("A's chunk changed")
	}
	old := onHost(t, s.preview("stage"), http.MethodGet, first["B"], nil, "")
	if old.status != http.StatusOK || !strings.Contains(old.body, "page-b-body-2778") {
		t.Errorf("a page on release 1 cannot load its B chunk after release 2: %d %s", old.status, old.body)
	}
}

// Criteria 3 and 6: a site with no dynamic imports and one with splitting
// false each serve one file and no chunk.
func TestIssue2778_NoDynamicImportsOrSplittingOffIsOneFile(t *testing.T) {
	c := connect(t)
	off := publish2774(t, c, split2778(`{"entries":["app.tsx"],"splitting":false}`, "page-b-body-2778"))
	js := off.served2774(t, "/app.js", "split-app-2778")
	if !strings.Contains(js.body, "page-a-body-2778") || !strings.Contains(js.body, "page-b-body-2778") || len(chunks2778(js.body)) != 0 {
		t.Errorf("splitting false did not compile one file:\n%s", js.body)
	}
	plain := publish2774(t, c, compiled2774(buildFile2774))
	if js := plain.served2774(t, "/app.js", "cell:"); strings.Contains(js.body, "_chunks/") {
		t.Errorf("a site with no dynamic import names a chunk:\n%s", js.body)
	}
}

// paint2778 paints the page green from a lazily loaded component.
const paint2778 = `import { createRoot } from "react-dom/client";
import { lazy, Suspense } from "react";

const Swatch = lazy(() => import("./Swatch.tsx"));
createRoot(document.getElementById("root")!).render(
  <Suspense fallback={<div style={{ position: "fixed", inset: 0, background: "rgb(220,0,0)" }} />}>
    <Swatch />
  </Suspense>,
);
`

// Criterion 7: the tile renderer draws a split application: the lazily
// loaded component paints the tile green.
func TestIssue2778_TheTileDrawsASplitApplication(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2778-tile-")
	bundled := strings.Replace(buildFile2774, `"entries"`, `"remote": "bundle", "entries"`, 1)
	connectAs(t, keyAdmin2604).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "tile",
		"files": []map[string]any{
			{"path": "_build.json", "content": bundled},
			{"path": "app.tsx", "content": paint2778},
			{"path": "Swatch.tsx", "content": `export default function Swatch(): JSX.Element { return <div style={{ position: "fixed", inset: 0, background: "rgb(0,170,0)" }}>lazy</div>; }`},
			{"path": "index.html", "content": `<!doctype html><html><body style="margin:0;background:#fff"><div id="root"></div><script type="module" src="/app.js"></script></body></html>`},
		},
	})
	a := &app2747{site: s}
	shot := a.drawnRelease(t, 1)
	if r, g, b := centre2747(t, shot); g < 140 || r > 60 || b > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); the lazy component paints it green", r, g, b)
	}
}
