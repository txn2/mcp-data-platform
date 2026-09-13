package persona

import (
	"context"
	"testing"
)

const (
	mapperTestUnexpectedErr = "unexpected error: %v"
	mapperTestNonStringVal  = 42
	mapperTestRoles         = "roles"
	mapperTestUser          = "user"
)

func TestOIDCRoleMapper_MapToRoles(t *testing.T) {
	t.Run("empty claims", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: mapperTestRoles,
		}

		roles, err := mapper.MapToRoles(map[string]any{})
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 0 {
			t.Errorf("expected empty roles, got %v", roles)
		}
	})

	t.Run("roles as []any", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: mapperTestRoles,
		}

		claims := map[string]any{
			mapperTestRoles: []any{"admin", mapperTestUser},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(roles))
		}
	})

	t.Run("roles as []string", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: mapperTestRoles,
		}

		claims := map[string]any{
			mapperTestRoles: []string{"admin", mapperTestUser},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(roles))
		}
	})

	t.Run("nested path", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: "realm_access.roles",
		}

		claims := map[string]any{
			"realm_access": map[string]any{
				mapperTestRoles: []any{"admin", mapperTestUser},
			},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(roles))
		}
	})

	t.Run("with role prefix filter", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath:  mapperTestRoles,
			RolePrefix: "app_",
		}

		claims := map[string]any{
			mapperTestRoles: []any{"app_admin", "other_role", "app_user"},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles with prefix, got %d: %v", len(roles), roles)
		}
	})
}

func TestOIDCRoleMapper_MapToPersona(t *testing.T) {
	registry := NewRegistry()
	admin := &Persona{Name: filterTestAdmin, DisplayName: "Admin", Roles: []string{filterTestAdmin}}
	user := &Persona{Name: mapperTestUser, DisplayName: "User", Roles: []string{mapperTestUser}}
	_ = registry.Register(admin)
	_ = registry.Register(user)

	t.Run("explicit mapping", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			PersonaMapping: map[string]string{
				"admin_role": filterTestAdmin,
			},
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), []string{"admin_role"})
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if persona.Name != filterTestAdmin {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("role matching", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), []string{filterTestAdmin})
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if persona.Name != filterTestAdmin {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	// A role that matches no persona resolves to the deny-all DefaultPersona:
	// there is no configured fallback that would hand an unmapped caller
	// another persona's tools.
	t.Run("unmapped role denies all", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), []string{"unknown_role"})
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if persona.Name != "default" {
			t.Errorf("expected deny-all default persona, got %q", persona.Name)
		}
		filter := NewToolFilter(registry)
		if filter.IsAllowed(persona, "trino_query") {
			t.Error("unmapped caller was allowed trino_query")
		}
	})
}

func TestStaticRoleMapper(t *testing.T) {
	registry := NewRegistry()
	admin := &Persona{Name: filterTestAdmin, DisplayName: "Admin"}
	_ = registry.Register(admin)

	t.Run("MapToRoles returns empty", func(t *testing.T) {
		mapper := &StaticRoleMapper{
			Registry: registry,
		}

		roles, err := mapper.MapToRoles(map[string]any{"role": "admin"})
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 0 {
			t.Errorf("expected empty roles, got %v", roles)
		}
	})

	t.Run("MapToPersona with configured name", func(t *testing.T) {
		mapper := &StaticRoleMapper{
			PersonaName: filterTestAdmin,
			Registry:    registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), nil)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if persona.Name != filterTestAdmin {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("MapToPersona with no configured name denies all", func(t *testing.T) {
		mapper := &StaticRoleMapper{
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), nil)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if persona.Name != "default" {
			t.Errorf("expected deny-all default persona, got %q", persona.Name)
		}
	})
}

func TestChainedRoleMapper(t *testing.T) {
	registry := NewRegistry()
	admin := &Persona{Name: filterTestAdmin, DisplayName: "Admin"}
	_ = registry.Register(admin)

	t.Run("MapToRoles aggregates from all mappers", func(t *testing.T) {
		mapper1 := &OIDCRoleMapper{
			ClaimPath: "roles1",
			Registry:  registry,
		}
		mapper2 := &OIDCRoleMapper{
			ClaimPath: "roles2",
			Registry:  registry,
		}

		chained := &ChainedRoleMapper{
			Mappers: []RoleMapper{mapper1, mapper2},
		}

		claims := map[string]any{
			"roles1": []any{"role1"},
			"roles2": []any{"role2"},
		}
		roles, err := chained.MapToRoles(claims)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles, got %d: %v", len(roles), roles)
		}
	})

	t.Run("MapToRoles deduplicates", func(t *testing.T) {
		mapper1 := &OIDCRoleMapper{
			ClaimPath: "roles1",
			Registry:  registry,
		}
		mapper2 := &OIDCRoleMapper{
			ClaimPath: "roles2",
			Registry:  registry,
		}

		chained := &ChainedRoleMapper{
			Mappers: []RoleMapper{mapper1, mapper2},
		}

		claims := map[string]any{
			"roles1": []any{"admin"},
			"roles2": []any{"admin", mapperTestUser},
		}
		roles, err := chained.MapToRoles(claims)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 unique roles, got %d: %v", len(roles), roles)
		}
	})

	t.Run("MapToPersona uses first match", func(t *testing.T) {
		mapper1 := &OIDCRoleMapper{
			PersonaMapping: map[string]string{filterTestAdmin: filterTestAdmin},
			Registry:       registry,
		}

		chained := &ChainedRoleMapper{
			Mappers: []RoleMapper{mapper1},
		}

		persona, err := chained.MapToPersona(context.Background(), []string{filterTestAdmin})
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		if persona.Name != filterTestAdmin {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("empty mappers returns default", func(t *testing.T) {
		chained := &ChainedRoleMapper{
			Mappers: []RoleMapper{},
		}

		persona, err := chained.MapToPersona(context.Background(), nil)
		if err != nil {
			t.Fatalf(mapperTestUnexpectedErr, err)
		}
		// Should return DefaultPersona
		if persona == nil {
			t.Error("expected non-nil persona")
		}
	})
}

func TestGetNestedValue(t *testing.T) {
	data := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"value": "found",
			},
		},
		"simple": "direct",
	}

	tests := []struct {
		name     string
		path     string
		expected any
	}{
		{"single level", "simple", "direct"},
		{"nested path", "level1.level2.value", "found"},
		{"missing path", "nonexistent", nil},
		{"partial path", "level1.nonexistent", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNestedValue(data, tt.path)
			if result != tt.expected {
				t.Errorf("getNestedValue(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}

	t.Run("path through non-map", func(t *testing.T) {
		data := map[string]any{
			"simple": "value",
		}
		result := getNestedValue(data, "simple.deeper")
		if result != nil {
			t.Errorf("expected nil for path through non-map, got %v", result)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		data := map[string]any{
			"key": "value",
		}
		result := getNestedValue(data, "")
		if result != nil {
			t.Errorf("expected nil for empty path, got %v", result)
		}
	})
}

func TestOIDCRoleMapper_MapToPersona_NoMatch(t *testing.T) {
	registry := NewRegistry()
	// Don't register any personas or set default

	mapper := &OIDCRoleMapper{
		Registry: registry,
	}

	persona, err := mapper.MapToPersona(context.Background(), []string{"unknown_role"})
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	if persona == nil || persona.Name != "default" {
		t.Fatalf("expected deny-all default persona, got %+v", persona)
	}
}

func TestStaticRoleMapper_MapToPersona_NotFound(t *testing.T) {
	registry := NewRegistry()
	// Don't register the default persona

	mapper := &StaticRoleMapper{
		PersonaName: "nonexistent",
		Registry:    registry,
	}

	persona, err := mapper.MapToPersona(context.Background(), nil)
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	// An unregistered persona name yields the deny-all DefaultPersona rather
	// than any persona that happens to be registered.
	if persona == nil || persona.Name != "default" {
		t.Fatalf("expected deny-all default persona, got %+v", persona)
	}
	if NewToolFilter(registry).IsAllowed(persona, "trino_query") {
		t.Error("persona resolved from an unregistered name allowed trino_query")
	}
}

func TestStaticRoleMapper_MapToPersona_NoDefault(t *testing.T) {
	registry := NewRegistry()
	// Don't set a default

	mapper := &StaticRoleMapper{
		Registry: registry,
	}

	persona, err := mapper.MapToPersona(context.Background(), nil)
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	if persona == nil || persona.Name != "default" {
		t.Fatalf("expected deny-all default persona, got %+v", persona)
	}
}

func TestChainedRoleMapper_MapToRoles_WithError(t *testing.T) {
	// ChainedRoleMapper should handle errors from individual mappers gracefully
	registry := NewRegistry()

	chained := &ChainedRoleMapper{
		Mappers: []RoleMapper{},
	}

	roles, err := chained.MapToRoles(map[string]any{})
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	if len(roles) != 0 {
		t.Errorf("expected empty roles from empty mappers, got %v", roles)
	}
	_ = registry
}

func TestOIDCRoleMapper_MapToRoles_NonStringValue(t *testing.T) {
	mapper := &OIDCRoleMapper{
		ClaimPath: mapperTestRoles,
	}

	// roles contains a non-string value
	claims := map[string]any{
		mapperTestRoles: []any{"admin", mapperTestNonStringVal, mapperTestUser},
	}
	roles, err := mapper.MapToRoles(claims)
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	// Should only include string values
	if len(roles) != 2 {
		t.Errorf("expected 2 string roles, got %d: %v", len(roles), roles)
	}
}

func TestOIDCRoleMapper_MapToPersona_MappingToNonExistent(t *testing.T) {
	registry := NewRegistry()
	user := &Persona{Name: mapperTestUser, DisplayName: "User", Roles: []string{mapperTestUser}}
	_ = registry.Register(user)

	mapper := &OIDCRoleMapper{
		PersonaMapping: map[string]string{
			"admin_role": "nonexistent", // persona doesn't exist
		},
		Registry: registry,
	}

	// The explicit mapping names a persona that is not registered, so it does
	// not apply. "admin_role" also matches no persona's roles, so the caller is
	// unmapped and gets the deny-all default rather than the one other
	// registered persona.
	persona, err := mapper.MapToPersona(context.Background(), []string{"admin_role"})
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	if persona.Name != "default" {
		t.Errorf("expected deny-all default persona, got %q", persona.Name)
	}
}

func TestOIDCRoleMapper_MapToRoles_StringSliceWithPrefix(t *testing.T) {
	mapper := &OIDCRoleMapper{
		ClaimPath:  mapperTestRoles,
		RolePrefix: "app_",
	}

	// Test with []string type (not []any)
	claims := map[string]any{
		mapperTestRoles: []string{"app_admin", "other_role", "app_user"},
	}
	roles, err := mapper.MapToRoles(claims)
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	if len(roles) != 2 {
		t.Errorf("expected 2 roles with prefix, got %d: %v", len(roles), roles)
	}
}

func TestChainedRoleMapper_MapToPersona_AllMappersFail(t *testing.T) {
	// Create mappers that return nil personas
	mapper1 := &OIDCRoleMapper{
		Registry: NewRegistry(), // Empty registry, no defaults
	}
	mapper2 := &StaticRoleMapper{
		Registry: NewRegistry(), // Empty registry, no defaults
	}

	chained := &ChainedRoleMapper{
		Mappers: []RoleMapper{mapper1, mapper2},
	}

	persona, err := chained.MapToPersona(context.Background(), []string{"unknown"})
	if err != nil {
		t.Fatalf(mapperTestUnexpectedErr, err)
	}
	// Should return DefaultPersona when all mappers fail
	if persona == nil {
		t.Error("expected non-nil default persona")
	}
}

// TestOIDCRoleMapper_Resolve holds the three answers MapToPersona collapses:
// an explicit mapping, a registry role match, and no persona at all, which
// Resolve reports as unmapped instead of the deny-all default (#1705).
func TestOIDCRoleMapper_Resolve(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&Persona{Name: "analyst", Roles: []string{"dp_analyst"}, Priority: 1})
	_ = registry.Register(&Persona{Name: "admin", Roles: []string{"dp_admin"}, Priority: 10})
	mapper := &OIDCRoleMapper{
		PersonaMapping: map[string]string{"sso_admins": "admin", "retired": "gone"},
		Registry:       registry,
	}

	for _, tc := range []struct {
		name  string
		roles []string
		want  string
	}{
		{"explicit mapping wins", []string{"dp_analyst", "sso_admins"}, "admin"},
		{"registry role match", []string{"dp_analyst"}, "analyst"},
		{"highest priority of several matches", []string{"dp_analyst", "dp_admin"}, "admin"},
		{"a persona name is not a role", []string{"admin"}, ""},
		{"mapping to an unregistered persona", []string{"retired"}, ""},
		{"no roles", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := mapper.Resolve(tc.roles)
			if tc.want == "" {
				if ok || p != nil {
					t.Fatalf("Resolve(%v) = %+v, true; want unmapped", tc.roles, p)
				}
				return
			}
			if !ok || p.Name != tc.want {
				t.Fatalf("Resolve(%v) = %+v, %v; want %s", tc.roles, p, ok, tc.want)
			}
		})
	}
}

// TestOIDCRoleMapper_GrantingRoles lists what an operator may choose a key's
// roles from: every persona role and every mapped role whose persona exists,
// sorted, once each.
func TestOIDCRoleMapper_GrantingRoles(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&Persona{Name: "analyst", Roles: []string{"dp_analyst", "analyst"}})
	_ = registry.Register(&Persona{Name: "admin", Roles: []string{"dp_admin", "analyst"}})
	mapper := &OIDCRoleMapper{
		PersonaMapping: map[string]string{"sso_admins": "admin", "retired": "gone"},
		Registry:       registry,
	}

	got := mapper.GrantingRoles()
	want := []string{"analyst", "dp_admin", "dp_analyst", "sso_admins"}
	if len(got) != len(want) {
		t.Fatalf("GrantingRoles() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GrantingRoles() = %v; want %v", got, want)
		}
	}

	if roles := (&OIDCRoleMapper{Registry: NewRegistry()}).GrantingRoles(); len(roles) != 0 {
		t.Fatalf("a deployment with no personas grants no roles, got %v", roles)
	}
}
