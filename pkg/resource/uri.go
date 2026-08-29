package resource

import (
	"fmt"
	"strings"
)

// DefaultURIScheme is used when no scheme is configured.
const DefaultURIScheme = "mcp"

// BuildURI constructs the canonical resource URI from its components: the
// library prefix, the folder path inside it, and the filename.
func BuildURI(scheme string, scope Scope, scopeID, path, filename string) string {
	return URIInLibrary(scheme, scope, scopeID, path+"/"+filename)
}

// URIInLibrary constructs a resource URI naming a library and a tail within it,
// where tail is the "path/filename" BuildURI composes.
//
// It is separate from BuildURI because the two halves of the tail move
// independently: refiling a resource in another library keeps its folder path,
// and refiling it in another folder keeps its library. Each rewrite composes the
// half it changes with the half it does not and passes the result through here
// (see RelocatedURI).
func URIInLibrary(scheme string, scope Scope, scopeID, tail string) string {
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	switch scope {
	case ScopeGlobal:
		return fmt.Sprintf("%s://global/%s", scheme, tail)
	case ScopePersona:
		return fmt.Sprintf("%s://persona/%s/%s", scheme, scopeID, tail)
	case ScopeUser:
		return fmt.Sprintf("%s://user/%s/%s", scheme, scopeID, tail)
	default:
		return fmt.Sprintf("%s://unknown/%s", scheme, tail)
	}
}

// RelocatedURI is the URI a resource takes when it is refiled: under the target
// library's prefix, at the target folder path, keeping its own filename.
//
// The filename is read off the stored URI rather than off the row's filename
// column, so a resource whose stored address was minted under an older scheme
// keeps answering at the address its citations use for everything except the
// half being changed. A stored URI that will not parse, or whose tail carries no
// filename, falls back to the row's own filename, which is what the URI would
// have been had it been minted now: that is the only answer available for an
// address the mover never chose, and it is better than refusing the move over
// it.
func RelocatedURI(scheme string, r *Resource, scope Scope, scopeID, path string) string {
	return URIInLibrary(scheme, scope, scopeID, path+"/"+URIFilename(scheme, r))
}

// URIFilename is the last segment of a resource's stored URI, falling back to
// its filename column when the stored URI does not parse into one.
func URIFilename(scheme string, r *Resource) string {
	if p, err := ParseURI(scheme, r.URI); err == nil {
		if i := strings.LastIndex(p.Path, "/"); i >= 0 && i+1 < len(p.Path) {
			return p.Path[i+1:]
		}
	}
	return r.Filename
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
