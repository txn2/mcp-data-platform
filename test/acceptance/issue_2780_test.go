//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2780: a site whose _build.json names a Tailwind CSS 4
// version has the stylesheet importing tailwindcss compiled at publish, in the
// platform: the utilities its source uses and no others, its @theme and a
// @custom-variant in effect, a class added by a patch added to the next
// release; @plugin and @config are refused naming the file and directive; a
// version that does not exist is refused naming it.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; manage_asset
// takes action and asset_id as strings and edits as a list of {find, replace}
// objects; _build.json's tailwind is a string. A request to a site is an HTTP
// GET naming the hostname in Host. No tool parameter changed its schema.

const tailwindVersion2780 = "4.1.13"

func tailwindSite2780(version, css string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": `{"entries":["app.tsx"],"tailwind":"` + version + `"}`},
		{"path": "app.tsx", "content": `import "./styles.css";` + "\n" +
			`document.body.innerHTML = '<div class="p-4 text-brand dark:bg-black flex">tailwind-2780</div>';`},
		{"path": "styles.css", "content": css},
		{"path": "index.html", "content": `<!doctype html><link rel="stylesheet" href="/app.css"><script type="module" src="/app.js"></script>`},
	}
}

const css2780 = "@import \"tailwindcss\";\n@custom-variant dark (&:where(.dark, .dark *));\n@theme { --color-brand: #123456; }\n"

// Criteria 1, 2 and 3: the utilities the source uses are in app.css and
// others are not, the theme colour and the dark variant take effect, and a
// class a patch adds is in the next release.
func TestIssue2780_TailwindCompilesTheUtilitiesTheSourceUses(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, tailwindSite2780(tailwindVersion2780, css2780))
	css := s.served2774(t, "/app.css", "tailwindcss v"+tailwindVersion2780).body
	for _, want := range []string{".p-4", ".flex", ".text-brand", "#123456", ".dark", "bg-black"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	for _, unused := range []string{".m-96", ".grid-cols-7", ".text-red-500"} {
		if strings.Contains(css, unused) {
			t.Errorf("app.css holds %s, which no file of the site uses", unused)
		}
	}
	if strings.Contains(css, `@import "tailwindcss"`) {
		t.Errorf("app.css still imports tailwindcss as written")
	}

	c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("app.tsx"), "edits": editOf("p-4 text-brand", "p-4 m-96 text-brand")})
	s.promote2592(t)
	if css := s.served2774(t, "/app.css", ".m-96").body; !strings.Contains(css, ".p-4") {
		t.Errorf("the patched release lost a utility:\n%s", css)
	}
}

// Criteria 4 and 5: @plugin and @config are refused naming the file and the
// directive, and a version that does not exist is refused naming it.
func TestIssue2780_PluginsConfigAndAMissingVersionAreRefused(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	for css, want := range map[string]string{
		"@import \"tailwindcss\";\n@plugin \"daisyui\";\n":              `@plugin "daisyui"`,
		"@import \"tailwindcss\";\n@config \"./tailwind.config.js\";\n": `@config "./tailwind.config.js"`,
	} {
		res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "refused", "files": tailwindSite2780(tailwindVersion2780, css)})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(text, "styles.css") || !strings.Contains(text, want) {
			t.Errorf("%s was not refused naming styles.css: %v %s", want, res.IsError, text)
		}
	}
	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "missing", "files": tailwindSite2780("4.99.99", css2780)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "4.99.99") {
		t.Errorf("a version that does not exist was not refused naming it: %v %s", res.IsError, text)
	}
	if rels := releases2588(t, c, s); len(rels) != 0 {
		t.Errorf("a refused publish made a release: %v", rels)
	}
}
