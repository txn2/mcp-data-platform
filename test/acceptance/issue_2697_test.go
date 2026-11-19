//go:build integration

package acceptance

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2697: a raster image was handed to the thumbnail renderer
// as stored, and the browser decoded it at its natural size, so one large
// attachment took the renderer past its memory limit on every attempt and
// stopped every other tile on the replica. A raster image larger than a tile
// is now reduced by the platform before the renderer sees it, and one whose
// header declares more pixels than the platform decodes is recorded as not
// drawable with its size named, without reaching the renderer at all.
//
// Both images are one color, so each is a small file declaring many pixels:
// the size that matters to a decoder, held under the source bound.
//
// Wire forms: the resource upload route takes a multipart form of strings and
// one file part; GET /api/v1/resources/{id} takes none. No tool parameter is
// touched.

// solid2697 is a w x h image of one color in model, as a PNG.
type solid2697 struct {
	w, h  int
	model color.Model
	c     color.Color
}

func (s solid2697) ColorModel() color.Model { return s.model }
func (s solid2697) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s solid2697) At(int, int) color.Color { return s.c }

func pngOf2697(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	return b.Bytes()
}

// TestIssue2697_ALargeImageIsDrawnAndTheRendererKeepsDrawing: a 12-megapixel
// PNG gets its tile, an image declaring 12000x12000 pixels (137 MiB of 8-bit
// gray decoded, past the 96 MiB bound) is recorded as not drawable naming its
// size, and a document filed after both is drawn, so the
// renderer kept answering throughout.
func TestIssue2697_ALargeImageIsDrawnAndTheRendererKeepsDrawing(t *testing.T) {
	c := connectFor(t, 4*tileWait1787)
	stamp := unique1568()

	large := pngOf2697(t, solid2697{w: 4000, h: 3000, model: color.NRGBAModel, c: color.NRGBA{R: 30, G: 120, B: 200, A: 255}})
	largeID, mime := uploadResource1568(t, c, "photo-"+stamp+".png", "image/png", large)
	if mime != "image/png" {
		t.Fatalf("the PNG stored as %q", mime)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+largeID, http.NoBody) })

	huge := pngOf2697(t, solid2697{w: 12000, h: 12000, model: color.GrayModel, c: color.Gray{Y: 200}})
	hugeID, _ := uploadResource1568(t, c, "scan-"+stamp+".png", "image/png", huge)
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+hugeID, http.NoBody) })

	row := awaitResourceTile1787(t, c, largeID, false)
	if key, _ := row["thumbnail_s3_key"].(string); key == "" {
		t.Errorf("the 4000x3000 image has no tile: %v", row)
	}

	deadline := time.Now().Add(tileWait1787)
	for {
		hugeRow := resourceRow1868(t, c, hugeID)
		failure, _ := hugeRow["thumbnail_failure"].(string)
		if strings.Contains(failure, "12000x12000") && strings.Contains(failure, "too large to draw a tile from") {
			break
		}
		if key, _ := hugeRow["thumbnail_s3_key"].(string); failure != "" || key != "" || time.Now().After(deadline) {
			t.Fatalf("the 12000x12000 image recorded failure %q and tile %q; want it refused naming its size", failure, key)
		}
		time.Sleep(time.Second)
	}

	afterID, _ := createResource1568(t, c, "after-"+stamp+".txt", "text/plain", "Drawn after both images.\n")
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+afterID, http.NoBody) })
	awaitResourceTile1787(t, c, afterID, true)
}
