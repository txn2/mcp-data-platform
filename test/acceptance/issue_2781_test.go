//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2781: an imports prefix may map to a folder of the site
// ("@/": "/src/"), resolved and tracked as a local import; a prefix mapping
// outside the site or not from its root is refused naming the key; define
// replaces import.meta.env values, a guarded branch dropped under minify; and
// a read of an import.meta.env value define does not set is refused naming
// the file, line and key.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; manage_asset
// takes action and asset_id as strings and edits as a list of {find, replace}
// objects; _build.json's imports values are strings and define values are
// JSON scalars (a boolean and a string here). No tool parameter changed its
// schema.

const build2781 = `{"entries":["app.tsx"],"minify":true,"imports":{"@/":"/src/"},` +
	`"define":{"import.meta.env.DEV":false,"import.meta.env.VITE_API_BASE_URL":"/api-2781"}}`

const app2781 = `import { greeting } from "@/lib/x";
if (import.meta.env.DEV) { console.log("dev-only-branch-2781"); }
document.title = greeting + import.meta.env.VITE_API_BASE_URL;
`

func files2781(build, app string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": build},
		{"path": "app.tsx", "content": app},
		{"path": "src/lib/x.ts", "content": `export const greeting = "from-src-lib-2781";`},
		{"path": "index.html", "content": `<!doctype html><script type="module" src="/app.js"></script>`},
	}
}

// Criteria 1 and 3: @/lib/x compiles from src/lib/x.ts and a change to it
// compiles the entry again; define fills the values and the DEV branch is
// gone.
func TestIssue2781_ALocalPrefixAndDefineCompile(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, files2781(build2781, app2781))
	js := s.served2774(t, "/app.js", "from-src-lib-2781").body
	if !strings.Contains(js, "/api-2781") || strings.Contains(js, "dev-only-branch-2781") || strings.Contains(js, "import.meta.env") {
		t.Errorf("define was not applied, or the DEV branch survived minify:\n%s", js)
	}
	c.call("manage_asset", map[string]any{"action": "patch", "asset_id": s.ref("src/lib/x.ts"), "edits": editOf("from-src-lib-2781", "changed-src-lib-2781")})
	s.promote2592(t)
	s.served2774(t, "/app.js", "changed-src-lib-2781")
}

// Criteria 2 and 4: a prefix outside the site or not from its root is refused
// naming the key, and an unset import.meta.env read is refused naming the
// file, line and key.
func TestIssue2781_BadPrefixesAndUnsetEnvReadsAreRefused(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	publishRefused := func(files []map[string]any) string {
		t.Helper()
		res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "refused", "files": files})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("the publish was not refused: %s", text)
		}
		return text
	}
	for _, dir := range []string{"/../", "src/"} {
		build := strings.Replace(build2781, `"@/":"/src/"`, `"@/":"`+dir+`"`, 1)
		if text := publishRefused(files2781(build, app2781)); !strings.Contains(text, `"@/"`) {
			t.Errorf("%s: the refusal does not name the key: %s", dir, text)
		}
	}
	unset := strings.Replace(app2781, "import.meta.env.VITE_API_BASE_URL", "import.meta.env.VITE_UNSET_2781", 1)
	text := publishRefused(files2781(build2781, unset))
	if !strings.Contains(text, "app.tsx:3:") || !strings.Contains(text, "import.meta.env.VITE_UNSET_2781") {
		t.Errorf("the refusal does not name app.tsx, line 3 and the key: %s", text)
	}
	if rels := releases2588(t, c, s); len(rels) != 0 {
		t.Errorf("a refused publish made a release: %v", rels)
	}
}
