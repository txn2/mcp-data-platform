// Package routepolicy builds the api-gateway's per-route authorization policy:
// it resolves a caller's roles and checks a (connection, method, path) tuple
// against the persona authorizer's APIRoutes rules.
//
// It layers on top of the standard MCP authorizer, which already gates whether
// a caller may invoke the api-gateway tool at all and on a given connection;
// this policy adds the per-route (which HTTP method on which path) check that
// the tool-level gate cannot express.
//
// New returns the concrete *Policy. The package imports only middleware and
// persona, not pkg/toolkits/apigateway: the SetRoutePolicy call site in
// pkg/platform is the compile-time proof that *Policy satisfies
// apigatewaykit.RoutePolicy, so this package does not depend on the toolkit it
// serves.
package routepolicy

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Deps are the dependencies a Policy needs.
type Deps struct {
	// Authenticator is the fallback used to resolve roles for callers that did
	// not pass through the standard MCP middleware chain. May be nil.
	Authenticator middleware.Authenticator
	// Authorizer holds the persona APIRoutes rules the policy enforces.
	Authorizer *persona.Authorizer
}

// Policy enforces per-route authorization for the api-gateway toolkit.
type Policy struct {
	authn middleware.Authenticator
	pa    *persona.Authorizer
}

// New builds a Policy from deps.
func New(deps Deps) *Policy {
	return &Policy{authn: deps.Authenticator, pa: deps.Authorizer}
}

// Allow reports whether the caller may perform method on path of connection,
// resolving the caller's roles first and consulting the persona authorizer's
// per-route rules.
//
// template is the catalog path that path resolved from, and travels with it so
// a rule naming an operation as its catalog declares it ("/v1/orders/{id}")
// governs the call that operation serves ("/v1/orders/42"), which is the form
// an invoke reaches this policy in.
func (p *Policy) Allow(ctx context.Context, connection, method, path, template string) (allowed bool, reason string) {
	roles := p.resolveRoles(ctx)
	allowed, _, reason = p.pa.IsAPIRouteAllowed(ctx, roles, persona.RouteQuery{
		Connection: connection,
		Method:     method,
		Path:       path,
		Template:   template,
	})
	return allowed, reason
}

// resolveRoles returns the caller's roles by preference order:
//  1. a pre-authenticated user (admin browser-cookie paths);
//  2. PlatformContext roles set by MCPToolCallMiddleware after a successful
//     Authenticate — the cheapest path on the MCP tool-call hot path;
//  3. the Authenticator fallback for callers that bypassed that middleware.
//
// The PlatformContext branch keys on UserID, not len(Roles) > 0: a legitimately
// authenticated user can have zero roles (default-persona-only deployments, a
// missing role-claim path), and gating on role count would force a redundant
// Authenticate for them.
func (p *Policy) resolveRoles(ctx context.Context) []string {
	if userInfo := middleware.GetPreAuthenticatedUser(ctx); userInfo != nil {
		return userInfo.Roles
	}
	if pc := middleware.GetPlatformContext(ctx); pc != nil && pc.UserID != "" {
		return pc.Roles
	}
	if p.authn != nil {
		if userInfo, err := p.authn.Authenticate(ctx); err == nil && userInfo != nil {
			return userInfo.Roles
		}
	}
	return nil
}
