//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Acceptance for #2790: on an imports_from site, a library mapped by hand in
// _build.json's imports and absent from the lockfile publishes and loads from
// the hand-written URL; a specifier neither locked nor mapped is still refused
// by name; with tailwind set, manage_site imports proposes no tailwindcss key.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; _build.json's
// imports_from and tailwind are strings and imports an object of strings. A
// request to a site is an HTTP GET naming the hostname in Host.

const lock2790 = `{"lockfileVersion":3,"packages":{"":{},"node_modules/react":{"version":"18.3.1"},"node_modules/react-dom":{"version":"18.3.1","peerDependencies":{"react":"^18.3.1"}}}}`

func project2790(build, app string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": build},
		{"path": "app.tsx", "content": app},
		{"path": "styles.css", "content": `@import "tailwindcss";`},
		{"path": "package.json", "content": `{"dependencies":{"react":"^18.3.1","react-dom":"^18.3.1"}}`},
		{"path": "package-lock.json", "content": lock2790},
		{"path": "index.html", "content": `<!doctype html><html><body><div id="root"></div><script type="module" src="/app.js"></script></body></html>`},
	}
}

const app2790 = `import { createRoot } from "react-dom/client"; import pad from "left-pad"; createRoot(document.getElementById("root")!).render(<p>{pad("a2790", 8)}</p>);`

// Criterion 1: the hand-mapped library publishes and the served script loads
// it from the hand-written URL.
func TestIssue2790_AHandMappedLibraryPublishes(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, project2790(`{"entries":["app.tsx"],"imports_from":"package.json","imports":{"left-pad":"https://esm.sh/left-pad@1.3.0"}}`, app2790))
	s.served2774(t, "/app.js", "https://esm.sh/left-pad@1.3.0")
}

// Criterion 3: a specifier neither locked nor mapped is still refused, naming
// it.
func TestIssue2790_AnUnmappedUnlockedLibraryIsRefused(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	res, text, err := c.callRaw("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "unmapped",
		"files": project2790(`{"entries":["app.tsx"],"imports_from":"package.json"}`, app2790),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "locks no version of left-pad") {
		t.Errorf("an unlocked, unmapped library was not refused by name: %v %s", res.IsError, text)
	}
}

// Criterion 4: with tailwind set, the proposal names no tailwindcss key, and a
// library the build file maps is not reported missing.
func TestIssue2790_TheProposalLeavesTailwindOut(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	app := `import "./styles.css"; ` + app2790
	c.call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "tailwind",
		"files": project2790(`{"entries":["app.tsx"],"tailwind":"4.1.8","imports":{"left-pad":"https://esm.sh/left-pad@1.3.0","react-dom/client":"https://esm.sh/react-dom@18.3.1/client?deps=react@18.3.1","react":"https://esm.sh/react@18.3.1"}}`, app),
	})
	proposed := c.call("manage_site", map[string]any{"command": "imports", "site": s.name})
	imports, _ := proposed["imports"].(map[string]any)
	for key := range imports {
		if key == "tailwindcss" || strings.HasPrefix(key, "tailwindcss/") {
			t.Errorf("the proposal names %s; tailwind compiles that stylesheet", key)
		}
	}
	if missing, _ := proposed["missing"].([]any); len(missing) != 0 {
		t.Errorf("a library the build file maps is reported missing: %v", missing)
	}
}

// Criterion 2: a React library mapped by hand without ?deps= is still refused
// as a second copy of React; its React peer is the lockfile's.
func TestIssue2790_AHandMappedReactLibraryWithoutDepsIsRefused(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	files := project2790(`{"entries":["app.tsx"],"imports_from":"package.json","imports":{"react-select":"https://esm.sh/react-select@5.8.0"}}`,
		`import { createRoot } from "react-dom/client"; import Select from "react-select"; createRoot(document.getElementById("root")!).render(<Select />);`)
	files[4] = map[string]any{"path": "package-lock.json", "content": strings.TrimSuffix(lock2790, "}}") +
		`,"node_modules/react-select":{"version":"5.8.0","peerDependencies":{"react":"^18","react-dom":"^18"}}}}`}
	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "singleton", "files": files})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "https://esm.sh/react-select@5.8.0") || !strings.Contains(text, "own copy of react") {
		t.Errorf("a hand-mapped React library without deps= was not refused naming its URL and react: %v %s", res.IsError, text)
	}
}
