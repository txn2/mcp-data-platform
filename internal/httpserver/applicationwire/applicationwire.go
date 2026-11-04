// Package applicationwire mounts applications on the portal (#2411, #2412):
// the service and bundler assembled from the platform's stores, behind the
// portal's authentication, with the caller read from the portal session.
package applicationwire

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/apphttp"
	"github.com/txn2/mcp-data-platform/internal/application/appwire"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Mount registers the portal application routes behind wrap. Nothing without
// a database.
func Mount(mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler, isAdmin func([]string) bool) {
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
	register(mux, wrap, d, isAdmin, p.Audit().Logger())
}

// register assembles the service and bundler over d and mounts the routes.
func register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, d appwire.Deps, isAdmin func([]string) bool, audit middleware.AuditLogger) {
	svc := appwire.Service(d)
	if svc == nil {
		return
	}
	apphttp.Register(mux, wrap, apphttp.Config{Service: svc, Bundles: appwire.Bundler(svc, d), Actor: Actor(isAdmin), Audit: audit})
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
