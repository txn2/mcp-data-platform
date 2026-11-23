// Package thumbwire assembles the tile worker for the HTTP composition root
// (#1787): the headless renderer the worker draws in, the client a document's
// public references are fetched with, and the stores and buckets it claims
// from and records to.
//
// It takes the assembled mux as well as the platform, because the page a tile
// is drawn from loads the viewer's chunks and a document's references from the
// platform's own routes, answered in-process.
package thumbwire

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/httpserver/sitepages"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttiles"
	"github.com/txn2/mcp-data-platform/internal/platform/thumbworker"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile/tilepage"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// source is the part of the platform the worker is assembled from.
type source interface {
	Config() *platform.Config
	PortalAssetStore() portal.AssetStore
	PortalS3Client() portal.S3Client
	PortalContentRefStore() assetrefs.Store
	PortalCollectionStore() portal.CollectionStore
	ResourceStore() resource.Store
	ResourceS3Client() resource.S3Client
	DB() *sql.DB
}

// Build returns the tile worker, or nil when there is nothing to draw: no
// platform, tiles turned off, no tile page in this build, or no asset store
// that can hand out work. A nil worker's Start and Stop do nothing.
func Build(p *platform.Platform, routes http.Handler) *thumbworker.Worker {
	if p == nil {
		return nil
	}
	return assemble(p, routes, contentviewer.TileEntryURL(), sitePage(p))
}

// tileCacheBytes bounds the memory the tile page's gateway holds file bytes
// in: tiles are drawn a few at a time, and the serving layer keeps its own.
const tileCacheBytes = 32 << 20

// sitePage builds the page a site environment's tile is drawn from, nil on a
// deployment that serves no sites. The page is answered by a gateway of its
// own over the same store, objects and media the serving layer reads, so a
// tile shows the release as the environment's address serves it (#2681).
func sitePage(p *platform.Platform) thumbworker.SitePage {
	e := p.Endpoints()
	if e == nil || e.Sites == nil || !e.Sites.Serving() {
		return nil
	}
	rt := e.Sites
	objects, ok := p.PortalS3Client().(sitegw.Objects)
	if !ok {
		return nil
	}
	// A memory cache of its own and no disk (#2721): a file past what memory
	// holds is read by range, so a large one costs the part a page is
	// answered with, not its whole size.
	cfg := rt.Config
	cfg.Cache = siteconfig.CacheConfig{MemoryBytes: tileCacheBytes}
	// The application endpoints run through the server a site's own address
	// runs them through, as the site's owner (#2747); no traffic tally is
	// given, so a draw is never the site's traffic.
	gw := sitegw.New(sitegw.Deps{
		Store: rt.Store, Objects: objects, Bucket: p.Config().Portal.S3Bucket, Config: cfg, Live: rt.Live, Media: rt.Media,
		Pages: sitepages.ForTiles(p), NoDiskCache: true,
	})
	host := ""
	if u, err := url.Parse(p.Config().Portal.PublicBaseURL); err == nil {
		host = u.Hostname()
	}
	return tilepage.Release{Store: rt.Store, Server: gw, PlatformHost: host, Owner: sitepages.TileOwner(p)}.Page
}

// ScriptTiles is the reader a script's tile is served from (#1909): the tile
// worker's record over the bucket it stores tiles in. Nil (not a nil pointer)
// where there is no database or no portal storage, which leaves the route
// unmounted.
func ScriptTiles(p *platform.Platform) TileReader {
	if p == nil {
		return nil
	}
	return tileReader(p)
}

// TileReader reads one script's stored tile.
type TileReader interface {
	Tile(ctx context.Context, scriptID, variant string) (data []byte, current bool, err error)
}

// tileReader is ScriptTiles over the part of the platform it reads.
func tileReader(p source) TileReader {
	if p.DB() == nil || p.PortalS3Client() == nil {
		return nil
	}
	return scripttiles.NewReader(scripttiles.NewPostgres(p.DB()), p.PortalS3Client(), p.Config().Portal.S3Bucket)
}

// NewRenderer is the headless renderer the platform draws tiles and prints
// PDFs in (#1983), at the renderer address the thumbnails section names.
//
// A document may name an image or a font on a public host. The platform
// fetches it through the same guard the util connection uses, so a document
// cannot make the platform reach an address inside the deployment beyond the
// prefixes thumbnails.allow_private_cidrs names. Redirects go back to the
// page, which asks again and is checked again.
func NewRenderer(cfg *platform.Config) (*headless.Renderer, error) {
	guard, err := egressguard.New(cfg.Thumbnails.AllowPrivateCIDRs)
	if err != nil {
		return nil, fmt.Errorf("building the renderer's egress guard: %w", err)
	}
	public := outbound.NewClient(outbound.Options{Kind: outbound.KindRenderer, Base: guard.Transport()})
	return headless.New(cfg.Thumbnails.EffectiveRendererURL(), public), nil
}

// assemble is Build over the part of the platform it reads.
func assemble(p source, routes http.Handler, tileEntry string, page thumbworker.SitePage) *thumbworker.Worker {
	if !p.Config().Thumbnails.IsEnabled() {
		return nil
	}
	// A binary built without the UI has no page to draw a tile from. Claiming
	// work it cannot draw would record every document as a failure.
	if tileEntry == "" {
		slog.Warn("Thumbnails not drawn: this build embeds no tile page")
		return nil
	}
	assets, ok := p.PortalAssetStore().(thumbworker.AssetWork)
	blobs := p.PortalS3Client()
	if !ok || blobs == nil {
		return nil
	}
	renderer, err := NewRenderer(p.Config())
	if err != nil {
		slog.Warn("Thumbnails disabled", "error", err)
		return nil
	}
	cfg := p.Config()
	deps := thumbworker.Deps{
		Drawer:           renderer,
		Assets:           assets,
		Refs:             p.PortalContentRefStore(),
		AssetBlobs:       blobs,
		CollectionBucket: cfg.Portal.S3Bucket,
		CollectionPrefix: cfg.Portal.S3Prefix,
		Routes:           routes,
		TileEntryURL:     tileEntry,
		TileCSS:          contentviewer.CSS,
		DSN:              cfg.Database.DSN,
	}
	if collections, ok := p.PortalCollectionStore().(thumbworker.CollectionWork); ok {
		deps.Collections = collections
	}
	withDatabase(&deps, p.DB(), page)
	if resources, ok := p.ResourceStore().(resource.ThumbnailWork); ok && p.ResourceS3Client() != nil {
		deps.Resources = resources
		deps.ResourceBlobs = p.ResourceS3Client()
		deps.ResourceBucket = cfg.Resources.Managed.S3Bucket
	}
	// Config.Validate refused a section that does not tune at startup; this
	// is the same answer.
	tuning, err := cfg.Thumbnails.Tuning()
	if err != nil {
		slog.Warn("Thumbnails disabled", "error", err)
		return nil
	}
	slog.Info("Thumbnails drawn by the renderer", "renderer_url", cfg.Thumbnails.EffectiveRendererURL(), "concurrency", tuning.Concurrency)
	return thumbworker.New(tuning, deps)
}

// withDatabase adds what a database holds work for: each script's tile and,
// where sites are served, each site environment's tile drawn from the release
// it serves (#2658).
func withDatabase(deps *thumbworker.Deps, db *sql.DB, page thumbworker.SitePage) {
	if db == nil {
		return
	}
	deps.Scripts = scripttiles.NewPostgres(db)
	if page != nil {
		deps.Sites, deps.SitePage = sitetile.NewStore(db), page
	}
}
