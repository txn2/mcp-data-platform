package contenttype

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// #2308: an upload is named a Lottie animation by its shape, and a zip under a
// .lottie name a dotLottie archive; neither shape alone is enough.
func TestDetectNamesDrawnUploads(t *testing.T) {
	lottie := []byte(`{"v":"5.7.0","fr":30,"ip":0,"op":60,"w":10,"h":10,"layers":[]}`)
	assert.Equal(t, LottieJSON, DetectFileBytes("", "pulse.json", lottie))
	assert.Equal(t, JSON, DetectFileBytes("", "rows.json", []byte(`{"rows":[1,2]}`)), "JSON without Lottie's keys stays JSON")
	assert.Equal(t, JSON, DetectFileBytes("", "list.json", []byte(`[{"fr":1}]`)), "an array is not an animation")

	buf := new(bytes.Buffer)
	z := zip.NewWriter(buf)
	w, _ := z.Create("manifest.json")
	_, _ = w.Write([]byte(`{"animations":[{"id":"a"}]}`))
	_ = z.Close()
	assert.Equal(t, DotLottie, DetectFileBytes("", "pulse.lottie", buf.Bytes()))
	assert.Equal(t, "application/zip", DetectFileBytes("", "bundle.zip", buf.Bytes()), "a zip under another name stays a zip")

	assert.Equal(t, D2, TypeForFilename("arch.d2"))
	assert.Equal(t, Excalidraw, Normalize("application/x-excalidraw+json"))
	assert.True(t, IsTextual(Excalidraw) && IsTextual(LottieJSON) && IsTextual(D2))
	assert.True(t, IsStorableText(D2) && IsStorableText(Excalidraw) && IsStorableText(LottieJSON))
	assert.False(t, IsStorableText(DotLottie), "a binary archive arrives as an upload, not a string")
}
