//go:build integration

package tilepage

import (
	"context"
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

// releasePage is a release holding index and one image, drawn as a site tile.
func releasePage(t *testing.T, index string) headless.Page {
	t.Helper()
	files := map[string]site.File{
		"index.html": {Path: "index.html", ContentType: "text/html"},
		"in.png":     {Path: "in.png", ContentType: "image/png"},
		"below.png":  {Path: "below.png", ContentType: "image/png"},
	}
	page, err := pageOf(context.Background(), files, func(_ context.Context, f site.File) ([]byte, error) {
		if f.Path == "index.html" {
			return []byte(index), nil
		}
		return pixel, nil
	})
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
