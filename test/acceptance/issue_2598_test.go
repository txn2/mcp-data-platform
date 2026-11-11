//go:build integration

package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// Issue #2598: the portal's build files under /portal/assets/ were sent with
// no compression, no Cache-Control and no validator, so every load downloaded
// the whole portal again at full size. A hashed build file is now sent
// compressed in the form Accept-Encoding allows, cached for good
// (immutable), with an ETag per form; index.html is still revalidated on every
// load.
//
// Against the running platform's own embedded build (make frontend-build
// before make dev): the chunk is the one index.html names, so the criteria
// read the file a browser would.
//
// Wire forms: plain HTTP GETs with and without Accept-Encoding (br, gzip, and
// none), and with If-None-Match carrying the ETag the first answer gave. No
// JSON is sent.

var issue2598Script = regexp.MustCompile(`src="(/portal/assets/[^"]+\.js)"`)

type issue2598Answer struct {
	status  int
	header  http.Header
	body    []byte
	decoded []byte
}

// issue2598Get fetches path with headers, without the transport's own gzip
// handling, and decodes the body by the Content-Encoding it came back with.
func issue2598Get(t *testing.T, path string, headers map[string]string) issue2598Answer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	a := issue2598Answer{status: resp.StatusCode, header: resp.Header, body: body, decoded: body}
	switch resp.Header.Get("Content-Encoding") {
	case "br":
		a.decoded, err = io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
	case "gzip":
		var r *gzip.Reader
		if r, err = gzip.NewReader(bytes.NewReader(body)); err == nil {
			a.decoded, err = io.ReadAll(r)
		}
	}
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return a
}

// issue2598Chunk is the entry script index.html names.
func issue2598Chunk(t *testing.T) string {
	t.Helper()
	index := issue2598Get(t, "/portal/", nil)
	if index.status != http.StatusOK {
		t.Fatalf("/portal/ answered %d; the criteria need the portal's embedded build (make frontend-build, then make dev)", index.status)
	}
	if got := index.header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index.html Cache-Control = %q, want no-cache", got)
	}
	m := issue2598Script.FindSubmatch(index.body)
	if m == nil {
		t.Fatalf("index.html names no /portal/assets/*.js: %.300s", index.body)
	}
	return string(m[1])
}

// TestIssue2598_ABuildFileIsSentCompressed: the entry chunk asked for with
// brotli comes back brotli, much smaller than the file, decoding to the file
// sent to a request that allows no encoding; gzip likewise.
func TestIssue2598_ABuildFileIsSentCompressed(t *testing.T) {
	chunk := issue2598Chunk(t)
	plain := issue2598Get(t, chunk, nil)
	if plain.status != http.StatusOK || plain.header.Get("Content-Encoding") != "" {
		t.Fatalf("with no Accept-Encoding: %d, Content-Encoding %q", plain.status, plain.header.Get("Content-Encoding"))
	}
	for _, enc := range []struct{ accept, want string }{{"br, gzip", "br"}, {"gzip", "gzip"}} {
		a := issue2598Get(t, chunk, map[string]string{"Accept-Encoding": enc.accept})
		if a.status != http.StatusOK || a.header.Get("Content-Encoding") != enc.want {
			t.Errorf("Accept-Encoding %q: %d, Content-Encoding %q; want %s", enc.accept, a.status, a.header.Get("Content-Encoding"), enc.want)
			continue
		}
		if len(a.body)*2 > len(plain.body) {
			t.Errorf("%s form is %d bytes of %d; want under half", enc.want, len(a.body), len(plain.body))
		}
		if !bytes.Equal(a.decoded, plain.body) {
			t.Errorf("the %s form does not decode to the file", enc.want)
		}
		// The CORS layer writes its own Vary line beside this one.
		if vary := strings.Join(a.header.Values("Vary"), ", "); !strings.Contains(vary, "Accept-Encoding") {
			t.Errorf("Vary = %q; want Accept-Encoding among its values", vary)
		}
	}
}

// TestIssue2598_ABuildFileIsCachedForGood: the chunk is immutable for a year,
// and a revalidation with its ETag answers 304 with no body, so a second
// visit downloads nothing new.
func TestIssue2598_ABuildFileIsCachedForGood(t *testing.T) {
	chunk := issue2598Chunk(t)
	first := issue2598Get(t, chunk, map[string]string{"Accept-Encoding": "br"})
	if got := first.header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	tag := first.header.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}
	again := issue2598Get(t, chunk, map[string]string{"Accept-Encoding": "br", "If-None-Match": tag})
	if again.status != http.StatusNotModified || len(again.body) != 0 {
		t.Errorf("revalidating with %s: %d and %d bytes; want 304 and none", tag, again.status, len(again.body))
	}
}
