package sitegw

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// TestServe_ARedirectDomainAnswersAPermanentRedirectToThePrimary pins #2687:
// path and query kept, 301 for GET and HEAD, 308 for anything else, a bounded
// Cache-Control, and no release read.
func TestServe_ARedirectDomainAnswersAPermanentRedirectToThePrimary(t *testing.T) {
	h := newHarness(Deps{Secure: true})
	h.store.added = append(h.store.added, "www.example.com")
	h.store.hosts["www.example.com"] = &site.Route{
		SiteID: testSiteID, Site: "marketing", Environment: "prod",
		Hostname: "www.example.com", PrimaryHost: "example.com",
	}

	rec := h.get("www.example.com", "/a/b?c=1")
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "https://example.com/a/b?c=1", rec.Header().Get(headerLoc))
	assert.Equal(t, primaryRedirectMaxAge, rec.Header().Get("Cache-Control"))

	rec = h.do(http.MethodHead, "www.example.com", "/", nil)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "https://example.com/", rec.Header().Get(headerLoc))

	rec = h.do(http.MethodPost, "www.example.com", "/form%20x?y=2", nil)
	assert.Equal(t, http.StatusPermanentRedirect, rec.Code)
	assert.Equal(t, "https://example.com/form%20x?y=2", rec.Header().Get(headerLoc))

	// A path that looks like another host stays a path on the primary.
	rec = h.get("www.example.com", "//evil.example/x")
	assert.Equal(t, "https://example.com//evil.example/x", rec.Header().Get(headerLoc))
	assert.Equal(t, 0, h.store.fileReads, "a redirect domain reads no release")

	// A development stack answering in the clear redirects in the clear.
	plain := newHarness(Deps{})
	plain.store.added = append(plain.store.added, "www.example.com")
	plain.store.hosts["www.example.com"] = h.store.hosts["www.example.com"]
	assert.Equal(t, "http://example.com/a", plain.get("www.example.com", "/a").Header().Get(headerLoc))
}

// TestServe_ARedirectDomainOfAPrivatePrimaryAsksForTheSignIn: when the
// primary answers only those who sign in, so does the domain redirecting to
// it, and with no guard it never redirects.
func TestServe_ARedirectDomainOfAPrivatePrimaryAsksForTheSignIn(t *testing.T) {
	h := newHarness(Deps{})
	h.store.added = append(h.store.added, "www.example.com")
	h.store.hosts["www.example.com"] = &site.Route{
		SiteID: testSiteID, Site: "marketing", Environment: "prod",
		Hostname: "www.example.com", PrimaryHost: "example.com", Private: true,
	}
	rec := h.get("www.example.com", "/")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, rec.Header().Get(headerLoc))
}
