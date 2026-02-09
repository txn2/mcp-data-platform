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
}
