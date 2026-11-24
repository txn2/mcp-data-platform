//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2774: a site written in TypeScript, TSX or JSX keeps its
// source in the site. A _build.json at the release root compiles each entry it
// names, with the local modules it imports, into one ES module beside it at
// publish; bare imports resolve only through its imports map to https URLs at
// an exact version; the source is read and patched through manage_asset and
// never served; a compile error refuses the release naming the file, line and
// column; remote "external" reads nothing from the network and remote
// "bundle" loads nothing from a CDN; a release without _build.json is
// published as it always was; and the tile renderer draws a compiled site.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; manage_asset
// takes action and asset_id as strings, dry_run as a boolean and edits as a
// list of {find, replace} objects; a request to a site is an HTTP GET naming
// the hostname in Host; the tile route takes the site and environment in its
// path. No tool parameter changed its schema.

// sourceOnly2774 is in the source of Table.tsx and must reach no served byte.
const sourceOnly2774 = "source-only-2774"

const buildFile2774 = `{
  "entries": ["app.tsx"],
  "imports": {
    "react": "https://esm.sh/react@18.3.1",
    "react-dom/client": "https://esm.sh/react-dom@18.3.1/client"
  }
}`

const app2774 = `import { createRoot } from "react-dom/client";
import { Table } from "./Table.tsx";

const rows: string[] = ["first-row", "second-row"];
createRoot(document.getElementById("root")!).render(<Table rows={rows} />);
`

const table2774 = `// ` + sourceOnly2774 + `: a comment that stays in the source.
export function Table({ rows }: { rows: string[] }): JSX.Element {
  return <table>{rows.map((r) => <tr key={r}><td>{"cell:" + r}</td></tr>)}</table>;
}
`

const index2774 = `<!doctype html><html><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>`

func compiled2774(build string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": build},
		{"path": "app.tsx", "content": app2774},
		{"path": "Table.tsx", "content": table2774},
		{"path": "index.html", "content": index2774},
	}
}

// publish2774 publishes files to a new site's dev and promotes it to stage,
// whose domain answers anyone.
func publish2774(t *testing.T, c *client, files []map[string]any) *siteClient {
	t.Helper()
	s := newSite2518(t, c)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "compiled", "files": files})
	s.promote2592(t)
	return s
}

// served2774 is a path of stage, waiting for the promote to reach it.
func (s *siteClient) served2774(t *testing.T, path, want string) pageResponse {
	t.Helper()
	return pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, want)
	})
}

// devRelease is the release dev serves.
func devRelease2774(t *testing.T, c *client, s *siteClient) int {
	t.Helper()
	got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("Table.tsx")})
	return number2588(got["version"])
}

// Criterion 1: the site publishes, /app.js is served as JavaScript holding the
// compiled Table with its React imports left as the pinned URLs, and neither
// TSX file nor _build.json is served. The comment written only in the source
// reaches nothing served.
func TestIssue2774_ACompiledSiteServesItsOutputAndNoSource(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, compiled2774(buildFile2774))
	js := s.served2774(t, "/app.js", "cell:")
	if ct := js.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("/app.js is served as %q", ct)
	}
	for _, want := range []string{`from "https://esm.sh/react-dom@18.3.1/client"`, `from "https://esm.sh/react@18.3.1/jsx-runtime"`, "first-row"} {
		if !strings.Contains(js.body, want) {
			t.Errorf("/app.js lacks %s:\n%s", want, js.body)
		}
	}
	if strings.Contains(js.body, "string[]") {
		t.Errorf("/app.js kept a TypeScript type:\n%s", js.body)
	}
	for _, path := range []string{"/app.tsx", "/Table.tsx", "/_build.json"} {
		if r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, ""); r.status != http.StatusNotFound {
			t.Errorf("%s answered %d; source is never served", path, r.status)
		}
	}
	for _, path := range []string{"/", "/app.js", "/app.tsx", "/Table.tsx", "/Table", "/_build.json"} {
		if r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, ""); strings.Contains(r.body, sourceOnly2774) {
			t.Errorf("%s served text written only in the source", path)
		}
	}
	got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("Table.tsx")})
	if got["content"] != table2774 {
		t.Errorf("Table.tsx does not read back as its source: %v", got["content"])
	}
}

// Criterion 2: a patch of Table.tsx makes a new release whose app.js is
// compiled again, and its dry run names the one output it changes.
func TestIssue2774_APatchRecompilesAndADryRunNamesTheOutput(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, compiled2774(buildFile2774))
	dry := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("Table.tsx"), "dry_run": true, "edits": editOf(`"cell:"`, `"patched:"`)})
	if dry["dry_run"] != true || number2588(dry["outputs_changed"]) != 1 {
		t.Fatalf("the dry run: %v", dry)
	}
	if v := devRelease2774(t, c, s); v != 1 {
		t.Fatalf("the dry run moved dev to release %d", v)
	}
	patched := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("Table.tsx"), "edits": editOf(`"cell:"`, `"patched:"`)})
	if number2588(patched["version"]) != 2 {
		t.Fatalf("the patch: %v", patched)
	}
	s.promote2592(t)
	if js := s.served2774(t, "/app.js", "patched:").body; strings.Contains(js, "cell:") {
		t.Errorf("app.js was not compiled again:\n%s", js)
	}
}

// Criterion 3: a syntax error in Table.tsx refuses the release, naming the
// file, line and column, and dev does not move.
func TestIssue2774_ASyntaxErrorIsRefusedNamingTheFileLineAndColumn(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, compiled2774(buildFile2774))
	msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("Table.tsx"), "edits": editOf("return <table>", "return <table>>{")})
	if !strings.Contains(msg, "Table.tsx:3:") {
		t.Errorf("the refusal does not name Table.tsx, line 3 and a column: %s", msg)
	}
	if v := devRelease2774(t, c, s); v != 1 {
		t.Errorf("a refused compile moved dev to release %d", v)
	}
}

// Criterion 4: an import of a name _build.json does not map refuses the
// publish, naming it; so does an import that climbs out of the site.
func TestIssue2774_AnUnmappedOrEscapingImportIsRefusedNamingIt(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	for spec, want := range map[string]string{"lodash": `"lodash"`, "../../../etc/passwd": "outside the site"} {
		files := compiled2774(buildFile2774)
		files[1] = map[string]any{"path": "app.tsx", "content": `import x from "` + spec + `"; console.log(x);`}
		res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "bad import", "files": files})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(text, want) || !strings.Contains(text, "app.tsx") {
			t.Errorf("importing %s: error=%v %s; want refused naming %s in app.tsx", spec, res.IsError, text, want)
		}
		if strings.Contains(text, "root:") {
			t.Errorf("the refusal holds a file of the platform's disk: %s", text)
		}
	}
	if rels := releases2588(t, c, s); len(rels) != 0 {
		t.Errorf("a refused publish made a release: %v", rels)
	}
}

// Criterion 5: remote "external" compiles with nothing fetched (its library
// is on a host that does not exist) and leaves the URL for the browser;
// remote "bundle" writes an app.js that imports nothing from a CDN.
func TestIssue2774_ExternalFetchesNothingAndBundleLoadsNothingFromACDN(t *testing.T) {
	c := connect(t)
	external := `{"entries":["app.tsx"],"remote":"external","imports":{"react":"https://cdn.invalid/react@18.3.1","react-dom/client":"https://cdn.invalid/react-dom@18.3.1/client"}}`
	s := publish2774(t, c, compiled2774(external))
	if js := s.served2774(t, "/app.js", "cell:").body; !strings.Contains(js, `from "https://cdn.invalid/react-dom@18.3.1/client"`) {
		t.Errorf("external did not leave the URL import:\n%s", js)
	}

	bundled := strings.Replace(buildFile2774, `"entries"`, `"remote": "bundle", "entries"`, 1)
	b := publish2774(t, c, compiled2774(bundled))
	js := b.served2774(t, "/app.js", "cell:").body
	for _, cdn := range []string{`from "https:`, `import("https:`, `from"https:`, `import "https:`} {
		if strings.Contains(js, cdn) {
			t.Errorf("a bundled app.js still imports from a CDN (%s)", cdn)
		}
	}
	if !strings.Contains(js, "createRoot") || len(js) < 50_000 {
		t.Errorf("app.js does not hold React (%d bytes)", len(js))
	}
}

// Criterion 6: a release without _build.json is served as published: its TSX
// is a file like any other, and nothing is compiled.
func TestIssue2774_AReleaseWithoutABuildFileIsServedAsPublished(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, compiled2774(buildFile2774)[1:])
	got := s.served2774(t, "/Table.tsx", sourceOnly2774)
	if got.body != table2774 {
		t.Errorf("Table.tsx is not byte for byte what was published:\n%s", got.body)
	}
	if r := onHost(t, s.preview("stage"), http.MethodGet, "/index.html", nil, ""); r.body != index2774 {
		t.Errorf("index.html changed: %s", r.body)
	}
	if r := onHost(t, s.preview("stage"), http.MethodGet, "/app.js", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("/app.js answered %d with no build file", r.status)
	}
}

// paint2774 paints the page green from an effect, which runs only when React's
// renderer and the component's hooks share one React: the tile shows that the
// bundled app.js ran.
const paint2774 = `import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { Swatch } from "./Swatch.tsx";

function App(): JSX.Element {
  const [colour, setColour] = useState("rgb(220,0,0)");
  useEffect(() => setColour("rgb(0,170,0)"), []);
  return <Swatch colour={colour} />;
}
createRoot(document.getElementById("root")!).render(<App />);
`

const swatch2774 = `export function Swatch({ colour }: { colour: string }): JSX.Element {
  return <div style={{ position: "fixed", inset: 0, background: colour }}>compiled</div>;
}
`

// Criterion 7: the tile renderer draws a compiled site like any other: a
// bundled React app paints the tile green.
func TestIssue2774_TheTileDrawsACompiledSite(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2774-tile-")
	bundled := strings.Replace(buildFile2774, `"entries"`, `"remote": "bundle", "entries"`, 1)
	connectAs(t, keyAdmin2604).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "tile",
		"files": []map[string]any{
			{"path": "_build.json", "content": bundled},
			{"path": "app.tsx", "content": paint2774},
			{"path": "Swatch.tsx", "content": swatch2774},
			{"path": "index.html", "content": `<!doctype html><html><body style="margin:0;background:#fff"><div id="root"></div><script type="module" src="/app.js"></script></body></html>`},
		},
	})
	a := &app2747{site: s}
	shot := a.drawnRelease(t, 1)
	if r, g, b := centre2747(t, shot); g < 140 || r > 60 || b > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); the compiled app paints it green", r, g, b)
	}
}
