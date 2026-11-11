//go:build integration

package acceptance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Issue #2589: manage_site crawl skipped every stylesheet and script linked
// with a query string (a cache-busting ?ver=), never read srcset or the
// attributes lazy loading keeps the real image address in, and stored a
// fetched stylesheet without reading the fonts and images it names, so a
// copied site came out unstyled. Now a file a page loads is copied by its path
// with the query dropped (the page keeps its ?ver= link, which answers with
// that file), every srcset candidate and lazy-loading address is copied, and
// a stylesheet's url() and @import references are copied, resolved against the
// stylesheet. A page link with a query is still not walked.
//
// Against the dev stack: a source site served from this test process, copied
// through manage_site crawl into a new site's dev environment, and read back
// on the hostname added to that environment.
//
// Wire forms: manage_site's command, site, environment and url are JSON
// strings; the copied site is read with plain HTTP GETs naming its hostname in
// Host.

// issue2589Source is a site in the shape the ticket names: a stylesheet and a
// script linked with ?ver=, responsive and lazy-loaded images, and CSS naming
// a font, a background image and an imported stylesheet.
func issue2589Source(t *testing.T) (*httptest.Server, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	files := map[string][2]string{
		"/": {"text/html", `<html><head>
<link rel="stylesheet" href="/wp-content/site.css?ver=1.2">
<script src="/wp-content/app.js?ver=6.4"></script>
</head><body><h1 class="styled">copied</h1>
<img src="/media/a-400.png" srcset="/media/a-400.png 400w, /media/a-800.png 800w">
<img src="data:image/gif;base64,R0lGOD" data-src="/media/lazy.png">
<a href="/search?q=1">search</a>
</body></html>`},
		"/wp-content/site.css":  {"text/css", `@import "theme.css"; @font-face { font-family: B; src: url("../fonts/brand.woff2") format("woff2"); } .styled { background: url(bg.png?v=2) }`},
		"/wp-content/theme.css": {"text/css", "h1 { color: rebeccapurple }"},
		"/wp-content/app.js":    {"text/javascript", "console.log('copied')"},
		"/wp-content/bg.png":    {"image/png", "\x89PNG\r\n\x1a\nbg"},
		"/fonts/brand.woff2":    {"font/woff2", "wOF2brand"},
		"/media/a-400.png":      {"image/png", "\x89PNG\r\n\x1a\n400"},
		"/media/a-800.png":      {"image/png", "\x89PNG\r\n\x1a\n800"},
		"/media/lazy.png":       {"image/png", "\x89PNG\r\n\x1a\nlazy"},
		"/search":               {"text/html", "<h1>results</h1>"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", f[0])
		_, _ = w.Write([]byte(f[1]))
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

// TestIssue2589_ACopiedSiteKeepsWhatItsPagesLoad: after the crawl, the
// stylesheet answers at its path and at the ?ver= link the page kept, and the
// script, the imported stylesheet, the font and background the CSS names, and
// every srcset and lazy-loaded image answer on the copied site; the search
// link was never fetched.
func TestIssue2589_ACopiedSiteKeepsWhatItsPagesLoad(t *testing.T) {
	admin := connect(t)
	src, hits := issue2589Source(t)
	s := newSite2518(t, admin)
	got := admin.call("manage_site", map[string]any{
		"command": "crawl", "site": s.name, "environment": "dev", "url": src.URL + "/",
	})
	if pages, _ := got["pages"].(float64); pages < 9 {
		t.Fatalf("the crawl copied %v files; want the page and the eight files it loads: %v", got["pages"], got)
	}
	host := s.toStage(t) // dev is the working copy and has no domain (#2604); stage's is public
	home := pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, host, http.MethodGet, "/", nil, "")
		return r, r.status == http.StatusOK
	})
	if !strings.Contains(home.body, `href="/wp-content/site.css?ver=1.2"`) {
		t.Errorf("the page lost its stylesheet link: %s", home.body)
	}
	for path, want := range map[string]string{
		"/wp-content/site.css?ver=1.2": "@font-face",
		"/wp-content/site.css":         "@font-face",
		"/wp-content/app.js?ver=6.4":   "console.log",
		"/wp-content/theme.css":        "rebeccapurple",
		"/wp-content/bg.png":           "bg",
		"/fonts/brand.woff2":           "wOF2brand",
		"/media/a-400.png":             "400",
		"/media/a-800.png":             "800",
		"/media/lazy.png":              "lazy",
	} {
		if r := onHost(t, host, http.MethodGet, path, nil, ""); r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Errorf("%s on the copied site: %d %.120q", path, r.status, r.body)
		}
	}
	if n := hits("/search"); n != 0 {
		t.Errorf("the search link was fetched %d times; a page link with a query is not walked", n)
	}
}
