package resource

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validation limits.
const (
	MaxUploadBytes     = 100 << 20 // 100 MB
	MaxDescriptionLen  = 2000
	MaxDisplayNameLen  = 200
	MaxTagsPerResource = 20
	MaxTagLen          = 50
	MaxCategoryLen     = 31
)

var (
	categoryRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	tagRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
)

// DeniedExtensions lists file extensions that are blocked for upload.
var DeniedExtensions = map[string]bool{
	".exe": true, ".sh": true, ".bat": true, ".cmd": true,
	".ps1": true, ".msi": true, ".com": true, ".scr": true,
}

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
	n := utf8.RuneCountInString(strings.TrimSpace(name))
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
	n := utf8.RuneCountInString(strings.TrimSpace(desc))
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
	base, _, _ := strings.Cut(mt, ";")
	base = strings.TrimSpace(base)
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

	name = normalizeFilename(name)

	if name == "" || name == "." {
		return "", fmt.Errorf("filename contains only invalid characters")
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
			b.WriteRune(r)
		}
	}
	return b.String()
}
