// Package sitepages assembles, from the platform, the application endpoint
// server a site environment's hosts run their application's endpoints
// through, and who an environment's tile is drawn as (#2747). The site
// gateway's own address and the tile worker's page both run endpoints through
// Over, so a tile's page reaches the endpoints exactly as a visitor's browser
// does; it is a package of its own because internal/httpserver/sitehttp is at
// its size budget.
package sitepages

import (
	"context"
	"net/url"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile/tilepage"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Over is the application endpoint server of a deployment that has an
// endpoint handler, given its sites runtime and portal address.
func Over(p *platform.Platform, rt *sitewire.Runtime, portal *url.URL) *sitegw.Pages {
	e := p.Endpoints()
	return &sitegw.Pages{
		Apps: rt.Apps, Assets: p.PortalAssetStore(), Versions: p.PortalVersionStore(), Server: e.Handler,
		Authenticate: e.PortalCaller, RouteHost: rt.Store.RouteHost, UseHandoff: rt.Store.UseHandoff,
		FormKey: e.Runner.FormKey, PortalURL: p.Config().Portal.PublicBaseURL, PagesDomain: func() string { return rt.Live.Get().PagesDomain },
		Secure: portal.Scheme == "https", PublicPort: portal.Port(),
	}
}

// ForTiles is the server a site tile's page runs its endpoints through, the
// one a site's own address runs them through; nil where the deployment serves
// no sites, has no endpoint handler or no portal address.
func ForTiles(p *platform.Platform) *sitegw.Pages {
	e := p.Endpoints()
	if e == nil || e.Sites == nil || !e.Sites.Serving() || e.Handler == nil {
		return nil
	}
	portal, err := url.Parse(p.Config().Portal.PublicBaseURL)
	if err != nil {
		return nil
	}
	return Over(p, e.Sites, portal)
}

// TileOwner resolves the person a site's owner address names when its tile
// is drawn: the subject and roles the directory last recorded for them, the
// persona those roles resolve to in the live registry, and whether that is the
// administrator persona. Nil, drawing for nobody, for a person the directory
// does not hold or has never seen sign in, and for one whose roles resolve to
// no persona; nothing about the owner is stored for the draw.
func TileOwner(p *platform.Platform) tilepage.Owner {
	return resolveOwner(p.BoundPrincipals(), p.PersonaRegistry(), p.Config().Admin.Persona)
}

func resolveOwner(people auth.PrincipalSource, reg *persona.Registry, adminPersona string) tilepage.Owner {
	if people == nil || reg == nil {
		return nil
	}
	return func(ctx context.Context, email string) *epform.Viewer {
		person, err := people.BoundPrincipal(ctx, email)
		if err != nil || person == nil || person.Subject == "" {
			return nil
		}
		per, ok := reg.GetForRoles(person.Roles)
		if !ok {
			return nil
		}
		return &epform.Viewer{
			UserID: person.Subject, Email: person.Email, Persona: per.Name, Roles: person.Roles,
			Admin: reg.IsAdminName(per.Name, adminPersona),
		}
	}
}
