//go:build integration

package acceptance

import (
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Acceptance for #2629's content route: one site file's bytes, read as /file
// is read, served through the platform's blob writer (sandbox policy, nosniff,
// scriptable kinds as attachments, byte ranges), dev's file or a release's.
//
// Wire forms: GET /api/v1/portal/sites/{site}/file/content takes path and
// release as query strings, release absent and present, with and without a
// Range header.

const png2629 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// fileSite2629 is a site whose dev holds the files the viewer reads, at
// release 1.
func fileSite2629(t *testing.T) *site2604 {
	t.Helper()
	s := newSite2604(t, keyOwner2604, "acc2629-")
	connectAs(t, s.key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "note": "files",
		"files": []map[string]any{
			{"path": "img/logo.png", "content": png2629, "encoding": "base64"},
			{"path": "media/intro.mp4", "content": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("v", 4096))), "encoding": "base64"},
			{"path": "logo.svg", "content": `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
			{"path": "index.html", "content": "<script>alert(1)</script><h1>home</h1>"},
			{"path": "notes.md", "content": "# Notes\n"},
			{"path": "_headers", "content": "/*\n  X-Frame-Options: DENY\n"},
		},
	})
	return s
}

// content2629 reads the route as key.
func content2629(t *testing.T, s *site2604, key, query string, header map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+s.path("/file/content?"+query), nil) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", key)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("reading %s: %v", query, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, body
}

// TestIssue2629_TheContentRouteServesSiteFiles: a PNG answers inline image/png
// with the sandbox policy and nosniff; a range of the MP4 answers 206 with 100
// bytes; index.html and logo.svg, each holding a script, are attachments; a
// release's file answers that release's bytes after dev changed; someone the
// site is not shared with gets /file's refusal; ../x is refused.
func TestIssue2629_TheContentRouteServesSiteFiles(t *testing.T) {
	s := fileSite2629(t)
	status, h, body := content2629(t, s, s.key, "path=img/logo.png", nil)
	if status != http.StatusOK || h.Get("Content-Type") != "image/png" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") {
		t.Errorf("the PNG: %d %v", status, h)
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the PNG's policy headers: %v", h)
	}
	firstPNG := body

	status, h, body = content2629(t, s, s.key, "path=media/intro.mp4", map[string]string{"Range": "bytes=0-99"})
	if status != http.StatusPartialContent || len(body) != 100 {
		t.Errorf("a range of the video: %d, %d bytes", status, len(body))
	}
	for _, p := range []string{"index.html", "logo.svg"} {
		status, h, _ = content2629(t, s, s.key, "path="+p, nil)
		if status != http.StatusOK || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: %d %v", p, status, h)
		}
	}
	if status, _, body = content2629(t, s, s.key, "path=_headers", nil); status != http.StatusOK || !strings.Contains(string(body), "X-Frame-Options") {
		t.Errorf("_headers: %d %s", status, body)
	}

	connectAs(t, s.key).call("manage_site", map[string]any{
		"command": "publish", "site": s.name, "base": 1, "note": "new logo",
		"files": []map[string]any{{"path": "img/logo.png", "content": base64.StdEncoding.EncodeToString([]byte("not the first logo")), "encoding": "base64"}},
	})
	if _, _, now := content2629(t, s, s.key, "path=img/logo.png", nil); string(now) != "not the first logo" {
		t.Errorf("dev's file after the change: %q", now)
	}
	if _, _, then := content2629(t, s, s.key, "path=img/logo.png&release="+strconv.Itoa(1), nil); string(then) != string(firstPNG) {
		t.Errorf("release 1's file is not the earlier bytes")
	}

	statusFile, _ := s.rest(t, keyPeer2604, http.MethodGet, "/file?path=img/logo.png", nil)
	if status, _, _ := content2629(t, s, keyPeer2604, "path=img/logo.png", nil); status != statusFile || status != http.StatusForbidden {
		t.Errorf("someone it is not shared with: content %d, /file %d", status, statusFile)
	}
	if status, _, _ := content2629(t, s, s.key, "path=../x", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("../x answered %d", status)
	}
}
