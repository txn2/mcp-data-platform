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

// Sink-class values (#633): the single organizing axis for the unified
// memory_capture write path. Each maps to a dimension and a review/promotion
// policy. personal_preference and episodic_event stay as live memory;
// business_knowledge, schema_entity, and operational_rule are reviewed and
// promotable to a canonical sink (DataHub for schema_entity; wiki/rules
// deferred).
const (
	SinkPersonalPreference = "personal_preference"
	SinkBusinessKnowledge  = "business_knowledge"
	SinkSchemaEntity       = "schema_entity"
	SinkOperationalRule    = "operational_rule"
	SinkEpisodicEvent      = "episodic_event"
)

// validSinkClasses is the set of accepted sink-class values.
var validSinkClasses = map[string]bool{
	SinkPersonalPreference: true,
	SinkBusinessKnowledge:  true,
	SinkSchemaEntity:       true,
	SinkOperationalRule:    true,
	SinkEpisodicEvent:      true,
}

// SinkClassDimension returns the LOCOMO dimension a sink-class is stored under.
// The three reviewed classes all live in the knowledge dimension; preference
// and event have their own.
func SinkClassDimension(sinkClass string) string {
	switch sinkClass {
	case SinkPersonalPreference:
		return DimensionPreference
	case SinkEpisodicEvent:
		return DimensionEvent
	default:
		return DimensionKnowledge
	}
}

// SinkClassIsLive reports whether a sink-class is live-for-the-capturer on
// write (no review). personal_preference and episodic_event are personal and
// live immediately; the rest are reviewed before promotion to a shared sink.
func SinkClassIsLive(sinkClass string) bool {
	return sinkClass == SinkPersonalPreference || sinkClass == SinkEpisodicEvent
}

// ValidateSinkClass checks whether a sink-class value is valid.
func ValidateSinkClass(s string) error {
	if !validSinkClasses[s] {
		return fmt.Errorf("invalid sink_class %q: must be one of: personal_preference, business_knowledge, schema_entity, operational_rule, episodic_event", s)
	}
	return nil
}

// DeriveSinkClass infers the sink-class of a pre-#633 record from its dimension
// and whether it carries entity URNs, matching the 000069 backfill so reads are
// consistent for rows captured before the column existed.
func DeriveSinkClass(dimension string, hasEntityURNs bool) string {
	switch dimension {
	case DimensionPreference:
		return SinkPersonalPreference
	case DimensionEvent:
		return SinkEpisodicEvent
	case DimensionEntity:
		return SinkSchemaEntity
	case DimensionRelationship:
		return SinkBusinessKnowledge
	case DimensionKnowledge:
		if hasEntityURNs {
			return SinkSchemaEntity
		}
		return SinkBusinessKnowledge
	default:
		return SinkBusinessKnowledge
	}
}

// Insight-overlay metadata keys and the pending status value (#296/#633). A
// knowledge-dimension memory record carries insight review state and catalog
// proposals in its metadata, so the knowledge toolkit's apply_knowledge reads it
// as an insight while the memory toolkit's memory_capture writes it directly,
// without either toolkit importing the other. These string values are the single
// source of truth for that convention.
const (
	MetaKeyInsightStatus    = "insight_status"
	MetaKeySuggestedActions = "suggested_actions"
	MetaKeySessionID        = "session_id"
	InsightStatusPending    = "pending"
	// InsightStatusSuperseded mirrors knowledgekit.StatusSuperseded. It is the
	// review-status counterpart of the StatusSuperseded lifecycle column: when a
	// record is superseded, its insight review status must follow, or the insights
	// read path (which filters on insight_status) keeps surfacing the stale record.
	InsightStatusSuperseded = "superseded"
)

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
	ID        string    `json:"id" example:"mem_a1b2c3d4e5f6"`
	CreatedAt time.Time `json:"created_at" example:"2026-03-18T08:11:08Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-03-18T08:11:08Z"`
	CreatedBy string    `json:"created_by" example:"sarah.chen@example.com"`
	Persona   string    `json:"persona" example:"admin"`
	Dimension string    `json:"dimension" example:"knowledge"`
	// SinkClass is the #633 organizing axis (personal_preference,
	// business_knowledge, schema_entity, operational_rule, episodic_event). It
	// drives routing in the unified write path. Empty on rows captured before
	// the axis existed; DeriveSinkClass reconstructs it from Dimension on read.
	SinkClass      string          `json:"sink_class,omitempty" example:"schema_entity"`
	Content        string          `json:"content" example:"The daily_sales table in the retail schema is partitioned by date."`
	Category       string          `json:"category" example:"business_context"`
	Confidence     string          `json:"confidence" example:"high"`
	Source         string          `json:"source" example:"user"`
	EntityURNs     []string        `json:"entity_urns"`
	RelatedColumns []RelatedColumn `json:"related_columns"`
	Embedding      []float32       `json:"embedding,omitempty"`
	// EmbeddingModel records the provider model that produced Embedding
	// (e.g. "nomic-embed-text"); EmbeddingTextHash is the SHA-256 of the
	// content fed to the embedder. They are the breadcrumbs the indexjobs
	// memory consumer uses to dedup re-embeds and detect model-swap gaps.
	// The synchronous write path stamps both when the embedder is healthy;
	// they are empty/nil on rows embedded before the column existed or
	// saved during an embedder outage (the reconciler later backfills).
	EmbeddingModel    string         `json:"embedding_model,omitempty"`
	EmbeddingTextHash []byte         `json:"embedding_text_hash,omitempty"`
	Metadata          map[string]any `json:"metadata"`
	Status            string         `json:"status" example:"active"`
	StaleReason       string         `json:"stale_reason,omitempty"`
	StaleAt           *time.Time     `json:"stale_at,omitempty"`
	LastVerified      *time.Time     `json:"last_verified,omitempty"`
}

// RelatedColumn represents a column related to a memory record.
type RelatedColumn struct {
	URN       string `json:"urn" example:"urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"`
	Column    string `json:"column" example:"amount"`
	Relevance string `json:"relevance" example:"direct"`
}

// Filter defines criteria for listing memory records.
type Filter struct {
	CreatedBy string
	Persona   string
	Dimension string
	// SinkClass filters on the #633 organizing axis (personal_preference,
	// business_knowledge, schema_entity, operational_rule, episodic_event). It is
	// the lifecycle axis the portal Memory view browses by; unlike Dimension, it
	// distinguishes the three reviewable knowledge-dimension classes from one
	// another.
	SinkClass string
	Category  string
	Status    string
	Source    string
	EntityURN string
	Since     *time.Time
	Until     *time.Time
	Limit     int
	Offset    int
	// OrderBy overrides the default ordering ("created_at DESC").
	// Must be a valid SQL ORDER BY clause (e.g. "last_verified ASC NULLS FIRST").
	OrderBy string
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
	// Status transitions the record's lifecycle column (active, archived,
	// superseded). Empty leaves it unchanged. Required so a status change made
	// via Update (e.g. an insight rejection that maps to archived) moves the
	// status column, not just metadata, so status-filtered reads honor it.
	Status    string
	Metadata  map[string]any
	Embedding []float32
	// EmbeddingModel and EmbeddingTextHash travel with Embedding: when an
	// update re-embeds changed content, the write path stamps the model
	// and content hash alongside the new vector so the row's breadcrumbs
	// stay consistent with the embedding the indexjobs consumer dedups on.
	EmbeddingModel    string
	EmbeddingTextHash []byte
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
// Length is measured in bytes (matching Go's len() behavior).
func ValidateContent(text string) error {
	if text == "" {
		return fmt.Errorf("content is required (minimum %d bytes)", MinContentLen)
	}
	if len(text) < MinContentLen {
		return fmt.Errorf("content must be at least %d bytes (got %d)", MinContentLen, len(text))
	}
	if len(text) > MaxContentLen {
		return fmt.Errorf("content must be at most %d bytes (got %d)", MaxContentLen, len(text))
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
