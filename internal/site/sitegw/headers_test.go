package sitegw

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

// TestServeSite_ReleaseHeaders is #2557 through the gateway: a release's
// _headers policy on every file under its path, the framing default beside a
// policy that sets no framing, and a rollback to a release without the file
// served the default alone.
func TestServeSite_ReleaseHeaders(t *testing.T) {
	h := newHarness(Deps{})
	withPolicy := h.addRelease(2, nil,
		testFile{path: indexFile, body: "home", contentType: ctHTML},
		testFile{path: "style.css", body: "body{}", contentType: ctCSS})
	withPolicy.Headers = []site.HeaderRule{{Path: "/*", Headers: []site.Header{{Name: "Content-Security-Policy", Value: "default-src 'self'"}, {Name: "Permissions-Policy", Value: "camera=()"}}}}
	h.mapHost(withPolicy)
	for _, p := range []string{"/", "/style.css", "/missing"} {
		rec := h.get(testSiteHost, p)
		assert.ElementsMatch(t, []string{"default-src 'self'", site.FramingDefault}, rec.Header().Values("Content-Security-Policy"), p)
		assert.Equal(t, "camera=()", rec.Header().Get("Permissions-Policy"), p)
	}

	framed := h.addRelease(3, nil, testFile{path: indexFile, body: "home", contentType: ctHTML})
	framed.Headers = []site.HeaderRule{{Path: "/*", Headers: []site.Header{{Name: "Content-Security-Policy", Value: "frame-ancestors https://partner.example"}}}}
	h.repoint(testSiteHost, framed)
	h.gateway.Invalidate(testSiteHost)
	assert.Equal(t, []string{"frame-ancestors https://partner.example"}, h.get(testSiteHost, "/").Header().Values("Content-Security-Policy"),
		"a release declaring its own framing is served only that")

	plain := h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML})
	h.repoint(testSiteHost, plain)
	h.gateway.Invalidate(testSiteHost)
	rec := h.get(testSiteHost, "/")
	assert.Equal(t, []string{site.FramingDefault}, rec.Header().Values("Content-Security-Policy"), "rolled back to a release without _headers: the default")
	assert.Empty(t, rec.Header().Get("Permissions-Policy"))

	h.store.hosts[testSiteHost] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "prod"}
	h.gateway.Invalidate(testSiteHost)
	assert.Equal(t, []string{site.FramingDefault}, h.get(testSiteHost, "/").Header().Values("Content-Security-Policy"), "no release yet")
}

// TestServe_StrictTransport is HSTS (#2557): on an https deployment with it
// turned on, every answer the gateway gives carries it; turned off, or over
// http, none does.
func TestServe_StrictTransport(t *testing.T) {
	on := siteconfig.Config{TLS: siteconfig.TLSConfig{HSTS: siteconfig.HSTSConfig{Enabled: true, IncludeSubdomains: true}}}
	h := newHarness(Deps{Config: on, Secure: true})
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	assert.Equal(t, "max-age=31536000; includeSubDomains", h.get(testSiteHost, "/").Header().Get(hdrHSTS))
	assert.Equal(t, "max-age=31536000; includeSubDomains", h.get("nothing."+testPagesDomain, "/").Header().Get(hdrHSTS), "a not-published answer too")

	insecure := newHarness(Deps{Config: on})
	insecure.mapHost(insecure.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	assert.Empty(t, insecure.get(testSiteHost, "/").Header().Get(hdrHSTS), "never over http")

	off := newHarness(Deps{Secure: true})
	off.mapHost(off.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	rec := off.get(testSiteHost, "/")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(hdrHSTS), "off by default")
}
