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
