//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2633: a site template whose for loop takes its range bound,
// or its limit, from an arithmetic filter publishes and renders, as one taking
// it from a literal does. Before, the release was refused with "can't convert
// int64(3) to type int". A whole number read from a JSON record bounds a loop
// too, and a float bound is refused naming the file, as Liquid refuses one.
//
// Wire forms: manage_site takes command, site and note as strings and files
// as a list of objects whose content is utf-8 text; a request to a site is an
// HTTP GET naming the hostname in Host.

func source2633() []map[string]any {
	return []map[string]any{
		// A site with a _layouts folder is rendered; one without is served as published.
		{"path": "_layouts/base.html", "content": "<html><body>{{ content }}</body></html>\n"},
		{"path": "_data/plans/team.json", "content": `{"name":"Team","seats":3}`},
		{"path": "index.html", "content": "---\ntitle: Home\n---\n" +
			"{% assign n = 2 | plus: 1 %}<p>plus:{% for i in (1..n) %}{{ i }}{% endfor %}</p>" +
			"{% assign half = 7 | divided_by: 2 %}<p>divided_by:{% for i in (1..half) %}{{ i }}{% endfor %}</p>" +
			"{% assign odd = 7 | modulo: 4 %}<p>modulo:{% for i in (1..odd) %}{{ i }}{% endfor %}</p>" +
			"{% assign few = 1 | plus: 1 %}<p>limit:{% for i in (1..5) limit: few %}{{ i }}{% endfor %}</p>" +
			"{% for p in site.data.plans %}<p>seats:{% for i in (1..p.seats) %}{{ i }}{% endfor %}</p>{% endfor %}"},
	}
}

func TestIssue2633_AnArithmeticLoopBoundPublishesAndRenders(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	c.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "arithmetic bounds", "files": source2633()})
	s.promote2592(t)
	body := s.serves2592(t, "/", "<p>plus:123</p>").body
	for _, want := range []string{"<p>divided_by:123</p>", "<p>modulo:123</p>", "<p>limit:12</p>", "<p>seats:123</p>"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not hold %s: %s", want, body)
		}
	}
}

func TestIssue2633_AFloatBoundIsRefusedNamingTheFile(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "a float bound", "files": []map[string]any{
		{"path": "_layouts/base.html", "content": "<html><body>{{ content }}</body></html>\n"},
		{"path": "index.html", "content": "---\n---\n{% assign n = 6 | divided_by: 4.0 %}{% for i in (1..n) %}{{ i }}{% endfor %}"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "index.html") {
		t.Fatalf("a float bound: error=%v %s; want refused naming index.html", res.IsError, text)
	}
}
