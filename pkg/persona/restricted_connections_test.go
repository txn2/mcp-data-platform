package persona

import (
	"context"
	"slices"
	"testing"
)

// TestIsConnectionAllowed_Restricted covers the admin-only-by-default
// connection semantics added for the platform-admin self-connection
// (issue #543): a restricted connection requires an explicit Allow match,
// while ordinary connections keep the empty-Allow "all allowed" default.
func TestIsConnectionAllowed_Restricted(t *testing.T) {
	const restricted = "platform-admin"

	tests := []struct {
		name    string
		persona *Persona
		conn    string
		want    bool
	}{
		{
			name:    "non-admin empty Allow denied for restricted",
			persona: &Persona{Name: "analyst", Connections: ConnectionRules{}},
			conn:    restricted,
			want:    false,
		},
		{
			name:    "non-admin empty Allow still allowed for ordinary",
			persona: &Persona{Name: "analyst", Connections: ConnectionRules{}},
			conn:    "trino-prod",
			want:    true,
		},
		{
			name:    "admin wildcard Allow reaches restricted",
			persona: AdminPersona(),
			conn:    restricted,
			want:    true,
		},
		{
			name:    "non-admin granted via explicit Allow",
			persona: &Persona{Name: "ops", Connections: ConnectionRules{Allow: []string{"platform-admin"}}},
			conn:    restricted,
			want:    true,
		},
		{
			name:    "non-admin explicit Allow not matching restricted is denied",
			persona: &Persona{Name: "ops", Connections: ConnectionRules{Allow: []string{"trino-*"}}},
			conn:    restricted,
			want:    false,
		},
		{
			name:    "deny precedence even with wildcard allow",
			persona: &Persona{Name: "admin2", Connections: ConnectionRules{Allow: []string{"*"}, Deny: []string{"platform-admin"}}},
			conn:    restricted,
			want:    false,
		},
		{
			name:    "empty connection name always allowed",
			persona: &Persona{Name: "analyst", Connections: ConnectionRules{}},
			conn:    "",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewToolFilter(nil)
			f.SetRestrictedConnections([]string{restricted})
			if got := f.IsConnectionAllowed(tt.persona, tt.conn); got != tt.want {
				t.Errorf("IsConnectionAllowed(%s, %q) = %v; want %v", tt.persona.Name, tt.conn, got, tt.want)
			}
		})
	}
}

func TestSetRestrictedConnections_ReplaceAndClear(t *testing.T) {
	f := NewToolFilter(nil)
	p := &Persona{Name: "analyst", Connections: ConnectionRules{}}

	f.SetRestrictedConnections([]string{"platform-admin", ""})
	if f.IsConnectionAllowed(p, "platform-admin") {
		t.Error("platform-admin should be restricted (denied) for empty-Allow persona")
	}
	// Empty name in the set is ignored, not treated as a restriction.
	if !f.IsConnectionAllowed(p, "other") {
		t.Error("unrelated connection should remain allowed")
	}

	// Clearing the set lifts the restriction.
	f.SetRestrictedConnections(nil)
	if !f.IsConnectionAllowed(p, "platform-admin") {
		t.Error("after clearing, platform-admin should fall back to the empty-Allow default (allowed)")
	}
}

func TestAuthorizer_SetRestrictedConnections(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(AdminPersona()); err != nil {
		t.Fatalf("register admin: %v", err)
	}
	analyst := &Persona{Name: "analyst", Roles: []string{"analyst"}, Tools: ToolRules{Allow: []string{"*"}}}
	if err := reg.Register(analyst); err != nil {
		t.Fatalf("register analyst: %v", err)
	}
	mapper := &mockRoleMapper{
		mapToPersonaFunc: func(_ context.Context, roles []string) (*Persona, error) {
			if slices.Contains(roles, "admin") {
				p, _ := reg.Get("admin")
				return p, nil
			}
			return analyst, nil
		},
	}
	a := NewAuthorizer(reg, mapper)
	a.SetRestrictedConnections([]string{"platform-admin"})

	// analyst denied the restricted connection
	allowed, _, _ := a.IsAuthorized(context.Background(), "", []string{"analyst"}, "api_invoke_endpoint", "platform-admin")
	if allowed {
		t.Error("analyst should be denied the restricted platform-admin connection")
	}
	// admin allowed
	allowedAdmin, _, _ := a.IsAuthorized(context.Background(), "", []string{"admin"}, "api_invoke_endpoint", "platform-admin")
	if !allowedAdmin {
		t.Error("admin should be allowed the restricted platform-admin connection")
	}
}
