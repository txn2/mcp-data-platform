// Package rasterfit reduces a raster image to fit a box, decoding it in the
// platform under a bound on the memory its pixels take (#2697). A browser
// decodes an image at its natural size, so its memory grows with the image's
// pixels rather than with the size it is shown at; the thumbnail renderer was
// taken past its memory limit by one photograph a mail attachment carried. The
// tile worker hands the renderer the reduced copy instead.
package rasterfit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	"image/png"
	"math"
	"strings"

	_ "golang.org/x/image/bmp" // registers the BMP decoder
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

// ErrUnreadable is an image whose header or pixels no decoder here reads. A
// browser may still read it (an animated WebP), so a caller can hand it on
// as stored.
var ErrUnreadable = errors.New("rasterfit: the image could not be decoded")

// TooLargeError is an image whose header declares more decoded pixels than
// the bound allows.
type TooLargeError struct {
	Width, Height int
	Limit         int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("the image is %dx%d pixels, more than %d MiB decoded", e.Width, e.Height, e.Limit>>20)
}

// decoded is the media types this package decodes.
//
//nolint:gochecknoglobals // a read-only set.
var decoded = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true,
}

// Decodes reports whether an image of contentType is one this package reads.
func Decodes(contentType string) bool {
	mt, _, _ := strings.Cut(contentType, ";")
	return decoded[strings.ToLower(strings.TrimSpace(mt))]
}

// slot lets one image be decoded at a time in the process, so maxBytes is
// what the process holds for reductions however many callers reduce at once.
var slot = make(chan struct{}, 1) //nolint:gochecknoglobals // one decode at a time per process

// Fit returns data as a PNG reduced to fit width x height, proportions and
// transparency kept, and reduced true. An image already within the box is
// returned unchanged with reduced false. An image whose decoded pixels would
// take more than maxBytes is a *TooLargeError, read from its header alone;
// one that cannot be decoded is ErrUnreadable. A context ended while waiting
// for the decode slot returns its error.
func Fit(ctx context.Context, data []byte, width, height int, maxBytes int64) (out []byte, reduced bool, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("reading the header: %w: %w", ErrUnreadable, err)
	}
	if cfg.Width <= width && cfg.Height <= height {
		return data, false, nil
	}
	if need := int64(cfg.Width) * int64(cfg.Height) * BytesPerPixel(cfg.ColorModel); need > maxBytes {
		return nil, false, &TooLargeError{Width: cfg.Width, Height: cfg.Height, Limit: maxBytes}
	}
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return nil, false, fmt.Errorf("rasterfit: waiting to decode: %w", ctx.Err())
	}
	defer func() { <-slot }()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("decoding: %w: %w", ErrUnreadable, err)
	}
	ratio := math.Min(float64(width)/float64(cfg.Width), float64(height)/float64(cfg.Height))
	w := max(1, int(math.Round(float64(cfg.Width)*ratio)))
	h := max(1, int(math.Round(float64(cfg.Height)*ratio)))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	var b bytes.Buffer
	if err := png.Encode(&b, dst); err != nil {
		return nil, false, fmt.Errorf("rasterfit: encoding: %w", err)
	}
	return b.Bytes(), true, nil
}

// Bytes a decoded pixel holds, by how much its color model stores.
const (
	onePlane   = 1
	twoPlanes  = 2
	fourPlanes = 4
	wideColor  = 8
)

// BytesPerPixel is what decoding an image of model holds per pixel. A JPEG's
// YCbCr counts 4: a progressive JPEG keeps its coefficients beside the image
// while it decodes.
func BytesPerPixel(model color.Model) int64 {
	switch model {
	case color.GrayModel, color.AlphaModel:
		return onePlane
	case color.Gray16Model, color.Alpha16Model:
		return twoPlanes
	case color.RGBA64Model, color.NRGBA64Model:
		return wideColor
	}
	if _, paletted := model.(color.Palette); paletted {
		return onePlane
	}
	return fourPlanes
}
