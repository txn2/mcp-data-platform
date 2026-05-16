// Package catalog models versioned, globally-owned OpenAPI spec
// bundles that api-gateway connections reference.
//
// An API catalog represents the *API* (Salesforce REST, Google Drive,
// Stripe), not the credential pointing at it. A single Salesforce
// catalog backs both the sandbox and production connections in a
// deployment, so operators don't paste the same documentation N
// times to talk to N environments.
//
// Each (name, version) pair is its own catalog row. Cloning to a new
// version creates a new row; existing connections stay on the old
// catalog until the operator explicitly migrates them. The model
// never sees catalogs directly — it queries connections, and the
// toolkit resolves connection → catalog → specs internally.
package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned when a catalog or spec lookup misses.
var ErrNotFound = errors.New("catalog: not found")

// ErrConflict is returned when a uniqueness invariant would be
// violated (duplicate (name, version) on a catalog, duplicate
// spec_name within a catalog).
var ErrConflict = errors.New("catalog: conflict")

// ErrInvalidID is returned when a catalog ID does not match the slug
// shape required by the store. IDs are operator-supplied and
// immutable after creation, so we validate aggressively up front.
var ErrInvalidID = errors.New("catalog: invalid id")

// ErrInvalidBasePath is returned when an operator-supplied per-spec
// base path fails validation. Exported so callers can map it to a
// 400 response without string-matching the error message.
var ErrInvalidBasePath = errors.New("catalog: invalid base_path")

// ErrInvalidSpecName is returned when a spec name doesn't match the
// component-slug shape. Spec names appear in MCP tool output (the
// `spec` field on OperationSummary) so we constrain them to a
// model-friendly subset. The message is operator-facing (it surfaces
// in the admin handler's 400 response) so it spells out the rule
// rather than just saying "invalid".
var ErrInvalidSpecName = errors.New("catalog: spec name must be lowercase letters, digits, hyphens, or underscores (1 to 64 chars, must start and end with a letter or digit)")

// SourceKind enumerates how a spec entered the system.
const (
	SourceInline = "inline"
	SourceUpload = "upload"
	SourceURL    = "url"
)

// Catalog is the header row in api_catalogs. The (Name, Version)
// pair is unique across the table; (ID) is the immutable handle
// connections reference. ID is operator-chosen at create and never
// changes — editing the catalog's display fields preserves it so
// downstream references stay valid.
type Catalog struct {
	ID          string
	Name        string
	Version     string
	DisplayName string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SpecEntry is a single component OpenAPI document inside a catalog.
// Content is plain text (YAML or JSON); the toolkit parses it at
// connection-load time. SourceURL/ETag/LastFetchedAt populate when
// SourceKind == SourceURL so the portal can offer a "Refresh" action.
//
// BasePath is the operator-supplied override for the URL path
// segment prepended to every operation in this spec when the
// connection invokes the upstream. Empty means "no override"; the
// toolkit falls back to deriving the prefix from servers[0].url
// in the spec content. Set this when the spec ships without a
// servers[] entry, or when the operator's deployment targets a
// path that does not match what the spec author wrote (sandbox,
// proxy, version pin). Must start with "/" when non-empty;
// trailing slash is stripped at validation time.
type SpecEntry struct {
	SpecName      string
	Content       string
	SourceKind    string
	SourceURL     string
	ETag          string
	BasePath      string
	LastFetchedAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Update carries the partial-edit shape used by Store.UpdateCatalog.
// Nil pointer = leave unchanged. The ID is immutable and intentionally
// absent.
type Update struct {
	Name        *string
	Version     *string
	DisplayName *string
	Description *string
}

// idPattern constrains catalog IDs to lowercase alphanumeric plus
// hyphens, 1-100 chars, with no leading/trailing hyphen. The shape
// is restrictive on purpose: IDs flow into HTTP paths
// (/api/v1/admin/api-catalogs/{id}) and the connection config JSONB,
// so anything that would need URL-encoding or shell-quoting is
// rejected up front.
var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,98}[a-z0-9])?$`)

// specNamePattern is identical in shape to idPattern. Spec names
// appear in the `spec` field of OperationSummary that the model
// reads, so we keep them slug-friendly for the same reasons.
var specNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$`)

// ValidateID reports whether s is acceptable as a catalog ID.
// Returns ErrInvalidID on failure so callers can wrap it without
// constructing a fresh sentinel.
func ValidateID(s string) error {
	if !idPattern.MatchString(s) {
		return ErrInvalidID
	}
	return nil
}

// ValidateSpecName reports whether s is acceptable as a component
// spec name within a catalog.
func ValidateSpecName(s string) error {
	if !specNamePattern.MatchString(s) {
		return ErrInvalidSpecName
	}
	return nil
}

// ValidateSourceKind reports whether s is one of the three known
// source kinds. Returns nil on match, an error otherwise.
func ValidateSourceKind(s string) error {
	switch s {
	case SourceInline, SourceUpload, SourceURL:
		return nil
	default:
		return errors.New("catalog: invalid source_kind (want inline|upload|url)")
	}
}

// NormalizeBasePath validates and normalizes an operator-supplied
// SpecEntry.BasePath. Empty input returns empty output (the "no
// override" sentinel). Non-empty input is required to start with
// "/", must not contain CR/LF/NUL (header-smuggling vector when
// the path lands in a request line) and must not contain "?" or
// "#" (those terminate the path component of an URL). A trailing
// slash on a non-root value is stripped so the prepended segment
// joins cleanly with operation paths that all start with "/".
func NormalizeBasePath(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("must start with leading slash: %w", ErrInvalidBasePath)
	}
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", fmt.Errorf("contains CR/LF/NUL: %w", ErrInvalidBasePath)
	}
	if strings.ContainsAny(s, "?#") {
		return "", fmt.Errorf("must not contain query or fragment: %w", ErrInvalidBasePath)
	}
	if s != "/" {
		s = strings.TrimSuffix(s, "/")
	}
	return s, nil
}
