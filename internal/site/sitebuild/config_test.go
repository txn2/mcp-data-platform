package sitebuild

import (
	"strings"
	"testing"
)

func TestParseRefusals(t *testing.T) {
	cases := map[string]string{
		`not json`:                             "not a build file",
		`{"entries":["a.tsx"]} {}`:             "more than one JSON value",
		`{"entries":["a.tsx"],"extra":1}`:      "unknown field",
		`{"entries":["a.tsx"],"remote":"cdn"}`: `remote is "cdn"`,
		`{"entries":[]}`:                       "entries is empty",
		`{"entries":["../a.tsx"]}`:             "not a path in the site",
		`{"entries":["/a.tsx"]}`:               "not a path in the site",
		`{"entries":["a.js"]}`:                 "not TypeScript or JSX",
		`{"entries":["a.tsx","a.ts"]}`:         "both compile to a.js",
		`{"entries":["a.tsx"],"imports":{"./x":"https://esm.sh/x@1.0.0"}}`: "not a package name",
		`{"entries":["a.tsx"],"imports":{"x":"https://esm.sh/x@1"}}`:       "not an https URL at an exact version",
		`{"entries":["a.tsx"],"imports":{"x":"http://esm.sh/x@1.0.0"}}`:    "not an https URL at an exact version",
		`{"entries":["a.tsx"],"imports":{"x/":"https://esm.sh/x@1.0.0"}}`:  "must both end in /",
	}
	for doc, want := range cases {
		if _, err := Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", doc, err, want)
		}
	}
	many := `{"entries":[` + strings.Repeat(`"a.tsx",`, maxEntries) + `"b.tsx"]}`
	if _, err := Parse([]byte(many)); err == nil || !strings.Contains(err.Error(), "more than the") {
		t.Errorf("too many entries: %v", err)
	}
}

func TestParseDefaultsAndMapping(t *testing.T) {
	cfg := mustParse(t, `{"entries":["app.tsx"],"imports":{
		"react":"https://esm.sh/react@18.3.1",
		"@tanstack/react-query":"https://esm.sh/@tanstack/react-query@5.59.0",
		"lodash-es/":"https://esm.sh/lodash-es@4.17.21/",
		"q":"https://esm.sh/q@1.0.0?bundle"}}`)
	if cfg.Remote != RemoteExternal {
		t.Errorf("remote defaults to %q", cfg.Remote)
	}
	cases := map[string]string{
		"react":                          "https://esm.sh/react@18.3.1",
		"react/jsx-runtime":              "https://esm.sh/react@18.3.1/jsx-runtime",
		"@tanstack/react-query":          "https://esm.sh/@tanstack/react-query@5.59.0",
		"@tanstack/react-query/devtools": "https://esm.sh/@tanstack/react-query@5.59.0/devtools",
		"lodash-es/debounce":             "https://esm.sh/lodash-es@4.17.21/debounce",
		"q/sub":                          "",
		"vue":                            "",
	}
	for spec, want := range cases {
		got, ok := cfg.mapped(spec)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v, want %q", spec, got, ok, want)
		}
	}
}

func TestPinnedAndSource(t *testing.T) {
	for u, want := range map[string]bool{
		"https://esm.sh/react@18.3.1":                   true,
		"https://esm.sh/react@18.3.1/jsx-runtime":       true,
		"https://esm.sh/@scope/pkg@1.2.3-beta.1?target": true,
		"https://cdn.jsdelivr.net/npm/lit@3.2.1/+esm":   true,
		"https://deno.land/std@v0.200.0/mod.ts":         true,
		"https://esm.sh/react@18":                       false,
		"https://esm.sh/react@^18.3.1":                  false,
		"https://esm.sh/react":                          false,
		"https://esm.sh/react@18.3.1x":                  false,
		"http://esm.sh/react@18.3.1":                    false,
		"https://user@host/x@1.2.3":                     false,
	} {
		if Pinned(u) != want {
			t.Errorf("Pinned(%s) = %v", u, !want)
		}
	}
	for name, want := range map[string]bool{"a.tsx": true, "b/c.TS": true, "x.jsx": true, ControlFile: true, "a.js": false, "a.d": false} {
		if IsSource(name) != want {
			t.Errorf("IsSource(%s) = %v", name, !want)
		}
	}
	if OutputOf("admin/a.tsx") != "admin/a.js" {
		t.Error(OutputOf("admin/a.tsx"))
	}
}
