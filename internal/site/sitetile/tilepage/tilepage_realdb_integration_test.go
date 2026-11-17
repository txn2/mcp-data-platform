//go:build integration

package tilepage

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/headless/headlesstest"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// The RealDB suffix is the selector the Docker lane runs integration-tagged
// tests by; this needs only the pinned renderer (testcontainers).

// pixel is a 1x1 PNG.
var pixel = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// releasePage is a release holding index and two images, drawn as a site
// tile through the fake gateway.
func releasePage(t *testing.T, index string) headless.Page {
	t.Helper()
	g := &gateway{paths: map[string]answer{
		"/":          {status: http.StatusOK, ctype: "text/html", body: index},
		"/in.png":    {status: http.StatusOK, ctype: "image/png", body: string(pixel)},
		"/below.png": {status: http.StatusOK, ctype: "image/png", body: string(pixel)},
	}}
	page, err := Release{Store: releases{}, Server: g}.Page(context.Background(), "s1", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// TestSiteTileWaitsOnlyForTheFrame_RealDB pins #2677 in the pinned renderer:
// a page whose loading="lazy" images sit below the 960px fold is drawn well
// inside the render deadline, an image in the frame (lazy or not) is waited
// on and drawn, and a page with no image draws as before.
func TestSiteTileWaitsOnlyForTheFrame_RealDB(t *testing.T) {
	r := headless.New(headlesstest.Start(t), nil)
	for name, index := range map[string]string{
		"lazy images below the fold": `<!doctype html><body style="margin:0">
<h1>Marketing</h1><img src="in.png" loading="lazy" width="200" height="200">
<div style="height:3000px"></div>
<img src="below.png" loading="lazy" width="200" height="200">
<section style="content-visibility:auto"><img src="below.png" loading="lazy"></section>
<div style="position:absolute;left:-5000px"><img src="below.png" loading="lazy" width="10" height="10"></div>
</body>`,
		"no image": `<!doctype html><body><h1>Plain</h1></body>`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			shot, err := r.Render(ctx, releasePage(t, index))
			if err != nil {
				t.Fatalf("drawing the tile: %v (after %s)", err, time.Since(start))
			}
			if len(shot) == 0 {
				t.Fatal("an empty tile")
			}
		})
	}
}

// slowGateway answers /data.json after a pause, as an upstream would.
type slowGateway struct {
	*gateway
	pause time.Duration
}

func (g slowGateway) Tile(w http.ResponseWriter, r *http.Request, route *site.Route) {
	if r.URL.Path == "/data.json" {
		time.Sleep(g.pause)
	}
	g.gateway.Tile(w, r, route)
}

// TestSiteTileDrawsAnAppThatFetchesAfterLoad_RealDB pins #2681 in the pinned
// renderer: a module application loaded from a public host (a CDN) that
// fetches its data from its own origin after the load event and paints from
// it is captured painted, not blank. Before, the CDN was refused and the
// capture was taken at the load event.
func TestSiteTileDrawsAnAppThatFetchesAfterLoad_RealDB(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write([]byte(`export async function paint(){
  const r = await fetch("/data.json");
  const d = await r.json();
  document.body.style.background = d.color;
}`))
	}))
	defer cdn.Close()
	g := slowGateway{gateway: &gateway{paths: map[string]answer{
		"/": {status: http.StatusOK, ctype: "text/html", body: `<!doctype html><html><body style="margin:0;background:#fff;height:100vh">
<script type="module">import { paint } from "` + cdn.URL + `/app.js"; paint();</script></body></html>`},
		"/data.json": {status: http.StatusOK, ctype: "application/json", body: `{"color":"rgb(255, 0, 0)"}`},
	}}, pause: 300 * time.Millisecond}
	page, err := Release{Store: releases{}, Server: g}.Page(context.Background(), "s1", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	page.RequireSubresources = false
	r := headless.New(headlesstest.Start(t), cdn.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shot, err := r.Render(ctx, page)
	if err != nil {
		t.Fatalf("drawing the tile: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	cr, cg, cb, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	if cr>>8 < 200 || cg>>8 > 60 || cb>>8 > 60 {
		t.Fatalf("the tile's centre is rgb(%d,%d,%d); the application painted it red after fetching its data", cr>>8, cg>>8, cb>>8)
	}
}
