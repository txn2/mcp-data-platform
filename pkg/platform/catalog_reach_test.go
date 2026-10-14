package platform

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// apply_knowledge's catalog writes reach what the persona the call is
// authorized under reaches, the same boundary the portal's catalog routes
// apply.
func TestCatalogReachFollowsThePersonasConnections(t *testing.T) {
	personas := persona.NewRegistry()
	if err := personas.Register(&persona.Persona{
		Name: "analyst", Roles: []string{"analyst"},
		Connections: persona.ConnectionRules{Allow: []string{"acme"}},
	}); err != nil {
		t.Fatal(err)
	}
	reach := catalogReach(&Platform{personaRegistry: personas, toolkitRegistry: registry.NewRegistry()})
	as := func(pc *middleware.PlatformContext) context.Context {
		return middleware.WithPlatformContext(context.Background(), pc)
	}
	analyst := as(&middleware.PlatformContext{PersonaName: "analyst"})

	for _, tc := range []struct {
		name string
		ctx  context.Context
		ref  string
		want bool
	}{
		{"a dataset on a reached connection", analyst, "mcp:dataset:(trino,acme,a.b.c)", true},
		{"a column on another connection", analyst, "mcp:column:(trino,hr,a.b.c,salary)", false},
		{"a definition, which names no connection", analyst, "mcp:tag:pii", true},
		{"an administrator", as(&middleware.PlatformContext{PersonaName: "analyst", IsAdmin: true}), "mcp:dataset:(trino,hr,a.b.c)", true},
		{"an unknown persona", as(&middleware.PlatformContext{PersonaName: "nobody"}), "mcp:dataset:(trino,acme,a.b.c)", false},
		{"the platform's own work", context.Background(), "mcp:dataset:(trino,hr,a.b.c)", true},
	} {
		if got := reach(tc.ctx, tc.ref); got != tc.want {
			t.Errorf("%s: reach %s = %v, want %v", tc.name, tc.ref, got, tc.want)
		}
	}
	if !catalogReach(&Platform{toolkitRegistry: registry.NewRegistry()})(analyst, "mcp:dataset:(trino,hr,a.b.c)") {
		t.Error("with no persona registry there is no boundary to apply")
	}
}
