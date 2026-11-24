//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2785: manage_site imports reads a site's package.json and
// lockfile and proposes an imports map that publishes and renders with one
// copy of React, with no hand edits; a map where a React library lacks
// deps=react@ is refused naming the URL and the singleton, and so is one
// naming two React versions; package.json, the lockfile and tsconfig.json are
// not served with _build.json and are without it; exclude keeps a file in the
// release unserved.
//
// Wire forms: manage_site takes command, site and note as strings and files as
// a list of {path, content} objects whose content is utf-8 text; _build.json's
// imports_from is a string, imports values strings, and exclude a list of
// strings. A request to a site is an HTTP GET naming the hostname in Host.

const pkg2785 = `{"name":"admin-2785","private":true,"dependencies":{"react":"^18.3.1","react-dom":"^18.3.1","react-router-dom":"^6.28.0",` +
	`"@tanstack/react-query":"^5.59.0","react-hook-form":"^7.53.2","@hookform/resolvers":"^3.9.1","zod":"^3.23.8",` +
	`"react-oidc-context":"^3.2.0","oidc-client-ts":"^3.1.0"}}`

const lock2785 = `{"name":"admin-2785","lockfileVersion":3,"requires":true,"packages":{
"":{"name":"admin-2785"},
"node_modules/react":{"version":"18.3.1"},
"node_modules/react-dom":{"version":"18.3.1","peerDependencies":{"react":"^18.3.1"}},
"node_modules/react-router-dom":{"version":"6.28.0","peerDependencies":{"react":">=16.8","react-dom":">=16.8"}},
"node_modules/@tanstack/react-query":{"version":"5.59.0","peerDependencies":{"react":"^18 || ^19"}},
"node_modules/react-hook-form":{"version":"7.53.2","peerDependencies":{"react":"^16.8.0 || ^17 || ^18 || ^19"}},
"node_modules/@hookform/resolvers":{"version":"3.9.1","peerDependencies":{"react-hook-form":"^7.0.0"}},
"node_modules/zod":{"version":"3.23.8"},
"node_modules/react-oidc-context":{"version":"3.2.0","peerDependencies":{"oidc-client-ts":"^3.1.0","react":">=16.14.0"}},
"node_modules/oidc-client-ts":{"version":"3.1.0"}}}`

const app2785 = `import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { hasAuthParams } from "react-oidc-context";

const schema = z.object({ name: z.string() });
function App(): JSX.Element {
  const navigate = useNavigate();
  const form = useForm({ resolver: zodResolver(schema), defaultValues: { name: "x" } });
  const q = useQuery({ queryKey: ["k"], queryFn: async () => "ready" });
  const [colour, setColour] = useState("rgb(220,0,0)");
  useEffect(() => {
    if (q.data === "ready" && form.getValues("name") === "x" && typeof navigate === "function" && hasAuthParams() === false) {
      setColour("rgb(0,170,0)");
    }
  }, [q.data]);
  return <div style={{ position: "fixed", inset: 0, background: colour }}>one-react-2785</div>;
}
const client = new QueryClient();
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={client}><MemoryRouter><App /></MemoryRouter></QueryClientProvider>,
);
`

func project2785(build string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": build},
		{"path": "app.tsx", "content": app2785},
		{"path": "package.json", "content": pkg2785},
		{"path": "package-lock.json", "content": lock2785},
		{"path": "tsconfig.json", "content": `{"compilerOptions":{"jsx":"react-jsx"}}`},
		{"path": "e2e/README.md", "content": "end-to-end notes 2785"},
		{"path": "index.html", "content": `<!doctype html><html><body style="margin:0;background:#fff"><div id="root"></div><script type="module" src="/app.js"></script></body></html>`},
	}
}

// Criterion 1: the proposed map, written into _build.json unchanged,
// publishes and the tile renders the page green, which happens only when every
// library shares one React.
func TestIssue2785_TheGeneratedMapRendersWithOneReact(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2785-")
	admin := connectAs(t, keyAdmin2604)
	admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "source", "files": project2785(`{"entries":["app.tsx"],"imports_from":"package.json","exclude":["e2e/**"]}`)})
	proposed := admin.call("manage_site", map[string]any{"command": "imports", "site": s.name})
	imports, _ := proposed["imports"].(map[string]any)
	for key, want := range map[string]string{
		"react-dom/client":        "https://esm.sh/react-dom@18.3.1/client?deps=react@18.3.1",
		"react-router-dom":        "https://esm.sh/react-router-dom@6.28.0?deps=react@18.3.1,react-dom@18.3.1",
		"@hookform/resolvers/zod": "https://esm.sh/@hookform/resolvers@3.9.1/zod?deps=react-hook-form@7.53.2",
		"react-oidc-context":      "https://esm.sh/react-oidc-context@3.2.0?deps=oidc-client-ts@3.1.0,react@18.3.1",
	} {
		if imports[key] != want {
			t.Errorf("%s = %v, want %s", key, imports[key], want)
		}
	}
	raw, _ := json.Marshal(imports)
	build := `{"entries":["app.tsx"],"imports":` + string(raw) + `}`
	admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "generated", "base": 1,
		"files": []map[string]any{{"path": "_build.json", "content": build}}})
	a := &app2747{site: s}
	shot := a.drawnRelease(t, 2)
	if r, g, b := centre2747(t, shot); g < 140 || r > 60 || b > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); with one React the page paints green", r, g, b)
	}
}

// Criteria 2 and 3: a React library without deps=react@ is refused naming its
// URL and react, and two React versions are refused naming both.
func TestIssue2785_DuplicateReactIsRefusedAtPublish(t *testing.T) {
	c := connect(t)
	s := newSite2518(t, c)
	for build, wants := range map[string][]string{
		`{"entries":["app.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1","react-dom/client":"https://esm.sh/react-dom@18.3.1/client?deps=react@18.3.1","react-router-dom":"https://esm.sh/react-router-dom@6.28.0"}}`: {"https://esm.sh/react-router-dom@6.28.0", "own copy of react"},
		`{"entries":["app.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1","react/jsx-runtime":"https://esm.sh/react@18.2.0/jsx-runtime"}}`:                                                                             {"two copies of react", `"react"`, `"react/jsx-runtime"`, "18.2.0", "18.3.1"},
	} {
		files := []map[string]any{
			{"path": "_build.json", "content": build},
			{"path": "app.tsx", "content": `import { BrowserRouter } from "react-router-dom"; export const r = <BrowserRouter />;`},
			{"path": "package-lock.json", "content": lock2785},
		}
		res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "refused", "files": files})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Errorf("a map loading two copies of React published: %s", build)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("the refusal does not name %s: %s", want, text)
			}
		}
	}
}

// Criteria 4 and 5: with _build.json, package.json, the lockfile, tsconfig.json
// and an excluded file are not served, and still read back; without
// _build.json the package files are served as before.
func TestIssue2785_ProjectFilesAreSourceOnlyWithABuildFile(t *testing.T) {
	c := connect(t)
	files := append(compiled2774(buildFile2774),
		map[string]any{"path": "package.json", "content": pkg2785},
		map[string]any{"path": "package-lock.json", "content": lock2785},
		map[string]any{"path": "tsconfig.json", "content": `{"compilerOptions":{}}`},
		map[string]any{"path": ".nvmrc", "content": "22"},
		map[string]any{"path": "e2e/README.md", "content": "end-to-end notes 2785"},
	)
	files[0] = map[string]any{"path": "_build.json", "content": strings.Replace(buildFile2774, `"entries"`, `"exclude": ["e2e/**"], "entries"`, 1)}
	s := publish2774(t, c, files)
	s.served2774(t, "/app.js", "cell:")
	for _, path := range []string{"/package.json", "/package-lock.json", "/tsconfig.json", "/.nvmrc", "/e2e/README.md"} {
		if r := onHost(t, s.preview("stage"), http.MethodGet, path, nil, ""); r.status != http.StatusNotFound {
			t.Errorf("%s answered %d with a build file", path, r.status)
		}
	}
	if got := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": s.ref("e2e/README.md")}); got["content"] != "end-to-end notes 2785" {
		t.Errorf("e2e/README.md does not read back: %v", got["content"])
	}

	plain := publish2774(t, c, []map[string]any{{"path": "index.html", "content": "<p>plain-2785</p>"}, {"path": "package.json", "content": pkg2785}})
	if r := plain.served2774(t, "/package.json", "admin-2785"); r.status != http.StatusOK {
		t.Errorf("without a build file package.json answered %d", r.status)
	}
	res, text, err := c.callRaw("manage_site", map[string]any{"command": "publish", "site": plain.name, "note": "npmrc", "base": 1,
		"files": []map[string]any{{"path": ".npmrc", "content": "//registry.npmjs.org/:_authToken=npm_secret_2785"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, ".npmrc") || strings.Contains(text, "npm_secret_2785") {
		t.Errorf(".npmrc was not refused, or the refusal repeats its token: %v %s", res.IsError, text)
	}
	searchLogs2518(t, "npm_secret_2785")
}
