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

	"github.com/txn2/mcp-data-platform/internal/contentviewer"
	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttiles"
	"github.com/txn2/mcp-data-platform/internal/platform/thumbworker"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
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
	return assemble(p, routes, contentviewer.TileEntryURL())
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
// cannot make the platform reach an address inside the deployment. Redirects
// go back to the page, which asks again and is checked again.
func NewRenderer(cfg *platform.Config) (*headless.Renderer, error) {
	guard, err := egressguard.New(nil)
	if err != nil {
		return nil, fmt.Errorf("building the renderer's egress guard: %w", err)
	}
	public := &http.Client{
		Transport:     guard.Transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return headless.New(cfg.Thumbnails.EffectiveRendererURL(), public), nil
}

// assemble is Build over the part of the platform it reads.
func assemble(p source, routes http.Handler, tileEntry string) *thumbworker.Worker {
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
	}
	if collections, ok := p.PortalCollectionStore().(thumbworker.CollectionWork); ok {
		deps.Collections = collections
	}
	if db := p.DB(); db != nil {
		deps.Scripts = scripttiles.NewPostgres(db)
	}
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
