// Package portal provides the asset portal data layer for persisting
// AI-generated artifacts (JSX dashboards, HTML reports, SVG charts).
package portal

import (
	"fmt"
	"strings"
	"time"
)

// Asset represents a persisted AI-generated artifact.
type Asset struct {
	ID          string     `json:"id"`
	OwnerID     string     `json:"owner_id"`
	OwnerEmail  string     `json:"owner_email"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	ContentType string     `json:"content_type"`
	S3Bucket    string     `json:"s3_bucket"`
	S3Key       string     `json:"s3_key"`
	SizeBytes   int64      `json:"size_bytes"`
	Tags        []string   `json:"tags"`
	Provenance  Provenance `json:"provenance"`
	SessionID   string     `json:"session_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// Provenance records the tool call history that produced an artifact.
type Provenance struct {
	ToolCalls []ProvenanceToolCall `json:"tool_calls,omitempty"`
	SessionID string               `json:"session_id,omitempty"`
	UserID    string               `json:"user_id,omitempty"`
}

// ProvenanceToolCall records a single tool invocation in the provenance chain.
type ProvenanceToolCall struct {
	ToolName  string `json:"tool_name"`
	Timestamp string `json:"timestamp"`
	Summary   string `json:"summary,omitempty"`
}

// Share represents a share link for an asset.
type Share struct {
	ID               string     `json:"id"`
	AssetID          string     `json:"asset_id"`
	Token            string     `json:"token"`
	CreatedBy        string     `json:"created_by"`
	SharedWithUserID string     `json:"shared_with_user_id,omitempty"`
	SharedWithEmail  string     `json:"shared_with_email,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	Revoked          bool       `json:"revoked"`
	AccessCount      int        `json:"access_count"`
	LastAccessedAt   *time.Time `json:"last_accessed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// SharedAsset combines an Asset with share metadata for "shared with me" results.
type SharedAsset struct {
	Asset    Asset     `json:"asset"`
	ShareID  string    `json:"share_id"`
	SharedBy string    `json:"shared_by"`
	SharedAt time.Time `json:"shared_at"`
}

// ShareSummary indicates what kinds of active shares exist for an asset.
type ShareSummary struct {
	HasUserShare  bool `json:"has_user_share"`
	HasPublicLink bool `json:"has_public_link"`
}

// AssetFilter defines filtering criteria for listing assets.
type AssetFilter struct {
	OwnerID     string `json:"owner_id,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Search      string `json:"search,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

// defaultLimit is the default page size for asset listing.
const defaultLimit = 50

// maxLimit caps the maximum number of assets per page.
const maxLimit = 200

// EffectiveLimit returns the limit with defaults applied.
func (f *AssetFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return defaultLimit
	}
	if f.Limit > maxLimit {
		return maxLimit
	}
	return f.Limit
}

// AssetUpdate holds mutable fields for updating an asset.
// Pointer fields distinguish "no change" (nil) from "clear to empty" (pointer to "").
type AssetUpdate struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	S3Key       string   `json:"s3_key,omitempty"`
	SizeBytes   int64    `json:"size_bytes,omitempty"`
	HasContent  bool     `json:"-"` // set when content replacement provides SizeBytes (even if 0)
}

// maxNameLength is the maximum length for asset names.
const maxNameLength = 255

// maxDescriptionLength is the maximum length for asset descriptions.
const maxDescriptionLength = 2000

// maxTags is the maximum number of tags per asset.
const maxTags = 20

// maxTagLength is the maximum length for a single tag.
const maxTagLength = 100

// ValidateAssetName checks that a name is non-empty and within length limits.
func ValidateAssetName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("name exceeds %d characters", maxNameLength)
	}
	return nil
}

// ValidateContentType checks that a content type is non-empty.
func ValidateContentType(ct string) error {
	if ct == "" {
		return fmt.Errorf("content_type is required")
	}
	return nil
}

// ValidateTags checks tag count and individual tag length.
func ValidateTags(tags []string) error {
	if len(tags) > maxTags {
		return fmt.Errorf("too many tags: %d (max %d)", len(tags), maxTags)
	}
	for _, t := range tags {
		if len(t) > maxTagLength {
			return fmt.Errorf("tag exceeds %d characters", maxTagLength)
		}
	}
	return nil
}

// ValidateDescription checks that a description is within length limits.
func ValidateDescription(desc string) error {
	if len(desc) > maxDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", maxDescriptionLength)
	}
	return nil
}

// maxEmailLength is the maximum length for an email address (RFC 5321).
const maxEmailLength = 254

// ValidateEmail checks that an email address has a basic valid format.
func ValidateEmail(email string) error {
	if len(email) > maxEmailLength {
		return fmt.Errorf("email exceeds %d characters", maxEmailLength)
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid email address")
	}
	if !strings.Contains(parts[1], ".") {
		return fmt.Errorf("invalid email address")
	}
	return nil
}
