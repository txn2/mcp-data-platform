package resource

import (
	"testing"
)

func TestCanWriteScope(t *testing.T) {
	admin := Claims{Sub: "admin-1", Roles: []string{"admin"}}
	user := Claims{Sub: "user-1", Roles: []string{"analyst"}}
	personaAdmin := Claims{Sub: "pa-1", Roles: []string{"persona-admin:finance"}}
	// Simulates an API key or OIDC user with prefixed role dp_admin mapped to admin persona.
	prefixedAdmin := Claims{Sub: "pa-2", Roles: []string{"dp_admin"}, IsAdmin: true}

	tests := []struct {
		name    string
		claims  Claims
		scope   Scope
		scopeID string
		want    bool
	}{
		{"admin writes global", admin, ScopeGlobal, "", true},
		{"admin writes persona", admin, ScopePersona, "finance", true},
		{"user cannot write global", user, ScopeGlobal, "", false},
		{"user writes own user scope", user, ScopeUser, "user-1", true},
		{"user cannot write other user scope", user, ScopeUser, "other", false},
		{"persona admin writes their persona", personaAdmin, ScopePersona, "finance", true},
		{"persona admin cannot write other persona", personaAdmin, ScopePersona, "engineering", false},
		{"prefixed role admin writes global via IsAdmin", prefixedAdmin, ScopeGlobal, "", true},
		{"prefixed role admin writes persona via IsAdmin", prefixedAdmin, ScopePersona, "finance", true},
		{"prefixed role admin writes user scope via IsAdmin", prefixedAdmin, ScopeUser, "other-user", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanWriteScope(tt.claims, tt.scope, tt.scopeID)
			if got != tt.want {
				t.Errorf("CanWriteScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanModifyResource(t *testing.T) {
	r := &Resource{Scope: ScopePersona, ScopeID: "finance", UploaderSub: "uploader-1"}

	// Uploader can modify
	if !CanModifyResource(Claims{Sub: "uploader-1"}, r) {
		t.Error("uploader should be able to modify")
	}

	// Admin can modify
	if !CanModifyResource(Claims{Sub: "other", Roles: []string{"admin"}}, r) {
		t.Error("admin should be able to modify")
	}

	// Random user cannot modify
	if CanModifyResource(Claims{Sub: "random", Roles: []string{"analyst"}}, r) {
		t.Error("random user should not be able to modify")
	}
}

func TestCanReadResource(t *testing.T) {
	tests := []struct {
		name   string
		claims Claims
		res    *Resource
		want   bool
	}{
		{
			"global visible to all",
			Claims{Sub: "anyone"},
			&Resource{Scope: ScopeGlobal},
			true,
		},
		{
			"user visible to owner",
			Claims{Sub: "user-1"},
			&Resource{Scope: ScopeUser, ScopeID: "user-1"},
			true,
		},
		{
			"user not visible to other",
			Claims{Sub: "user-2"},
			&Resource{Scope: ScopeUser, ScopeID: "user-1"},
			false,
		},
		{
			"persona visible to member",
			Claims{Sub: "u1", Personas: []string{"finance"}},
			&Resource{Scope: ScopePersona, ScopeID: "finance"},
			true,
		},
		{
			"persona not visible to non-member",
			Claims{Sub: "u1", Personas: []string{"engineering"}},
			&Resource{Scope: ScopePersona, ScopeID: "finance"},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanReadResource(tt.claims, tt.res)
			if got != tt.want {
				t.Errorf("CanReadResource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisibleScopes(t *testing.T) {
	claims := Claims{
		Sub:      "user-1",
		Email:    "user@example.com",
		Personas: []string{"finance", "analyst"},
	}
	scopes := VisibleScopes(claims)

	// Should have: global, user/user-1, user/user@example.com, persona/finance, persona/analyst
	if len(scopes) != 5 {
		t.Fatalf("expected 5 scopes, got %d: %v", len(scopes), scopes)
	}

	if scopes[0].Scope != ScopeGlobal {
		t.Errorf("first scope should be global, got %v", scopes[0])
	}
	if scopes[1].Scope != ScopeUser || scopes[1].ScopeID != "user-1" {
		t.Errorf("second scope should be user/user-1, got %v", scopes[1])
	}
	if scopes[2].Scope != ScopeUser || scopes[2].ScopeID != "user@example.com" {
		t.Errorf("third scope should be user/user@example.com, got %v", scopes[2])
	}
}

func TestVisibleScopes_EmailSameAsSub(t *testing.T) {
	// When email equals sub, don't duplicate.
	claims := Claims{Sub: "same", Email: "same"}
	scopes := VisibleScopes(claims)
	userScopes := 0
	for _, s := range scopes {
		if s.Scope == ScopeUser {
			userScopes++
		}
	}
	if userScopes != 1 {
		t.Errorf("expected 1 user scope, got %d", userScopes)
	}
}

func TestIsPlatformAdmin(t *testing.T) {
	if !isPlatformAdmin(Claims{Roles: []string{"admin"}}) {
		t.Error("admin role should be platform admin")
	}
	if !isPlatformAdmin(Claims{Roles: []string{"platform-admin"}}) {
		t.Error("platform-admin role should be platform admin")
	}
	if isPlatformAdmin(Claims{Roles: []string{"analyst"}}) {
		t.Error("analyst should not be platform admin")
	}
	// IsAdmin flag set by caller based on persona resolution — works
	// regardless of role name (e.g., dp_admin, custom_superuser).
	if !isPlatformAdmin(Claims{Roles: []string{"dp_admin"}, IsAdmin: true}) {
		t.Error("IsAdmin=true should be platform admin regardless of role name")
	}
	if !isPlatformAdmin(Claims{IsAdmin: true}) {
		t.Error("IsAdmin=true with no roles should still be platform admin")
	}
	if isPlatformAdmin(Claims{Roles: []string{"dp_analyst"}, IsAdmin: false}) {
		t.Error("IsAdmin=false with non-admin role should not be platform admin")
	}
}

func TestIsPersonaAdmin(t *testing.T) {
	if !isPersonaAdmin(Claims{Roles: []string{"persona-admin:finance"}}, "finance") {
		t.Error("persona-admin:finance should be admin of finance")
	}
	if isPersonaAdmin(Claims{Roles: []string{"persona-admin:finance"}}, "engineering") {
		t.Error("persona-admin:finance should not be admin of engineering")
	}
	if !isPersonaAdmin(Claims{Roles: []string{"admin"}}, "anything") {
		t.Error("platform admin should be persona admin of any persona")
	}
}
