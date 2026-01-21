package auth

import "testing"

func TestClaimsExtractor_Extract(t *testing.T) {
	extractor := &ClaimsExtractor{
		RoleClaimPath:    "realm_access.roles",
		RolePrefix:       "dp_",
		EmailClaimPath:   "email",
		NameClaimPath:    "name",
		SubjectClaimPath: "sub",
	}

	claims := map[string]any{
		"sub":   "user123",
		"email": "user@example.com",
		"name":  "Test User",
		"realm_access": map[string]any{
			"roles": []any{"dp_analyst", "dp_admin", "other_role"},
		},
	}

	uc, err := extractor.Extract(claims)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if uc.UserID != "user123" {
		t.Errorf("UserID = %q, want %q", uc.UserID, "user123")
	}
	if uc.Email != "user@example.com" {
		t.Errorf("Email = %q, want %q", uc.Email, "user@example.com")
	}
	if uc.Name != "Test User" {
		t.Errorf("Name = %q, want %q", uc.Name, "Test User")
	}
	if len(uc.Roles) != 2 {
		t.Errorf("Roles count = %d, want 2 (should filter by prefix)", len(uc.Roles))
	}
}

func TestDefaultClaimsExtractor(t *testing.T) {
	extractor := DefaultClaimsExtractor()

	if extractor.RoleClaimPath != "roles" {
		t.Errorf("RoleClaimPath = %q, want %q", extractor.RoleClaimPath, "roles")
	}
	if extractor.SubjectClaimPath != "sub" {
		t.Errorf("SubjectClaimPath = %q, want %q", extractor.SubjectClaimPath, "sub")
	}
}

func TestValidateClaims(t *testing.T) {
	t.Run("all required present", func(t *testing.T) {
		claims := map[string]any{"sub": "user", "email": "user@example.com"}
		err := ValidateClaims(claims, []string{"sub", "email"})
		if err != nil {
			t.Errorf("ValidateClaims() error = %v", err)
		}
	})

	t.Run("missing required", func(t *testing.T) {
		claims := map[string]any{"sub": "user"}
		err := ValidateClaims(claims, []string{"sub", "email"})
		if err == nil {
			t.Error("ValidateClaims() expected error for missing claim")
		}
	})
}

func TestClaimsExtractor_getValue(t *testing.T) {
	extractor := &ClaimsExtractor{}

	t.Run("empty path returns nil", func(t *testing.T) {
		claims := map[string]any{"key": "value"}
		value := extractor.getValue(claims, "")
		if value != nil {
			t.Errorf("expected nil for empty path, got %v", value)
		}
	})

	t.Run("simple path", func(t *testing.T) {
		claims := map[string]any{"key": "value"}
		value := extractor.getValue(claims, "key")
		if value != "value" {
			t.Errorf("expected 'value', got %v", value)
		}
	})

	t.Run("nested path", func(t *testing.T) {
		claims := map[string]any{
			"nested": map[string]any{
				"deep": map[string]any{
					"value": "found",
				},
			},
		}
		value := extractor.getValue(claims, "nested.deep.value")
		if value != "found" {
			t.Errorf("expected 'found', got %v", value)
		}
	})

	t.Run("non-existent path", func(t *testing.T) {
		claims := map[string]any{"key": "value"}
		value := extractor.getValue(claims, "nonexistent")
		if value != nil {
			t.Errorf("expected nil for non-existent path, got %v", value)
		}
	})

	t.Run("path through non-map value", func(t *testing.T) {
		claims := map[string]any{"key": "value"}
		value := extractor.getValue(claims, "key.nested")
		if value != nil {
			t.Errorf("expected nil for path through non-map, got %v", value)
		}
	})
}

func TestClaimsExtractor_getStringValue(t *testing.T) {
	extractor := &ClaimsExtractor{}

	t.Run("string value", func(t *testing.T) {
		claims := map[string]any{"name": "test"}
		value := extractor.getStringValue(claims, "name")
		if value != "test" {
			t.Errorf("expected 'test', got %q", value)
		}
	})

	t.Run("non-string value", func(t *testing.T) {
		claims := map[string]any{"count": 42}
		value := extractor.getStringValue(claims, "count")
		if value != "" {
			t.Errorf("expected empty string for non-string value, got %q", value)
		}
	})

	t.Run("missing value", func(t *testing.T) {
		claims := map[string]any{"key": "value"}
		value := extractor.getStringValue(claims, "missing")
		if value != "" {
			t.Errorf("expected empty string for missing value, got %q", value)
		}
	})
}

func TestClaimsExtractor_getStringSlice(t *testing.T) {
	extractor := &ClaimsExtractor{}

	t.Run("slice of any (string values)", func(t *testing.T) {
		claims := map[string]any{
			"roles": []any{"admin", "user"},
		}
		value := extractor.getStringSlice(claims, "roles")
		if len(value) != 2 {
			t.Errorf("expected 2 roles, got %d", len(value))
		}
		if value[0] != "admin" || value[1] != "user" {
			t.Errorf("unexpected roles: %v", value)
		}
	})

	t.Run("slice of any with non-string", func(t *testing.T) {
		claims := map[string]any{
			"mixed": []any{"string", 42, "another"},
		}
		value := extractor.getStringSlice(claims, "mixed")
		if len(value) != 2 {
			t.Errorf("expected 2 strings (skipping non-string), got %d", len(value))
		}
	})

	t.Run("native string slice", func(t *testing.T) {
		claims := map[string]any{
			"roles": []string{"admin", "user"},
		}
		value := extractor.getStringSlice(claims, "roles")
		if len(value) != 2 {
			t.Errorf("expected 2 roles, got %d", len(value))
		}
	})

	t.Run("non-slice value", func(t *testing.T) {
		claims := map[string]any{
			"name": "test",
		}
		value := extractor.getStringSlice(claims, "name")
		if value != nil {
			t.Errorf("expected nil for non-slice value, got %v", value)
		}
	})

	t.Run("missing value", func(t *testing.T) {
		claims := map[string]any{}
		value := extractor.getStringSlice(claims, "roles")
		if value != nil {
			t.Errorf("expected nil for missing value, got %v", value)
		}
	})
}

func TestClaimsExtractor_Extract_EdgeCases(t *testing.T) {
	t.Run("empty role claim path", func(t *testing.T) {
		extractor := &ClaimsExtractor{
			SubjectClaimPath: "sub",
		}
		claims := map[string]any{
			"sub":   "user123",
			"roles": []any{"admin"},
		}
		uc, err := extractor.Extract(claims)
		if err != nil {
			t.Fatalf("Extract() error = %v", err)
		}
		if len(uc.Roles) != 0 {
			t.Errorf("expected no roles when RoleClaimPath is empty, got %v", uc.Roles)
		}
	})

	t.Run("no role prefix filter", func(t *testing.T) {
		extractor := &ClaimsExtractor{
			RoleClaimPath:    "roles",
			SubjectClaimPath: "sub",
		}
		claims := map[string]any{
			"sub":   "user123",
			"roles": []any{"admin", "user"},
		}
		uc, err := extractor.Extract(claims)
		if err != nil {
			t.Fatalf("Extract() error = %v", err)
		}
		if len(uc.Roles) != 2 {
			t.Errorf("expected 2 roles without prefix filter, got %d", len(uc.Roles))
		}
	})

	t.Run("with groups", func(t *testing.T) {
		extractor := &ClaimsExtractor{
			SubjectClaimPath: "sub",
			GroupClaimPath:   "groups",
		}
		claims := map[string]any{
			"sub":    "user123",
			"groups": []any{"group1", "group2"},
		}
		uc, err := extractor.Extract(claims)
		if err != nil {
			t.Fatalf("Extract() error = %v", err)
		}
		if len(uc.Groups) != 2 {
			t.Errorf("expected 2 groups, got %d", len(uc.Groups))
		}
	})
}

func TestFilterByPrefix(t *testing.T) {
	t.Run("filter with prefix", func(t *testing.T) {
		items := []string{"dp_admin", "dp_user", "other", "dp_viewer"}
		filtered := filterByPrefix(items, "dp_")
		if len(filtered) != 3 {
			t.Errorf("expected 3 items with dp_ prefix, got %d", len(filtered))
		}
	})

	t.Run("empty prefix returns all", func(t *testing.T) {
		items := []string{"admin", "user"}
		filtered := filterByPrefix(items, "")
		if len(filtered) != 2 {
			t.Errorf("expected all items with empty prefix, got %d", len(filtered))
		}
	})

	t.Run("no matches", func(t *testing.T) {
		items := []string{"admin", "user"}
		filtered := filterByPrefix(items, "dp_")
		if len(filtered) != 0 {
			t.Errorf("expected no matches, got %d", len(filtered))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		items := []string{}
		filtered := filterByPrefix(items, "dp_")
		if len(filtered) != 0 {
			t.Errorf("expected empty result, got %d", len(filtered))
		}
	})
}
