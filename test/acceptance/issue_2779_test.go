//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2779: a compiled site with no _layouts serves neither
// _includes/ nor _data/, both still read through manage_asset; and a
// library stylesheet a component imports by URL in external mode becomes an
// @import of the entry's stylesheet, with no stylesheet left as a module
// import in the JavaScript.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; manage_asset
// takes action and asset_id as strings. A request to a site is an HTTP GET
// naming the hostname in Host. No tool parameter changed its schema.

// Criterion 1: _includes/README.md and _data/x.json are not served, and read
// back through manage_asset.
func TestIssue2779_LiquidFoldersAreNeverServed(t *testing.T) {
	c := connect(t)
	files := append(compiled2774(buildFile2774),
		map[string]any{"path": "_includes/README.md", "content": "notes for agents 2779"},
		map[string]any{"path": "_data/x.json", "content": `{"record":"2779"}`},
	)
	s := publish2774(t, c, files)
	s.served2774(t, "/app.js", "cell:")
	for _, path := range []string{"/_includes/README.md", "/_data/x.json"} {
		if r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, ""); r.status != http.StatusNotFound {
			t.Errorf("%s answered %d", path, r.status)
		}
	}
	for path, want := range map[string]string{"_includes/README.md": "notes for agents 2779", "_data/x.json": `"record"`} {
		got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref(path)})
		if s, _ := got["content"].(string); !strings.Contains(s, want) {
			t.Errorf("%s does not read back through manage_asset: %v", path, got["content"])
		}
	}
}

// Criterion 2: import "editor/dist/style.css" mapped to a URL becomes an
// @import at the top of app.css, and app.js imports no stylesheet.
func TestIssue2779_ALibraryStylesheetBecomesAnImportOfTheEntrysStylesheet(t *testing.T) {
	c := connect(t)
	build := `{"entries":["app.tsx"],"imports":{"editor/":"https://esm.sh/some-editor@1.2.3/"}}`
	s := publish2774(t, c, []map[string]any{
		{"path": "_build.json", "content": build},
		{"path": "app.tsx", "content": `import "editor/dist/style.css";` + "\n" + `document.title = "css-hoist-2779";`},
		{"path": "index.html", "content": `<!doctype html><link rel="stylesheet" href="/app.css"><script type="module" src="/app.js"></script>`},
	})
	js := s.served2774(t, "/app.js", "css-hoist-2779")
	if strings.Contains(js.body, ".css") {
		t.Errorf("app.js still imports a stylesheet:\n%s", js.body)
	}
	css := s.served2774(t, "/app.css", "@import")
	if !strings.HasPrefix(strings.TrimSpace(css.body), `@import "https://esm.sh/some-editor@1.2.3/dist/style.css"`) {
		t.Errorf("app.css does not start with the @import:\n%s", css.body)
	}
	if ct := css.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("app.css is served as %q", ct)
	}
}
