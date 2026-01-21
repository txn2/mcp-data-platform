package auth

import (
	"fmt"
	"strings"
)

// ClaimsExtractor extracts values from JWT claims.
type ClaimsExtractor struct {
	// RoleClaimPath is the dot-separated path to roles in claims.
	// e.g., "realm_access.roles" or "roles"
	RoleClaimPath string

	// RolePrefix filters roles to those starting with this prefix.
	RolePrefix string

	// GroupClaimPath is the dot-separated path to groups in claims.
	GroupClaimPath string

	// EmailClaimPath is the path to the email claim.
	EmailClaimPath string

	// NameClaimPath is the path to the name claim.
	NameClaimPath string

	// SubjectClaimPath is the path to the subject claim.
	SubjectClaimPath string
}

// DefaultClaimsExtractor returns an extractor with common defaults.
func DefaultClaimsExtractor() *ClaimsExtractor {
	return &ClaimsExtractor{
		RoleClaimPath:    "roles",
		GroupClaimPath:   "groups",
		EmailClaimPath:   "email",
		NameClaimPath:    "name",
		SubjectClaimPath: "sub",
	}
}

// Extract extracts user context from claims.
func (e *ClaimsExtractor) Extract(claims map[string]any) (*UserContext, error) {
	uc := &UserContext{
		Claims:   claims,
		AuthType: "oidc",
	}

	// Extract subject (user ID)
	if sub := e.getStringValue(claims, e.SubjectClaimPath); sub != "" {
		uc.UserID = sub
	}

	// Extract email
	if email := e.getStringValue(claims, e.EmailClaimPath); email != "" {
		uc.Email = email
	}

	// Extract name
	if name := e.getStringValue(claims, e.NameClaimPath); name != "" {
		uc.Name = name
	}

	// Extract roles
	if e.RoleClaimPath != "" {
		roles := e.getStringSlice(claims, e.RoleClaimPath)
		if e.RolePrefix != "" {
			roles = filterByPrefix(roles, e.RolePrefix)
		}
		uc.Roles = roles
	}

	// Extract groups
	if e.GroupClaimPath != "" {
		uc.Groups = e.getStringSlice(claims, e.GroupClaimPath)
	}

	return uc, nil
}

// getStringValue gets a string value at a dot-separated path.
func (e *ClaimsExtractor) getStringValue(claims map[string]any, path string) string {
	value := e.getValue(claims, path)
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

// getStringSlice gets a string slice at a dot-separated path.
func (e *ClaimsExtractor) getStringSlice(claims map[string]any, path string) []string {
	value := e.getValue(claims, path)
	if arr, ok := value.([]any); ok {
		result := make([]string, 0, len(arr))
		for _, v := range arr {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	if arr, ok := value.([]string); ok {
		return arr
	}
	return nil
}

// getValue gets a value at a dot-separated path.
func (e *ClaimsExtractor) getValue(claims map[string]any, path string) any {
	if path == "" {
		return nil
	}

	parts := strings.Split(path, ".")
	var current any = claims

	for _, part := range parts {
		if m, ok := current.(map[string]any); ok {
			current = m[part]
		} else {
			return nil
		}
	}

	return current
}

// filterByPrefix filters strings to those starting with prefix.
func filterByPrefix(items []string, prefix string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return result
}

// ValidateClaims validates required claims are present.
func ValidateClaims(claims map[string]any, required []string) error {
	for _, key := range required {
		if _, ok := claims[key]; !ok {
			return fmt.Errorf("missing required claim: %s", key)
		}
	}
	return nil
}
