//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2592: a site with a _layouts folder is rendered when it is
// published, and manage_asset edits its source.
//
// Wire forms: manage_site takes command, site, note, from and to as strings,
// base as an integer and files as a list of objects whose content is utf-8 text
// and whose delete is a boolean. manage_asset takes action and asset_id as
// strings, base_version as an integer, dry_run as a boolean and edits as a list
// of objects whose find and replace are strings. A request to a site is an HTTP
// GET naming the hostname in Host. Front matter is YAML; records are JSON, YAML
// and Markdown files.

const layout2592 = "<html><body><nav>{% include \"nav.html\" %}</nav>{{ content }}</body></html>\n"

func source2592() []map[string]any {
	return []map[string]any{
		{"path": "_layouts/base.html", "content": layout2592},
		{"path": "_includes/nav.html", "content": "<a href=\"/\">home</a>"},
		{"path": "_data/products/anvil.json", "content": `{"name":"Anvil","price":10}`},
		{"path": "_data/products/bolt.yaml", "content": "name: Bolt\nprice: 2\n"},
		{"path": "_data/products/crate.md", "content": "---\nname: Crate\nprice: 7\n---\n**wooden**\n"},
		{"path": "index.html", "content": "---\nlayout: base\ntitle: Home\n---\n<h1>{{ page.title }}</h1>{% for p in site.data.products %}<li>{{ p.name }}</li>{% endfor %}"},
		{"path": "about.md", "content": "---\nlayout: base\ntitle: About\ntags: [news]\n---\n# About us\n"},
		{"path": "product.html", "content": "---\nlayout: base\nfor_each: products\npermalink: /products/{{ item.slug }}/\n---\n<h2>{{ item.name }}</h2>{{ item.content }}"},
		{"path": "tag.html", "content": "---\nfor_each: tags\npermalink: /tags/{{ item.slug }}/\n---\n{% for p in item.pages %}{{ p.title }}{% endfor %}"},
		{"path": "sitemap.xml", "content": "---\n---\n<urlset>{% for p in site.data.products %}<url>{{ p.slug }}</url>{% endfor %}</urlset>"},
		{"path": "static.txt", "content": "no front matter {{ left alone }}"},
	}
}

// rendered2592 is a site whose default environment serves a rendered release,
// promoted to a public hostname so requests need no sign-in.
func rendered2592(t *testing.T, c *client) *siteClient {
	t.Helper()
	s := newSite2518(t, c)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "first", "files": source2592()})
	s.promote2592(t)
	return s
}

func (s *siteClient) promote2592(t *testing.T) {
	t.Helper()
	s.c.call("manage_site", map[string]any{"command": "promote", "site": s.name, "from": "dev", "to": "stage"})
}

func (s *siteClient) serves2592(t *testing.T, path, want string) pageResponse {
	t.Helper()
	return pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, "")
		return r, r.status == http.StatusOK && strings.Contains(r.body, want)
	})
}

func TestIssue2592_LayoutsRecordsAndTaxonomiesRenderAtPublish(t *testing.T) {
	c := connect(t)
	s := rendered2592(t, c)
	for path, want := range map[string]string{
		"/":                "<li>Anvil</li>",
		"/about/":          "<h1>About us</h1>",
		"/products/anvil/": "<h2>Anvil</h2>",
		"/products/crate/": "<strong>wooden</strong>",
		"/tags/news/":      "About",
		"/sitemap.xml":     "<url>bolt</url>",
		"/static.txt":      "{{ left alone }}",
	} {
		s.serves2592(t, path, want)
	}
	if body := s.serves2592(t, "/", "<nav>").body; !strings.Contains(body, `<a href="/">home</a>`) {
		t.Errorf("the include did not reach the page: %s", body)
	}
}

func TestIssue2592_SourceIsNeverServed(t *testing.T) {
	c := connect(t)
	s := rendered2592(t, c)
	s.serves2592(t, "/", "Anvil")
	for _, path := range []string{"/_layouts/base.html", "/_includes/nav.html", "/_data/products/anvil.json", "/product.html", "/tag.html"} {
		if r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, ""); r.status == http.StatusOK {
			t.Errorf("%s was served: %s", path, r.body)
		}
	}
}

func TestIssue2592_ManagersReadSourceAndViewersReadWhatIsServed(t *testing.T) {
	owner := connectAs(t, keyEngineer2559)
	s := rendered2592(t, owner)
	peer := connectAs(t, keyDirector2559)
	owner.call("manage_site", map[string]any{"command": "grant", "site": s.name, "email": emailDirector2559, "level": "view"})

	source := owner.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("_layouts/base.html")})
	if source["content"] != layout2592 {
		t.Fatalf("someone who manages reads source: %v", source)
	}
	refused2588(t, peer, map[string]any{"action": "get_content", "asset_id": s.ref("_layouts/base.html")})
	refused2588(t, peer, map[string]any{"action": "get_content", "asset_id": s.ref("_data/products/anvil.json")})
	served := peer.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("products/anvil/index.html")})
	if !strings.Contains(fmt.Sprint(served["content"]), "<h2>Anvil</h2>") {
		t.Errorf("view reads what is served: %v", served)
	}
}

func TestIssue2592_ChangingALayoutUpdatesEveryPageInOneRelease(t *testing.T) {
	c := connect(t)
	s := rendered2592(t, c)
	before := len(releases2588(t, c, s))
	c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("_layouts/base.html"), "edits": editOf("<nav>", "<nav class=\"new\">")})
	if got := len(releases2588(t, c, s)); got != before+1 {
		t.Fatalf("one edit, one release: %d then %d", before, got)
	}
	s.promote2592(t)
	for _, path := range []string{"/", "/about/", "/products/anvil/", "/products/bolt/"} {
		s.serves2592(t, path, `<nav class="new">`)
	}
}

func TestIssue2592_ChangingOneRecordChangesOnlyWhatReadsIt(t *testing.T) {
	c := connect(t)
	s := rendered2592(t, c)
	c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("_data/products/anvil.json"), "edits": editOf("Anvil", "Hammer")})
	s.promote2592(t)
	s.serves2592(t, "/products/anvil/", "<h2>Hammer</h2>")
	s.serves2592(t, "/", "<li>Hammer</li>")
	s.serves2592(t, "/products/bolt/", "<h2>Bolt</h2>")
}

func TestIssue2592_ATemplateErrorRefusesTheReleaseAndNamesTheFile(t *testing.T) {
	c := connect(t)
	s := rendered2592(t, c)
	before := len(releases2588(t, c, s))
	msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("index.html"), "edits": editOf("{{ page.title }}", "{% if %}")})
	if !strings.Contains(msg, "index.html") {
		t.Errorf("the refusal must name the file: %s", msg)
	}
	if got := len(releases2588(t, c, s)); got != before {
		t.Errorf("a failed render wrote a release: %d then %d", before, got)
	}
	if body := s.serves2592(t, "/", "Home").body; strings.Contains(body, "{% if") {
		t.Errorf("the environment moved: %s", body)
	}
}

func TestIssue2592_ADryRunRendersAndWritesNothing(t *testing.T) {
	c := connect(t)
	s := rendered2592(t, c)
	before := len(releases2588(t, c, s))
	dry := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("_layouts/base.html"), "dry_run": true, "edits": editOf("<nav>", "<nav id=\"x\">")})
	if dry["dry_run"] != true || number2588(dry["outputs_changed"]) == 0 {
		t.Errorf("a dry run reports what would change: %v", dry)
	}
	if msg := refused2588(t, c, map[string]any{"action": "patch", "asset_id": s.ref("_layouts/base.html"), "dry_run": true, "edits": editOf("{{ content }}", "{% for %}")}); !strings.Contains(msg, "_layouts/base.html") {
		t.Errorf("a dry run reports a template error: %s", msg)
	}
	if got := len(releases2588(t, c, s)); got != before {
		t.Errorf("a dry run wrote a release: %d then %d", before, got)
	}
}

func TestIssue2592_WhatATemplateCanSpendIsBounded(t *testing.T) {
	c := connect(t)
	for name, body := range map[string]string{
		"a silent loop":     "{% for i in (1..2000000000) %}{% endfor %}",
		"a range to a list": `{{ (1..2000000000) | join: "," }}`,
		"a self include":    `{% include "loop.html" %}`,
		"the clock":         `{{ "now" | date: "%Y" }}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newSite2518(t, c)
			res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "files": []map[string]any{
				{"path": "_layouts/base.html", "content": "{{ content }}"},
				{"path": "_includes/loop.html", "content": `{% include "loop.html" %}`},
				{"path": "index.html", "content": "---\nlayout: base\n---\n" + body},
			}})
			if err != nil || !res.IsError {
				t.Fatalf("must be refused: %v %s", err, text)
			}
			if !strings.Contains(text, "index.html") && !strings.Contains(text, "loop.html") {
				t.Errorf("the refusal must name the file: %s", text)
			}
		})
	}
}

func TestIssue2592_ASiteWithNoLayoutsPublishesExactlyAsBefore(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	raw := "---\nlooks: like front matter\n---\n<p>{{ never rendered }}</p>"
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "files": []map[string]any{{"path": "index.html", "content": raw}}})
	s.promote2592(t)
	if r := s.serves2592(t, "/", "never rendered"); r.body != raw {
		t.Errorf("a site with no layouts must be served byte for byte: %q", r.body)
	}
}

func TestIssue2592_SixThousandPagesFromOneLayoutAndOneTemplate(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	const records = 6000
	files := make([]map[string]any, 0, records+2)
	files = append(files,
		map[string]any{"path": "_layouts/base.html", "content": "<body>{{ content }}</body>"},
		map[string]any{"path": "product.html", "content": "---\nlayout: base\nfor_each: products\npermalink: /products/{{ item.slug }}/\n---\n<h1>{{ item.name }}</h1>"},
	)
	for i := range records {
		files = append(files, map[string]any{"path": fmt.Sprintf("_data/products/p%d.json", i), "content": fmt.Sprintf(`{"name":"Product %d"}`, i)})
	}
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "files": files})
	s.promote2592(t)
	s.serves2592(t, "/products/p5999/", "Product 5999")

	patched := c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("_data/products/p42.json"), "edits": editOf("Product 42", "Renamed")})
	if len(fmt.Sprint(patched)) > 2048 {
		t.Errorf("a one-line patch answered with %d bytes", len(fmt.Sprint(patched)))
	}
	s.promote2592(t)
	s.serves2592(t, "/products/p42/", "Renamed")
	s.serves2592(t, "/products/p43/", "Product 43")
}

func TestIssue2592_TheKnowledgePageTeachesIt(t *testing.T) {
	c := connect(t)
	page := fmt.Sprint(c.call("fetch", map[string]any{"reference": "mcp:knowledge_page:platform-pages-and-sites", "purpose": "Reading how layouts and records work to check the guidance teaches it."}))
	for _, want := range []string{"_layouts", "_includes", "_data", "for_each", "per_page", "permalink"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page must teach %s", want)
		}
	}
}
