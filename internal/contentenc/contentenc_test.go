package contentenc

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccepted(t *testing.T) {
	tests := []struct {
		header string
		br, gz bool
	}{
		{"", false, false},
		{"br, gzip", true, true},
		{"gzip, deflate", false, true},
		{"BR;q=0.8", true, false},
		{"br;q=0, gzip", false, true},
		{"br;q=0.0, gzip;q=0", false, false},
		{"identity", false, false},
		{" br ; q=1 ,gzip", true, true},
	}
	for _, tt := range tests {
		br, gz := Accepted(tt.header)
		if br != tt.br || gz != tt.gz {
			t.Errorf("Accepted(%q) = %v, %v; want %v, %v", tt.header, br, gz, tt.br, tt.gz)
		}
	}
}

func TestCompressible(t *testing.T) {
	for _, ct := range []string{
		"text/css", "text/html; charset=utf-8", "application/javascript", "text/javascript; charset=utf-8",
		"image/svg+xml", "application/ld+json", "application/wasm", "font/ttf", "Application/JSON",
	} {
		if !Compressible(ct) {
			t.Errorf("%s should be compressible", ct)
		}
	}
	for _, ct := range []string{"image/png", "font/woff2", "application/zip", "application/octet-stream", ""} {
		if Compressible(ct) {
			t.Errorf("%s should not be compressible", ct)
		}
	}
}

// Both forms round-trip to the original.
func TestGzipAndBrotliRoundTrip(t *testing.T) {
	body := []byte(strings.Repeat("function portal(){return 'x'}\n", 200))

	gz, err := Gzip(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(r); !bytes.Equal(got, body) {
		t.Error("gzip did not round-trip")
	}

	br, err := Brotli(body)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(br))); !bytes.Equal(got, body) {
		t.Error("brotli did not round-trip")
	}
	if !Worthwhile(len(body), len(br)) {
		t.Errorf("repetitive text compressed to %d of %d bytes and was judged not worth sending", len(br), len(body))
	}
}

func TestWorthwhile(t *testing.T) {
	if Worthwhile(1000, 950) {
		t.Error("a 5% saving is not worth a second form")
	}
	if !Worthwhile(1000, 949) {
		t.Error("just over a 5% saving is")
	}
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A form that cannot be written names the form and the cause, at open, write
// or close.
func TestCompressTo_AFailureNamesTheForm(t *testing.T) {
	body := []byte(strings.Repeat("x", 4096))
	for _, open := range []func(io.Writer) (io.WriteCloser, error){gzipWriter, brotliWriter} {
		err := compressTo(failingWriter{}, "form", open, body)
		if err == nil || !strings.Contains(err.Error(), "writing the form form: ") || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("writing to a failing destination: %v", err)
		}
	}
	err := compressTo(&bytes.Buffer{}, "form", func(io.Writer) (io.WriteCloser, error) { return nil, errors.New("bad level") }, body)
	if err == nil || !strings.Contains(err.Error(), "writing the form form: bad level") {
		t.Errorf("a writer that cannot open: %v", err)
	}
}

// TestRespond: an answer produced per request is sent in the form the caller
// accepts, brotli first, and as it is when it may hold a platform token, is a
// HEAD, a 204 or 304, under the threshold, an image, or already encoded
// (#2655).
func TestRespond(t *testing.T) {
	big := []byte(strings.Repeat(`{"slot":"09:00","room":"A"},`, 100))
	send := func(method, accept, contentType, encoded string, status int, body []byte, secret bool) (http.Header, []byte) {
		r := httptest.NewRequestWithContext(context.Background(), method, "/", http.NoBody)
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		h := http.Header{"Content-Type": {contentType}}
		if encoded != "" {
			h.Set("Content-Encoding", encoded)
		}
		return h, Respond(r, h, status, body, secret)
	}
	h, out := send(http.MethodGet, "br, gzip", "application/json", "", http.StatusOK, big, false)
	assert.Equal(t, EncodingBrotli, h.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", h.Get("Vary"))
	got, err := io.ReadAll(brotli.NewReader(bytes.NewReader(out)))
	require.NoError(t, err)
	assert.Equal(t, big, got)

	h, out = send(http.MethodGet, "gzip", "text/html; charset=utf-8", "", http.StatusOK, big, false)
	assert.Equal(t, EncodingGzip, h.Get("Content-Encoding"))
	zr, err := gzip.NewReader(bytes.NewReader(out))
	require.NoError(t, err)
	got, err = io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, big, got)

	h, out = send(http.MethodGet, "", "application/json", "", http.StatusOK, big, false)
	assert.Empty(t, h.Get("Content-Encoding"), "no Accept-Encoding gets identity")
	assert.Equal(t, "Accept-Encoding", h.Get("Vary"), "but the answer still varies by it")
	assert.Equal(t, big, out)

	for label, c := range map[string]struct {
		method, contentType, encoded string
		status                       int
		body                         []byte
		secret                       bool
	}{
		"a platform token": {http.MethodGet, "application/json", "", http.StatusOK, big, true},
		"HEAD":             {http.MethodHead, "application/json", "", http.StatusOK, big, false},
		"204":              {http.MethodGet, "application/json", "", http.StatusNoContent, big, false},
		"304":              {http.MethodGet, "application/json", "", http.StatusNotModified, big, false},
		"small":            {http.MethodGet, "application/json", "", http.StatusOK, big[:100], false},
		"an image":         {http.MethodGet, "image/png", "", http.StatusOK, big, false},
		"already encoded":  {http.MethodGet, "application/json", "zstd", http.StatusOK, big, false},
	} {
		h, out := send(c.method, "br, gzip", c.contentType, c.encoded, c.status, c.body, c.secret)
		assert.Equal(t, c.encoded, h.Get("Content-Encoding"), label)
		assert.Equal(t, c.body, out, label)
	}
	h, out = send(http.MethodGet, "br", "application/json", "", http.StatusOK, randomish(4096), false)
	assert.Empty(t, h.Get("Content-Encoding"), "a form that saves too little is not sent")
	assert.Len(t, out, 4096)
}

// randomish is n bytes compression cannot shrink.
func randomish(n int) []byte {
	out := make([]byte, n)
	_, _ = rand.Read(out)
	return out
}
