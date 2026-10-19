//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2200: on a fresh install the built-in knowledge pages formed
// no graph. Their citations of one another sat in code spans, which the
// reference scan skips, and a citation by slug named no row id, so it was
// dropped. After the platform starts, each built-in page has references to
// the pages it cites, the portal shows them in both directions, a citation by
// slug renders as a live link, a page lists every built-in page, and a
// deployment's own page citing a built-in one by slug keeps that reference.
//
// Wire forms: fetch takes `reference` as a string; refs/resolve takes `urns`
// as a list of strings; the page create takes a JSON object of strings. Each
// is sent in the one form its schema admits.

// issue2200Builtins is every live built-in page on the dev stack, slug to id.
func issue2200Builtins(t *testing.T) map[string]string {
	t.Helper()
	rows, err := devDB(t).Query(`SELECT slug, id FROM portal_knowledge_pages WHERE builtin AND deleted_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck // read below
	out := map[string]string{}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			t.Fatal(err)
		}
		out[slug] = id
	}
	if len(out) < 12 {
		t.Fatalf("the dev stack holds %d built-in pages; the release ships 12", len(out))
	}
	return out
}

// issue2200Backlinks is the slugs of the pages the portal lists as citing id.
func issue2200Backlinks(t *testing.T, c *client, id string) []string {
	t.Helper()
	status, body := c.rest(http.MethodGet,
		"/api/v1/portal/knowledge-pages/backlinks?urn="+url.QueryEscape("mcp:knowledge_page:"+id), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("backlinks of %s: %d %v", id, status, body)
	}
	var slugs []string
	pages, _ := body["pages"].([]any)
	for _, p := range pages {
		m, _ := p.(map[string]any)
		s, _ := m["slug"].(string)
		slugs = append(slugs, s)
	}
	return slugs
}

// issue2200RefURNs is the references the portal lists for a page.
func issue2200RefURNs(t *testing.T, c *client, id string) []string {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/knowledge-pages/"+id+"/refs", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("refs of %s: %d %v", id, status, body)
	}
	var urns []string
	refs, _ := body["refs"].([]any)
	for _, r := range refs {
		m, _ := r.(map[string]any)
		u, _ := m["urn"].(string)
		urns = append(urns, u)
	}
	return urns
}

// TestIssue2200_EveryBuiltinPageIsCitedAndCites: each built-in page other
// than the index is cited by another built-in page and cites one, as the
// portal's backlinks and references report.
func TestIssue2200_EveryBuiltinPageIsCitedAndCites(t *testing.T) {
	c := connect(t)
	builtins := issue2200Builtins(t)
	isBuiltin := map[string]bool{}
	for slug := range builtins {
		isBuiltin[slug] = true
	}
	byID := map[string]string{}
	for slug, id := range builtins {
		byID[id] = slug
	}
	for slug, id := range builtins {
		if slug == "platform-index" {
			continue
		}
		cited := 0
		for _, from := range issue2200Backlinks(t, c, id) {
			if isBuiltin[from] && from != "platform-index" {
				cited++
			}
		}
		if cited == 0 {
			t.Errorf("no built-in page other than the index cites %s", slug)
		}
		cites := 0
		for _, urn := range issue2200RefURNs(t, c, id) {
			if target := byID[strings.TrimPrefix(urn, "mcp:knowledge_page:")]; target != "" && target != "platform-index" {
				cites++
			}
		}
		if cites == 0 {
			t.Errorf("%s cites no other built-in page", slug)
		}
	}
}

// TestIssue2200_TheIndexListsEveryBuiltinPage: fetch on the index page by its
// slug returns a page citing every other built-in page.
func TestIssue2200_TheIndexListsEveryBuiltinPage(t *testing.T) {
	c := connect(t)
	builtins := issue2200Builtins(t)
	out := c.call("fetch", map[string]any{
		"reference": "mcp:knowledge_page:platform-index",
		"purpose":   "Acceptance #2200: the index of the platform's own pages.",
	})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for slug := range builtins {
		if slug != "platform-index" && !strings.Contains(string(raw), "mcp:knowledge_page:"+slug) {
			t.Errorf("the index does not cite %s", slug)
		}
	}
	refs := issue2200RefURNs(t, c, builtins["platform-index"])
	if len(refs) < len(builtins)-1 {
		t.Errorf("the index carries %d references; it cites %d pages", len(refs), len(builtins)-1)
	}
}

// TestIssue2200_ACitationBySlugIsALiveLink: the portal resolves a built-in
// page cited by slug as a live, named reference rather than a broken one.
func TestIssue2200_ACitationBySlugIsALiveLink(t *testing.T) {
	c := connect(t)
	status, body := c.rest(http.MethodPost, "/api/v1/portal/knowledge-pages/refs/resolve",
		strings.NewReader(`{"urns":["mcp:knowledge_page:platform-maps"]}`))
	if status != http.StatusOK {
		t.Fatalf("refs/resolve: %d %v", status, body)
	}
	refs, _ := body["refs"].([]any)
	if len(refs) != 1 {
		t.Fatalf("refs/resolve answered %v", body)
	}
	ref, _ := refs[0].(map[string]any)
	if ref["exists"] != true || ref["label"] == "platform-maps" {
		t.Fatalf("a citation of platform-maps by slug resolved as %v; want a live reference named by the page's title", ref)
	}
}

// TestIssue2200_AnOwnPageCitingABuiltinBySlugKeepsTheReference: a page a
// deployment writes that cites a built-in page by slug stores the reference
// to that page, where its write used to fail on the foreign key.
func TestIssue2200_AnOwnPageCitingABuiltinBySlugKeepsTheReference(t *testing.T) {
	c := connect(t)
	builtins := issue2200Builtins(t)
	slug := "acceptance-2200-" + time.Now().UTC().Format("150405.000000")
	slug = strings.ReplaceAll(slug, ".", "-")
	status, page := c.rest(http.MethodPost, "/api/v1/portal/knowledge-pages", strings.NewReader(`{
		"slug": "`+slug+`",
		"title": "Acceptance 2200 regions",
		"body": "Our regions follow mcp:knowledge_page:platform-maps for drawing.",
		"force_new": true
	}`))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create page: %d %v", status, page)
	}
	id, _ := page["id"].(string)
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/knowledge-pages/"+id, http.NoBody) })

	want := "mcp:knowledge_page:" + builtins["platform-maps"]
	var urns []string
	waitFor(t, "the page's reference to platform-maps", func() (bool, string) {
		urns = issue2200RefURNs(t, c, id)
		for _, u := range urns {
			if u == want {
				return true, ""
			}
		}
		return false, strings.Join(urns, ", ")
	})
	cited := issue2200Backlinks(t, c, builtins["platform-maps"])
	found := false
	for _, s := range cited {
		found = found || s == slug
	}
	if !found {
		t.Errorf("platform-maps does not list %s among the pages citing it: %v", slug, cited)
	}
}
