package resource

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Validation limits.
const (
	// MaxUploadBytes is the upload ceiling a deployment that configures none
	// gets. It is a default rather than the limit: a deployment sets its own
	// with resources.managed.max_upload_bytes, which reaches the write routes
	// as Deps.MaxUploadBytes (#1628). It bounds bytes streamed rather than
	// bytes held: the upload path carries the file from the request into the
	// multipart uploader without assembling it (#1631), so raising it raises
	// what a deployment will store, not what it has to fit in memory.
	MaxUploadBytes     = 100 << 20 // 100 MB
	MaxDescriptionLen  = 2000
	MaxDisplayNameLen  = 200
	MaxTagsPerResource = 20
	MaxTagLen          = 50
)

// NormalizeMaxUploadBytes resolves the upload ceiling that applies: the
// configured value when it is positive, and MaxUploadBytes when it is absent,
// zero, or negative. Zero has to select the default rather than mean "nothing
// may be uploaded", because an unset field and a field set to zero are the
// same value in Go and a deployment that sets neither must keep uploading.
func NormalizeMaxUploadBytes(configured int64) int64 {
	if configured <= 0 {
		return MaxUploadBytes
	}
	return configured
}

// uploadLimitUnits are the units DescribeUploadLimit scales through, matching
// the browser's formatBytes unit for unit so a refusal from the server and the
// file chooser beside it name the same ceiling the same way.
var uploadLimitUnits = []string{"B", "KB", "MB", "GB", "TB"}

const (
	// uploadLimitStep is the factor between two of those units.
	uploadLimitStep = 1024
	// uploadLimitBits is the float width DescribeUploadLimit formats at. The
	// value is a byte count divided down, so float64 is exact well past any
	// ceiling a deployment would set.
	uploadLimitBits = 64
)

// DescribeUploadLimit renders an upload ceiling the way the person who set it
// reads it, and the way the browser renders the same number (formatBytes in
// ui/src/lib/format.ts): the largest unit the value reaches, to one decimal
// place, with a whole number left whole. A ceiling below a megabyte is
// reported in bytes or kilobytes rather than as "0 MB".
func DescribeUploadLimit(n int64) string {
	value, unit := float64(n), 0
	for value >= uploadLimitStep && unit < len(uploadLimitUnits)-1 {
		value /= uploadLimitStep
		unit++
	}
	return strings.TrimSuffix(
		strconv.FormatFloat(value, 'f', 1, uploadLimitBits), ".0",
	) + " " + uploadLimitUnits[unit]
}

var (
	// pathSegmentRe is the rule one folder name in a resource's path must meet.
	// It is the rule the flat category carried before a path could nest (#1529),
	// unchanged, so every value that existed then is a legal segment now.
	pathSegmentRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	tagRe         = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
)

// DeniedExtensions lists file extensions that are blocked for upload.
var DeniedExtensions = map[string]bool{
	".exe": true, ".sh": true, ".bat": true, ".cmd": true,
	".ps1": true, ".msi": true, ".com": true, ".scr": true,
}

// DeniedMIMETypes lists MIME types that are blocked for upload.
//
// A resource is human-uploaded reference material — report templates, brand
// files, sample documents, CAD exports — so this stays a denylist. An allowlist
// here would refuse the long tail the library exists to hold, and it would buy
// almost nothing: blobserve serves every stored byte under a sandbox CSP and
// hands the scriptable document families to the browser as attachments, and
// DeniedExtensions already refuses the executable extensions.
var DeniedMIMETypes = map[string]bool{
	"application/x-executable":    true,
	"application/x-msdos-program": true,
	"application/x-msdownload":    true,
	"application/x-sh":            true,
	"application/x-shellscript":   true,
	"application/x-bat":           true,
	"application/x-msi":           true,
	// A browser renders XHTML natively and runs the script inside it. Serving
	// already contains it; storing it has no use case that text/html does not
	// cover, so it is refused at the door as well.
	contenttype.XHTML: true,
}

// ValidateDisplayName checks display name length and content.
func ValidateDisplayName(name string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	if n == 0 {
		return errors.New("display_name is required")
	}
	if n > MaxDisplayNameLen {
		return fmt.Errorf("display_name exceeds %d characters", MaxDisplayNameLen)
	}
	return nil
}

// ValidateDescription checks description length and content.
func ValidateDescription(desc string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(desc))
	if n == 0 {
		return errors.New("description is required")
	}
	if n > MaxDescriptionLen {
		return fmt.Errorf("description exceeds %d characters", MaxDescriptionLen)
	}
	return nil
}

// ValidateTags checks tag count, length, and format.
func ValidateTags(tags []string) error {
	if len(tags) > MaxTagsPerResource {
		return fmt.Errorf("too many tags: max %d, got %d", MaxTagsPerResource, len(tags))
	}
	for _, t := range tags {
		if !tagRe.MatchString(t) {
			return fmt.Errorf("invalid tag %q: must be lowercase alphanumeric with hyphens, max %d chars", t, MaxTagLen)
		}
	}
	return nil
}

// ValidateMIMEType checks that the MIME type is not on the deny list. The type
// is normalized first (parameters stripped, aliases collapsed), so a denied
// family cannot be smuggled in under a spelling the map does not list.
func ValidateMIMEType(mt string) error {
	base := contenttype.Normalize(mt)
	if base == "" {
		base = strings.TrimSpace(strings.Split(mt, ";")[0])
	}
	if DeniedMIMETypes[base] {
		return fmt.Errorf("mime type %q is not allowed", base)
	}
	return nil
}

// ValidateScope checks scope and scope_id consistency.
func ValidateScope(scope Scope, scopeID string) error {
	switch scope {
	case ScopeGlobal:
		if scopeID != "" {
			return &invalidScopeError{msg: "scope_id must be empty for global scope"}
		}
	case ScopePersona, ScopeUser:
		if scopeID == "" {
			return &invalidScopeError{msg: fmt.Sprintf("scope_id is required for %s scope", scope)}
		}
	default:
		return &invalidScopeError{msg: fmt.Sprintf("unknown scope: %q", scope)}
	}
	return nil
}

// invalidScopeError marks a scope/scope_id pair that names no library. It is a
// type rather than a wrapped sentinel so the message a caller shows stays the
// sentence ValidateScope wrote, with no sentinel text appended to it.
type invalidScopeError struct{ msg string }

func (e *invalidScopeError) Error() string { return e.msg }

// IsInvalidScope reports whether an error names an unusable scope/scope_id
// pair, so a caller several layers above ValidateScope can answer 400 rather
// than treating it as a failure of its own.
func IsInvalidScope(err error) bool {
	var e *invalidScopeError
	return errors.As(err, &e)
}

// SanitizeFilename normalizes a filename for storage: lowercase, no spaces,
// no path separators or shell metacharacters, preserves extension.
func SanitizeFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("filename is empty")
	}

	name = normalizeFilename(name)

	if name == "" || name == "." {
		return "", errors.New("filename contains only invalid characters")
	}

	ext := filepath.Ext(name)
	if DeniedExtensions[ext] {
		return "", fmt.Errorf("file extension %q is not allowed", ext)
	}

	return name, nil
}

// normalizeFilename strips path components, lowercases, replaces spaces,
// and removes non-safe characters.
func normalizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")

	var b strings.Builder
	for _, r := range name {
		if r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			_, _ = b.WriteRune(r) // strings.Builder.WriteRune never returns a non-nil error
		}
	}
	return b.String()
}
