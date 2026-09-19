package headless

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

// paintDensity is the device pixel ratio a page is painted at for scale: the
// scale itself when it enlarges, and 1 when it reduces, which reduce then
// does to the full-size picture.
func paintDensity(scale float64) float64 {
	if scale > 1 {
		return scale
	}
	return 1
}

// reduce scales a screenshot of a width x height viewport down to scale with
// a Catmull-Rom filter.
//
// Asking the browser for the smaller picture instead has it paint the page at
// that size, and a 1280-pixel page painted 400 pixels wide sets its body text
// four pixels tall: every tile of a document was a smear of grey (#1789).
// Painting at full size and reducing the picture keeps what a reader would
// see, smaller.
func reduce(src []byte, width, height int, scale float64) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("headless: decoding the screenshot: %w", err)
	}
	w := max(1, int(math.Round(float64(width)*scale)))
	h := max(1, int(math.Round(float64(height)*scale)))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("headless: encoding the reduced screenshot: %w", err)
	}
	return out.Bytes(), nil
}
