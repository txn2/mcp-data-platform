package resource

import (
	"fmt"
	"strings"
)

// DefaultURIScheme is used when no scheme is configured.
const DefaultURIScheme = "mcp"

// BuildURI constructs the canonical resource URI from its components.
func BuildURI(scheme string, scope Scope, scopeID, category, filename string) string {
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	switch scope {
	case ScopeGlobal:
		return fmt.Sprintf("%s://global/%s/%s", scheme, category, filename)
	case ScopePersona:
		return fmt.Sprintf("%s://persona/%s/%s/%s", scheme, scopeID, category, filename)
	case ScopeUser:
		return fmt.Sprintf("%s://user/%s/%s/%s", scheme, scopeID, category, filename)
	default:
		return fmt.Sprintf("%s://unknown/%s/%s", scheme, category, filename)
	}
}

// BuildS3Key constructs the S3 object key for a resource blob.
func BuildS3Key(scope Scope, scopeID, resourceID, filename string) string {
	scopeDir := string(scope)
	scopeIDDir := "global"
	if scopeID != "" {
		scopeIDDir = scopeID
	}
	return fmt.Sprintf("resources/%s/%s/%s/%s", scopeDir, scopeIDDir, resourceID, filename)
}

// ParseURI extracts scope, scopeID, and path from a resource URI.
// Returns an error if the URI does not match the expected format.
func ParseURI(scheme, uri string) (scope Scope, scopeID, path string, err error) {
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	prefix := scheme + "://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", "", fmt.Errorf("URI does not start with %s: %s", prefix, uri)
	}
	rest := strings.TrimPrefix(uri, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("URI missing path: %s", uri)
	}

	scopeStr := parts[0]
	remainder := parts[1]

	switch scopeStr {
	case "global":
		return ScopeGlobal, "", remainder, nil
	case "persona":
		subParts := strings.SplitN(remainder, "/", 2)
		if len(subParts) < 2 {
			return "", "", "", fmt.Errorf("persona URI missing scope_id: %s", uri)
		}
		return ScopePersona, subParts[0], subParts[1], nil
	case "user":
		subParts := strings.SplitN(remainder, "/", 2)
		if len(subParts) < 2 {
			return "", "", "", fmt.Errorf("user URI missing scope_id: %s", uri)
		}
		return ScopeUser, subParts[0], subParts[1], nil
	default:
		return "", "", "", fmt.Errorf("unknown scope in URI: %s", scopeStr)
	}
}
