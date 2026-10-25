// Package resourcesharewire assembles resource shares (#2258) from the
// platform: the share store, the persona each caller resolves to, the people
// of a persona, and the share email.
//
// It is a package of its own rather than a function in the composition root
// because that root is at its size budget.
package resourcesharewire

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpserver/channelhttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/personashare"
	"github.com/txn2/mcp-data-platform/internal/httpserver/tablesource"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/resourceshare"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Shares is the assembled service, nil on a deployment without a database,
// where nothing is shared and every method does nothing.
type Shares struct {
	svc    *resourceshare.Service
	caller func([]string) resourceshare.Caller
	deps   *resourceshare.Deps
	// channels posts a file to a notification channel through the notify
	// tool; nil mounts no channel route.
	channels  poster
	portalURL string
}

// New assembles the service for p, telling recipients through notify.
func New(p *platform.Platform, notify *notifydelivery.Handle) *Shares {
	if p.DB() == nil || p.ResourceStore() == nil {
		return nil
	}
	reg := p.PersonaRegistry()
	d := &resourceshare.Deps{
		Shares: resourceshare.NewStore(p.DB()), Resources: p.ResourceStore(),
		Caller: CallerOf(reg), Members: membersOf(p),
		PersonaExists: func(name string) bool { _, ok := reg.Get(name); return ok },
	}
	if bridge := notify.PortalNotifier(notifydelivery.PortalStores{}, p.Config().Portal.PublicBaseURL); bridge != nil {
		d.Notify = func(ctx context.Context, share *portaldomain.Share, id, title, message string) {
			bridge.NotifyShare(ctx, share, portal.ShareEvent{Kind: notification.KindResource, ItemID: id, ItemTitle: title, Message: message})
		}
	}
	s := &Shares{svc: resourceshare.New(*d), caller: d.Caller, deps: d, portalURL: p.Config().Portal.PublicBaseURL}
	if server := p.MCPServer(); server != nil {
		s.channels = &channelhttp.API{Server: server}
	}
	return s
}

// Bind wraps the resources API's claims so each request's carry the caller's
// share lookup, and makes them the claims the share routes authenticate with.
func (s *Shares) Bind(base resource.ClaimsExtractor) resource.ClaimsExtractor {
	if s == nil {
		return base
	}
	bound := func(r *http.Request) (*resource.Claims, error) {
		claims, err := base(r)
		if err != nil || claims == nil {
			return claims, err
		}
		claims.Shared = s.svc.Lookup(r.Context(), claims, s.caller(claims.Roles))
		return claims, nil
	}
	s.deps.Claims = bound
	s.svc = resourceshare.New(*s.deps)
	return bound
}

// Register mounts the share routes on mux, and the channel route when the
// deployment can post.
func (s *Shares) Register(mux *http.ServeMux) {
	if s == nil {
		return
	}
	s.svc.Register(mux)
	if s.channels != nil {
		mux.HandleFunc(channelPath, s.postToChannel)
	}
}

// CallerOf resolves a caller's persona for the share routes.
func CallerOf(reg *persona.Registry) func(roles []string) resourceshare.Caller {
	return func(roles []string) resourceshare.Caller {
		p, ok := reg.GetForRoles(roles)
		if !ok {
			return resourceshare.Caller{}
		}
		return resourceshare.Caller{
			Persona: p.Name, External: p.External, Sharing: p.SharingAllowed(),
		}
	}
}

// membersOf lists the people of a persona from the directory, as a persona
// share of an asset does; nil without one.
func membersOf(p *platform.Platform) func(ctx context.Context, name string) []string {
	var deps portal.Deps
	if users := p.UserStore(); users != nil {
		personashare.Wire(&deps, p.PersonaRegistry(), users)
	}
	return deps.PersonaMembers
}

// TablePermission is the permission a share gives a table registration's
// caller on a resource, which lets an editor share register a table over it
// (#2258); nil without a database.
func TablePermission(p *platform.Platform) tablesource.SharePermission {
	if p.DB() == nil {
		return nil
	}
	store, caller := resourceshare.NewStore(p.DB()), CallerOf(p.PersonaRegistry())
	return func(ctx context.Context, resourceID string, c tableregister.Caller) string {
		perm, err := store.Permission(ctx, resourceID, resourceshare.Recipient{
			UserID: c.UserID, Email: c.Email, Persona: caller(c.Roles).Persona,
		})
		if err != nil {
			return ""
		}
		return perm
	}
}
