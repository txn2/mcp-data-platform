// Package mapwire assembles the maps service (#2068) from the platform: the
// settings and region stores, a bucket client per S3 connection the settings
// name, the outbound client a fetch reads its source through, and the refresh
// of the maps knowledge page when what it lists changes.
package mapwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/admin/mapsapi"
	"github.com/txn2/mcp-data-platform/internal/httpserver/maphttp"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/maps"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgebuiltin"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcelayer"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// fetchTimeout bounds one request to the source build. A read is at most
// 16 MiB; a build host that stalls longer than this is retried.
const fetchTimeout = 5 * time.Minute

// Maps is the assembled maps surface: the service, and the platform its
// admin routes are mounted for. A nil Maps (no database) mounts and runs
// nothing.
type Maps struct {
	svc *maps.Service
	p   *platform.Platform
}

// Build returns the maps surface, or nil without a database.
func Build(p *platform.Platform) *Maps {
	if p == nil || p.DB() == nil {
		return nil
	}
	cfg := p.Config()
	return &Maps{p: p, svc: assemble(p.DB(), cfg.Toolkits, cfg.Resources.Managed.S3Connection,
		cfg.Resources.Managed.S3Bucket, p.PortalKnowledgePageStore())}
}

// Mount registers the public archive routes and, when the admin API is on,
// the settings routes behind its authentication. Both are registered ahead of
// the portal UI's catch-all by the caller.
func (m *Maps) Mount(mux *http.ServeMux, adminAuth func() func(http.Handler) http.Handler, author func(*http.Request) string) {
	if m == nil {
		return
	}
	maphttp.Mount(mux, m.svc)
	if !m.p.Config().Admin.IsEnabled() {
		return
	}
	mapsapi.Register(mux, adminAuth(), mapsapi.Config{Service: m.svc, BucketOf: BucketOf(m.p), Author: author})
	slog.Info("Maps settings admin API enabled on /api/v1/admin/settings/maps")
}

// Start runs the fetch loop.
func (m *Maps) Start(ctx context.Context) {
	if m != nil {
		m.svc.Start(ctx)
	}
}

// Stop ends the fetch loop.
func (m *Maps) Stop() {
	if m != nil {
		m.svc.Stop()
	}
}

// assemble builds the service from what Build reads off the platform.
func assemble(db *sql.DB, toolkits map[string]any, defaultConn, defaultBucket string,
	pages knowledgepage.Store,
) *maps.Service {
	buckets := &bucketCache{
		toolkits: toolkits, defaultConn: defaultConn, defaultBucket: defaultBucket,
		clients: map[string]*s3adapter.ClientAdapter{},
	}
	reader := maps.NewReader(db)
	return maps.New(maps.Deps{
		DB: db, Settings: reader.Settings, Regions: reader.Regions, Open: buckets.open,
		Client: outbound.NewClient(outbound.Options{
			Kind: outbound.KindMaps, Timeout: fetchTimeout, CheckRedirect: outbound.FollowRedirects,
		}),
		Changed: func(ctx context.Context) {
			if err := knowledgebuiltin.Reconcile(context.WithoutCancel(ctx), pages, reader); err != nil {
				slog.WarnContext(ctx, "maps: refreshing the maps knowledge page",
					"error", logsan.SanitizeForLog(err.Error()))
			}
		},
	})
}

// BucketOf names the bucket archives go to under s: its own, or the
// managed-resources bucket.
func BucketOf(p *platform.Platform) maps.ResolvedBucket {
	return bucketOrDefault(p.Config().Resources.Managed.S3Bucket)
}

func bucketOrDefault(def string) maps.ResolvedBucket {
	return func(s maps.Settings) string {
		if s.Bucket != "" {
			return s.Bucket
		}
		return def
	}
}

// The refusals of a bucket that cannot be opened.
var (
	errNoBucket     = errors.New("no bucket is named, and managed resources name none")
	errNoConnection = errors.New("no S3 connection is configured")
)

// bucketCache opens one client per S3 connection and keeps it.
type bucketCache struct {
	toolkits      map[string]any
	defaultConn   string
	defaultBucket string

	mu      sync.Mutex
	clients map[string]*s3adapter.ClientAdapter
}

func (b *bucketCache) open(_ context.Context, s maps.Settings) (maps.Bucket, error) {
	conn := s.S3Connection
	if conn == "" {
		conn = b.defaultConn
	}
	bucket := s.Bucket
	if bucket == "" {
		bucket = b.defaultBucket
	}
	if bucket == "" {
		return maps.Bucket{}, errNoBucket
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.clients[conn]; ok {
		return maps.Bucket{Objects: c, Name: bucket}, nil
	}
	client, err := resourcelayer.OpenS3(resourcelayer.Config{S3Connection: conn, Toolkits: b.toolkits})
	if err != nil {
		return maps.Bucket{}, fmt.Errorf("opening S3 connection %q: %w", conn, err)
	}
	if client == nil {
		return maps.Bucket{}, errNoConnection
	}
	c := s3adapter.NewFor(client, observability.StoragePurposeMaps)
	b.clients[conn] = c
	return maps.Bucket{Objects: c, Name: bucket}, nil
}
