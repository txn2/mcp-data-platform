package ui

import (
	"cmp"
	"io/fs"
	"net/http"
)

// bundledIcon is the mark the portal ships with (ui/public/images), the icon
// of a deployment that configures no logo. The portal's rail falls back to the
// same file (ui/src/lib/portalLogo.ts).
const bundledIcon = "dist/images/activity-svgrepo-com.svg"

// iconMaxAge is how long a browser keeps the icon before asking again.
const iconMaxAge = "public, max-age=3600"

// MountSiteFiles registers the two files a browser or crawler asks a site for
// without a session: robots.txt, which keeps search engines from indexing the
// portal, and /favicon.ico (Favicon), whose logo is the first of logos that is
// set.
func MountSiteFiles(mux *http.ServeMux, logos ...string) {
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
	mux.Handle("GET /favicon.ico", Favicon(cmp.Or(logos...)))
}

// Favicon answers /favicon.ico, the address a browser asks for a site's icon
// when the page names none and the address every server-rendered page and the
// portal shell name (#2460). It is served without a session: the share viewer
// is read signed out, and an icon request that answers 401 leaves the tab
// blank and logs an error on every page.
//
// A deployment's configured logo is the icon, by redirect, so the tab carries
// the deployment's mark before the portal's own script has run; otherwise the
// bundled mark is served. With neither (a build without the portal) the
// address is not found.
func Favicon(logoURL string) http.Handler {
	icon, _ := fs.ReadFile(distFS, bundledIcon) // nil, and not found, in a build without the portal
	return faviconHandler(logoURL, icon)
}

// faviconHandler serves logoURL by redirect, else icon, else not found.
func faviconHandler(logoURL string, icon []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", iconMaxAge)
		if logoURL != "" {
			http.Redirect(w, r, logoURL, http.StatusFound)
			return
		}
		if icon == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(icon)
	})
}
