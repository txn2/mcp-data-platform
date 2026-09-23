package platform

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/connrecords"
	"github.com/txn2/mcp-data-platform/internal/platform/exportadapters"
	"github.com/txn2/mcp-data-platform/internal/platform/graphqlcatalog"
	"github.com/txn2/mcp-data-platform/internal/platform/graphqlwiring"
	"github.com/txn2/mcp-data-platform/internal/platform/routepolicy"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// GraphQLToolkits returns the live graphql toolkits. The index-jobs
// consumer reads its corpus through this, so a connection added after
// startup is indexed without a restart.
func (p *Platform) GraphQLToolkits() []*graphqlkit.Toolkit {
	return graphqlwiring.Toolkits(p.toolkitRegistry)
}

// graphQLHydrateTimeout bounds the startup schema read across every
// graphql connection. The reads run concurrently, so this is the
// slowest endpoint's budget rather than their sum; an endpoint that
// exceeds it registers with its failure recorded and is retried by an
// administrator or by its next reload, which is better than a
// connection that is down holding up the whole platform's start.
const graphQLHydrateTimeout = 30 * time.Second

// WireGraphQL attaches the platform's dependencies to every live
// graphql toolkit and brings each connection's schema up. The substance
// is in internal/platform/graphqlwiring; this assembles what only the
// facade holds.
//
// Nil-safe and idempotent, like every other Wire step, so a deployment
// missing a database, an embedding provider or a portal wires what it
// has and leaves the rest absent.
func (p *Platform) WireGraphQL(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, graphQLHydrateTimeout)
	defer cancel()
	graphqlwiring.Wire(ctx, graphqlwiring.Deps{
		Registry:    p.toolkitRegistry,
		DB:          p.db,
		Embedder:    p.embeddingProv,
		RoutePolicy: p.graphQLRoutePolicy(),
		OAuthStore:  p.connAuth.Store(),
		AuthEvents:  p.connAuth.AuthEventWriter(),
		MemBudget:   p.apiMemBudget,
		Metrics:     p.Metrics(),
		Catalog:     graphqlcatalog.Store(p.APIGatewayCatalogStore()),
		Connections: connrecords.Saved[*ConnectionInstance](p.connectionStore, graphqlkit.Kind,
			ErrConnectionNotFound, graphqlkit.ErrConnectionNotFound,
			func(inst *ConnectionInstance) map[string]any { return inst.Config }),
	})
}

// wireGraphQLExport attaches graphql_export's dependencies to every live
// graphql toolkit.
//
// It runs from initPortal, beside wireTrinoExport and wireAPIGatewayExport,
// rather than from WireGraphQL with the rest of the kind's dependencies. The
// tool is registered on the MCP server only when these are already set, and
// Start registers the toolkits' tools before WireRuntime runs, so the late
// path left the name in the toolkit's Tools() list and the tool unknown to
// every client (#1675).
//
// A package-level function rather than a Platform method, matching
// wireUtilConnection: the wiring sequence grows without growing the facade.
func wireGraphQLExport(p *Platform) {
	graphqlwiring.AttachExport(p.toolkitRegistry, p.graphQLExportDeps())
}

// graphQLRoutePolicy builds the per-operation authorization gate, or
// nil when the platform's authorizer is not the persona-based one.
func (p *Platform) graphQLRoutePolicy() *routepolicy.Policy {
	pa, ok := p.authorizer.(*persona.Authorizer)
	if !ok {
		return nil
	}
	return routepolicy.New(routepolicy.Deps{Authenticator: p.authenticator, Authorizer: pa})
}

// graphQLExportDeps assembles graphql_export's portal dependencies, or
// nil when export is disabled or the portal is not configured. The
// limits are the same ones trino_export and api_export honor: an
// operator sets the cap once and every export tool respects it.
func (p *Platform) graphQLExportDeps() *graphqlkit.ExportDeps {
	if isExplicitlyDisabled(p.config.Portal.Export.Enabled) {
		return nil
	}
	if p.portalStore.S3Client() == nil || p.portalStore.AssetStore() == nil {
		return nil
	}
	cfg := p.parseExportConfig()
	exporter := exportadapters.NewGraphQLExporter(
		p.portalStore.AssetStore(), p.portalStore.VersionStore(), p.portalStore.ShareStore(),
		p.config.Portal.PublicBaseURL, p.captureProvenance,
	)
	return &graphqlkit.ExportDeps{
		AssetStore:     exporter,
		VersionStore:   exporter,
		S3Client:       p.portalStore.S3Client(),
		ShareCreator:   exporter,
		ResourceLander: p.portalStore.ResourceLanding(),
		S3Bucket:       p.config.Portal.S3Bucket,
		S3Prefix:       p.config.Portal.S3Prefix,
		BaseURL:        p.config.Portal.PublicBaseURL,
		Config: graphqlkit.ExportConfig{
			MaxBytes:       cfg.MaxBytes,
			DefaultTimeout: cfg.DefaultTimeout,
			MaxTimeout:     cfg.MaxTimeout,
		},
		GetUserContext: func(ctx context.Context) *graphqlkit.ExportUserContext {
			pc := middleware.GetPlatformContext(ctx)
			if pc == nil {
				return nil
			}
			return &graphqlkit.ExportUserContext{
				UserID: pc.UserID, UserEmail: pc.UserEmail, SessionID: pc.SessionID,
				RunOutputKey: producedby.RunOutputKey(ctx),
			}
		},
	}
}
