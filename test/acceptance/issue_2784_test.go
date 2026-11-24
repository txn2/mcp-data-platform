//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2784: import.meta.site.release compiles to the release
// number, and the release promoted to another environment serves the same
// bytes; with "sourcemap": "private" a map answers 404 to an anonymous request
// and is served to a person who manages the site, signed in on a private
// environment; symbolicate maps a minified stack trace to original files,
// lines and columns; true and false behave as before.
//
// Wire forms: manage_site takes command, site, note and stack as strings and
// files as a list of {path, content} objects whose content is utf-8 text;
// _build.json's sourcemap is a boolean or the string "private". A request to a
// site is an HTTP GET naming the hostname in Host, with and without a Cookie.

const app2784 = `import { fail } from "./lib/fail.ts";
document.title = "release-" + import.meta.site.release + "-of-" + import.meta.site.site;
(window as any).fail2784 = fail;
`

const fail2784 = `export function fail(): never {
  const reason = "boom-2784";
  throw new Error(reason);
}
`

func files2784(sourcemap string) []map[string]any {
	return []map[string]any{
		{"path": "_build.json", "content": `{"entries":["app.tsx"],"minify":true,"sourcemap":` + sourcemap + `}`},
		{"path": "app.tsx", "content": app2784},
		{"path": "lib/fail.ts", "content": fail2784},
		{"path": "index.html", "content": `<!doctype html><script type="module" src="/app.js"></script>`},
	}
}

// Criteria 1, 2, 3 and 4: app.js holds release 1's number and the same bytes
// on dev and stage; its private map is 404 to anyone on the public stage
// domain and served to the site's manager signed in on dev; symbolicate maps a
// frame of the minified app.js to lib/fail.ts.
func TestIssue2784_ReleaseMetadataPrivateMapsAndSymbolicate(t *testing.T) {
	c := connect(t)
	s := publish2774(t, c, files2784(`"private"`))
	stage := s.served2774(t, "/app.js", "release-")
	if !strings.Contains(stage.body, `release:1`) && !strings.Contains(stage.body, `"release":1`) && !strings.Contains(stage.body, "release-1") {
		t.Errorf("app.js does not hold release 1's number:\n%s", stage.body)
	}
	if !strings.Contains(stage.body, "sourceMappingURL=app.js.map") {
		t.Errorf("app.js does not name its map")
	}
	if r := onHost(t, s.preview("stage"), http.MethodGet, "/app.js.map", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("a private map answered %d to an anonymous request", r.status)
	}

	dev := s.name + "--dev." + pagesDomain2517
	manager := signIn2604(t, dev, emailAdminKC2604, passAdminKC2604)
	devJS := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, dev, http.MethodGet, "/app.js", withCookie2559(manager), "")
		return r, r.status == http.StatusOK
	})
	if devJS.body != stage.body {
		t.Errorf("the release promoted to stage serves other bytes than dev")
	}
	m := onHost(t, dev, http.MethodGet, "/app.js.map", withCookie2559(manager), "")
	if m.status != http.StatusOK || !strings.Contains(m.body, "lib/fail.ts") {
		t.Errorf("the site's manager does not get the map: %d %s", m.status, m.body)
	}

	line, col := 1, strings.Index(stage.body, "throw")+1
	got := c.call("manage_site", map[string]any{"command": "symbolicate", "site": s.name,
		"stack": "Error: boom-2784\n    at fail (https://" + s.preview("stage") + "/app.js:" + itoa2784(line) + ":" + itoa2784(col) + ")"})
	raw, _ := json.Marshal(got)
	if number2588(got["mapped"]) != 1 || !strings.Contains(string(raw), `"source":"lib/fail.ts"`) || !strings.Contains(string(raw), `"line":3`) {
		t.Errorf("symbolicate did not map the frame to lib/fail.ts line 3: %s", raw)
	}
}

func itoa2784(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Criterion 5: "sourcemap": true serves the map to anyone, and false writes
// none.
func TestIssue2784_PublicAndNoMapsBehaveAsBefore(t *testing.T) {
	c := connect(t)
	public := publish2774(t, c, files2784("true"))
	public.served2774(t, "/app.js", "release-")
	if r := onHost(t, public.preview("stage"), http.MethodGet, "/app.js.map", nil, ""); r.status != http.StatusOK {
		t.Errorf("a public map answered %d", r.status)
	}
	none := publish2774(t, c, files2784("false"))
	if js := none.served2774(t, "/app.js", "release-"); strings.Contains(js.body, "sourceMappingURL") {
		t.Errorf("sourcemap false names a map")
	}
	if r := onHost(t, none.preview("stage"), http.MethodGet, "/app.js.map", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("sourcemap false served a map: %d", r.status)
	}
}
