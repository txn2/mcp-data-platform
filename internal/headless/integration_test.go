//go:build integration

package headless

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
)

// rendererImage is the renderer the platform documents, pinned by digest.
const rendererImage = "chromedp/headless-shell@sha256:2d349b544a1ea6b5b5fd7c0fe99215ff662339c57407ee2e8c0a11af93516b04"

// protectionOff switches off the browser's own private-network protection.
// With it on, the browser refuses a request to a private address by itself,
// so a test of the platform's layers would pass whether the layers worked or
// not. The control renderer runs with it off, so what stops a request there is
// the platform and nothing else.
var protectionOff = []string{"--disable-features=" + strings.Join([]string{
	"LocalNetworkAccessChecks", "LocalNetworkAccessChecksWebSockets", "LocalNetworkAccessChecksWebRTC",
	"BlockInsecurePrivateNetworkRequests", "PrivateNetworkAccessSendPreflights",
	"PrivateNetworkAccessRespectPreflightResults", "PrivateNetworkAccessForWorkers",
	"PrivateNetworkAccessForIframes", "PrivateNetworkAccessForNavigations",
}, ",")}

// startRenderer runs the renderer image with extra flags and returns its
// DevTools address. hostPorts are ports on this machine the renderer may reach
// at testcontainers.HostInternal.
func startRenderer(t *testing.T, flags []string, hostPorts []int) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:           rendererImage,
			ExposedPorts:    []string{"9222/tcp"},
			Cmd:             flags,
			HostAccessPorts: hostPorts,
			WaitingFor:      wait.ForHTTP("/json/version").WithPort("9222/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting the renderer: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	endpoint, err := c.PortEndpoint(ctx, "9222/tcp", "http")
	if err != nil {
		t.Fatalf("renderer endpoint: %v", err)
	}
	return endpoint
}

// probe records every request that reaches it. It stands in for a service
// inside the network perimeter.
type probe struct {
	mu   sync.Mutex
	hits []string
	port int
}

func startProbe(t *testing.T) *probe {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("starting the probe: %v", err)
	}
	p := &probe{port: ln.Addr().(*net.TCPAddr).Port}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits = append(p.hits, r.URL.Path)
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return p
}

func (p *probe) marked(marker string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, h := range p.hits {
		if strings.Contains(h, marker) {
			out = append(out, h)
		}
	}
	return out
}

// settled resolves once the page has loaded and every request it starts on
// load has had time to be made.
const settled = `new Promise(function(r){function go(){setTimeout(function(){r("")},2000)}if(document.readyState==="complete"){go()}else{addEventListener("load",go)}})`

func tilePage(doc string) Page {
	return Page{Document: []byte(doc), Ready: settled, Width: 1280, Height: 960, Scale: 0.625}
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the screenshot is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 600 {
		t.Fatalf("the screenshot is %dx%d, want 800x600", b.Dx(), b.Dy())
	}
	return img
}

func share(img image.Image, r image.Rectangle, target color.RGBA, tolerance float64) float64 {
	r = r.Intersect(img.Bounds())
	total, hit := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			total++
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if math.Abs(float64(cr>>8)-float64(target.R)) <= tolerance &&
				math.Abs(float64(cg>>8)-float64(target.G)) <= tolerance &&
				math.Abs(float64(cb>>8)-float64(target.B)) <= tolerance {
				hit++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

// transformedSlide lays its slide out the way the served slide runtime does:
// a 960x700 box at the centre of the page, scaled by a CSS transform.
// html2canvas drew an empty field for exactly this.
const transformedSlide = `<!DOCTYPE html><html><head><style>
html,body{margin:0;width:1280px;height:960px;background:#191919;overflow:hidden}
.slides{position:absolute;left:640px;top:480px;width:960px;height:700px;
  transform:matrix(1.28,0,0,1.28,-480,-350);display:flex;align-items:center;justify-content:center}
h1{margin:0;color:#ffcc00;font:bold 120px sans-serif}
</style></head><body><div class="slides"><h1>Quarterly</h1></div></body></html>`

func TestRendererIntegration_DrawsATransformedSlide_RealDB(t *testing.T) {
	r := New(startRenderer(t, nil, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, err := r.Render(ctx, tilePage(transformedSlide))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decode(t, data)
	if got := share(img, image.Rect(132, 100, 668, 500), color.RGBA{R: 0xff, G: 0xcc}, 40); got < 0.04 {
		t.Fatalf("the slide's title covers %.1f%% of the middle of the tile: the transformed slide was not drawn", 100*got)
	}
}

// framedDocument is how the tile page draws an HTML document: in a frame of
// its own, taller than the viewport. Its stylesheet answers the color scheme.
const framedDocument = `<!DOCTYPE html><html><head><style>
html,body{margin:0;overflow:hidden}iframe{display:block;width:100vw;height:100vh;border:0}
</style></head><body><iframe srcdoc="<!DOCTYPE html><style>
body{margin:0;height:4000px;background:#f6f7f9}
@media (prefers-color-scheme: dark){body{background:#0d1117}}
p{font:15px sans-serif;margin:24px}</style><p>Weather watch</p>"></iframe></body></html>`

// The scheme the renderer emulates reaches a document drawn in a frame, which
// is how the tile page draws HTML and JSX, and a document taller than the
// frame puts no scrollbar in the picture (#1789).
func TestRendererIntegration_AFramedDocumentAnswersTheSchemeWithoutAScrollbar_RealDB(t *testing.T) {
	r := New(startRenderer(t, nil, nil), nil)
	for _, tc := range []struct {
		dark bool
		bg   color.RGBA
	}{
		{false, color.RGBA{R: 0xf6, G: 0xf7, B: 0xf9}},
		{true, color.RGBA{R: 0x0d, G: 0x11, B: 0x17}},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		page := tilePage(framedDocument)
		page.Dark = tc.dark
		data, err := r.Render(ctx, page)
		cancel()
		if err != nil {
			t.Fatalf("Render dark=%v: %v", tc.dark, err)
		}
		img := decode(t, data)
		if got := share(img, image.Rect(0, 100, 800, 600), tc.bg, 6); got < 0.98 {
			t.Errorf("dark=%v: the document's background covers %.1f%% of the tile", tc.dark, 100*got)
		}
		// A scrollbar is drawn down the right edge of the frame.
		if got := share(img, image.Rect(770, 0, 800, 600), tc.bg, 6); got < 0.98 {
			t.Errorf("dark=%v: the right edge is %.1f%% the document's background: a scrollbar was drawn", tc.dark, 100*got)
		}
	}
}

const insetShadow = `<!DOCTYPE html><html><head><style>
html,body{margin:0;background:#fff}
.best{height:480px;background:#FBE7F3;box-shadow:inset 0 -2px 0 #DC108A}
</style></head><body><div class="best"></div></body></html>`

func TestRendererIntegration_AnInsetShadowIsAHairlineNotAFill_RealDB(t *testing.T) {
	r := New(startRenderer(t, nil, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, err := r.Render(ctx, tilePage(insetShadow))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decode(t, data)
	interior := image.Rect(0, 30, 800, 270)
	if got := share(img, interior, color.RGBA{R: 0xDC, G: 0x10, B: 0x8A}, 40); got > 0.01 {
		t.Fatalf("%.1f%% of the block's interior is the rule's color: the inset shadow was painted as a fill", 100*got)
	}
	if got := share(img, interior, color.RGBA{R: 0xFB, G: 0xE7, B: 0xF3}, 8); got < 0.95 {
		t.Fatalf("the wash covers %.1f%% of the block's interior, want nearly all of it", 100*got)
	}
}

func TestRendererIntegration_ServesItsOwnFilesAndAnswersTheRest404_RealDB(t *testing.T) {
	r := New(startRenderer(t, nil, nil), nil)
	page := tilePage(`<!DOCTYPE html><html><head><link rel="stylesheet" href="/theme.css"></head><body></body></html>`)
	page.Files = func(path string) (File, bool) {
		if path == "/theme.css" {
			return File{Body: []byte("html,body{margin:0;height:100%;background:#16a34a}"), ContentType: "text/css"}, true
		}
		return File{}, false
	}
	page.Ready = `fetch("/nothing-here").then(function(res){return res.status===404?"":"a file the page does not have answered "+res.status})`
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, err := r.Render(ctx, page)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := share(decode(t, data), image.Rect(0, 0, 800, 600), color.RGBA{R: 0x16, G: 0xa3, B: 0x4a}, 12); got < 0.95 {
		t.Fatalf("the page's own stylesheet covers %.1f%% of the tile: it was not served", 100*got)
	}
}

func TestRendererIntegration_APageThatSaysItCannotBeDrawnFails_RealDB(t *testing.T) {
	r := New(startRenderer(t, nil, nil), nil)
	page := tilePage(`<!DOCTYPE html><p>x</p>`)
	page.Ready = `Promise.resolve("a referenced file did not load")`
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := r.Render(ctx, page)
	if err == nil || !strings.Contains(err.Error(), "a referenced file did not load") {
		t.Fatalf("Render = %v, want the page's own reason", err)
	}
}

func TestRendererIntegration_AHungDocumentIsAbandonedAtTheDeadline_RealDB(t *testing.T) {
	r := New(startRenderer(t, nil, nil), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	_, err := r.Render(ctx, tilePage(`<!DOCTYPE html><script>while(true){}</script>`))
	if err == nil {
		t.Fatal("a document whose script never returns was drawn")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second+disposeTimeout+2*time.Second {
		t.Fatalf("Render returned after %s; it must give up at its deadline", elapsed)
	}
}

// hostile makes a request to the probe in every way a page can: an image,
// fetch, a WebSocket, a beacon, a dedicated worker's fetch and WebSocket, a
// sandboxed frame's fetch and WebSocket, and a freshly created frame's own
// fetch and WebSocket, which is how a document would recover a constructor the
// scrub removed. It also reaches for the renderer's own DevTools port on
// loopback. Every path carries marker.
func hostile(host string, port int, marker string) string {
	at := fmt.Sprintf("http://%s:%d/%s", host, port, marker)
	ws := strings.Replace(at, "http://", "ws://", 1)
	worker := fmt.Sprintf(`fetch('%s-worker-fetch').catch(function(){});try{new WebSocket('%s-worker-ws')}catch(e){}`, at, ws)
	frame := fmt.Sprintf(`<script>fetch('%s-frame-fetch').catch(function(){});try{new WebSocket('%s-frame-ws')}catch(e){}</script>`, at, ws)
	return `<!DOCTYPE html><html><body style="margin:0;background:#1d4ed8;height:100vh">
<img src="` + at + `-img">
<iframe sandbox="allow-scripts" srcdoc="` + strings.ReplaceAll(frame, `"`, "&quot;") + `"></iframe>
<script>
fetch("` + at + `-fetch").catch(function(){});
try { new WebSocket("` + ws + `-ws"); } catch (e) {}
try { navigator.sendBeacon("` + at + `-beacon"); } catch (e) {}
try { new Worker(URL.createObjectURL(new Blob([` + "`" + worker + "`" + `]))); } catch (e) {}
try { var f = document.createElement("iframe"); document.body.appendChild(f);
      f.contentWindow.fetch("` + at + `-realm-fetch").catch(function(){});
      new f.contentWindow.WebSocket("` + ws + `-realm-ws"); } catch (e) {}
try { new WebSocket("ws://127.0.0.1:9222/devtools/browser"); } catch (e) {}
fetch("http://127.0.0.1:9222/json/version").catch(function(){});
</script></body></html>`
}

// renderUnarmed loads doc in a plain page with none of the platform's layers:
// the positive control. If the probe hears nothing from this, it cannot hear
// an escape either, and every negative result below would mean nothing.
func renderUnarmed(t *testing.T, endpoint, doc string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.close()
	var tgt struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &tgt); err != nil {
		t.Fatalf("createTarget: %v", err)
	}
	defer func() {
		_ = c.call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": tgt.TargetID}, nil)
	}()
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": tgt.TargetID, "flatten": true}, &att); err != nil {
		t.Fatalf("attachToTarget: %v", err)
	}
	url := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(doc))
	if err := c.call(ctx, att.SessionID, "Page.navigate", map[string]any{"url": url}, nil); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	time.Sleep(4 * time.Second)
}

// TestRendererIntegration_NoLayerLetsADocumentReachTheNetwork_RealDB is the security
// property, executed against a renderer with the browser's own protection
// switched off so that only the platform stands in the way. It fails unless
// the unarmed control reaches the probe, and then requires that no request
// reaches it with every layer on, and with each layer that can be switched off
// switched off in turn.
func TestRendererIntegration_NoLayerLetsADocumentReachTheNetwork_RealDB(t *testing.T) {
	p := startProbe(t)
	endpoint := startRenderer(t, protectionOff, []int{p.port})
	guard, err := egressguard.New(nil)
	if err != nil {
		t.Fatalf("egressguard: %v", err)
	}
	public := &http.Client{Transport: guard.Transport(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	renderUnarmed(t, endpoint, hostile(testcontainers.HostInternal, p.port, "control"))
	if hits := p.marked("control"); len(hits) == 0 {
		t.Fatal("the unarmed control never reached the probe, so this test cannot tell an escape from silence")
	} else {
		t.Logf("the unarmed control reached the probe %d times: %v", len(hits), hits)
	}

	for _, tc := range []struct {
		name         string
		withoutProxy bool
		withoutScrub bool
	}{
		{name: "every-layer"},
		{name: "proxy-and-interception", withoutScrub: true},
		{name: "scrub-and-interception", withoutProxy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(endpoint, public)
			r.withoutProxy, r.withoutScrub = tc.withoutProxy, tc.withoutScrub
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			data, err := r.Render(ctx, tilePage(hostile(testcontainers.HostInternal, p.port, tc.name)))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got := share(decode(t, data), image.Rect(0, 0, 800, 600), color.RGBA{R: 0x1d, G: 0x4e, B: 0xd8}, 12); got < 0.5 {
				t.Fatalf("the document itself covers %.1f%% of the tile: it was not drawn, so its requests were not tried", 100*got)
			}
			time.Sleep(time.Second)
			if hits := p.marked(tc.name); len(hits) != 0 {
				t.Fatalf("the document reached the network: %v", hits)
			}
		})
	}
}
