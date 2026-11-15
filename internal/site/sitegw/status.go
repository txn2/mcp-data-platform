package sitegw

import (
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitestatus"
)

// hdrRetryAfter is the header an offline answer says when to come back with.
const hdrRetryAfter = "Retry-After"

// serveStatus answers a request to an environment that is offline or
// redirecting (#2631, #2632), in place of its release and its endpoints, and
// reports whether it answered. It runs after the access check, so on a
// private address only someone who may open the environment learns its
// status or where it points.
func (g *Gateway) serveStatus(w http.ResponseWriter, r *http.Request, route *site.Route) bool {
	switch route.Status.Status {
	case site.StatusOffline:
		serveOffline(w, r, route, g.now())
		return true
	case site.StatusRedirect:
		target, err := url.Parse(route.Status.RedirectTo)
		if err != nil || target.Scheme != "https" || target.Host == "" {
			// Checked when it was set; a row that no longer parses is
			// answered as unavailable rather than followed.
			serveOffline(w, r, route, g.now())
			return true
		}
		h := w.Header()
		h.Set(hdrCache, noStore)
		h.Set(hdrRobots, noIndex)
		h.Set("Location", sitestatus.RedirectLocation(target, route.Status.KeepPath, r.URL.Path, r.URL.RawQuery))
		w.WriteHeader(http.StatusFound)
		return true
	}
	return false
}

// now is the gateway's clock.
func (g *Gateway) now() time.Time {
	if g.deps.Now != nil {
		return g.deps.Now()
	}
	return time.Now()
}

// serveOffline answers 503 with the platform's offline page: the site's name
// and the message, as text. Retry-After is sent while the expected-back time
// is ahead; a 503 tells a search engine the outage is temporary.
func serveOffline(w http.ResponseWriter, r *http.Request, route *site.Route, now time.Time) {
	h := w.Header()
	h.Set(hdrRobots, noIndex)
	h.Set(hdrNosniff, nosniff)
	if after := sitestatus.RetryAfter(route.Status.BackAt, now); after != "" {
		h.Set(hdrRetryAfter, after)
	}
	view := offlineView{Site: route.Site, Message: route.Status.Message}
	if view.Message == "" {
		view.Message = sitestatus.DefaultOfflineMessage
	}
	if b := route.Status.BackAt; b != nil && b.After(now) {
		view.BackAt = b.UTC().Format("Mon 2 Jan 2006, 15:04 UTC")
	}
	(*Access)(nil).page(w, r, http.StatusServiceUnavailable, view)
}

// offlineView is what the offline page shows. Every field is written as text
// by html/template: no markup or link comes from the message.
type offlineView struct{ Site, Message, BackAt string }

var offlinePage = template.Must(template.New("offline").Parse(pageHead + `<style>.msg{white-space:pre-line;color:inherit}</style>` +
	`<title>{{.Site}} is offline</title>` + pageOpen +
	`<h1>{{.Site}} is offline</h1><p class="msg">{{.Message}}</p>` +
	`{{if .BackAt}}<p>Expected back {{.BackAt}}</p>{{end}}` + pageClose))
