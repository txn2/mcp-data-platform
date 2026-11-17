// Package foldergrantwire assembles write grants on Global folders (#2684):
// it installs the source every resource check reads a caller's grants from,
// and mounts the administrator's routes that list, add and remove them.
package foldergrantwire

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/grantsource"

	"github.com/txn2/mcp-data-platform/internal/portal/foldergrant"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Mount installs the grant source wherever the platform keeps resources in a
// database, and mounts the admin routes when the admin API is on.
func Mount(mux *http.ServeMux, p *platform.Platform, auth func() func(http.Handler) http.Handler) {
	if p == nil || p.DB() == nil || p.ResourceStore() == nil {
		return
	}
	store := foldergrant.NewStore(p.DB())
	reg := p.PersonaRegistry()
	external := func(name string) bool {
		per, ok := reg.Get(name)
		return ok && per.External
	}
	grantsource.Install(foldergrant.Source{Store: store, External: external})
	if !p.Config().Admin.IsEnabled() {
		return
	}
	d := foldergrant.Deps{
		Store: store, Actor: actor, ExternalPersona: external, Audit: p.Audit().Logger(),
		PersonaExists: func(name string) bool { _, ok := reg.Get(name); return ok },
		FolderExists:  store.FolderExists, Sites: store.Sites,
	}
	if users := p.UserStore(); users != nil {
		d.ExternalPerson = func(ctx context.Context, email string) bool {
			u, err := users.Get(ctx, email)
			if err != nil || u == nil {
				return false
			}
			per, ok := reg.GetForRoles(u.Roles)
			return ok && per.External
		}
	}
	foldergrant.NewAPI(d).Register(mux, auth())
}

// actor is the administrator a request passed the admin API as.
func actor(r *http.Request) string {
	u := admin.GetUser(r.Context())
	if u == nil {
		return ""
	}
	if u.Email != "" {
		return u.Email
	}
	return u.UserID
}
