package rasterfit

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngHeaderOf is a PNG that declares w x h pixels and holds none.
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

func TestFit(t *testing.T) {
	ctx := context.Background()
	out, reduced, err := Fit(ctx, pngOf(t, 2000, 500), 800, 600, 64<<20)
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || !reduced || cfg.Width != 800 || cfg.Height != 200 {
		t.Errorf("a wide PNG: %dx%d reduced=%v err=%v; want 800x200", cfg.Width, cfg.Height, reduced, err)
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 900, 1800)), nil); err != nil {
		t.Fatal(err)
	}
	out, reduced, err = Fit(ctx, jb.Bytes(), 800, 600, 64<<20)
	cfg, format, _ := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || !reduced || format != "png" || cfg.Width != 300 || cfg.Height != 600 {
		t.Errorf("a tall JPEG: %s %dx%d reduced=%v err=%v; want a 300x600 PNG", format, cfg.Width, cfg.Height, reduced, err)
	}

	small := pngOf(t, 640, 480)
	if out, reduced, err := Fit(ctx, small, 800, 600, 64<<20); err != nil || reduced || !bytes.Equal(out, small) {
		t.Errorf("an image within the box was changed: reduced=%v err=%v", reduced, err)
	}
	if _, _, err := Fit(ctx, []byte("\x89PNG-not"), 800, 600, 64<<20); !errors.Is(err, ErrUnreadable) {
		t.Errorf("unreadable header: err %v", err)
	}
	if _, _, err := Fit(ctx, pngHeaderOf(4000, 3000, 8, 6), 800, 600, 64<<20); !errors.Is(err, ErrUnreadable) {
		t.Errorf("a header with no pixels: err %v; want ErrUnreadable", err)
	}
	for _, c := range []struct {
		w, h      uint32
		depth, ct byte
	}{{9000, 9000, 8, 6}, {6000, 4000, 16, 6}} {
		_, _, err := Fit(ctx, pngHeaderOf(c.w, c.h, c.depth, c.ct), 800, 600, 96<<20)
		var tl *TooLargeError
		if !errors.As(err, &tl) || tl.Width != int(c.w) || tl.Error() == "" {
			t.Errorf("%dx%d at %d bits: err %v; want too large", c.w, c.h, c.depth, err)
		}
	}

	stopped, cancel := context.WithCancel(ctx)
	cancel()
	slot <- struct{}{}
	_, _, err = Fit(stopped, pngOf(t, 1000, 1000), 800, 600, 64<<20)
	<-slot
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped caller waiting for the slot: err %v", err)
	}
}

func TestDecodes(t *testing.T) {
	for ct, want := range map[string]bool{
		"image/png": true, "IMAGE/JPEG; q=1": true, "image/webp": true, "image/avif": false, "image/svg+xml": false, "text/html": false,
	} {
		if Decodes(ct) != want {
			t.Errorf("Decodes(%q) = %v", ct, !want)
		}
	}
}

func TestBytesPerPixel(t *testing.T) {
	for _, c := range []struct {
		model color.Model
		want  int64
	}{
		{color.GrayModel, 1},
		{color.Gray16Model, 2},
		{color.YCbCrModel, 4},
		{color.RGBAModel, 4},
		{color.NRGBA64Model, 8},
		{color.Palette{color.Black}, 1},
		{color.CMYKModel, 4},
	} {
		if got := BytesPerPixel(c.model); got != c.want {
			t.Errorf("BytesPerPixel(%T) = %d, want %d", c.model, got, c.want)
		}
	}
}
