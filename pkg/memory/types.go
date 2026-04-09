// Package memory provides persistent memory storage for agent and analyst sessions.
package memory

import (
	"fmt"
	"time"
)

// LOCOMO dimension values.
const (
	DimensionKnowledge    = "knowledge"
	DimensionEvent        = "event"
	DimensionEntity       = "entity"
	DimensionRelationship = "relationship"
	DimensionPreference   = "preference"
)

// validDimensions is the set of accepted dimension values.
var validDimensions = map[string]bool{
	DimensionKnowledge:    true,
	DimensionEvent:        true,
	DimensionEntity:       true,
	DimensionRelationship: true,
	DimensionPreference:   true,
}

// Status values for memory records.
const (
	StatusActive     = "active"
	StatusStale      = "stale"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"
)

// validStatuses is the set of accepted status values.
var validStatuses = map[string]bool{
	StatusActive:     true,
	StatusStale:      true,
	StatusSuperseded: true,
	StatusArchived:   true,
}

// Category values for memory records.
const (
	CategoryCorrection    = "correction"
	CategoryBusinessCtx   = "business_context"
	CategoryDataQuality   = "data_quality"
	CategoryUsageGuidance = "usage_guidance"
	CategoryRelationship  = "relationship"
	CategoryEnhancement   = "enhancement"
	CategoryGeneral       = "general"
)

// validCategories is the set of accepted category values.
var validCategories = map[string]bool{
	CategoryCorrection:    true,
	CategoryBusinessCtx:   true,
	CategoryDataQuality:   true,
	CategoryUsageGuidance: true,
	CategoryRelationship:  true,
	CategoryEnhancement:   true,
	CategoryGeneral:       true,
}

// Source values for memory records.
const (
	SourceUser           = "user"
	SourceAgentDiscovery = "agent_discovery"
	SourceEnrichmentGap  = "enrichment_gap"
	SourceAutomation     = "automation"
	SourceLineageEvent   = "lineage_event"
)

// validSources is the set of accepted source values.
var validSources = map[string]bool{
	SourceUser:           true,
	SourceAgentDiscovery: true,
	SourceEnrichmentGap:  true,
	SourceAutomation:     true,
	SourceLineageEvent:   true,
}

// Confidence values for memory records.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// validConfidences is the set of accepted confidence values.
var validConfidences = map[string]bool{
	ConfidenceHigh:   true,
	ConfidenceMedium: true,
	ConfidenceLow:    true,
}

// Validation constraints.
const (
	MinContentLen    = 10
	MaxContentLen    = 4000
	MaxEntityURNs    = 10
	MaxRelatedCols   = 20
	DefaultLimit     = 20
	MaxLimit         = 100
	DefaultDimension = DimensionKnowledge
)

// Record represents a single memory record.
type Record struct {
	ID             string          `json:"id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	CreatedBy      string          `json:"created_by"`
	Persona        string          `json:"persona"`
	Dimension      string          `json:"dimension"`
	Content        string          `json:"content"`
	Category       string          `json:"category"`
	Confidence     string          `json:"confidence"`
	Source         string          `json:"source"`
	EntityURNs     []string        `json:"entity_urns"`
	RelatedColumns []RelatedColumn `json:"related_columns"`
	Embedding      []float32       `json:"embedding,omitempty"`
	Metadata       map[string]any  `json:"metadata"`
	Status         string          `json:"status"`
	StaleReason    string          `json:"stale_reason,omitempty"`
	StaleAt        *time.Time      `json:"stale_at,omitempty"`
	LastVerified   *time.Time      `json:"last_verified,omitempty"`
}

// RelatedColumn represents a column related to a memory record.
type RelatedColumn struct {
	URN       string `json:"urn"`
	Column    string `json:"column"`
	Relevance string `json:"relevance"`
}

// Filter defines criteria for listing memory records.
type Filter struct {
	CreatedBy string
	Persona   string
	Dimension string
	Category  string
	Status    string
	Source    string
	EntityURN string
	Since     *time.Time
	Until     *time.Time
	Limit     int
	Offset    int
}

// EffectiveLimit returns the limit capped to MaxLimit, defaulting to DefaultLimit.
func (f *Filter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	if f.Limit > MaxLimit {
		return MaxLimit
	}
	return f.Limit
}

// RecordUpdate holds fields that can be updated on a memory record.
type RecordUpdate struct {
	Content    string
	Category   string
	Confidence string
	Dimension  string
	Metadata   map[string]any
	Embedding  []float32
}

// ValidateDimension checks whether a dimension value is valid.
func ValidateDimension(d string) error {
	if d == "" {
		return nil
	}
	if !validDimensions[d] {
		return fmt.Errorf("invalid dimension %q: must be one of: knowledge, event, entity, relationship, preference", d)
	}
	return nil
}

// ValidateCategory checks whether a category value is valid.
func ValidateCategory(c string) error {
	if c == "" {
		return nil
	}
	if !validCategories[c] {
		return fmt.Errorf("invalid category %q: must be one of: correction, business_context, data_quality, usage_guidance, relationship, enhancement, general", c)
	}
	return nil
}

// ValidateConfidence checks whether a confidence value is valid.
func ValidateConfidence(c string) error {
	if c == "" {
		return nil
	}
	if !validConfidences[c] {
		return fmt.Errorf("invalid confidence %q: must be one of: high, medium, low", c)
	}
	return nil
}

// ValidateSource checks whether a source value is valid.
func ValidateSource(s string) error {
	if s == "" {
		return nil
	}
	if !validSources[s] {
		return fmt.Errorf("invalid source %q: must be one of: user, agent_discovery, enrichment_gap, automation, lineage_event", s)
	}
	return nil
}

// ValidateStatus checks whether a status value is valid.
func ValidateStatus(s string) error {
	if s == "" {
		return nil
	}
	if !validStatuses[s] {
		return fmt.Errorf("invalid status %q: must be one of: active, stale, superseded, archived", s)
	}
	return nil
}

// ValidateContent checks content length constraints.
func ValidateContent(text string) error {
	if text == "" {
		return fmt.Errorf("content is required (minimum %d characters)", MinContentLen)
	}
	if len(text) < MinContentLen {
		return fmt.Errorf("content must be at least %d characters (got %d)", MinContentLen, len(text))
	}
	if len(text) > MaxContentLen {
		return fmt.Errorf("content must be at most %d characters (got %d)", MaxContentLen, len(text))
	}
	return nil
}

// ValidateEntityURNs checks the entity URN slice length.
func ValidateEntityURNs(urns []string) error {
	if len(urns) > MaxEntityURNs {
		return fmt.Errorf("entity_urns exceeds maximum of %d (got %d)", MaxEntityURNs, len(urns))
	}
	return nil
}

// ValidateRelatedColumns checks the related columns slice length.
func ValidateRelatedColumns(cols []RelatedColumn) error {
	if len(cols) > MaxRelatedCols {
		return fmt.Errorf("related_columns exceeds maximum of %d (got %d)", MaxRelatedCols, len(cols))
	}
	return nil
}

// NormalizeConfidence returns the confidence value, defaulting to "medium".
func NormalizeConfidence(c string) string {
	if c == "" {
		return ConfidenceMedium
	}
	return c
}

// NormalizeSource returns the source value, defaulting to "user".
func NormalizeSource(s string) string {
	if s == "" {
		return SourceUser
	}
	return s
}

// NormalizeDimension returns the dimension value, defaulting to "knowledge".
func NormalizeDimension(d string) string {
	if d == "" {
		return DefaultDimension
	}
	return d
}

// NormalizeCategory returns the category value, defaulting to "business_context".
func NormalizeCategory(c string) string {
	if c == "" {
		return CategoryBusinessCtx
	}
	return c
}
