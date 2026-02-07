// Package semantic provides semantic layer abstractions.
package semantic

import (
	"regexp"
	"strings"
	"unicode"
)

// MaxStringLength is the maximum length for sanitized strings.
const MaxStringLength = 2000

// maxTagLength is the maximum allowed length for a tag name.
const maxTagLength = 100

// SanitizeConfig configures sanitization behavior.
type SanitizeConfig struct {
	// MaxLength is the maximum length for strings (default: 2000).
	MaxLength int

	// StripInjectionPatterns removes detected injection patterns instead of flagging.
	StripInjectionPatterns bool

	// LogInjectionAttempts enables logging of detected injection attempts.
	LogInjectionAttempts bool
}

// DefaultSanitizeConfig returns a safe default configuration.
func DefaultSanitizeConfig() SanitizeConfig {
	return SanitizeConfig{
		MaxLength:              MaxStringLength,
		StripInjectionPatterns: true,
		LogInjectionAttempts:   true,
	}
}

// Sanitizer sanitizes metadata strings to prevent prompt injection and other attacks.
type Sanitizer struct {
	cfg SanitizeConfig
}

// NewSanitizer creates a new sanitizer with the given configuration.
func NewSanitizer(cfg SanitizeConfig) *Sanitizer {
	if cfg.MaxLength <= 0 {
		cfg.MaxLength = MaxStringLength
	}
	return &Sanitizer{cfg: cfg}
}

// SanitizeString sanitizes a string by removing control characters,
// truncating to max length, and optionally stripping injection patterns.
func (s *Sanitizer) SanitizeString(input string) string {
	if input == "" {
		return ""
	}

	// Step 1: Remove control characters (except \n, \t)
	cleaned := removeControlChars(input)

	// Step 2: Strip or flag injection patterns
	if s.cfg.StripInjectionPatterns {
		cleaned = stripInjectionPatterns(cleaned)
	}

	// Step 3: Truncate to max length
	if len(cleaned) > s.cfg.MaxLength {
		cleaned = cleaned[:s.cfg.MaxLength] + "..."
	}

	return cleaned
}

// SanitizeDescription sanitizes a description field.
func (s *Sanitizer) SanitizeDescription(desc string) string {
	return s.SanitizeString(desc)
}

// SanitizeTag validates and sanitizes a tag name.
// Returns empty string if the tag is invalid.
func (*Sanitizer) SanitizeTag(tag string) string {
	if tag == "" {
		return ""
	}

	// Validate tag format: alphanumeric + underscore/hyphen only
	if !isValidTagName(tag) {
		return ""
	}

	// Truncate to reasonable tag length
	if len(tag) > maxTagLength {
		return ""
	}

	return tag
}

// SanitizeTags sanitizes a slice of tags, removing invalid ones.
func (s *Sanitizer) SanitizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}

	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		sanitized := s.SanitizeTag(tag)
		if sanitized != "" {
			result = append(result, sanitized)
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// DetectInjection checks if the input contains potential prompt injection patterns.
// Returns true if injection is detected along with matched patterns.
func (*Sanitizer) DetectInjection(input string) (detected bool, patterns []string) {
	return detectInjectionPatterns(input)
}

// removeControlChars removes control characters except newline and tab.
func removeControlChars(s string) string {
	var result strings.Builder
	result.Grow(len(s))

	for _, r := range s {
		// Allow printable characters, newline, and tab
		if unicode.IsPrint(r) || r == '\n' || r == '\t' {
			_, _ = result.WriteRune(r)
		}
		// Skip all other control characters
	}

	return result.String()
}

// tagNamePattern validates tag names.
var tagNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// isValidTagName checks if a tag name is valid.
func isValidTagName(tag string) bool {
	return tagNamePattern.MatchString(tag)
}

// injectionPatterns are regex patterns that detect potential prompt injection.
var injectionPatterns = []*regexp.Regexp{
	// Direct instruction overrides
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions?`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)`),
	regexp.MustCompile(`(?i)forget\s+(all\s+)?(previous|prior|above)`),

	// Role/identity manipulation
	regexp.MustCompile(`(?i)you\s+are\s+now\s+`),
	regexp.MustCompile(`(?i)act\s+as\s+(if\s+you\s+are|a)\s+`),
	regexp.MustCompile(`(?i)pretend\s+(to\s+be|you\s+are)\s+`),
	regexp.MustCompile(`(?i)from\s+now\s+on\s*,?\s*(you|act|pretend)`),

	// System prompt access attempts
	regexp.MustCompile(`(?i)system\s+prompt`),
	regexp.MustCompile(`(?i)show\s+me\s+(your|the)\s+(instructions|prompt|rules)`),
	regexp.MustCompile(`(?i)what\s+are\s+your\s+(instructions|rules|constraints)`),
	regexp.MustCompile(`(?i)reveal\s+(your|the)\s+(system|initial)`),

	// Jailbreak patterns
	regexp.MustCompile(`(?i)developer\s+mode`),
	regexp.MustCompile(`(?i)DAN\s+mode`),
	regexp.MustCompile(`(?i)jailbreak`),
	regexp.MustCompile(`(?i)bypass\s+(safety|content|security)\s+(filter|restriction|guard)`),

	// Script/code injection
	regexp.MustCompile(`(?i)<\s*script[^>]*>`),
	regexp.MustCompile(`(?i)javascript\s*:`),
	regexp.MustCompile(`(?i)data\s*:\s*text/html`),
	regexp.MustCompile(`(?i)on(load|error|click|mouse)\s*=`),

	// Special token injection attempts
	regexp.MustCompile(`\[\s*SYSTEM\s*\]`),
	regexp.MustCompile(`\[\s*INST\s*\]`),
	regexp.MustCompile(`<\|im_start\|>`),
	regexp.MustCompile(`<\|im_end\|>`),
	regexp.MustCompile(`\[/INST\]`),

	// Base64 encoded payloads (suspicious in metadata)
	regexp.MustCompile(`(?i)base64\s*:\s*[A-Za-z0-9+/=]{50,}`),
}

// injectionPatternNames provides human-readable names for each pattern.
var injectionPatternNames = []string{
	"ignore_instructions",
	"disregard_instructions",
	"forget_instructions",
	"role_manipulation_you_are",
	"role_manipulation_act_as",
	"role_manipulation_pretend",
	"role_manipulation_from_now",
	"system_prompt_access",
	"show_instructions",
	"what_instructions",
	"reveal_system",
	"developer_mode",
	"dan_mode",
	"jailbreak",
	"bypass_safety",
	"script_tag",
	"javascript_url",
	"data_url",
	"event_handler",
	"system_token",
	"inst_token",
	"im_start_token",
	"im_end_token",
	"inst_close_token",
	"base64_payload",
}

// detectInjectionPatterns checks for prompt injection patterns.
func detectInjectionPatterns(input string) (detected bool, patterns []string) {
	if input == "" {
		return false, nil
	}

	var matched []string
	for i, pattern := range injectionPatterns {
		if pattern.MatchString(input) {
			matched = append(matched, injectionPatternNames[i])
		}
	}

	return len(matched) > 0, matched
}

// stripInjectionPatterns removes detected injection patterns from the input.
func stripInjectionPatterns(input string) string {
	result := input

	for _, pattern := range injectionPatterns {
		result = pattern.ReplaceAllString(result, "[REMOVED]")
	}

	return result
}

// SanitizeTableContext sanitizes all string fields in a TableContext.
func (s *Sanitizer) SanitizeTableContext(tc *TableContext) *TableContext {
	if tc == nil {
		return nil
	}

	return &TableContext{
		URN:              tc.URN, // URN is a system identifier, keep as-is
		Description:      s.SanitizeDescription(tc.Description),
		Owners:           s.sanitizeOwners(tc.Owners),
		Tags:             s.SanitizeTags(tc.Tags),
		GlossaryTerms:    s.sanitizeGlossaryTerms(tc.GlossaryTerms),
		Domain:           s.sanitizeDomain(tc.Domain),
		Deprecation:      s.sanitizeDeprecation(tc.Deprecation),
		CustomProperties: s.sanitizeProperties(tc.CustomProperties),
		QualityScore:     tc.QualityScore,
		LastModified:     tc.LastModified,
	}
}

// SanitizeColumnContext sanitizes all string fields in a ColumnContext.
func (s *Sanitizer) SanitizeColumnContext(cc *ColumnContext) *ColumnContext {
	if cc == nil {
		return nil
	}

	return &ColumnContext{
		Name:          cc.Name, // Field name is a system identifier
		Description:   s.SanitizeDescription(cc.Description),
		Tags:          s.SanitizeTags(cc.Tags),
		GlossaryTerms: s.sanitizeGlossaryTerms(cc.GlossaryTerms),
		IsPII:         cc.IsPII,
		IsSensitive:   cc.IsSensitive,
	}
}

func (s *Sanitizer) sanitizeOwners(owners []Owner) []Owner {
	if len(owners) == 0 {
		return nil
	}

	result := make([]Owner, 0, len(owners))
	for _, owner := range owners {
		result = append(result, Owner{
			URN:   owner.URN,
			Type:  owner.Type,
			Name:  s.SanitizeString(owner.Name),
			Email: owner.Email, // Email is structured, keep as-is
		})
	}
	return result
}

func (s *Sanitizer) sanitizeGlossaryTerms(terms []GlossaryTerm) []GlossaryTerm {
	if len(terms) == 0 {
		return nil
	}

	result := make([]GlossaryTerm, 0, len(terms))
	for _, term := range terms {
		result = append(result, GlossaryTerm{
			URN:         term.URN,
			Name:        s.SanitizeString(term.Name),
			Description: s.SanitizeDescription(term.Description),
		})
	}
	return result
}

func (s *Sanitizer) sanitizeDomain(domain *Domain) *Domain {
	if domain == nil {
		return nil
	}

	return &Domain{
		URN:         domain.URN,
		Name:        s.SanitizeString(domain.Name),
		Description: s.SanitizeDescription(domain.Description),
	}
}

func (s *Sanitizer) sanitizeDeprecation(dep *Deprecation) *Deprecation {
	if dep == nil {
		return nil
	}

	return &Deprecation{
		Deprecated: dep.Deprecated,
		Note:       s.SanitizeString(dep.Note),
		Actor:      dep.Actor,
		DecommDate: dep.DecommDate,
	}
}

func (s *Sanitizer) sanitizeProperties(props map[string]string) map[string]string {
	if len(props) == 0 {
		return nil
	}

	result := make(map[string]string, len(props))
	for k, v := range props {
		// Sanitize both key and value
		sanitizedKey := s.SanitizeTag(k)
		if sanitizedKey != "" {
			result[sanitizedKey] = s.SanitizeString(v)
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}
