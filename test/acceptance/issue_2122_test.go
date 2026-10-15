//go:build integration

package acceptance

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"image/color"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2122: a tile is captured once the document's styles are
// in. Each fixture styles its whole page teal and nothing else; a page drawn
// before its styles applied is white. Every fixture is drawn by the real tile
// worker and renderer, asked for again through the Thumbnail panel's route
// (DELETE .../thumbnail) between draws, and every stored tile is held to the
// stylesheet's color.
//
// Wire forms: save_asset's content and content_type are strings and its
// references a list of strings; manage_resource's content_base64 and
// content_type are strings. Each is sent as literal params.

// draws2122 is how many times each fixture is drawn.
const draws2122 = 20

var teal2122 = color.RGBA{R: 0, G: 128, B: 128, A: 255}

// assertStyled2122 holds the asset's stored tile to the stylesheet's color.
func assertStyled2122(t *testing.T, c *client, assetID string, draw int) {
	t.Helper()
	img := tile1794(t, c, "/api/v1/portal/assets/"+assetID+"/thumbnail")
	if teal := share1794(img, teal2122, 24); teal < 0.6 {
		t.Fatalf("draw %d: the stored tile is %.0f%% the stylesheet's color: it was captured before its styles applied", draw, 100*teal)
	}
}

// drawRepeatedly2122 draws the asset draws2122 times, asking for the tile
// again between draws, and checks each stored tile.
func drawRepeatedly2122(t *testing.T, c *client, assetID string) {
	t.Helper()
	for draw := 1; draw <= draws2122; draw++ {
		if draw > 1 {
			if status, body := c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID+"/thumbnail", http.NoBody); status != http.StatusOK {
				t.Fatalf("asking for the tile again: status %d: %v", status, body)
			}
		}
		awaitAssetTile1794(t, c, assetID)
		assertStyled2122(t, c, assetID, draw)
	}
}

// save2122 saves an asset and deletes it when the test ends.
func save2122(t *testing.T, c *client, contentType, content string, references []any) string {
	t.Helper()
	args := map[string]any{
		"name": "acceptance-2122-" + unique1794(), "content": content, "content_type": contentType,
		"description": "Acceptance #2122: a page whose styles arrive late.",
	}
	if references != nil {
		args["references"] = references
	}
	saved := c.call("save_asset", args)
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// TestIssue2122_AStylesheetAddedAfterLoadIsInEveryTile: a script adds the
// page's stylesheet, a declared managed resource, one second after load.
func TestIssue2122_AStylesheetAddedAfterLoadIsInEveryTile(t *testing.T) {
	c := connectFor(t, draws2122*tileWait1794)
	name := "acceptance-2122-css-" + unique1794()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": name + ".css", "display_name": name, "path": "acceptance-2122",
		"description":    "Acceptance #2122: a stylesheet a page adds after load.",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("html, body { margin: 0; height: 100%; background: rgb(0,128,128); }")),
		"content_type":   "text/css",
	})
	resourceID, _ := out["resource_id"].(string)
	if resourceID == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })
	_, row := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID, http.NoBody)
	uri, _ := row["uri"].(string)
	if uri == "" {
		t.Fatalf("the stylesheet resource carries no uri: %v", row)
	}
	doc := `<!DOCTYPE html><html><head><title>late</title></head><body><h1>Quarterly report</h1>
<script>
window.addEventListener("load", function () {
  setTimeout(function () {
    var l = document.createElement("link");
    l.rel = "stylesheet";
    l.href = "` + uri + `";
    document.head.appendChild(l);
  }, 1000);
});
</script></body></html>`
	drawRepeatedly2122(t, c, save2122(t, c, "text/html", doc, []any{uri}))
}

// TestIssue2122_ATailwindPlayCDNPageIsStyledInEveryTile: the Tailwind Play
// CDN script writes the page's CSS only after it sees the DOM.
func TestIssue2122_ATailwindPlayCDNPageIsStyledInEveryTile(t *testing.T) {
	c := connectFor(t, draws2122*tileWait1794)
	doc := `<!DOCTYPE html><html><head><script src="https://cdn.tailwindcss.com"></script></head>
<body class="m-0 h-screen bg-[rgb(0,128,128)]"><div class="p-8 text-4xl font-bold text-white">Quarterly report</div></body></html>`
	drawRepeatedly2122(t, c, save2122(t, c, "text/html", doc, nil))
}

// TestIssue2122_AJSXPageWhoseModulesArriveLateIsStyledInEveryTile: the
// component's module finishes evaluating three seconds after the frame
// parses (a top-level await before it renders), past the fixed two seconds
// the tile used to wait before reporting ready.
func TestIssue2122_AJSXPageWhoseModulesArriveLateIsStyledInEveryTile(t *testing.T) {
	c := connectFor(t, draws2122*tileWait1794)
	jsx := `import { useState } from "react";

await new Promise((resolve) => setTimeout(resolve, 3000));

export default function App() {
  const [title] = useState("Quarterly report");
  return (
    <div style={{ background: "rgb(0,128,128)", width: "100vw", height: "100vh", margin: 0 }}>
      <h1 style={{ margin: 0, color: "white" }}>{title}</h1>
    </div>
  );
}
`
	drawRepeatedly2122(t, c, save2122(t, c, "text/jsx", jsx, nil))
}

// cdn2122 is a stylesheet host the test controls: it answers 503 until it
// is told to serve, counting every request, and serves a font path that
// always answers 503.
type cdn2122 struct {
	ln    net.Listener
	mu    sync.Mutex
	serve bool
	hits  []int
	ready chan struct{}
}

func startCDN2122(t *testing.T) *cdn2122 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cdn := &cdn2122{ln: ln, ready: make(chan struct{}, 16)}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(cdn.handle)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return cdn
}

func (cdn *cdn2122) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, ".woff2") {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	cdn.mu.Lock()
	serve := cdn.serve
	if serve {
		cdn.hits = append(cdn.hits, http.StatusOK)
	} else {
		cdn.hits = append(cdn.hits, http.StatusServiceUnavailable)
	}
	cdn.mu.Unlock()
	select {
	case cdn.ready <- struct{}{}:
	default:
	}
	if !serve {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/css")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "html, body { margin: 0; height: 100%; background: rgb(0,128,128); }")
}

func (cdn *cdn2122) url(path string) string {
	return "http://" + cdn.ln.Addr().String() + path
}

func (cdn *cdn2122) answer() {
	cdn.mu.Lock()
	cdn.serve = true
	cdn.mu.Unlock()
}

func (cdn *cdn2122) answered() []int {
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	return append([]int(nil), cdn.hits...)
}

// subresourceFailures2122 sums thumbnail_render_failures_total with reason
// subresource over the replicas' metrics endpoints.
func subresourceFailures2122() float64 {
	var sum float64
	for _, addr := range []string{"http://localhost:9464/metrics", "http://localhost:9465/metrics"} {
		res, err := http.Get(addr) // #nosec G107 -- the dev stack's metrics
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "thumbnail_render_failures_total{") || !strings.Contains(line, `reason="subresource"`) {
				continue
			}
			fields := strings.Fields(line)
			if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
				sum += v
			}
		}
	}
	return sum
}

// TestIssue2122_AStylesheetTheRendererCannotFetchIsRetriedNotStored: the
// page's stylesheet host answers 503. The attempt stores no tile and records
// no failure, it is counted as an attempt that did not finish, and once the
// host answers the page is drawn styled, with no one touching the asset.
func TestIssue2122_AStylesheetTheRendererCannotFetchIsRetriedNotStored(t *testing.T) {
	c := connectFor(t, 2*tileWait1794)
	cdn := startCDN2122(t)
	before := subresourceFailures2122()
	doc := `<!DOCTYPE html><html><head><link rel="stylesheet" href="` + cdn.url("/report.css") + `"></head>
<body><h1>Quarterly report</h1></body></html>`
	assetID := save2122(t, c, "text/html", doc, nil)

	select {
	case <-cdn.ready:
	case <-time.After(tileWait1794):
		t.Fatal("the renderer never asked for the stylesheet")
	}
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	defer func() { _ = db.Close() }()
	waitFor(t, "the attempt held back without a tile or a failure", func() (bool, string) {
		var (
			version  int
			failure  sql.NullString
			attempts int
			key      sql.NullString
		)
		if err := db.QueryRowContext(c.ctx, `SELECT thumbnail_version, thumbnail_failure, thumbnail_attempts, thumbnail_s3_key
			FROM portal_assets WHERE id = $1`, assetID).Scan(&version, &failure, &attempts, &key); err != nil {
			return false, err.Error()
		}
		if failure.String != "" {
			t.Fatalf("the missing stylesheet was recorded as the document's failure: %q", failure.String)
		}
		if key.String != "" || version != 0 {
			t.Fatalf("a tile was stored without its stylesheet (version %d, key %q)", version, key.String)
		}
		return attempts >= 1, fmt.Sprintf("thumbnail_attempts %d", attempts)
	})

	cdn.answer()
	awaitAssetTile1794(t, c, assetID)
	assertStyled2122(t, c, assetID, 1)
	hits := cdn.answered()
	if len(hits) < 2 || hits[0] != http.StatusServiceUnavailable || hits[len(hits)-1] != http.StatusOK {
		t.Fatalf("the stylesheet host answered %v, want a 503 then a 200", hits)
	}
	if after := subresourceFailures2122(); after <= before {
		t.Errorf("thumbnail_render_failures_total{reason=\"subresource\"} went from %v to %v", before, after)
	}
}

// TestIssue2122_AFailedFontIsStillDrawnInItsFallbackFace: a page whose font
// host answers 503 is still stored, in its fallback face, as #2062 decided,
// every time it is drawn.
func TestIssue2122_AFailedFontIsStillDrawnInItsFallbackFace(t *testing.T) {
	c := connectFor(t, draws2122*tileWait1794)
	cdn := startCDN2122(t)
	doc := `<!DOCTYPE html><html><head><style>
@font-face { font-family: "Brand2122"; src: url("` + cdn.url("/brand.woff2") + `") format("woff2"); }
html, body { margin: 0; height: 100%; background: rgb(0,128,128); font-family: "Brand2122", Arial, sans-serif; }
</style></head><body><h1 style="color:white">Quarterly report</h1></body></html>`
	drawRepeatedly2122(t, c, save2122(t, c, "text/html", doc, nil))
}
