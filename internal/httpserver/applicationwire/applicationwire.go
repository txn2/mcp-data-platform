// Package applicationwire mounts applications on the portal (#2411, #2412):
// the service and bundler assembled from the platform's stores, behind the
// portal's authentication, with the caller read from the portal session.
package applicationwire

import (
	"context"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/apphttp"
	"github.com/txn2/mcp-data-platform/internal/application/apprunrole"
	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/application/appwire"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// errNoKit is a schema read with no application database toolkit registered.
var errNoKit = errors.New("no application database toolkit is registered")

// Mount registers the portal application routes behind wrap. Nothing without
// a database.
// gate is the save gate the portal's script editor saves through, which an
// import puts each script through (#2476).
func Mount(mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler, isAdmin func([]string) bool, gate *scriptsave.Gate) {
	if p == nil || p.DB() == nil {
		return
	}
	d := appwire.Deps{
		DB: p.DB(), Toolkits: p.ToolkitRegistry(), Bucket: p.Config().Portal.S3Bucket, Prefix: p.Config().Portal.S3Prefix,
	}
	if s := p.PortalAssetStore(); s != nil {
		d.Assets = s
	}
	if v := p.PortalVersionStore(); v != nil {
		d.AssetVersions = v
	}
	if c := p.PortalS3Client(); c != nil {
		d.Blobs = c
	}
	if e := p.Endpoints(); e != nil && e.Commands != nil {
		d.Endpoints = e.Commands.Run
	}
	if gate != nil {
		d.SaveGate, d.Recordings = gate, gate.Recordings
	}
	if enc := p.RestEncryptor(); enc != nil {
		d.Sealer = enc
	}
	lister := connreach.New(connreach.Deps{Toolkits: p.ToolkitRegistry(), Personas: p.PersonaRegistry(), Stored: platform.StoredConnections(p)})
	src := databaseSources{
		reach:   lister.ForPersona,
		persona: func(roles []string) string { return personaOf(p, roles) },
		schemas: readSchemas(func() *postgreskit.Toolkit { return appwire.Live(p.ToolkitRegistry()) }),
		held:    appstore.New(p.DB()).List,
	}
	d.Reach = apprunrole.ReachVia(lister, func(roles []string) string { return personaOf(p, roles) })
	register(mux, wrap, d, routes{
		isAdmin: isAdmin, audit: p.Audit().Logger(), databases: src.choices,
		viewAsset: assetViewer(p.PortalShareStore()),
	})
}

// routes is what the routes are mounted with besides the stores.
type routes struct {
	isAdmin   func([]string) bool
	audit     middleware.AuditLogger
	databases func(*http.Request, application.Actor) []apphttp.DatabaseChoice
	viewAsset appsvc.AssetViewer
}

// assetViewer reports whether the portal caller on ctx opens an asset, by
// the portal's own view rule (#2527): its owner, or a person a share or a
// shared collection reaches. With no share store, only the owner.
func assetViewer(shares portaldomain.ShareStore) appsvc.AssetViewer {
	c := access.New(access.Config{Shares: shares})
	return func(ctx context.Context, asset *portaldomain.Asset) bool {
		user := portal.GetUser(ctx)
		if user == nil || asset == nil {
			return false
		}
		if shares == nil {
			return access.OwnsAsset(asset, user)
		}
		return c.CanViewAsset(ctx, asset.ID, asset, user)
	}
}

// personaOf is the persona a caller's roles resolve to, "" for none.
func personaOf(p *platform.Platform, roles []string) string {
	if pr := p.PersonaRegistry(); pr != nil {
		if per, ok := pr.GetForRoles(roles); ok && per != nil {
			return per.Name
		}
	}
	return ""
}

// register assembles the service and bundler over d and mounts the routes.
func register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, d appwire.Deps, r routes) {
	svc := appwire.Service(d)
	if svc == nil {
		return
	}
	apphttp.Register(mux, wrap, apphttp.Config{
		Service: svc, Bundles: appwire.Bundler(svc, d), Actor: Actor(r.isAdmin), Audit: r.audit, Databases: r.databases,
		ViewAsset: r.viewAsset,
	})
}

// Actor is the portal caller as an application actor, nil when signed out.
func Actor(isAdmin func([]string) bool) func(r *http.Request) *application.Actor {
	return func(r *http.Request) *application.Actor {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		return &application.Actor{Email: user.Email, Admin: isAdmin(user.Roles), Subject: user.UserID, Roles: user.Roles}
	}
}
