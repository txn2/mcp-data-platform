//go:build integration

package thumbworker

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
	"github.com/txn2/mcp-data-platform/internal/headless"
)

// The tile page is the only place the PDF family is drawn, and the thing that
// draws it -- pdf.js rasterizing page one onto a canvas -- runs in the
// renderer and nowhere else (#1794). A unit test of the dispatch proves the
// page reaches PdfTile; it cannot prove that pdf.js paints in
// chromedp/headless-shell, which ships no PDF plugin and is the reason the
// family was excluded in the first place.
//
// So this draws a real PDF through the real tile page, with the real embedded
// chunks, in the pinned renderer, and asks the only question that matters:
// are there marks on it.
//
// The names carry the RealDB suffix because that is the selector `make
// test-realdb` runs integration-tagged tests by, and a test CI never runs is a
// test that did not run (test/structure/integration_guard_test.go). These need
// only testcontainers, which that lane has.

// rendererImage is the renderer the platform documents, pinned by digest, as
// internal/headless's own integration tests pin it.
const rendererImage = "chromedp/headless-shell@sha256:2d349b544a1ea6b5b5fd7c0fe99215ff662339c57407ee2e8c0a11af93516b04"

func startRenderer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        rendererImage,
			ExposedPorts: []string{"9222/tcp"},
			WaitingFor:   wait.ForHTTP("/json/version").WithPort("9222/tcp").WithStartupTimeout(60 * time.Second),
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

// pdfWorker is a worker wired with just enough to build a tile page: the
// embedded tile entry and stylesheet, and the viewer's chunks as the routes
// the page loads them from.
func pdfWorker() *Worker {
	return &Worker{deps: Deps{
		TileEntryURL: contentviewer.TileEntryURL(),
		TileCSS:      contentviewer.CSS,
		Routes:       contentviewer.Handler(),
	}}
}

func readTestPDF(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return body
}

// TestPDFTileIsDrawnInTheRenderer_RealDB is the ticket's gate: a PDF drawn through
// the real page in the pinned image, checked for marks rather than for a
// particular picture. A blank tile is what the family had before, and it is
// what every way this can fail produces.
func TestPDFTileIsDrawnInTheRenderer_RealDB(t *testing.T) {
	if contentviewer.TileEntryURL() == "" {
		t.Skip("no viewer bundle embedded; run make content-viewer-embed")
	}
	r := headless.New(startRenderer(t), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	shot, err := r.Render(ctx, pdfWorker().tilePage(tileSource{
		contentType: "application/pdf",
		content:     readTestPDF(t, "marked.pdf"),
		name:        "marked.pdf",
	}))
	if err != nil {
		t.Fatalf("drawing the PDF tile: %v", err)
	}
	img := decodePNG(t, shot)
	if got, want := img.Bounds().Dx(), tileWidth*int(tileScale); got != want {
		t.Errorf("tile is %d px wide, want %d", got, want)
	}
	if inked := inkedFraction(img); inked < 0.01 {
		t.Errorf("the tile is blank: %.4f of its pixels differ from the page, want at least 0.01", inked)
	}
}

// TestPDFTileRefusesADocumentItCannotRead_RealDB holds the other half of the
// contract: a file that is not a PDF settles with a reason rather than
// hanging until the render's deadline, which is what would hold the lease and
// re-offer the row forever.
func TestPDFTileRefusesADocumentItCannotRead_RealDB(t *testing.T) {
	if contentviewer.TileEntryURL() == "" {
		t.Skip("no viewer bundle embedded; run make content-viewer-embed")
	}
	r := headless.New(startRenderer(t), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, err := r.Render(ctx, pdfWorker().tilePage(tileSource{
		contentType: "application/pdf",
		content:     []byte("this is not a PDF at all"),
		name:        "broken.pdf",
	}))
	if err == nil {
		t.Fatal("a file that is not a PDF was drawn; want the render to report why it could not be")
	}
	if ctx.Err() != nil {
		t.Fatalf("the page never settled; it was abandoned at the deadline: %v", err)
	}
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the screenshot is not a PNG: %v", err)
	}
	return img
}

// inkedFraction is the share of pixels that differ appreciably from the most
// common color in the image. A drawn page is mostly its own background with
// marks on it; a page that failed to draw is that background and nothing else.
func inkedFraction(img image.Image) float64 {
	counts := map[uint32]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			counts[quantize(img.At(x, y).RGBA())]++
		}
	}
	var background, total int
	for _, n := range counts {
		total += n
		if n > background {
			background = n
		}
	}
	if total == 0 {
		return 0
	}
	return float64(total-background) / float64(total)
}

// quantize folds a color to 5 bits per channel, so the resampling filter's
// near-background pixels count as background rather than as marks.
func quantize(r, g, b, _ uint32) uint32 {
	return (r>>11)<<10 | (g>>11)<<5 | (b >> 11)
}
