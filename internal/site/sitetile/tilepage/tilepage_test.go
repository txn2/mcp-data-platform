package tilepage

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// answer is one canned answer of the fake gateway.
type answer struct {
	status int
	ctype  string
	body   string
	header map[string]string
}

// gateway is a fake site gateway: paths it answers for the release, and the
// site each hostname serves.
type gateway struct {
	paths map[string]answer
	hosts map[string]string
	// asked is every path Tile was asked for, with the release it was asked
	// of.
	asked []string
}

func (g *gateway) Tile(w http.ResponseWriter, r *http.Request, route *site.Route) {
	g.asked = append(g.asked, route.SiteID+":"+r.URL.RequestURI())
	a, ok := g.paths[r.URL.Path]
	if !ok {
		a = answer{status: http.StatusNotFound, ctype: "text/html", body: "<404.html>"}
	}
	for k, v := range a.header {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", a.ctype)
	w.WriteHeader(a.status)
	_, _ = w.Write([]byte(a.body))
}

func (g *gateway) SiteOf(_ context.Context, host string) (string, bool) {
	id, ok := g.hosts[host]
	return id, ok
}

func newGateway() *gateway {
	return &gateway{
		paths: map[string]answer{
			"/":          {status: http.StatusOK, ctype: "text/html; charset=utf-8", body: "<index.html>"},
			"/app.js":    {status: http.StatusOK, ctype: "text/javascript", body: "<app.js>"},
			"/data.json": {status: http.StatusOK, ctype: "application/json", body: `{"ok":true}`},
			"/old":       {status: http.StatusMovedPermanently, ctype: "text/html", header: map[string]string{"Location": "/new/"}},
		},
		hosts: map[string]string{
			"shop.pages.example.com": "s1", "shop.example.com": "s1",
			"other.pages.example.com": "s2", "app.pages.example.com": "",
		},
	}
}

// releases is one release.
type releases struct{ err error }

func (r releases) Release(_ context.Context, siteID string, n int) (*site.Release, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &site.Release{ID: "rel-" + siteID, Number: n}, nil
}

func platformRoutes(p string, _ http.Header) (headless.File, bool) {
	if p == "/portal/vendor/lib.css" {
		return headless.File{Body: []byte("<lib.css>"), ContentType: "text/css"}, true
	}
	return headless.File{}, false
}

func page(t *testing.T, g *gateway) headless.Page {
	t.Helper()
	p, err := Release{Store: releases{}, Server: g, PlatformHost: "platform.example.com"}.Page(context.Background(), "s1", 3, platformRoutes)
	require.NoError(t, err)
	return p
}

// TestPage_OwnOriginIsTheReleaseAsItsAddressServesIt: the document and every
// file of the page's origin are the gateway's answers for the release, its
// redirects and not-found page included, and the page waits for a quiet
// network.
func TestPage_OwnOriginIsTheReleaseAsItsAddressServesIt(t *testing.T) {
	g := newGateway()
	p := page(t, g)
	assert.Equal(t, "<index.html>", string(p.Document))
	assert.Positive(t, p.Quiet, "a page that draws from what it fetches is waited on")
	assert.Greater(t, p.QuietMax, p.Quiet)
	assert.True(t, p.RequireSubresources, "a public script that did not arrive is retried, not drawn without")

	f, ok := p.Files("/data.json", nil)
	require.True(t, ok)
	assert.Equal(t, `{"ok":true}`, string(f.Body))
	assert.Equal(t, "application/json", f.ContentType)

	f, ok = p.Files("/old", nil)
	require.True(t, ok)
	assert.Equal(t, http.StatusMovedPermanently, f.Status)
	assert.Equal(t, "/new/", f.Header["Location"], "a redirect reaches the page with where it goes")

	f, ok = p.Files("/missing.js", nil)
	require.True(t, ok, "every path is the gateway's to answer")
	assert.Equal(t, http.StatusNotFound, f.Status)
	assert.Equal(t, "<404.html>", string(f.Body))
	assert.Contains(t, g.asked, "s1:/data.json")
}

// TestPage_OtherHostnames: the same site's other addresses are the release,
// the platform's public routes are answered in process, another site or an
// application's origin is not found, and anything else is a public fetch.
func TestPage_OtherHostnames(t *testing.T) {
	g := newGateway()
	p := page(t, g)
	for _, tc := range []struct {
		url    string
		claim  bool
		status int
		body   string
	}{
		{"https://shop.example.com/app.js", true, http.StatusOK, "<app.js>"},
		{"https://shop.pages.example.com/data.json?v=2", true, http.StatusOK, `{"ok":true}`},
		{"https://platform.example.com/portal/vendor/lib.css", true, http.StatusOK, "<lib.css>"},
		{"https://platform.example.com/api/v1/admin/users", true, http.StatusNotFound, ""},
		{"https://other.pages.example.com/x.js", true, http.StatusNotFound, ""},
		{"https://app.pages.example.com/endpoints/x", true, http.StatusNotFound, ""},
		{"https://cdn.jsdelivr.net/npm/react@18.3.1/+esm", false, 0, ""},
		{"https://fonts.googleapis.com/css2?family=Archivo", false, 0, ""},
	} {
		u, err := url.Parse(tc.url)
		require.NoError(t, err)
		f, ok := p.Substitute(u, false)
		require.Equal(t, tc.claim, ok, tc.url)
		if !ok {
			continue
		}
		status := f.Status
		if status == 0 {
			status = http.StatusOK
		}
		assert.Equal(t, tc.status, status, tc.url)
		if tc.body != "" {
			assert.Equal(t, tc.body, string(f.Body), tc.url)
		}
	}
	assert.Contains(t, g.asked, "s1:/data.json?v=2", "the query reaches the gateway")
	own, _ := url.Parse("http://t-x.render.invalid/app.js")
	_, ok := p.Substitute(own, true)
	assert.False(t, ok, "the page's own origin is Files'")
}

// TestReleasePage_Refusals: no server, a missing release and a release with
// no page at its root refuse.
func TestReleasePage_Refusals(t *testing.T) {
	_, err := Release{}.Page(context.Background(), "s1", 1, nil)
	require.Error(t, err)
	_, err = Release{Store: releases{err: errors.New("gone")}, Server: newGateway()}.Page(context.Background(), "s1", 1, nil)
	require.Error(t, err)

	noIndex := newGateway()
	delete(noIndex.paths, "/")
	_, err = Release{Store: releases{}, Server: noIndex}.Page(context.Background(), "s1", 1, nil)
	require.ErrorIs(t, err, ErrNoDocument)

	notHTML := newGateway()
	notHTML.paths["/"] = answer{status: http.StatusOK, ctype: "application/json", body: "{}"}
	_, err = Release{Store: releases{}, Server: notHTML}.Page(context.Background(), "s1", 1, nil)
	require.ErrorIs(t, err, ErrNoDocument)
}

// TestPage_NoPlatformRoutes: with no platform routes handed in, the
// platform's hostname is not found rather than fetched from outside.
func TestPage_NoPlatformRoutes(t *testing.T) {
	p, err := Release{Store: releases{}, Server: newGateway(), PlatformHost: "platform.example.com"}.Page(context.Background(), "s1", 1, nil)
	require.NoError(t, err)
	u, _ := url.Parse("https://platform.example.com/portal/vendor/lib.css")
	f, ok := p.Substitute(u, false)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, f.Status)
}
