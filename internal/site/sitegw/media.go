package sitegw

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// Media is what a site's mounts serve (#2591): the folders mounted at URL
// prefixes, and the file a mounted path names.
type Media interface {
	Mounts(ctx context.Context, siteID string) ([]site.Mount, error)
	// Find is the file at rel under a mount, nil when there is none.
	Find(ctx context.Context, m site.Mount, rel string) (*MediaFile, error)
}

// MediaFile is one file a mount serves.
type MediaFile struct {
	Name        string
	ContentType string
	ModTime     time.Time
	Object      blobserve.Object
}

// mediaEntry is a site's mounts as this replica last read them.
type mediaEntry struct {
	mounts []site.Mount
	until  time.Time
}

// mountsOf is a site's mounts, read again every routeTTL like a route.
func (g *Gateway) mountsOf(ctx context.Context, siteID string) ([]site.Mount, error) {
	g.mu.Lock()
	if e, ok := g.media[siteID]; ok && time.Now().Before(e.until) {
		g.mu.Unlock()
		return e.mounts, nil
	}
	g.mu.Unlock()
	mounts, err := g.deps.Media.Mounts(ctx, siteID)
	if err != nil {
		return nil, err //nolint:wrapcheck // logged by the caller
	}
	g.mu.Lock()
	g.media[siteID] = mediaEntry{mounts: mounts, until: time.Now().Add(routeTTL)}
	g.mu.Unlock()
	return mounts, nil
}

// serveMedia answers a path a mount serves, and reports whether it did. A
// file is served by range with its validators, an active type (HTML, SVG,
// JavaScript, XML) as a download, so a media folder cannot become a second,
// unreviewed site. A path no mount serves, or no file at it, is left to the
// redirects and the not-found page.
func (g *Gateway) serveMedia(w http.ResponseWriter, r *http.Request, route *site.Route, p string) bool {
	if g.deps.Media == nil {
		return false
	}
	mounts, err := g.mountsOf(r.Context(), route.SiteID)
	if err != nil {
		slog.ErrorContext(r.Context(), "sites: reading a site's media failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		return false
	}
	m, rel, ok := site.MountFor(mounts, p)
	if !ok {
		return false
	}
	f, err := g.deps.Media.Find(r.Context(), m, rel)
	if err != nil {
		g.unreadable(w, r, err)
		return true
	}
	if f == nil {
		return false
	}
	cache := cacheRevalidate
	if route.Private {
		cache = cachePrivate
	}
	w.Header().Set(hdrCache, cache)
	if !blobserve.ServeRanged(w, r, blobserve.Options{Name: f.Name, ContentType: f.ContentType, ModTime: f.ModTime}, f.Object) {
		w.Header().Del(hdrCache)
		return false
	}
	return true
}
