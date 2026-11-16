// Package integrationwire mounts the integrations' admin routes (#2659) behind
// the admin API's authentication, with the administrator each request names.
package integrationwire

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/integrationapi"
	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Mount mounts the routes when the platform has integrations and an admin API.
func Mount(mux *http.ServeMux, p *platform.Platform, auth func() func(http.Handler) http.Handler) {
	if p == nil || p.Endpoints() == nil || p.Endpoints().Integrations == nil || !p.Config().Admin.IsEnabled() {
		return
	}
	integrationapi.Register(mux, auth(), integrationapi.Config{
		Service: p.Endpoints().Integrations, Actor: Actor, Audit: p.Audit().Logger(),
	})
}

// Actor is the administrator a request passed the admin API as.
func Actor(r *http.Request) integration.Actor {
	u := admin.GetUser(r.Context())
	if u == nil {
		return integration.Actor{}
	}
	email := u.Email
	if email == "" {
		email = u.UserID
	}
	return integration.Actor{Email: email, Roles: u.Roles, Admin: true}
}
