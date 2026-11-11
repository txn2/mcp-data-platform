package sitecrawl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

const htmlType = "text/html; charset=utf-8"

// siteServer answers the paths routes names and 404 for the rest, counting
// each request.
type siteServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
}

type answer struct {
	status   int
	location string
	ctype    string
	body     string
}

func page(body string) answer { return answer{status: http.StatusOK, ctype: htmlType, body: body} }

func serve(t *testing.T, routes map[string]answer) *siteServer {
	t.Helper()
	s := &siteServer{hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()
		a, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if a.location != "" {
			w.Header().Set("Location", a.location)
		}
		if a.ctype != "" {
			w.Header().Set("Content-Type", a.ctype)
		}
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *siteServer) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func noFollow(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func crawler(s *siteServer) *Crawler {
	client := s.Client()
	client.CheckRedirect = noFollow
	return &Crawler{Client: client, MaxPages: 100, MaxBytes: 1 << 20}
}

func addresses(skipped []site.Skipped) []string {
	out := make([]string, 0, len(skipped))
	for _, sk := range skipped {
		out = append(out, sk.Address)
	}
	return out
}

func files(r *Result) map[string]string {
	out := map[string]string{}
	for _, f := range r.Files {
		out[f.Path] = string(f.Body)
	}
	return out
}

func TestCrawl_PagesAreFiledToAnswerAtTheSameAddress(t *testing.T) {
	s := serve(t, map[string]answer{
		"/":                page(`<a href="/about/">a</a><a href="/pricing">p</a><a href="/docs/guide.html">d</a><a href="/doc.pdf">f</a>`),
		"/about/":          page("about"),
		"/pricing":         page("pricing"),
		"/docs/guide.html": page("guide"),
		"/doc.pdf":         {status: http.StatusOK, ctype: "application/pdf", body: "pdf"},
		"/robots.txt":      {status: http.StatusOK, ctype: "text/plain", body: "User-agent: *"},
		"/sitemap.xml":     {status: http.StatusOK, ctype: "application/xml", body: "<urlset/>"},
	})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)

	byPath := files(got)
	assert.Contains(t, byPath, "index.html", "the root is the index")
	assert.Equal(t, "about", byPath["about/index.html"], "a trailing slash is a directory's index")
	assert.Equal(t, "pricing", byPath["pricing.html"], "a page with no extension gets .html")
	assert.Equal(t, "guide", byPath["docs/guide.html"])
	assert.Equal(t, "pdf", byPath["doc.pdf"])
	assert.Equal(t, "User-agent: *", byPath["robots.txt"], "the robots file comes along")
	assert.Equal(t, "<urlset/>", byPath["sitemap.xml"], "the sitemap comes along")
	assert.Equal(t, 7, got.Pages)
	assert.Zero(t, got.Redirects)
	assert.Equal(t, []site.Skipped{{Address: "/favicon.ico", Reason: "answered 404"}}, got.Skipped, "what did not answer is reported, with why")
}

func TestFilePath(t *testing.T) {
	cases := []struct {
		path   string
		isHTML bool
		want   string
	}{
		{"/", true, "index.html"},
		{"/", false, "index.html"},
		{"/a/", true, "a/index.html"},
		{"/a/b/", false, "a/b/index.html"},
		{"/a", true, "a.html"},
		{"/a/b", true, "a/b.html"},
		{"/a", false, "a"},
		{"/a.html", true, "a.html"},
		{"/a.css", false, "a.css"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, filePath(tc.path, tc.isHTML), "%s html=%v", tc.path, tc.isHTML)
	}
}

func TestCrawl_ARedirectBecomesARuleAndItsTargetIsWalked(t *testing.T) {
	s := serve(t, map[string]answer{
		"/":    page(`<a href="/old">o</a>`),
		"/old": {status: http.StatusMovedPermanently, location: "/new"},
		"/new": page("the new page"),
	})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)

	byPath := files(got)
	assert.Equal(t, "/old /new 301\n", byPath["_redirects"])
	assert.Equal(t, "the new page", byPath["new.html"], "the target is walked")
	assert.NotContains(t, byPath, "old.html", "the address that redirected is not filed")
	assert.Equal(t, 1, got.Redirects)
	assert.Equal(t, 1, s.count("/new"), "and walked once")

	rules, err := site.ParseRedirects(byPath["_redirects"])
	require.NoError(t, err, "the map parses as the publish will read it")
	assert.Equal(t, []site.Redirect{{From: "/old", To: "/new", Status: 301}}, rules)
}

func TestCrawl_ARedirectOffTheSiteIsAnAbsoluteRuleAndIsNotWalked(t *testing.T) {
	other := serve(t, map[string]answer{"/x": page("elsewhere")})
	s := serve(t, map[string]answer{
		"/":     page(`<a href="/gone">g</a>`),
		"/gone": {status: http.StatusFound, location: other.URL + "/x"},
	})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("/gone %s/x 302\n", other.URL), files(got)["_redirects"])
	assert.Zero(t, other.count("/x"))
}

func TestCrawl_ARedirectThatCannotBeRecordedIsSkipped(t *testing.T) {
	for name, a := range map[string]answer{
		"no Location":     {status: http.StatusFound},
		"a script target": {status: http.StatusFound, location: "javascript:alert(1)"},
		"not a redirect":  {status: http.StatusNotModified, location: "/x"},
	} {
		t.Run(name, func(t *testing.T) {
			s := serve(t, map[string]answer{"/": page(`<a href="/r">r</a>`), "/r": a})
			got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
			require.NoError(t, err)
			assert.Contains(t, addresses(got.Skipped), "/r")
			assert.NotContains(t, files(got), "_redirects")
		})
	}
}

func TestCrawl_TheRedirectMapIsBounded(t *testing.T) {
	s := serve(t, map[string]answer{"/": page(`<a href="/r">r</a>`), "/r": {status: http.StatusFound, location: "/x"}})
	c := crawler(s)
	r := &run{c: c, req: Request{}, root: mustRoot(t, s.URL), seen: map[string]bool{}, redirects: make([]site.Redirect, redirectsMax)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+"/r", http.NoBody)
	require.NoError(t, err)
	res, err := c.Client.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	r.redirected("/r", res)
	assert.Equal(t, []site.Skipped{{Address: "/r", Reason: "the site has more redirects than a release can hold"}}, r.skipped)
	assert.Len(t, r.redirects, redirectsMax)
}

func mustRoot(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestCrawl_StaysOnTheSite(t *testing.T) {
	other := serve(t, map[string]answer{"/x": page("x"), "/y.png": page("y")})
	s := serve(t, map[string]answer{
		"/": page(fmt.Sprintf(`
			<a href="%[1]s/x">off the host</a>
			<img src="%[1]s/y.png">
			<a href="mailto:a@example.com">mail</a>
			<a href="javascript:void(0)">js</a>
			<a href="/search?q=1">a query</a>
			<a href="/kept">kept</a>`, other.URL)),
		"/kept": page("kept"),
	})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Zero(t, other.count("/x"), "an off-host link is not followed")
	assert.Zero(t, other.count("/y.png"))
	assert.Zero(t, s.count("/search"), "an address with a query is not walked")
	assert.Contains(t, files(got), "kept.html")
}

func TestCrawl_FollowsEveryKindOfReference(t *testing.T) {
	s := serve(t, map[string]answer{
		"/": page(`<link href="/style.css"><script src="/app.bin"></script><img src="/i.png">
			<video src="/v.mp4" poster="/p.png"></video><audio src="/a.mp3"></audio><iframe src="/f.html"></iframe>
			<picture><source src="/s.webp"></picture>`),
		"/style.css": {status: http.StatusOK, ctype: "text/css", body: "css"},
		"/app.bin":   {status: http.StatusOK, body: "bin"},
		"/i.png":     {status: http.StatusOK, ctype: "image/png", body: "png"},
		"/v.mp4":     {status: http.StatusOK, ctype: "video/mp4", body: "v"},
		"/p.png":     {status: http.StatusOK, ctype: "image/png", body: "p"},
		"/a.mp3":     {status: http.StatusOK, ctype: "audio/mpeg", body: "a"},
		"/f.html":    page("frame"),
		"/s.webp":    {status: http.StatusOK, ctype: "image/webp", body: "s"},
	})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	byPath := files(got)
	for _, name := range []string{"style.css", "app.bin", "i.png", "v.mp4", "p.png", "a.mp3", "f.html", "s.webp"} {
		assert.Contains(t, byPath, name)
	}
}

// TestCrawl_FilesAPageLoadsAreCopied pins #2589: a stylesheet and a script
// linked with a cache-busting query are filed at their path, the page keeping
// its link; every srcset candidate and lazy-loading address is copied; the
// fonts, backgrounds and imports a stylesheet, a <style> element or a style
// attribute names are copied, resolved against where the CSS was read; a
// page link with a query is still not walked.
func TestCrawl_FilesAPageLoadsAreCopied(t *testing.T) {
	css := answer{status: http.StatusOK, ctype: "text/css"}
	img := answer{status: http.StatusOK, ctype: "image/png", body: "png"}
	s := serve(t, map[string]answer{
		"/": page(`<html><head>
			<link rel="stylesheet" href="/wp/site.css?ver=1.2">
			<script src="/wp/app.js?ver=6.4.2"></script>
			<style>.hero{background:url(/img/hero.jpg)}</style>
			</head><body>
			<img src="/img/a-400.png" srcset="/img/a-400.png 400w, /img/a-800.png 800w,/img/a-1200.png 1200w"
			     sizes="(max-width: 600px) 400px, 800px">
			<img src="data:image/gif;base64,R0lGOD" data-src="/img/lazy.png" data-srcset="/img/lazy-2x.png 2x">
			<img data-lazy-src="/img/lazier.png">
			<picture><source srcset="/img/b.webp 1x, data:image/webp;base64,AAA,BBB 2x"></picture>
			<div style="background-image: url('/img/inline.png')"></div>
			<a href="/search?q=1">search</a>
			</body></html>`),
		"/wp/site.css": {status: http.StatusOK, ctype: "text/css; charset=utf-8", body: `
			@import "theme.css";
			/* url(/img/commented-out.png) */
			@font-face { font-family: X; src: url("../fonts/x.woff2") format("woff2"), url(../fonts/x.woff?v=3) format("woff"); }
			body { background: url( 'bg.png' ) }`},
		"/wp/theme.css":          css,
		"/wp/app.js":             {status: http.StatusOK, ctype: "text/javascript", body: "js"},
		"/fonts/x.woff2":         {status: http.StatusOK, ctype: "font/woff2", body: "w2"},
		"/fonts/x.woff":          {status: http.StatusOK, ctype: "font/woff", body: "w"},
		"/wp/bg.png":             img,
		"/img/hero.jpg":          img,
		"/img/a-400.png":         img,
		"/img/a-800.png":         img,
		"/img/a-1200.png":        img,
		"/img/lazy.png":          img,
		"/img/lazy-2x.png":       img,
		"/img/lazier.png":        img,
		"/img/b.webp":            img,
		"/img/inline.png":        img,
		"/img/commented-out.png": img,
	})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	byPath := files(got)
	for _, name := range []string{
		"wp/site.css", "wp/app.js", "wp/theme.css", "fonts/x.woff2", "fonts/x.woff", "wp/bg.png",
		"img/hero.jpg", "img/a-400.png", "img/a-800.png", "img/a-1200.png", "img/lazy.png", "img/lazy-2x.png",
		"img/lazier.png", "img/b.webp", "img/inline.png",
	} {
		assert.Contains(t, byPath, name)
	}
	assert.Contains(t, byPath["index.html"], `href="/wp/site.css?ver=1.2"`, "the page keeps its link")
	assert.Zero(t, s.count("/img/commented-out.png"), "a reference inside a CSS comment is not one")
	assert.Zero(t, s.count("/search"), "a page link with a query is still not walked")
	assert.NotContains(t, addresses(got.Skipped), "/AAA", "a data: candidate holding a comma stays whole")
}

func TestSrcsetURLs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"/a.png", []string{"/a.png"}},
		{"/a.png 1x, /b.png 2x", []string{"/a.png", "/b.png"}},
		{"/a.png 400w,/b.png 800w", []string{"/a.png", "/b.png"}},
		{" ,/a.png, /b.png 2x ", []string{"/a.png", "/b.png"}},
		{"data:image/png;base64,AA,BB 1x, /c.png 2x", []string{"data:image/png;base64,AA,BB", "/c.png"}},
		{"/a.png (max-width: 1px, 2px) 1x, /b.png", []string{"/a.png", "/b.png"}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, srcsetURLs(tt.in), tt.in)
	}
}

func TestCrawl_MaxBytesRefusesTheCrawl(t *testing.T) {
	s := serve(t, map[string]answer{"/": page(strings.Repeat("x", 500))})
	c := crawler(s)
	c.MaxBytes = 100
	got, err := c.Crawl(context.Background(), Request{Start: s.URL})
	var r *sitesvc.RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "sites.crawl.max_bytes")
	assert.Nil(t, got, "a site too big for the bound is not half copied")
}

func TestCrawl_MaxBytesCountsAcrossPages(t *testing.T) {
	s := serve(t, map[string]answer{
		"/":  page(`<a href="/a">a</a>` + strings.Repeat("x", 60)),
		"/a": page(strings.Repeat("y", 60)),
	})
	c := crawler(s)
	c.MaxBytes = 100
	_, err := c.Crawl(context.Background(), Request{Start: s.URL})
	var r *sitesvc.RefusedError
	require.ErrorAs(t, err, &r)
}

func TestCrawl_MaxPagesBoundsTheWalk(t *testing.T) {
	s := serve(t, map[string]answer{
		"/":  page(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`),
		"/a": page("a"), "/b": page("b"), "/c": page("c"),
	})
	c := crawler(s)
	c.MaxPages = 2
	got, err := c.Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Pages)
	assert.Len(t, got.Files, 2)
	assert.Zero(t, s.count("/b"), "the walk stops at the bound")
	assert.Zero(t, s.count("/c"))
}

func TestCrawl_FormMapReplacesTheFormWithAPlatformFrame(t *testing.T) {
	s := serve(t, map[string]answer{
		"/contact": page(`<html><body><h1>Talk to us</h1><form action="https://cms.example/submit"><input name="email"><a href="/in-form">x</a></form><p>after</p></body></html>`),
		"/other":   page(`<form action="/kept"><input name="q"></form>`),
		"/":        page(`<a href="/contact">c</a><a href="/other">o</a>`),
	})
	got, err := crawler(s).Crawl(context.Background(), Request{
		Start: s.URL, FormMap: map[string]string{"/contact": "contact-us"}, PortalURL: "https://portal.example.com/",
	})
	require.NoError(t, err)
	byPath := files(got)
	contact := byPath["contact.html"]
	assert.Contains(t, contact, `<iframe src="https://portal.example.com/forms/contact-us"`)
	assert.NotContains(t, contact, "<form")
	assert.NotContains(t, contact, "cms.example", "nothing of the old form survives")
	assert.Contains(t, contact, "<h1>Talk to us</h1>", "the rest of the page is kept")
	assert.Contains(t, contact, "<p>after</p>")
	assert.Zero(t, s.count("/in-form"), "a link inside a replaced form is not walked")

	assert.Equal(t, `<form action="/kept"><input name="q"></form>`, byPath["other.html"], "a page not in the map is stored as it came")
}

func TestCrawl_FormMapRefusals(t *testing.T) {
	cases := map[string]map[string]string{
		"an endpoint that climbs":   {"/contact": "../admin"},
		"an endpoint with a slash":  {"/contact": "a/b"},
		"an endpoint with a quote":  {"/contact": `a"><script>`},
		"an empty endpoint":         {"/contact": ""},
		"an uppercase endpoint":     {"/contact": "Contact"},
		"a page that is not a path": {"contact": "contact-us"},
	}
	for name, formMap := range cases {
		t.Run(name, func(t *testing.T) {
			s := serve(t, map[string]answer{"/": page("x")})
			_, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL, FormMap: formMap, PortalURL: "https://portal.example.com"})
			var r *sitesvc.RefusedError
			require.ErrorAs(t, err, &r)
			assert.Contains(t, r.Msg, "form_map")
			assert.Zero(t, s.count("/"), "refused before anything is fetched")
		})
	}
}

func TestCrawl_RefusesAnAddressThatIsNotHTTP(t *testing.T) {
	c := &Crawler{Client: http.DefaultClient}
	for _, start := range []string{"", "ftp://example.com/", "example.com", "http://", "http://[bad"} {
		_, err := c.Crawl(context.Background(), Request{Start: start})
		var r *sitesvc.RefusedError
		require.ErrorAs(t, err, &r, start)
	}
}

func TestCrawl_RefusesAnAddressHoldingCredentials(t *testing.T) {
	c := &Crawler{Client: &http.Client{Transport: failing{}}, MaxPages: 10, MaxBytes: 100}
	for _, start := range []string{"https://user:s3cret@example.com/", "https://user@example.com/"} {
		_, err := c.Crawl(context.Background(), Request{Start: start})
		var r *sitesvc.RefusedError
		require.ErrorAs(t, err, &r, start)
		assert.NotContains(t, err.Error(), "s3cret", "the refusal does not repeat the address")
		assert.NotContains(t, err.Error(), "user@", "the refusal does not repeat the address")
	}
}

func TestCrawl_ARefusedAddressIsNotRepeated(t *testing.T) {
	c := &Crawler{Client: http.DefaultClient}
	_, err := c.Crawl(context.Background(), Request{Start: "ftp://example.com/?token=s3cret"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret")
}

func TestSourceOf_KeepsOnlySchemeHostAndPath(t *testing.T) {
	cases := map[string]string{
		"https://example.com/blog/":             "https://example.com/blog/",
		"https://example.com/blog?token=s3cret": "https://example.com/blog",
		"https://example.com/a#fragment":        "https://example.com/a",
		"https://user:s3cret@example.com/a":     "https://example.com/a",
		"http://[bad":                           "",
	}
	for in, want := range cases {
		assert.Equal(t, want, SourceOf(in), in)
	}
}

func TestCrawl_ProgressReportsAndCanStop(t *testing.T) {
	routes := map[string]answer{"/": page(`<a href="/a">a</a><a href="/b">b</a>`), "/a": page("a"), "/b": page("b")}

	t.Run("told after each address", func(t *testing.T) {
		s := serve(t, routes)
		var pages, known []int
		got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL, Progress: func(c sitesvc.CopyStats) bool {
			p, k := c.Pages, c.Known
			pages, known = append(pages, p), append(known, k)
			return false
		}})
		require.NoError(t, err)
		require.NotEmpty(t, pages)
		assert.Equal(t, got.Pages, pages[len(pages)-1], "the last report is the final count")
		assert.GreaterOrEqual(t, known[0], pages[0])
	})

	t.Run("stops when told to", func(t *testing.T) {
		s := serve(t, routes)
		calls := 0
		got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL, Progress: func(sitesvc.CopyStats) bool {
			calls++
			return true
		}})
		require.ErrorIs(t, err, ErrStopped)
		assert.Nil(t, got)
		assert.Equal(t, 1, calls)
		assert.Zero(t, s.count("/a"), "nothing after the stop was fetched")
	})
}

func TestCrawl_TheRequestsBoundsOverrideTheCrawlers(t *testing.T) {
	routes := map[string]answer{"/": page(`<a href="/a">a</a>`), "/a": page("a")}

	t.Run("pages", func(t *testing.T) {
		s := serve(t, routes)
		got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL, MaxPages: 1})
		require.NoError(t, err)
		assert.Equal(t, 1, got.Pages)
	})
	t.Run("bytes", func(t *testing.T) {
		s := serve(t, routes)
		_, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL, MaxBytes: 2})
		var r *sitesvc.RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, err.Error(), "the 2 bytes")
	})
}

func TestRedirected_EachReasonIsNamed(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   string
	}{
		{"no usable address", http.Header{}, "redirected with no usable address"},
		{"somewhere a release cannot point", http.Header{"Location": {"javascript:alert(1)"}}, "redirected somewhere a release cannot point"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &run{root: mustRoot(t, "https://example.com"), seen: map[string]bool{}}
			res := &http.Response{StatusCode: http.StatusFound, Header: tc.header, Request: &http.Request{URL: mustRoot(t, "https://example.com/r")}}
			r.redirected("/r", res)
			assert.Equal(t, []site.Skipped{{Address: "/r", Reason: tc.want}}, r.skipped)
		})
	}
}

func TestCrawl_APathAReleaseCannotHoldIsSkippedWithItsReason(t *testing.T) {
	s := serve(t, map[string]answer{"/": page(`<a href="/.htaccess">h</a>`), "/.htaccess": {status: http.StatusOK, ctype: "text/plain", body: "deny"}})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Contains(t, got.Skipped, site.Skipped{Address: "/.htaccess", Reason: "a release cannot hold a file at this path"})
}

func TestCrawl_AStartPathIsWhereTheWalkBegins(t *testing.T) {
	s := serve(t, map[string]answer{"/blog/": page("blog")})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL + "/blog/"})
	require.NoError(t, err)
	assert.Equal(t, "blog", files(got)["blog/index.html"])
	assert.Zero(t, s.count("/"))
}

func TestCrawl_NothingAnsweringIsAResultOfSkippedAddresses(t *testing.T) {
	c := &Crawler{Client: &http.Client{Transport: failing{}}, MaxPages: 10, MaxBytes: 100}
	got, err := c.Crawl(context.Background(), Request{Start: "https://example.com"})
	require.NoError(t, err)
	assert.Empty(t, got.Files)
	assert.Equal(t, []string{"/", "/sitemap.xml", "/robots.txt", "/favicon.ico"}, addresses(got.Skipped))
	for _, sk := range got.Skipped {
		assert.Equal(t, "the address did not answer, or is not one a copy may reach", sk.Reason)
		assert.NotContains(t, sk.Reason, "egress guard", "what the guard said stays out of the reason")
	}
}

func TestCrawl_ServerErrorsAreSkipped(t *testing.T) {
	s := serve(t, map[string]answer{"/": page(`<a href="/boom">b</a>`), "/boom": {status: http.StatusInternalServerError, body: "oops"}})
	got, err := crawler(s).Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Contains(t, got.Skipped, site.Skipped{Address: "/boom", Reason: "answered 500"})
	assert.NotContains(t, files(got), "boom.html")
}

func TestCrawl_ABodyThatBreaksOffIsSkipped(t *testing.T) {
	c := &Crawler{Client: &http.Client{Transport: brokenBodyTransport{}}, MaxPages: 10, MaxBytes: 100}
	got, err := c.Crawl(context.Background(), Request{Start: "https://example.com"})
	require.NoError(t, err)
	assert.Empty(t, got.Files)
	assert.Zero(t, got.Pages)
	assert.Contains(t, got.Skipped, site.Skipped{Address: "/", Reason: "the answer broke off"})
}

func TestCrawl_ACancelledContextSkipsRatherThanHangs(t *testing.T) {
	s := serve(t, map[string]answer{"/": page("x")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := crawler(s).Crawl(ctx, Request{Start: s.URL})
	require.NoError(t, err)
	assert.Empty(t, got.Files)
}

func TestNew(t *testing.T) {
	c, err := New(siteconfig.CrawlConfig{})
	require.NoError(t, err)
	assert.Equal(t, siteconfig.DefaultCrawlPages, c.MaxPages)
	assert.Equal(t, siteconfig.DefaultCrawlBytes, c.MaxBytes)

	c, err = New(siteconfig.CrawlConfig{MaxPages: 3, MaxBytes: 4})
	require.NoError(t, err)
	assert.Equal(t, 3, c.MaxPages)
	assert.Equal(t, int64(4), c.MaxBytes)

	_, err = New(siteconfig.CrawlConfig{AllowPrivateCIDRs: []string{"nope"}})
	require.ErrorContains(t, err, "egress guard")
}

// The client New builds records a redirect rather than following it, and goes
// through the guard: a private address is refused until it is admitted.
func TestNew_TheRealClientRecordsRedirectsAndHonorsTheGuard(t *testing.T) {
	s := serve(t, map[string]answer{
		"/":    page(`<a href="/old">o</a>`),
		"/old": {status: http.StatusMovedPermanently, location: "/new"},
		"/new": page("new"),
	})

	guarded, err := New(siteconfig.CrawlConfig{})
	require.NoError(t, err)
	got, err := guarded.Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Empty(t, got.Files, "a private address is not fetched")
	assert.Zero(t, s.count("/"))

	admitted, err := New(siteconfig.CrawlConfig{AllowPrivateCIDRs: []string{"127.0.0.0/8"}})
	require.NoError(t, err)
	got, err = admitted.Crawl(context.Background(), Request{Start: s.URL})
	require.NoError(t, err)
	assert.Equal(t, "/old /new 301\n", files(got)["_redirects"])
	assert.Equal(t, "new", files(got)["new.html"])
}

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("refused by the egress guard")
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("broke off") }
func (brokenBody) Close() error             { return nil }

type brokenBodyTransport struct{}

func (brokenBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {htmlType}}, Body: brokenBody{}}, nil
}

// An empty url() names nothing and is not walked.
func TestFirstNonEmpty(t *testing.T) {
	assert.Equal(t, "", firstNonEmpty([]string{"", "", ""}))
	assert.Equal(t, "b", firstNonEmpty([]string{"", "b", "c"}))
}
