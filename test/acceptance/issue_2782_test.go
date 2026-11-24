//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2782: AGENTS.md at a site's root is never served, with or
// without _build.json or _layouts; manage_site get returns it; the first
// read, patch or publish on a site in a session carries it, and later calls
// in that session do not; and a site without one behaves as before.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; manage_asset
// takes action and asset_id as strings. A request to a site is an HTTP GET
// naming the hostname in Host. No tool parameter changed its schema.

const guide2782 = "# How this site is built\nguide-body-2782: entries are app.tsx; settings live in src/settings.ts.\n"

func guideOf2782(got map[string]any) string {
	g, _ := got["site_guide"].(map[string]any)
	s, _ := g["content"].(string)
	return s
}

// Criterion 1: AGENTS.md answers 404 on a compiled site, a site with layouts
// and a plain site.
func TestIssue2782_TheGuideIsNeverServed(t *testing.T) {
	c := connect(t)
	sites := map[string][]map[string]any{
		"compiled": append(compiled2774(buildFile2774), map[string]any{"path": "AGENTS.md", "content": guide2782}),
		"layouts": {
			{"path": "_layouts/base.html", "content": "<main>{{ content }}</main>"},
			{"path": "index.html", "content": "---\nlayout: base\n---\nlayout-page-2782"},
			{"path": "AGENTS.md", "content": guide2782},
		},
		"plain": {
			{"path": "index.html", "content": "<p>plain-page-2782</p>"},
			{"path": "AGENTS.md", "content": guide2782},
		},
	}
	for kind, files := range sites {
		s := publish2774(t, c, files)
		pollUntil2518(t, func() (pageResponse, bool) {
			r := onHost(t, s.preview("stage"), http.MethodGet, "/", nil, "")
			return r, r.status == http.StatusOK
		})
		if r := onHost(t, s.preview("stage"), http.MethodGet, "/AGENTS.md", nil, ""); r.status != http.StatusNotFound || strings.Contains(r.body, "guide-body-2782") {
			t.Errorf("%s: /AGENTS.md answered %d", kind, r.status)
		}
	}
}

// Criteria 2, 3 and 4: get returns the guide; the first read in a session
// carries it and the second does not; a site without a guide carries none.
func TestIssue2782_TheGuideIsHandedToTheAgent(t *testing.T) {
	owner := connect(t)
	s := publish2774(t, owner, append(compiled2774(buildFile2774), map[string]any{"path": "AGENTS.md", "content": guide2782}))
	if g := guideOf2782(owner.call("manage_site", map[string]any{"command": "get", "site": s.name})); g != guide2782 {
		t.Errorf("get does not return the guide: %q", g)
	}

	fresh := connect(t)
	first := fresh.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("app.tsx")})
	if g := guideOf2782(first); g != guide2782 {
		t.Errorf("the first read in a session does not carry the guide: %v", first["site_guide"])
	}
	second := fresh.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("Table.tsx")})
	if _, again := second["site_guide"]; again {
		t.Errorf("a later read in the same session repeats the guide")
	}

	bare := publish2774(t, owner, compiled2774(buildFile2774))
	other := connect(t)
	if got := other.call("manage_asset", map[string]any{"action": "get_content", "asset_id": bare.ref("app.tsx")}); got["site_guide"] != nil {
		t.Errorf("a site without a guide carries one: %v", got["site_guide"])
	}
	if got := owner.call("manage_site", map[string]any{"command": "get", "site": bare.name}); got["site_guide"] != nil {
		t.Errorf("get returns a guide for a site without one")
	}
}
