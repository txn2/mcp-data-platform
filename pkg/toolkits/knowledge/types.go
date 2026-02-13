// Package knowledge provides a knowledge capture toolkit for the MCP data platform.
package knowledge

import (
	"fmt"
	"time"
)

// category classifies the type of insight being captured.
type category string

// Valid insight categories.
const (
	categoryCorrection    category = "correction"
	categoryBusinessCtx   category = "business_context"
	categoryDataQuality   category = "data_quality"
	categoryUsageGuidance category = "usage_guidance"
	categoryRelationship  category = "relationship"
	categoryEnhancement   category = "enhancement"
)

// validCategories is the set of accepted category values.
var validCategories = map[category]bool{
	categoryCorrection:    true,
	categoryBusinessCtx:   true,
	categoryDataQuality:   true,
	categoryUsageGuidance: true,
	categoryRelationship:  true,
	categoryEnhancement:   true,
}

// ValidateCategory checks whether a category value is valid.
func ValidateCategory(c string) error {
	if c == "" {
		return fmt.Errorf("category is required and must be one of: correction, business_context, data_quality, usage_guidance, relationship, enhancement")
	}
	if !validCategories[category(c)] {
		return fmt.Errorf("invalid category %q: must be one of: correction, business_context, data_quality, usage_guidance, relationship, enhancement", c)
	}
	return nil
}

// confidence indicates how confident the submitter is in the insight.
type confidence string

// Valid confidence levels.
const (
	confidenceHigh   confidence = "high"
	confidenceMedium confidence = "medium"
	confidenceLow    confidence = "low"
)

// validConfidences is the set of accepted confidence values.
var validConfidences = map[confidence]bool{
	confidenceHigh:   true,
	confidenceMedium: true,
	confidenceLow:    true,
}

// ValidateConfidence checks whether a confidence value is valid.
// An empty string is valid and defaults to "medium".
func ValidateConfidence(c string) error {
	if c == "" {
		return nil
	}
	if !validConfidences[confidence(c)] {
		return fmt.Errorf("invalid confidence %q: must be one of: high, medium, low", c)
	}
	return nil
}

// NormalizeConfidence returns the confidence value, defaulting to "medium" if empty.
func NormalizeConfidence(c string) string {
	if c == "" {
		return string(confidenceMedium)
	}
	return c
}

// Insight validation constraints.
const (
	MinInsightTextLen   = 10
	MaxInsightTextLen   = 4000
	MaxEntityURNs       = 10
	MaxRelatedColumns   = 20
	MaxSuggestedActions = 5
	MaxApplyChanges     = 20
	MaxInsightIDs       = 50
)

// ValidateInsightText checks whether the insight text meets length requirements.
func ValidateInsightText(text string) error {
	if text == "" {
		return fmt.Errorf("insight_text is required (minimum %d characters)", MinInsightTextLen)
	}
	if len(text) < MinInsightTextLen {
		return fmt.Errorf("insight_text must be at least %d characters (got %d)", MinInsightTextLen, len(text))
	}
	if len(text) > MaxInsightTextLen {
		return fmt.Errorf("insight_text must be at most %d characters (got %d)", MaxInsightTextLen, len(text))
	}
	return nil
}

// actionType classifies a suggested catalog change.
type actionType string

// Valid action types.
const (
	actionUpdateDescription actionType = "update_description"
	actionAddTag            actionType = "add_tag"
	actionAddGlossaryTerm   actionType = "add_glossary_term"
	actionFlagQualityIssue  actionType = "flag_quality_issue"
	actionAddDocumentation  actionType = "add_documentation"
)

// validActionTypes is the set of accepted action type values.
var validActionTypes = map[actionType]bool{
	actionUpdateDescription: true,
	actionAddTag:            true,
	actionAddGlossaryTerm:   true,
	actionFlagQualityIssue:  true,
	actionAddDocumentation:  true,
}

// SuggestedAction represents a proposed catalog change.
type SuggestedAction struct {
	ActionType string `json:"action_type"`
	Target     string `json:"target"`
	Detail     string `json:"detail"`
}

// ValidateSuggestedActions validates a slice of suggested actions.
func ValidateSuggestedActions(actions []SuggestedAction) error {
	if len(actions) > MaxSuggestedActions {
		return fmt.Errorf("suggested_actions exceeds maximum of %d (got %d)", MaxSuggestedActions, len(actions))
	}
	for i, a := range actions {
		if !validActionTypes[actionType(a.ActionType)] {
			return fmt.Errorf("suggested_actions[%d]: invalid action_type %q: must be one of: update_description, add_tag, add_glossary_term, flag_quality_issue, add_documentation", i, a.ActionType)
		}
	}
	return nil
}

// RelatedColumn represents a column related to an insight.
type RelatedColumn struct {
	URN       string `json:"urn"`
	Column    string `json:"column"`
	Relevance string `json:"relevance"`
}

// ValidateEntityURNs validates the entity URN slice.
func ValidateEntityURNs(urns []string) error {
	if len(urns) > MaxEntityURNs {
		return fmt.Errorf("entity_urns exceeds maximum of %d (got %d)", MaxEntityURNs, len(urns))
	}
	return nil
}

// ValidateRelatedColumns validates the related columns slice.
func ValidateRelatedColumns(cols []RelatedColumn) error {
	if len(cols) > MaxRelatedColumns {
		return fmt.Errorf("related_columns exceeds maximum of %d (got %d)", MaxRelatedColumns, len(cols))
	}
	return nil
}

// Insight represents a captured domain knowledge insight.
type Insight struct {
	ID               string            `json:"id"`
	CreatedAt        time.Time         `json:"created_at"`
	SessionID        string            `json:"session_id"`
	CapturedBy       string            `json:"captured_by"`
	Persona          string            `json:"persona"`
	Category         string            `json:"category"`
	InsightText      string            `json:"insight_text"`
	Confidence       string            `json:"confidence"`
	EntityURNs       []string          `json:"entity_urns"`
	RelatedColumns   []RelatedColumn   `json:"related_columns"`
	SuggestedActions []SuggestedAction `json:"suggested_actions"`
	Status           string            `json:"status"`

	// Lifecycle fields (populated by migrations 000007 and 000008)
	ReviewedBy   string     `json:"reviewed_by,omitempty"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
	ReviewNotes  string     `json:"review_notes,omitempty"`
	AppliedBy    string     `json:"applied_by,omitempty"`
	AppliedAt    *time.Time `json:"applied_at,omitempty"`
	ChangesetRef string     `json:"changeset_ref,omitempty"`
}

// Insight status constants.
const (
	StatusPending    = "pending"
	StatusApproved   = "approved"
	StatusRejected   = "rejected"
	StatusApplied    = "applied"
	StatusSuperseded = "superseded"
	StatusRolledBack = "rolled_back"
)

// validTransitions defines allowed status transitions.
var validTransitions = map[string]map[string]bool{
	StatusPending: {
		StatusApproved:   true,
		StatusRejected:   true,
		StatusSuperseded: true,
	},
	StatusApproved: {
		StatusApplied: true,
	},
	StatusApplied: {
		StatusRolledBack: true,
	},
}

// ValidateStatusTransition checks whether a status transition is allowed.
func ValidateStatusTransition(from, to string) error {
	allowed, ok := validTransitions[from]
	if !ok || !allowed[to] {
		return fmt.Errorf("invalid status transition from %q to %q", from, to)
	}
	return nil
}

// InsightFilter defines filtering criteria for listing insights.
type InsightFilter struct {
	Status     string
	Category   string
	EntityURN  string
	CapturedBy string
	Confidence string
	Since      *time.Time
	Until      *time.Time
	Limit      int
	Offset     int
}

// DefaultLimit is the default page size for list queries.
const DefaultLimit = 20

// MaxLimit is the maximum page size for list queries.
const MaxLimit = 100

// EffectiveLimit returns the limit to use, applying defaults and caps.
func (f *InsightFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	if f.Limit > MaxLimit {
		return MaxLimit
	}
	return f.Limit
}

// InsightStats holds aggregated insight statistics.
type InsightStats struct {
	TotalPending int                    `json:"total_pending"`
	ByEntity     []EntityInsightSummary `json:"by_entity"`
	ByCategory   map[string]int         `json:"by_category"`
	ByConfidence map[string]int         `json:"by_confidence"`
	ByStatus     map[string]int         `json:"by_status"`
}

// EntityInsightSummary summarizes insights for a single entity.
type EntityInsightSummary struct {
	EntityURN  string   `json:"entity_urn"`
	Count      int      `json:"count"`
	Categories []string `json:"categories"`
	LatestAt   string   `json:"latest_at"`
}

// InsightUpdate holds fields that can be edited on a non-applied insight.
type InsightUpdate struct {
	InsightText string `json:"insight_text,omitempty"`
	Category    string `json:"category,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// Changeset records a set of changes applied to DataHub from insights.
type Changeset struct {
	ID               string         `json:"id"`
	CreatedAt        time.Time      `json:"created_at"`
	TargetURN        string         `json:"target_urn"`
	ChangeType       string         `json:"change_type"`
	PreviousValue    map[string]any `json:"previous_value"`
	NewValue         map[string]any `json:"new_value"`
	SourceInsightIDs []string       `json:"source_insight_ids"`
	ApprovedBy       string         `json:"approved_by"`
	AppliedBy        string         `json:"applied_by"`
	RolledBack       bool           `json:"rolled_back"`
	RolledBackBy     string         `json:"rolled_back_by,omitempty"`
	RolledBackAt     *time.Time     `json:"rolled_back_at,omitempty"`
}

// ChangesetFilter defines filtering criteria for listing changesets.
type ChangesetFilter struct {
	EntityURN  string
	AppliedBy  string
	Since      *time.Time
	Until      *time.Time
	RolledBack *bool
	Limit      int
	Offset     int
}

// EffectiveLimit returns the limit to use, applying defaults and caps.
func (f *ChangesetFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	if f.Limit > MaxLimit {
		return MaxLimit
	}
	return f.Limit
}

// ApplyChange represents a single change to apply to DataHub.
type ApplyChange struct {
	ChangeType string `json:"change_type"`
	Target     string `json:"target"`
	Detail     string `json:"detail"`
}

// ValidateApplyChanges validates the changes slice for the apply action.
func ValidateApplyChanges(changes []ApplyChange) error {
	if len(changes) == 0 {
		return fmt.Errorf("changes is required and must not be empty")
	}
	if len(changes) > MaxApplyChanges {
		return fmt.Errorf("changes exceeds maximum of %d (got %d)", MaxApplyChanges, len(changes))
	}
	for i, c := range changes {
		if !validActionTypes[actionType(c.ChangeType)] {
			return fmt.Errorf("changes[%d]: invalid change_type %q: must be one of: update_description, add_tag, add_glossary_term, flag_quality_issue, add_documentation", i, c.ChangeType)
		}
	}
	return nil
}

// EntityMetadata holds current metadata for an entity from DataHub.
type EntityMetadata struct {
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	GlossaryTerms []string `json:"glossary_terms"`
	Owners        []string `json:"owners"`
}

// ProposedChange represents a deterministic change proposal from synthesis.
type ProposedChange struct {
	ChangeType       string   `json:"change_type"`
	Target           string   `json:"target"`
	CurrentValue     string   `json:"current_value"`
	SuggestedValue   string   `json:"suggested_value"`
	SourceInsightIDs []string `json:"source_insight_ids"`
}

// ValidateAction checks whether an action value is valid.
func ValidateAction(action string) error {
	validActions := map[string]bool{
		"bulk_review": true,
		"review":      true,
		"synthesize":  true,
		"apply":       true,
		"approve":     true,
		"reject":      true,
	}
	if action == "" {
		return fmt.Errorf("action is required and must be one of: bulk_review, review, synthesize, apply, approve, reject")
	}
	if !validActions[action] {
		return fmt.Errorf("invalid action %q: must be one of: bulk_review, review, synthesize, apply, approve, reject", action)
	}
	return nil
}
