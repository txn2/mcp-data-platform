package sitegw

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitestatus"
)

// statusHarness maps a public domain to prod, serving a release, with the
// clock fixed.
func statusHarness(t *testing.T, st site.EnvironmentStatus) *harness {
	t.Helper()
	now := time.Date(2026, 11, 14, 13, 0, 0, 0, time.UTC)
	h := newHarness(Deps{Now: func() time.Time { return now }})
	rel := h.addRelease(1, nil,
		testFile{path: indexFile, body: "RELEASE BYTES", contentType: ctHTML},
		testFile{path: "pricing/index.html", body: "RELEASE BYTES", contentType: ctHTML},
		testFile{path: "img/logo.png", body: "PNG", contentType: "image/png"})
	h.store.hosts[testSiteHost] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "prod", Release: rel, Hostname: testSiteHost, Status: st}
	h.store.added = append(h.store.added, testSiteHost)
	h.gateway.Invalidate(testSiteHost)
	return h
}

// TestServe_AnOfflineEnvironmentAnswers503 pins #2631: every path, GET and
// HEAD, answers 503 with the message as text, Retry-After while the time is
// ahead, no-store and noindex, and none of the release.
func TestServe_AnOfflineEnvironmentAnswers503(t *testing.T) {
	back := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC)
	h := statusHarness(t, site.EnvironmentStatus{Status: site.StatusOffline, Message: "Back at 2pm\n<script>alert(1)</script>", BackAt: &back})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, p := range []string{"/", "/pricing/", "/img/logo.png", "/endpoints/order"} {
			rec := h.do(method, testSiteHost, p, nil)
			require.Equal(t, http.StatusServiceUnavailable, rec.Code, method+" "+p)
			assert.Equal(t, "3600", rec.Header().Get(hdrRetryAfter))
			assert.Equal(t, noStore, rec.Header().Get(hdrCache))
			assert.Equal(t, noIndex, rec.Header().Get(hdrRobots))
			assert.NotContains(t, rec.Body.String(), "RELEASE BYTES")
			if method == http.MethodGet {
				body := rec.Body.String()
				assert.Contains(t, body, "Back at 2pm")
				assert.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;", "the message is text")
				assert.NotContains(t, strings.ToLower(body), "<script", "the page holds no script element")
				assert.Contains(t, body, "marketing is offline")
				assert.Contains(t, body, "Expected back Sat 14 Nov 2026, 14:00 UTC")
			} else {
				assert.Empty(t, rec.Body.String())
			}
		}
	}

	past := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	h = statusHarness(t, site.EnvironmentStatus{Status: site.StatusOffline, BackAt: &past})
	rec := h.get(testSiteHost, "/")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Empty(t, rec.Header().Get(hdrRetryAfter), "a time that has passed sends no Retry-After")
	assert.Contains(t, rec.Body.String(), sitestatus.DefaultOfflineMessage)
	assert.NotContains(t, rec.Body.String(), "Expected back")
}

// TestServe_ARedirectingEnvironmentAnswers302 pins #2632.
func TestServe_ARedirectingEnvironmentAnswers302(t *testing.T) {
	h := statusHarness(t, site.EnvironmentStatus{Status: site.StatusRedirect, RedirectTo: "https://status.example.com"})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := h.do(method, testSiteHost, "/pricing/?a=1", nil)
		require.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, "https://status.example.com", rec.Header().Get(headerLoc))
		assert.Equal(t, noStore, rec.Header().Get(hdrCache))
	}
	h = statusHarness(t, site.EnvironmentStatus{Status: site.StatusRedirect, RedirectTo: "https://status.example.com", KeepPath: true})
	assert.Equal(t, "https://status.example.com/pricing/?a=1", h.get(testSiteHost, "/pricing/?a=1").Header().Get(headerLoc))
	assert.Equal(t, "https://status.example.com/evil.example/x", h.get(testSiteHost, "//evil.example/x").Header().Get(headerLoc))

	h = statusHarness(t, site.EnvironmentStatus{Status: site.StatusRedirect, RedirectTo: "javascript:alert(1)"})
	assert.Equal(t, http.StatusServiceUnavailable, h.get(testSiteHost, "/").Code, "a stored target that is not https is never followed")
}

// TestServe_TheAccessCheckRunsBeforeTheStatus: on a private address someone
// not signed in is sent to sign in, never shown the message or the target;
// someone who may open the environment sees the offline page.
func TestServe_TheAccessCheckRunsBeforeTheStatus(t *testing.T) {
	ah := newAccessHarness()
	ah.store.own["marketing"].Status = site.EnvironmentStatus{Status: site.StatusRedirect, RedirectTo: "https://secret-target.example"}
	rec := ah.open("/")
	assertGoesToSignIn(t, ah, rec, "/")
	assert.NotContains(t, rec.Header().Get(headerLoc), "secret-target")
	assert.NotContains(t, rec.Body.String(), "secret-target")

	ah.store.own["marketing"].Status = site.EnvironmentStatus{Status: site.StatusOffline, Message: "Planned work"}
	ah.gateway.Invalidate("marketing")
	rec = ah.open("/")
	assert.NotEqual(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Planned work")

	rec = ah.open("/", ah.cookieFor(viewerOf(ownerMail)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "Planned work")
	assertNoRelease(t, rec)
}
