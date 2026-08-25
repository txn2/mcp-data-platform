// Package apiwire composes the operation browser's read surface from the
// registries a deployment holds: which connections a caller reaches, which
// toolkit answers for one, and how a caller's identity reaches the route
// policy.
//
// It is a package of its own rather than another file in the composition root
// because that root is at its size budget, and it takes the registries and the
// portal's already-built auth chain as inputs rather than the platform facade,
// which is the boundary every other adapter under internal/httpserver keeps.
package apiwire

import (
	"context"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/httpserver/apishttp"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// Deps are what the browser needs to answer for one caller. The registries are
// held rather than snapshotted, so a connection added or a persona edited
// through the admin API takes effect on the next request.
type Deps struct {
	// Toolkits is the live toolkit registry. Nil leaves the routes unmounted.
	Toolkits *registry.Registry
	// Personas resolves a persona's connection rules. Nil denies every named
	// connection, matching the fail-closed action path.
	Personas *persona.Registry
	// Resolver maps a caller's roles to their persona. Nil leaves every caller
	// without one, which reaches nothing.
	Resolver portal.PersonaResolver
	// AdminRoles are the roles the admin persona is granted, which is what
	// makes a caller's reach over this surface unrestricted.
	AdminRoles []string
}

// Mount registers the browser's routes on mux behind the portal's own
// authentication and persona gate.
//
// The routes are mounted whether or not an api-gateway toolkit is loaded. An
// absent route under /api/v1 falls through to the MCP root handler, which
// answers 401 to a browser fetch carrying only a session cookie, and the portal
// client turns a 401 into a redirect to the login screen. A deployment with no
// api connections must answer "you reach none", which is the empty state the
// page renders, rather than throwing its reader out of the portal.
func Mount(mux *http.ServeMux, wrap func(http.Handler) http.Handler, deps Deps) {
	lister := connreach.New(connreach.Deps{Toolkits: deps.Toolkits, Personas: deps.Personas})
	if lister == nil {
		return
	}
	apishttp.New(apishttp.Deps{
		Caller:      callerResolver(deps),
		Connections: connectionEnumerator(lister),
		Locate:      browserLocator(deps.Toolkits),
		Elevate:     elevate,
	}).Register(mux, wrap)
}

// callerResolver turns the portal user the auth chain left on the context into
// the caller a listing is narrowed to.
func callerResolver(deps Deps) func(*http.Request) *apishttp.Caller {
	return func(r *http.Request) *apishttp.Caller {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		c := &apishttp.Caller{
			UserID: user.UserID, Email: user.Email, Roles: user.Roles,
			IsAdmin: rolesIntersect(user.Roles, deps.AdminRoles),
		}
		if deps.Resolver != nil {
			if info := deps.Resolver(user.Roles); info != nil {
				c.Persona = info.Name
			}
		}
		return c
	}
}

// rolesIntersect reports whether any of the caller's roles is an admin role.
func rolesIntersect(userRoles, adminRoles []string) bool {
	return slices.ContainsFunc(userRoles, func(role string) bool {
		return slices.Contains(adminRoles, role)
	})
}

// connectionEnumerator adapts the shared connection enumeration. It is
// connreach's, which is also what a script's connection picker is filled from,
// so one question about what a caller reaches has one answer.
func connectionEnumerator(
	lister *connreach.Lister,
) func(context.Context, *apishttp.Caller) []apishttp.Connection {
	return func(ctx context.Context, caller *apishttp.Caller) []apishttp.Connection {
		conns := lister.ForPersona(ctx, caller.Persona, caller.IsAdmin)
		out := make([]apishttp.Connection, 0, len(conns))
		for _, c := range conns {
			out = append(out, apishttp.Connection{
				Name: c.Name, Kind: c.Kind, Description: c.Description,
			})
		}
		return out
	}
}

// browserLocator finds the live api-gateway toolkit serving one connection. The
// registry is read per request rather than snapshotted, so a connection added
// through the admin API is browsable without a restart.
func browserLocator(toolkits *registry.Registry) func(string) apishttp.OperationBrowser {
	return func(connection string) apishttp.OperationBrowser {
		for _, tk := range toolkits.GetByKind(apigatewaykit.Kind) {
			api, ok := tk.(*apigatewaykit.Toolkit)
			if ok && api.HasConnection(connection) {
				return api
			}
		}
		return nil
	}
}

// elevate puts the caller on the context the route policy reads roles from, so
// an operation this surface shows is one that caller's tool call could also
// reach.
func elevate(ctx context.Context, c *apishttp.Caller) context.Context {
	return middleware.WithPreAuthenticatedUser(ctx, &middleware.UserInfo{
		UserID: c.UserID, Email: c.Email, Roles: c.Roles,
	})
}
