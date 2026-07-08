package routepolicy_test

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/routepolicy"
)

// panicAuthenticator fails the test if its Authenticate is called. It proves the
// policy does not redundantly call Authenticate when PlatformContext.Roles is
// already populated by the upstream MCP middleware (the common hot-path case).
type panicAuthenticator struct{}

func (panicAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	panic("Authenticate called when PlatformContext.Roles should have been used")
}

// stubAuthenticator returns fixed roles, used to exercise the resolveRoles
// fallback for callers that carry neither a pre-authenticated user nor a
// PlatformContext.
type stubAuthenticator struct{ roles []string }

func (s stubAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: "u", Roles: s.roles}, nil
}

// TestAllow_DirectCases exercises the policy that bridges persona.Authorizer
// onto the api-gateway route-policy contract, including the resolveRoles
// preference order.
func TestAllow_DirectCases(t *testing.T) {
	personaReg := persona.NewRegistry()
	if err := personaReg.Register(&persona.Persona{
		Name:  "analyst",
		Roles: []string{"analyst"},
		Tools: persona.ToolRules{Allow: []string{"*"}},
		APIRoutes: []persona.APIRouteRule{
			{Connection: "crm", Methods: []string{"GET"}, Paths: []string{"/v1/users/*"}},
			{Connection: "crm", Methods: []string{"DELETE"}, Action: persona.ActionDeny},
		},
	}); err != nil {
		t.Fatalf("register persona: %v", err)
	}
	mapper := &persona.OIDCRoleMapper{Registry: personaReg}
	authzr := persona.NewAuthorizer(personaReg, mapper)
	policy := routepolicy.New(routepolicy.Deps{Authorizer: authzr})

	t.Run("allow rule matches → true", func(t *testing.T) {
		ctx := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{
			UserID: "u1", Roles: []string{"analyst"},
		})
		allowed, reason := policy.Allow(ctx, "crm", "GET", "/v1/users/123")
		if !allowed {
			t.Errorf("got allowed=false, reason=%q; want allowed=true", reason)
		}
	})

	t.Run("deny rule matches → false with reason", func(t *testing.T) {
		ctx := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{
			UserID: "u1", Roles: []string{"analyst"},
		})
		allowed, reason := policy.Allow(ctx, "crm", "DELETE", "/v1/users/123")
		if allowed {
			t.Error("DELETE was allowed despite deny rule")
		}
		if reason == "" {
			t.Error("expected non-empty reason on denial")
		}
	})

	t.Run("connection with no APIRoutes for resolved persona → passthrough", func(t *testing.T) {
		// Empty roles resolve to DefaultPersona (no APIRoutes). The route policy
		// is layered on top of the platform's main IsAuthorized gate; when the
		// persona has no APIRoutes touching the connection the route check is a
		// no-op (true). Fail-closed for unauthenticated callers is
		// MCPToolCallMiddleware's job, not this policy's.
		allowed, _ := policy.Allow(context.Background(), "crm", "GET", "/v1/users/1")
		if !allowed {
			t.Error("expected passthrough for persona without APIRoutes for crm")
		}
	})

	t.Run("PlatformContext roles preferred over Authenticator (no double-verify)", func(t *testing.T) {
		// Reads PlatformContext.Roles instead of forcing a second Authenticate.
		// The Authenticator would PANIC if invoked, so passing proves the
		// pre-extracted roles were reused.
		policyNoAuthn := routepolicy.New(routepolicy.Deps{Authenticator: panicAuthenticator{}, Authorizer: authzr})
		pc := middleware.NewPlatformContext("req-1")
		pc.UserID = "u1"
		pc.Roles = []string{"analyst"}
		ctx := middleware.WithPlatformContext(context.Background(), pc)
		allowed, _ := policyNoAuthn.Allow(ctx, "crm", "GET", "/v1/users/123")
		if !allowed {
			t.Error("PlatformContext.Roles path did not produce allow decision")
		}
	})

	t.Run("authenticated user with empty roles still uses PlatformContext (no double-verify)", func(_ *testing.T) {
		// Regression for the resolveRoles guard: keying on len(Roles) > 0 would
		// fall an authenticated-but-role-less user through to the Authenticator
		// fallback. The correct signal is "auth middleware ran" (pc.UserID
		// populated). panicAuthenticator catches the regression.
		policyNoAuthn := routepolicy.New(routepolicy.Deps{Authenticator: panicAuthenticator{}, Authorizer: authzr})
		pc := middleware.NewPlatformContext("req-2")
		pc.UserID = "u2"
		pc.Roles = nil // authenticated but role-less
		ctx := middleware.WithPlatformContext(context.Background(), pc)
		_, _ = policyNoAuthn.Allow(ctx, "crm", "GET", "/v1/users")
	})

	t.Run("Authenticator fallback resolves roles when ctx carries none", func(t *testing.T) {
		// No pre-auth user and no PlatformContext: resolveRoles falls through to
		// the Authenticator, whose roles must drive the decision.
		policyAuthn := routepolicy.New(routepolicy.Deps{
			Authenticator: stubAuthenticator{roles: []string{"analyst"}},
			Authorizer:    authzr,
		})
		allowed, reason := policyAuthn.Allow(context.Background(), "crm", "GET", "/v1/users/123")
		if !allowed {
			t.Errorf("got allowed=false, reason=%q; want the Authenticator's analyst role to allow", reason)
		}
	})

	t.Run("connection-targeted persona but auth context absent → fail-closed", func(t *testing.T) {
		// The default persona has APIRoutes touching crm, so empty roles still
		// resolve to it and the route check applies; an unmatched (method, path)
		// is denied.
		reg := persona.NewRegistry()
		def := &persona.Persona{
			Name:  "default",
			Roles: []string{},
			Tools: persona.ToolRules{Allow: []string{"*"}},
			APIRoutes: []persona.APIRouteRule{
				{Connection: "crm", Methods: []string{"GET"}, Paths: []string{"/v1/users/*"}},
			},
		}
		_ = reg.Register(def)
		reg.SetDefault("default")
		mapper2 := &persona.OIDCRoleMapper{Registry: reg}
		auth2 := persona.NewAuthorizer(reg, mapper2)
		policy2 := routepolicy.New(routepolicy.Deps{Authorizer: auth2})
		allowed, _ := policy2.Allow(context.Background(), "crm", "DELETE", "/v1/anything")
		if allowed {
			t.Error("DELETE on path-restricted connection allowed without explicit allow rule")
		}
	})
}
