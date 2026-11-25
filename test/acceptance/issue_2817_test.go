//go:build integration

package acceptance

import (
	"testing"
)

// Issue #2817: a manage_site publish dry run answers with what the publish
// would write, or a classified refusal, on every site shape: a plain file set
// on an environment that serves no release yet (the case that failed as "the
// tool call failed unexpectedly"), a templated site, and a compiled one.
//
// Wire forms: manage_site's command, site and name are strings, files a list
// of objects whose path and content are strings, and dry_run a boolean, each
// sent in that one form. No parameter touched admits a second form.

// newSite2817 creates a site and deletes it when the test ends.
func newSite2817(t *testing.T, c *client) string {
	t.Helper()
	name := "acc2817-" + unique1579()
	c.call("manage_site", map[string]any{"command": "create", "name": name, "description": "Acceptance 2817"})
	t.Cleanup(func() { _, _, _ = connect(t).callRaw("manage_site", map[string]any{"command": "delete", "site": name}) })
	return name
}

// TestIssue2817_ADryRunAnswersOnEverySiteShape: before any release, a plain
// file set, a templated site and a compiled site each answer with the outputs
// they would change and store no release.
func TestIssue2817_ADryRunAnswersOnEverySiteShape(t *testing.T) {
	c := connect(t)
	shapes := map[string][]map[string]any{
		"plain": {
			{"path": "index.html", "content": "<h1>Hello</h1>"},
			{"path": "about/index.html", "content": "<p>About</p>"},
		},
		"templated": {
			{"path": "_layouts/base.html", "content": "<b>{{ content }}</b>"},
			{"path": "_data/products/a.json", "content": `{"name":"Anvil"}`},
			{"path": "product.html", "content": "---\nlayout: base\nfor_each: products\npermalink: /p/{{ item.slug }}/\n---\n{{ item.name }}"},
			{"path": "index.html", "content": "---\nlayout: base\n---\nhome"},
		},
		"compiled": compiled2774(buildFile2774),
	}
	for shape, files := range shapes {
		t.Run(shape, func(t *testing.T) {
			site := newSite2817(t, c)
			res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": site, "files": files, "dry_run": true})
			if err != nil || res.IsError {
				t.Fatalf("the dry run failed: %v %s", err, text)
			}
			out := c.call("manage_site", map[string]any{"command": "publish", "site": site, "files": files, "dry_run": true})
			if n, _ := out["outputs_changed"].(float64); n < 1 {
				t.Errorf("the dry run names no output it would write: %v", out)
			}
			if out["release"] != nil {
				t.Errorf("an environment that serves nothing reported a current release: %v", out["release"])
			}
			releases := c.call("manage_site", map[string]any{"command": "releases", "site": site})
			if rels, _ := releases["releases"].([]any); len(rels) != 0 {
				t.Errorf("a dry run stored a release: %v", rels)
			}
		})
	}
}
