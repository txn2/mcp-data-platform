package sitegw

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// chunkPrefix is the path a compiled entry's chunks are served under (#2778).
const chunkPrefix = pathSep + sitebuild.ChunkDir + pathSep

// earlierReleases is how many releases before the current one a missing chunk
// is looked for in: enough for a page left open across a few releases.
const earlierReleases = 5

// releaseLister lists a site's releases, newest first; the PostgreSQL store is
// one.
type releaseLister interface {
	Releases(ctx context.Context, siteID string) ([]site.Release, error)
}

// earlierChunk is a chunk the current release does not hold, from one of the
// releases before it (#2778): a page loaded from release N asks for N's chunks
// after N+1 is published, and a chunk's name is a hash of its content, so the
// same name is the same bytes in whichever release holds it.
func (g *Gateway) earlierChunk(ctx context.Context, route *site.Route, p string) (site.File, bool) {
	if !strings.HasPrefix(p, chunkPrefix) || route.Release == nil {
		return site.File{}, false
	}
	lister, ok := g.deps.Store.(releaseLister)
	if !ok {
		return site.File{}, false
	}
	releases, err := lister.Releases(ctx, route.SiteID)
	if err != nil {
		return site.File{}, false
	}
	name := strings.TrimPrefix(p, pathSep)
	for _, rel := range before(releases, route.Release.Number) {
		if m, err := g.manifestOf(ctx, &rel); err == nil {
			if f, ok := m.files[name]; ok {
				return f, true
			}
		}
	}
	return site.File{}, false
}

// before are the releases kept before number, newest first, at most
// earlierReleases of them.
func before(releases []site.Release, number int) []site.Release {
	out := make([]site.Release, 0, earlierReleases)
	for _, rel := range releases {
		if rel.Number < number && !rel.Pruned {
			out = append(out, rel)
		}
	}
	slices.SortFunc(out, func(a, b site.Release) int { return b.Number - a.Number })
	return out[:min(len(out), earlierReleases)]
}

// hiddenMap reports a source map a release keeps for its managers alone
// (#2784): served only on a private environment's own address, to someone
// signed in there who manages the site.
func (g *Gateway) hiddenMap(r *http.Request, route *site.Route, p string, viewer *epform.Viewer) bool {
	if route.Release == nil || !route.Release.PrivateMaps || !strings.HasSuffix(p, ".map") {
		return false
	}
	if viewer == nil || g.deps.Access == nil || g.deps.Access.MayManage == nil || viewer.EmailUnverified {
		return true
	}
	persona := g.deps.Access.livePersona(*viewer)
	if g.deps.Access.IsAdmin != nil && g.deps.Access.IsAdmin(persona) {
		return false
	}
	return !g.deps.Access.MayManage(r.Context(), route.SiteID, viewer.Email)
}
