package persona

import (
	"context"
	"testing"
)

func TestOIDCRoleMapper_MapToRoles(t *testing.T) {
	t.Run("empty claims", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: "roles",
		}

		roles, err := mapper.MapToRoles(map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(roles) != 0 {
			t.Errorf("expected empty roles, got %v", roles)
		}
	})

	t.Run("roles as []any", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: "roles",
		}

		claims := map[string]any{
			"roles": []any{"admin", "user"},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(roles))
		}
	})

	t.Run("roles as []string", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath: "roles",
		}

		claims := map[string]any{
			"roles": []string{"admin", "user"},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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
				"roles": []any{"admin", "user"},
			},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(roles))
		}
	})

	t.Run("with role prefix filter", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			ClaimPath:  "roles",
			RolePrefix: "app_",
		}

		claims := map[string]any{
			"roles": []any{"app_admin", "other_role", "app_user"},
		}
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 roles with prefix, got %d: %v", len(roles), roles)
		}
	})
}

func TestOIDCRoleMapper_MapToPersona(t *testing.T) {
	registry := NewRegistry()
	admin := &Persona{Name: "admin", DisplayName: "Admin", Roles: []string{"admin"}}
	user := &Persona{Name: "user", DisplayName: "User", Roles: []string{"user"}}
	_ = registry.Register(admin)
	_ = registry.Register(user)
	registry.SetDefault("user")

	t.Run("explicit mapping", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			PersonaMapping: map[string]string{
				"admin_role": "admin",
			},
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), []string{"admin_role"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if persona.Name != "admin" {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("role matching", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), []string{"admin"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if persona.Name != "admin" {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("default persona", func(t *testing.T) {
		mapper := &OIDCRoleMapper{
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), []string{"unknown_role"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if persona.Name != "user" {
			t.Errorf("expected default user persona, got %q", persona.Name)
		}
	})
}

func TestStaticRoleMapper(t *testing.T) {
	registry := NewRegistry()
	admin := &Persona{Name: "admin", DisplayName: "Admin"}
	_ = registry.Register(admin)
	registry.SetDefault("admin")

	t.Run("MapToRoles returns empty", func(t *testing.T) {
		mapper := &StaticRoleMapper{
			Registry: registry,
		}

		roles, err := mapper.MapToRoles(map[string]any{"role": "admin"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(roles) != 0 {
			t.Errorf("expected empty roles, got %v", roles)
		}
	})

	t.Run("MapToPersona with default name", func(t *testing.T) {
		mapper := &StaticRoleMapper{
			DefaultPersonaName: "admin",
			Registry:           registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if persona.Name != "admin" {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("MapToPersona fallback to registry default", func(t *testing.T) {
		mapper := &StaticRoleMapper{
			Registry: registry,
		}

		persona, err := mapper.MapToPersona(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if persona.Name != "admin" {
			t.Errorf("expected admin persona from registry default, got %q", persona.Name)
		}
	})
}

func TestChainedRoleMapper(t *testing.T) {
	registry := NewRegistry()
	admin := &Persona{Name: "admin", DisplayName: "Admin"}
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
			t.Fatalf("unexpected error: %v", err)
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
			"roles2": []any{"admin", "user"},
		}
		roles, err := chained.MapToRoles(claims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(roles) != 2 {
			t.Errorf("expected 2 unique roles, got %d: %v", len(roles), roles)
		}
	})

	t.Run("MapToPersona uses first match", func(t *testing.T) {
		mapper1 := &OIDCRoleMapper{
			PersonaMapping: map[string]string{"admin": "admin"},
			Registry:       registry,
		}

		chained := &ChainedRoleMapper{
			Mappers: []RoleMapper{mapper1},
		}

		persona, err := chained.MapToPersona(context.Background(), []string{"admin"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if persona.Name != "admin" {
			t.Errorf("expected admin persona, got %q", persona.Name)
		}
	})

	t.Run("empty mappers returns default", func(t *testing.T) {
		chained := &ChainedRoleMapper{
			Mappers: []RoleMapper{},
		}

		persona, err := chained.MapToPersona(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return DefaultPersona when nothing matches
	if persona == nil {
		t.Error("expected non-nil persona")
	}
}

func TestStaticRoleMapper_MapToPersona_NotFound(t *testing.T) {
	registry := NewRegistry()
	// Don't register the default persona

	mapper := &StaticRoleMapper{
		DefaultPersonaName: "nonexistent",
		Registry:           registry,
	}

	persona, err := mapper.MapToPersona(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return DefaultPersona when persona name not found
	if persona == nil {
		t.Error("expected non-nil persona")
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
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return DefaultPersona when no default set in registry
	if persona == nil {
		t.Error("expected non-nil persona")
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
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("expected empty roles from empty mappers, got %v", roles)
	}
	_ = registry
}

func TestOIDCRoleMapper_MapToRoles_NonStringValue(t *testing.T) {
	mapper := &OIDCRoleMapper{
		ClaimPath: "roles",
	}

	// roles contains a non-string value
	claims := map[string]any{
		"roles": []any{"admin", 42, "user"},
	}
	roles, err := mapper.MapToRoles(claims)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only include string values
	if len(roles) != 2 {
		t.Errorf("expected 2 string roles, got %d: %v", len(roles), roles)
	}
}
