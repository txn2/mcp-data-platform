package resource

import (
	"fmt"
	"strings"
)

// DefaultURIScheme is used when no scheme is configured.
const DefaultURIScheme = "mcp"

// BuildURI constructs the canonical resource URI from its components.
func BuildURI(scheme string, scope Scope, scopeID, category, filename string) string {
	return URIInLibrary(scheme, scope, scopeID, category+"/"+filename)
}

// URIInLibrary constructs a resource URI naming a library and a path within it,
// where path is the "category/filename" tail BuildURI composes.
//
// It is separate from BuildURI because a move must not disturb the path. A
// resource's stored URI can already disagree with its category column -- editing
// the category has never rewritten the URI -- so rebuilding the whole URI on a
// move would silently change the address as a side effect of refiling the file,
// which is not what the person asked for. The move parses the path off the URI
// it has and passes it through here (see MovedURI).
func URIInLibrary(scheme string, scope Scope, scopeID, path string) string {
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	switch scope {
	case ScopeGlobal:
		return fmt.Sprintf("%s://global/%s", scheme, path)
	case ScopePersona:
		return fmt.Sprintf("%s://persona/%s/%s", scheme, scopeID, path)
	case ScopeUser:
		return fmt.Sprintf("%s://user/%s/%s", scheme, scopeID, path)
	default:
		return fmt.Sprintf("%s://unknown/%s", scheme, path)
	}
}

// MovedURI is the URI a resource takes when it is refiled in another library:
// its own path under the target library's prefix.
//
// A stored URI that will not parse falls back to composing the path from the
// row's category and filename, which is what the URI would have been had it
// been minted now. That is the only answer available for a row whose URI
// predates the current scheme or was written by hand, and it is better than
// refusing the move over an address the mover never chose.
func MovedURI(scheme string, r *Resource, scope Scope, scopeID string) string {
	if p, err := ParseURI(scheme, r.URI); err == nil && p.Path != "" {
		return URIInLibrary(scheme, scope, scopeID, p.Path)
	}
	return BuildURI(scheme, scope, scopeID, r.Category, r.Filename)
}

// BuildS3Key constructs the S3 object key for a resource blob.
func BuildS3Key(scope Scope, scopeID, resourceID, filename string) string {
	scopeDir := string(scope)
	scopeIDDir := string(ScopeGlobal)
	if scopeID != "" {
		scopeIDDir = scopeID
	}
	return fmt.Sprintf("resources/%s/%s/%s/%s", scopeDir, scopeIDDir, resourceID, filename)
}

// ParsedURI holds the components extracted from a resource URI.
type ParsedURI struct {
	Scope   Scope
	ScopeID string
	Path    string
}

// ParseURI extracts scope, scopeID, and path from a resource URI.
// Returns an error if the URI does not match the expected format.
func ParseURI(scheme, uri string) (ParsedURI, error) {
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	prefix := scheme + "://"
	if !strings.HasPrefix(uri, prefix) {
		return ParsedURI{}, fmt.Errorf("uri does not start with %s: %s", prefix, uri)
	}
	rest := strings.TrimPrefix(uri, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		return ParsedURI{}, fmt.Errorf("uri missing path: %s", uri)
	}

	scopeStr := parts[0]
	remainder := parts[1]

	switch scopeStr {
	case string(ScopeGlobal):
		return ParsedURI{Scope: ScopeGlobal, Path: remainder}, nil
	case string(ScopePersona):
		subParts := strings.SplitN(remainder, "/", 2)
		if len(subParts) < 2 {
			return ParsedURI{}, fmt.Errorf("persona URI missing scope_id: %s", uri)
		}
		return ParsedURI{Scope: ScopePersona, ScopeID: subParts[0], Path: subParts[1]}, nil
	case string(ScopeUser):
		subParts := strings.SplitN(remainder, "/", 2)
		if len(subParts) < 2 {
			return ParsedURI{}, fmt.Errorf("user URI missing scope_id: %s", uri)
		}
		return ParsedURI{Scope: ScopeUser, ScopeID: subParts[0], Path: subParts[1]}, nil
	default:
		return ParsedURI{}, fmt.Errorf("unknown scope in URI: %s", scopeStr)
	}
}
