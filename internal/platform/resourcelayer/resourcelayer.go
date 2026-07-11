// Package resourcelayer assembles the managed-resources layer behind one Handle:
// the Postgres-backed resource store for human-uploaded reference material, the
// S3 blob client that holds the file bytes, and the MCP-server registration that
// makes each resource visible in the SDK's native resources/list.
//
// Construction takes explicit inputs — a *sql.DB and the resolved
// managed-resources config (the S3 connection name, bucket, URI scheme, and the
// toolkits config map used to resolve a default S3 instance) — so the subsystem
// is constructible and testable without a Platform. It imports pkg/resource,
// pkg/platform/toolkitcfg, the mcp-s3 client + its resource adapter, and the MCP
// SDK, never pkg/platform. The *sql.DB is a shared foundation owned by the
// caller and passed in.
//
// The MCP server is NOT captured at construction: the store must exist before
// the caller wires the resources/read middleware, but the server is created
// later in the caller's setup, so Register / Unregister / LoadAll take the
// *mcp.Server per call (the caller passes the server it owns at the moment it
// registers, after the server exists). A snapshot at construction would freeze a
// nil server and silently skip every registration.
//
// New returns (nil, nil) when db is nil: managed resources need a database, so a
// no-DB deployment gets the nil Handle and every accessor and mutation degrades
// to a no-op. Otherwise it builds the store and — when an S3 connection resolves
// — the blob client, returning an error only when the referenced connection is
// missing or its client fails to build. The layer owns no background goroutine,
// so it needs no Stop/Close; the caller closes the shared *sql.DB.
package resourcelayer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/platform/toolkitcfg"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// cfgKeyInstances is the config map key for toolkit instances, matched when
// resolving the default S3 instance for blob storage.
const cfgKeyInstances = "instances"

// logKeyCount is the slog key for item counts in log messages.
const logKeyCount = "count"

// Config carries the resolved managed-resources values the owner needs to
// assemble the layer. The caller translates its own config into this shape so
// this package stays free of the platform's config types and defaulting rules.
type Config struct {
	// S3Connection is the explicit S3 toolkit instance name for blob storage.
	// Empty falls back to the default/first S3 instance resolved from Toolkits.
	S3Connection string
	// S3Bucket is the bucket managed resources are stored in (logged at startup).
	S3Bucket string
	// URIScheme is the URI prefix for resource URIs; empty selects the resource
	// package default.
	URIScheme string
	// Toolkits is the raw toolkits config map, walked to resolve a default S3
	// instance when S3Connection is empty.
	Toolkits map[string]any
}

// Handle owns the assembled managed-resources layer: the resource store and the
// S3 blob client. Store and S3Client expose the two backends the caller hands to
// its REST resources API; Register / Unregister / LoadAll take the *mcp.Server
// per call (the caller owns it and passes it once the server exists) and are the
// SDK-registration seams wired as the create/delete callbacks and called once at
// startup. All methods are nil-safe, so a no-DB deployment (nil Handle) degrades
// cleanly.
type Handle struct {
	store     resource.Store
	s3Client  resource.S3Client
	uriScheme string
}

// New assembles the resource store and — when an S3 connection resolves — the S3
// blob client. It returns (nil, nil) when db is nil (managed resources are a
// no-op without a database). It returns an error when a referenced S3 connection
// is missing from the toolkits config or its client fails to build. When no S3
// connection resolves, blob storage is disabled with a WARN but the store-only
// layer is still returned so metadata operations work. The "managed resources
// enabled" INFO is emitted in both cases (with an empty s3_connection when blob
// storage is off) so the startup log always confirms the feature is active.
func New(db *sql.DB, cfg Config) (*Handle, error) {
	if db == nil {
		return nil, nil //nolint:nilnil // nil handle = managed resources disabled (no database)
	}

	h := &Handle{
		store:     resource.NewPostgresStore(db),
		uriScheme: uriScheme(cfg),
	}

	connName := s3Connection(cfg)
	if connName != "" {
		s3Cfg := toolkitcfg.S3Config(cfg.Toolkits, connName)
		if s3Cfg == nil {
			return nil, fmt.Errorf("resource s3 connection %q not found in toolkits config", connName)
		}

		c, err := s3client.New(context.Background(), &s3client.Config{
			Region:          s3Cfg.Region,
			Endpoint:        s3Cfg.Endpoint,
			AccessKeyID:     s3Cfg.AccessKeyID,
			SecretAccessKey: s3Cfg.SecretKey,
			Name:            s3Cfg.ConnectionName,
			UsePathStyle:    s3Cfg.UsePathStyle,
		})
		if err != nil {
			return nil, fmt.Errorf("creating resource s3 client for connection %q: %w", connName, err)
		}
		h.s3Client = s3adapter.New(c)
	} else {
		slog.Warn("managed resources: no s3_connection configured; blob storage disabled")
	}

	slog.Info("managed resources enabled",
		"s3_connection", connName,
		"s3_bucket", cfg.S3Bucket,
		"uri_scheme", h.uriScheme,
	)
	return h, nil
}

// uriScheme returns the configured URI scheme or the resource package default.
func uriScheme(cfg Config) string {
	if cfg.URIScheme != "" {
		return cfg.URIScheme
	}
	return resource.DefaultURIScheme
}

// s3Connection returns the S3 connection name for managed resources: the
// explicit config value if set, otherwise the default/first S3 toolkit instance
// resolved from the toolkits config (so managed resources automatically use an
// available S3 backend).
func s3Connection(cfg Config) string {
	if cfg.S3Connection != "" {
		return cfg.S3Connection
	}
	resolved := resolveDefaultS3Instance(cfg.Toolkits)
	if resolved == "" {
		slog.Debug("managed resources: no S3 toolkit available for default resolution")
		return ""
	}
	slog.Debug("managed resources: using default S3 connection", "s3_connection", resolved)
	return resolved
}

// resolveDefaultS3Instance returns the name of the default/first S3 toolkit
// instance in the toolkits config, or "" when no S3 toolkit is configured.
func resolveDefaultS3Instance(toolkits map[string]any) string {
	toolkitsCfg, ok := toolkits["s3"]
	if !ok {
		return ""
	}
	kindCfg, ok := toolkitsCfg.(map[string]any)
	if !ok {
		return ""
	}
	instances, ok := kindCfg[cfgKeyInstances].(map[string]any)
	if !ok {
		return ""
	}
	return toolkitcfg.ResolveDefaultInstance(kindCfg, instances)
}

// Store returns the managed resource store, or nil on a nil Handle (managed
// resources disabled or no database).
func (h *Handle) Store() resource.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// S3Client returns the S3 blob client for managed resources, or nil on a nil
// Handle or when no S3 connection was configured.
func (h *Handle) S3Client() resource.S3Client {
	if h == nil {
		return nil
	}
	return h.s3Client
}

// URIScheme returns the resolved resource URI scheme (the configured value or
// the resource package default), or "" on a nil Handle. The caller reads it to
// configure the resources/read middleware.
func (h *Handle) URIScheme() string {
	if h == nil {
		return ""
	}
	return h.uriScheme
}

// Register registers a managed resource with the given MCP server so it appears
// in the SDK's native resource list and triggers notifications/resources/list_changed.
// The SDK handler is a fallback only — the resources middleware normally
// intercepts resources/read with auth and the S3 fetch before it runs. No-op on
// a nil Handle, a nil server, or a nil resource.
func (h *Handle) Register(server *mcp.Server, res *resource.Resource) {
	if h == nil || server == nil || res == nil {
		slog.Debug("resourcelayer.Register: skipping",
			"handle_nil", h == nil, "server_nil", server == nil, "res_nil", res == nil)
		return
	}
	slog.Debug("resourcelayer.Register: registering with SDK", "uri", res.URI, "name", res.DisplayName)
	server.AddResource(&mcp.Resource{
		URI:         res.URI,
		Name:        res.DisplayName,
		Description: res.Description,
		MIMEType:    res.MIMEType,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Fallback handler — the middleware normally intercepts resources/read
		// before this runs. If we get here, the middleware fell through (auth
		// failure or config issue). Return a placeholder instead of nil to
		// avoid the SDK's "nil information" error.
		slog.Warn("managed resource: SDK fallback handler called (middleware did not intercept)", "uri", req.Params.URI)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: res.MIMEType,
				Text:     "(resource content unavailable — authentication required)",
			}},
		}, nil
	})
}

// Unregister removes a managed resource from the given MCP server's resource
// list, triggering notifications/resources/list_changed. No-op on a nil Handle
// or a nil server.
func (h *Handle) Unregister(server *mcp.Server, uri string) {
	if h == nil || server == nil {
		slog.Debug("resourcelayer.Unregister: skipping, no server")
		return
	}
	slog.Debug("resourcelayer.Unregister: removing from SDK", "uri", uri)
	server.RemoveResources(uri)
}

// LoadAll registers every existing global managed resource from the store with
// the given MCP server so they are visible on the first resources/list call.
// Called once at startup. No-op on a nil Handle, a nil store, or a nil server.
func (h *Handle) LoadAll(server *mcp.Server) {
	if h == nil || h.store == nil {
		slog.Debug("resourcelayer.LoadAll: no resource store, skipping")
		return
	}
	if server == nil {
		slog.Debug("resourcelayer.LoadAll: no MCP server, skipping")
		return
	}
	resources, _, err := h.store.List(context.Background(), resource.Filter{
		Scopes: []resource.ScopeFilter{{Scope: resource.ScopeGlobal}},
		Limit:  1000,
	})
	if err != nil {
		slog.Warn("managed resources: failed to load existing resources", "error", err)
		return
	}
	for i := range resources {
		h.Register(server, &resources[i])
	}
	if len(resources) > 0 {
		slog.Info("managed resources: registered existing resources", logKeyCount, len(resources))
	}
}
