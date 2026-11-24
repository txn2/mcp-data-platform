package pkgmap

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func fsOf(name, body string) fstest.MapFS {
	return fstest.MapFS{name: &fstest.MapFile{Data: []byte(body)}}
}

const npmLock = `{"lockfileVersion":3,"packages":{"":{},
"node_modules/react":{"version":"18.3.1"},
"node_modules/react-dom":{"version":"18.3.1","peerDependencies":{"react":"^18.3.1"}},
"node_modules/react-router-dom":{"version":"6.28.0","peerDependencies":{"react":">=16.8","react-dom":">=16.8"}},
"node_modules/@hookform/resolvers":{"version":"3.9.1","peerDependencies":{"react-hook-form":"^7.0.0"}},
"node_modules/react-hook-form":{"version":"7.53.2","peerDependencies":{"react":"^18"}},
"node_modules/x/node_modules/react":{"version":"17.0.0"}}}`

func TestReadLockForms(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"npm": fsOf("package-lock.json", npmLock),
		"pnpm": fsOf("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      react:\n        specifier: ^18.3.1\n        version: 18.3.1\n"+
			"      react-dom:\n        specifier: ^18.3.1\n        version: 18.3.1(react@18.3.1)\npackages:\n  react-dom@18.3.1:\n    peerDependencies:\n      react: ^18.3.1\n  react@18.3.1: {}\n"),
		"yarn1": fsOf("yarn.lock", "# yarn lockfile v1\n\n\"react@^18.3.1\":\n  version \"18.3.1\"\n\nreact-dom@^18.3.1, react-dom@^18.2.0:\n  version \"18.3.1\"\n"),
		"berry": fsOf("yarn.lock", "__metadata:\n  version: 8\n\"react@npm:^18.3.1\":\n  version: 18.3.1\n\"react-dom@npm:^18.3.1\":\n  version: 18.3.1\n  peerDependencies:\n    react: ^18.3.1\n"),
	}
	for name, src := range cases {
		lock, err := ReadLock(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lock.Packages["react"].Version != "18.3.1" || lock.Packages["react-dom"].Version != "18.3.1" {
			t.Errorf("%s: %+v", name, lock.Packages)
		}
	}
	lock, _ := ReadLock(cases["pnpm"])
	if !slices.Equal(lock.Packages["react-dom"].Peers, []string{"react"}) {
		t.Errorf("pnpm peers %v", lock.Packages["react-dom"].Peers)
	}
	if lock, _ := ReadLock(fstest.MapFS{}); lock.File != "" {
		t.Error("no lockfile read as one")
	}
	if _, err := ReadLock(fsOf("package-lock.json", "{")); err == nil {
		t.Error("bad lockfile accepted")
	}
}

type registry map[string][]string

func (r registry) PeersOf(_ context.Context, pkg, version string) ([]string, error) {
	if p, ok := r[pkg+"@"+version]; ok {
		return p, nil
	}
	return nil, errors.New("not in registry")
}

func TestGenerateAndCheck(t *testing.T) {
	lock, err := ReadLock(fsOf("package-lock.json", npmLock))
	if err != nil {
		t.Fatal(err)
	}
	specs := []string{"react", "react/jsx-runtime", "react-dom/client", "react-router-dom", "@hookform/resolvers/zod", "react-hook-form", "lodash"}
	gen, err := Generate(context.Background(), specs, lock, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"react":                   "https://esm.sh/react@18.3.1",
		"react/jsx-runtime":       "https://esm.sh/react@18.3.1/jsx-runtime",
		"react-dom/client":        "https://esm.sh/react-dom@18.3.1/client?deps=react@18.3.1",
		"react-router-dom":        "https://esm.sh/react-router-dom@6.28.0?deps=react@18.3.1,react-dom@18.3.1",
		"@hookform/resolvers/zod": "https://esm.sh/@hookform/resolvers@3.9.1/zod?deps=react-hook-form@7.53.2",
		"react-hook-form":         "https://esm.sh/react-hook-form@7.53.2?deps=react@18.3.1",
	}
	for k, v := range want {
		if gen.Imports[k] != v {
			t.Errorf("%s = %q, want %q", k, gen.Imports[k], v)
		}
	}
	if !slices.Equal(gen.Missing, []string{"lodash"}) {
		t.Errorf("missing %v", gen.Missing)
	}
	msgs, err := CheckSingletons(context.Background(), gen.Imports, nil, lock, nil)
	if err != nil || len(msgs) != 0 {
		t.Errorf("the generated map is refused: %v %v", msgs, err)
	}

	bad := map[string]string{
		"react":            "https://esm.sh/react@18.3.1",
		"react-dom/client": "https://esm.sh/react-dom@18.3.1/client?deps=react@18.2.0",
		"react-router-dom": "https://esm.sh/react-router-dom@6.28.0",
		"other":            "https://esm.sh/other@1.0.0",
	}
	msgs, err = CheckSingletons(context.Background(), bad, nil, lock, registry{"other@1.0.0": {"react"}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{`pins react@18.2.0`, `"react-router-dom" (https://esm.sh/react-router-dom@6.28.0) loads its own copy of react`, `"other"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("refusals lack %s:\n%s", want, joined)
		}
	}
	classic := map[string]string{"react": "https://esm.sh/react@18.3.1", "react-dom/client": "https://esm.sh/react-dom@18.3.1/client"}
	if msgs, _ := CheckSingletons(context.Background(), classic, nil, lock, nil); len(msgs) != 0 {
		t.Errorf("react-dom beside react at one version is refused: %v", msgs)
	}
	classic["react-dom/client"] = "https://esm.sh/react-dom@18.2.0/client"
	if msgs, _ := CheckSingletons(context.Background(), classic, nil, &Lock{Packages: map[string]Package{"react-dom": {Version: "18.2.0", Peers: []string{"react"}}}}, nil); len(msgs) != 1 {
		t.Errorf("react-dom at another version than react without deps: %v", msgs)
	}
	two := map[string]string{"react": "https://esm.sh/react@18.3.1", "react/jsx-runtime": "https://cdn.jsdelivr.net/npm/react@18.2.0/jsx-runtime/+esm"}
	if msgs, _ := CheckSingletons(context.Background(), two, nil, nil, nil); len(msgs) != 0 {
		t.Logf("jsdelivr path is not read: %v", msgs)
	}
	two["react/jsx-runtime"] = "https://esm.sh/react@18.2.0/jsx-runtime"
	if msgs, _ := CheckSingletons(context.Background(), two, nil, nil, nil); len(msgs) != 1 || !strings.Contains(msgs[0], "two copies of react") {
		t.Errorf("two versions: %v", msgs)
	}
	if _, err := (Registry{}).PeersOf(context.Background(), "x", "1"); err != nil {
		t.Error(err)
	}
	r := Registry{Fetch: func(context.Context, string) ([]byte, string, error) {
		return []byte(`{"peerDependencies":{"react":"*"}}`), "application/json", nil
	}}
	if p, err := r.PeersOf(context.Background(), "@a/b", "1.0.0"); err != nil || !slices.Equal(p, []string{"react"}) {
		t.Errorf("registry %v %v", p, err)
	}
}
