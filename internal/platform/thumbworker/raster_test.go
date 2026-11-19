package thumbworker

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: 200})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngHeaderOf is a PNG whose header declares w x h pixels of the given bit
// depth and color type and which holds no pixel data: what an image too
// large to decode is checked by, without the test decoding one.
func pngHeaderOf(w, h uint32, depth, colorType byte) []byte {
	var b bytes.Buffer
	_, _ = b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = depth, colorType
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	chunk := append([]byte("IHDR"), ihdr...)
	_, _ = b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

// A raster image larger than the stored tile reaches the tile page as a PNG
// reduced to fit it, proportions kept (#2697).
func TestReducedRaster_ALargeImageIsReducedToTheTile(t *testing.T) {
	src := tileSource{contentType: "image/png", content: pngOf(t, 2000, 500), name: "wide.png"}
	out, why := reducedRaster(context.Background(), src)
	if why != "" {
		t.Fatalf("refused: %s", why)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out.content))
	if err != nil || format != "png" || out.contentType != "image/png" {
		t.Fatalf("handed %s %q: %v", out.contentType, format, err)
	}
	if cfg.Width != rasterFitWidth || cfg.Height != 200 {
		t.Errorf("reduced to %dx%d; want %dx200", cfg.Width, cfg.Height, rasterFitWidth)
	}

	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 900, 1800)), nil); err != nil {
		t.Fatal(err)
	}
	out, why = reducedRaster(context.Background(), tileSource{contentType: "image/jpeg", content: jb.Bytes()})
	cfg, _, _ = image.DecodeConfig(bytes.NewReader(out.content))
	if why != "" || out.contentType != "image/png" || cfg.Height != rasterFitHeight || cfg.Width != 300 {
		t.Errorf("a tall JPEG was handed as %s %dx%d (%q); want a 300x%d PNG", out.contentType, cfg.Width, cfg.Height, why, rasterFitHeight)
	}
}

// An image within the tile, a family the platform does not decode, and bytes
// no decoder reads all reach the page as stored.
func TestReducedRaster_WhatIsDrawnAsStored(t *testing.T) {
	small := pngOf(t, 640, 480)
	for _, src := range []tileSource{
		{contentType: "image/png", content: small},
		{contentType: "image/avif", content: []byte("avif bytes")},
		{contentType: "text/html", content: []byte("<p>hi</p>")},
		{contentType: "image/png", content: []byte("\x89PNG-not-really")},
		{contentType: "image/png", content: pngHeaderOf(4000, 3000, 8, 6)},
	} {
		out, why := reducedRaster(context.Background(), src)
		if why != "" || !bytes.Equal(out.content, src.content) || out.contentType != src.contentType {
			t.Errorf("%s (%d bytes) was changed to %s (%d bytes), why %q", src.contentType, len(src.content), out.contentType, len(out.content), why)
		}
	}
}

// An image whose decoded pixels would pass the bound is recorded as not
// drawable, never decoded and never handed to the browser.
func TestReducedRaster_AnImageTooLargeIsTheDocumentsFailure(t *testing.T) {
	for _, c := range []struct {
		w, h      uint32
		depth, ct byte
	}{
		{9000, 9000, 8, 6},  // 81 MP of 8-bit RGBA
		{6000, 4000, 16, 6}, // 24 MP of 16-bit RGBA, 8 bytes a pixel
	} {
		_, why := reducedRaster(context.Background(), tileSource{contentType: "image/png", content: pngHeaderOf(c.w, c.h, c.depth, c.ct)})
		if !strings.Contains(why, "too large to draw a tile from") {
			t.Errorf("%dx%d at %d bits: why %q; want the image refused", c.w, c.h, c.depth, why)
		}
	}
}

// A large image asset reaches the renderer reduced: the page's content route
// serves the platform's copy, not the stored file.
func TestDrawAsset_ALargeImageReachesThePageReduced(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a8", "image/png", 1)
	stored := pngOf(t, 1600, 1200)
	blobs.objects[bucket+"/"+a.S3Key] = stored
	if err := worker(d, assets, blobs).drawAsset(context.Background(), a); err != nil {
		t.Fatalf("drawAsset: %v", err)
	}
	if len(d.pages) != 1 {
		t.Fatalf("rendered %d pages", len(d.pages))
	}
	f, ok := d.pages[0].Files(contentPath, nil)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(f.Body))
	if !ok || err != nil || cfg.Width != rasterFitWidth || cfg.Height != rasterFitHeight || len(f.Body) >= len(stored) {
		t.Fatalf("the page was served %dx%d (%d bytes of %d stored): %v", cfg.Width, cfg.Height, len(f.Body), len(stored), err)
	}

	d2, assets2 := &fakeDrawer{}, &fakeAssets{}
	huge := asset("a9", "image/png", 1)
	blobs.objects[bucket+"/"+huge.S3Key] = pngHeaderOf(12000, 12000, 8, 6)
	if err := worker(d2, assets2, blobs).drawAsset(context.Background(), huge); err != nil {
		t.Fatalf("drawAsset: %v", err)
	}
	if len(d2.pages) != 0 {
		t.Error("an image too large to decode was handed to the renderer")
	}
	if u := assets2.updates["a9"]; u.ThumbnailFailure == nil || !strings.Contains(*u.ThumbnailFailure, "12000x12000") {
		t.Errorf("the failure recorded was %+v; want the image's size named", u.ThumbnailFailure)
	}
}
