package resource

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Validation limits.
const (
	MaxUploadBytes      = 100 << 20 // 100 MB
	MaxDescriptionLen   = 2000
	MaxDisplayNameLen   = 200
	MaxTagsPerResource  = 20
	MaxTagLen           = 50
	MaxCategoryLen      = 31
)

var categoryRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)

// DeniedMIMETypes lists MIME types that are blocked for upload.
var DeniedMIMETypes = map[string]bool{
	"application/x-executable":    true,
	"application/x-msdos-program": true,
	"application/x-msdownload":    true,
	"application/x-sh":            true,
	"application/x-shellscript":   true,
	"application/x-bat":           true,
	"application/x-msi":           true,
}

// ValidateCategory checks that a category matches the required pattern.
func ValidateCategory(cat string) error {
	if !categoryRe.MatchString(cat) {
		return fmt.Errorf("category must match %s, got %q", categoryRe.String(), cat)
	}
	return nil
}

// ValidateDisplayName checks display name length and content.
func ValidateDisplayName(name string) error {
	n := len(strings.TrimSpace(name))
	if n == 0 {
		return fmt.Errorf("display_name is required")
	}
	if n > MaxDisplayNameLen {
		return fmt.Errorf("display_name exceeds %d characters", MaxDisplayNameLen)
	}
	return nil
}

// ValidateDescription checks description length and content.
func ValidateDescription(desc string) error {
	n := len(strings.TrimSpace(desc))
	if n == 0 {
		return fmt.Errorf("description is required")
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

// ValidateMIMEType checks that the MIME type is not on the deny list.
// The base type is extracted (e.g. "text/html" from "text/html; charset=utf-8")
// before checking the deny list.
func ValidateMIMEType(mt string) error {
	base := mt
	if idx := strings.IndexByte(mt, ';'); idx >= 0 {
		base = strings.TrimSpace(mt[:idx])
	}
	if DeniedMIMETypes[base] {
		return fmt.Errorf("MIME type %q is not allowed", base)
	}
	return nil
}

// ValidateScope checks scope and scope_id consistency.
func ValidateScope(scope Scope, scopeID string) error {
	switch scope {
	case ScopeGlobal:
		if scopeID != "" {
			return fmt.Errorf("scope_id must be empty for global scope")
		}
	case ScopePersona, ScopeUser:
		if scopeID == "" {
			return fmt.Errorf("scope_id is required for %s scope", scope)
		}
	default:
		return fmt.Errorf("unknown scope: %q", scope)
	}
	return nil
}

// SanitizeFilename normalizes a filename for storage: lowercase, no spaces,
// no path separators or shell metacharacters, preserves extension.
func SanitizeFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("filename is empty")
	}

	// Strip directory components.
	name = filepath.Base(name)

	// Lowercase.
	name = strings.ToLower(name)

	// Replace spaces with hyphens.
	name = strings.ReplaceAll(name, " ", "-")

	// Strip dangerous characters.
	var b strings.Builder
	for _, r := range name {
		if r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	name = b.String()

	if name == "" || name == "." {
		return "", fmt.Errorf("filename contains only invalid characters")
	}
	return name, nil
}
