//go:build integration

package acceptance

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"testing"
	"time"
)

// Acceptance for #2681: a site environment's tile shows the site as its
// address serves it. A single-page application whose shell is an empty root
// and a module script, which imports React from a public CDN and renders
// only after fetching a data file from its own release once the page has
// loaded, is drawn painted, not blank: the CDN is fetched, the rewrite in the
// release's _redirects answers its routes, and the capture waits for the page
// to go quiet.
//
// Wire forms: manage_site takes command, site and files ([{path, content}] of
// strings) as literal tools/call params; the tile is read with a GET.

const app2681 = `import React from "https://cdn.jsdelivr.net/npm/react@18.3.1/+esm";
import { createRoot } from "https://cdn.jsdelivr.net/npm/react-dom@18.3.1/client/+esm";
const data = await (await fetch("/data/colors.json")).json();
createRoot(document.getElementById("root")).render(
  React.createElement("div", { style: { background: data.color, position: "fixed", inset: 0 } }, "painted"));
`

func TestIssue2681_AnApplicationOnACDNIsDrawnPainted(t *testing.T) {
	admin := connectAs(t, keyAdmin2604)
	s := newSite2604(t, keyAdmin2604, "acc2681-")
	admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": "spa", "files": []map[string]any{
		{"path": "index.html", "content": `<!doctype html><html><body style="margin:0;background:#fff"><div id="root"></div><script type="module" src="/app.js"></script></body></html>`},
		{"path": "app.js", "content": app2681},
		{"path": "data/colors.json", "content": `{"color": "rgb(255, 0, 0)"}`},
		{"path": "_redirects", "content": "/* /index.html 200\n"},
	}})

	var shot []byte
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(admin.ctx, http.MethodGet, admin.base+portal2559+"/"+s.name+"/environments/dev/tile", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+admin.apiKey)
		res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				shot = body
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if shot == nil {
		t.Fatal("the environment had no tile within three minutes")
	}
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatalf("the tile is not a PNG: %v", err)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	if r>>8 < 200 || g>>8 > 60 || bl>>8 > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); the application paints it red once React from the CDN renders the data it fetched", r>>8, g>>8, bl>>8)
	}
}
