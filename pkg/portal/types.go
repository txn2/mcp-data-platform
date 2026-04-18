// Package portal provides the asset portal data layer for persisting
// AI-generated artifacts (JSX dashboards, HTML reports, SVG charts).
package portal

import (
	"fmt"
	"strings"
	"time"
)

// MaxContentUploadBytes is the maximum size for content uploads (10 MB).
const MaxContentUploadBytes = 10 << 20

// MaxThumbnailUploadBytes is the maximum size for thumbnail uploads (512 KB).
const MaxThumbnailUploadBytes = 512 << 10

// AssetCollectionRef is a lightweight reference to a collection that contains an asset.
type AssetCollectionRef struct {
	ID   string `json:"id" example:"col_01HK7R8Z"`
	Name string `json:"name" example:"Q4 Performance Review"`
}

// Asset represents a persisted AI-generated artifact.
type Asset struct {
	ID             string               `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	OwnerID        string               `json:"owner_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	OwnerEmail     string               `json:"owner_email" example:"alice@example.com"`
	Name           string               `json:"name" example:"Q4 Revenue Dashboard"`
	Description    string               `json:"description,omitempty" example:"Interactive revenue breakdown by region"`
	ContentType    string               `json:"content_type" example:"text/html"`
	S3Bucket       string               `json:"s3_bucket" example:"portal-assets"`
	S3Key          string               `json:"s3_key" example:"assets/01HK7R8Z/content.html"`
	ThumbnailS3Key string               `json:"thumbnail_s3_key,omitempty" example:"assets/01HK7R8Z/thumb.png"`
	SizeBytes      int64                `json:"size_bytes" example:"4200"`
	Tags           []string             `json:"tags"`
	Provenance     Provenance           `json:"provenance"`
	SessionID      string               `json:"session_id,omitempty" example:"sess_abc123"`
	CurrentVersion int                  `json:"current_version" example:"1"`
	Collections    []AssetCollectionRef `json:"collections,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	DeletedAt      *time.Time           `json:"deleted_at,omitempty"`
}

// AssetVersion records a single version of an asset's content.
type AssetVersion struct {
	ID            string    `json:"id" example:"ver_01HK7R9A"`
	AssetID       string    `json:"asset_id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Version       int       `json:"version" example:"2"`
	S3Key         string    `json:"s3_key" example:"assets/01HK7R8Z/v2/content.html"`
	S3Bucket      string    `json:"s3_bucket" example:"portal-assets"`
	ContentType   string    `json:"content_type" example:"text/html"`
	SizeBytes     int64     `json:"size_bytes" example:"4500"`
	CreatedBy     string    `json:"created_by" example:"alice@example.com"`
	ChangeSummary string    `json:"change_summary" example:"Updated regional breakdown chart"`
	CreatedAt     time.Time `json:"created_at"`
}

// ExtensionForContentType returns a file extension based on content type.
func ExtensionForContentType(ct string) string {
	switch {
	case strings.Contains(ct, "html") || strings.Contains(ct, "jsx"):
		return ".html"
	case strings.Contains(ct, "svg"):
		return ".svg"
	case strings.Contains(ct, "markdown"):
		return ".md"
	case strings.Contains(ct, "json"):
		return ".json"
	case strings.Contains(ct, "csv"):
		return ".csv"
	default:
		return ".bin"
	}
}

// Provenance records the tool call history that produced an artifact.
type Provenance struct {
	ToolCalls []ProvenanceToolCall `json:"tool_calls,omitempty"`
	SessionID string               `json:"session_id,omitempty" example:"sess_abc123"`
	UserID    string               `json:"user_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ProvenanceToolCall records a single tool invocation in the provenance chain.
type ProvenanceToolCall struct {
	ToolName   string         `json:"tool_name" example:"trino_query"`
	Timestamp  string         `json:"timestamp" example:"2026-04-15T14:30:00Z"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// SharePermission defines the access level for a share recipient.
type SharePermission string

const (
	// PermissionViewer allows read-only access.
	PermissionViewer SharePermission = "viewer"
	// PermissionEditor allows read and write access.
	PermissionEditor SharePermission = "editor"
)

// ValidSharePermission checks whether a permission string is valid.
func ValidSharePermission(p string) bool {
	return p == string(PermissionViewer) || p == string(PermissionEditor)
}

// Share represents a share link for an asset or collection.
// Exactly one of AssetID or CollectionID is set.
type Share struct {
	ID               string          `json:"id" example:"share_01HK7R9B"`
	AssetID          string          `json:"asset_id,omitempty" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	CollectionID     string          `json:"collection_id,omitempty"`
	Token            string          `json:"token" example:"tk_a1b2c3d4e5f6"`
	CreatedBy        string          `json:"created_by" example:"alice@example.com"`
	SharedWithUserID string          `json:"shared_with_user_id,omitempty"`
	SharedWithEmail  string          `json:"shared_with_email,omitempty" example:"bob@example.com"`
	Permission       SharePermission `json:"permission" example:"viewer"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
	Revoked          bool            `json:"revoked" example:"false"`
	HideExpiration   bool            `json:"hide_expiration" example:"false"`
	NoticeText       string          `json:"notice_text" example:"Proprietary & Confidential"`
	AccessCount      int             `json:"access_count" example:"3"`
	LastAccessedAt   *time.Time      `json:"last_accessed_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// SharedAsset combines an Asset with share metadata for "shared with me" results.
type SharedAsset struct {
	Asset      Asset           `json:"asset"`
	ShareID    string          `json:"share_id" example:"share_01HK7R9B"`
	SharedBy   string          `json:"shared_by" example:"alice@example.com"`
	SharedAt   time.Time       `json:"shared_at"`
	Permission SharePermission `json:"permission" example:"viewer"`
}

// ShareSummary indicates what kinds of active shares exist for an asset.
type ShareSummary struct {
	HasUserShare  bool `json:"has_user_share" example:"true"`
	HasPublicLink bool `json:"has_public_link" example:"false"`
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
	Name           *string  `json:"name,omitempty"`
	Description    *string  `json:"description,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	ContentType    string   `json:"content_type,omitempty"`
	S3Key          string   `json:"s3_key,omitempty"`
	SizeBytes      int64    `json:"size_bytes,omitempty"`
	ThumbnailS3Key *string  `json:"thumbnail_s3_key,omitempty"`
	HasContent     bool     `json:"-"` // set when content replacement provides SizeBytes (even if 0)
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

// maxNoticeTextLength is the maximum length for share notice text.
const maxNoticeTextLength = 500

// ValidateNoticeText checks that notice text is within length limits.
func ValidateNoticeText(text string) error {
	if len(text) > maxNoticeTextLength {
		return fmt.Errorf("notice_text exceeds %d characters", maxNoticeTextLength)
	}
	return nil
}

// MaxChangeSummaryLength is the maximum length for a version change summary.
const MaxChangeSummaryLength = 500

// ValidateChangeSummary checks that a change summary is within length limits.
func ValidateChangeSummary(s string) error {
	if len(s) > MaxChangeSummaryLength {
		return fmt.Errorf("change_summary exceeds %d characters", MaxChangeSummaryLength)
	}
	return nil
}

// --- Collection types ---

// CollectionConfig holds extensible per-collection settings.
type CollectionConfig struct {
	ThumbnailSize string `json:"thumbnail_size,omitempty" example:"medium"` // "large", "medium", "small", "none"
}

// Collection represents a curated, ordered group of assets organized into sections.
type Collection struct {
	ID             string              `json:"id" example:"col_01HK7R8Z"`
	OwnerID        string              `json:"owner_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	OwnerEmail     string              `json:"owner_email" example:"alice@example.com"`
	Name           string              `json:"name" example:"Q4 Performance Review"`
	Description    string              `json:"description" example:"Executive collection with revenue dashboards"`
	ThumbnailS3Key string              `json:"thumbnail_s3_key,omitempty"`
	Config         CollectionConfig    `json:"config"`
	Sections       []CollectionSection `json:"sections"`
	AssetTags      []string            `json:"asset_tags,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	DeletedAt      *time.Time          `json:"deleted_at,omitempty"`
}

// CollectionSection is an ordered section within a collection.
type CollectionSection struct {
	ID           string           `json:"id" example:"sec_01HK7R9C"`
	CollectionID string           `json:"collection_id" example:"col_01HK7R8Z"`
	Title        string           `json:"title" example:"Overview"`
	Description  string           `json:"description" example:"High-level revenue and KPI snapshots"`
	Position     int              `json:"position" example:"0"`
	Items        []CollectionItem `json:"items"`
	CreatedAt    time.Time        `json:"created_at"`
}

// CollectionItem is an ordered reference to an asset within a section.
// Asset* fields are populated by the store on read (JOIN with portal_assets).
type CollectionItem struct {
	ID               string    `json:"id" example:"item_01HK7R9D"`
	SectionID        string    `json:"section_id" example:"sec_01HK7R9C"`
	AssetID          string    `json:"asset_id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	Position         int       `json:"position" example:"0"`
	AssetName        string    `json:"asset_name,omitempty" example:"Q4 Revenue Dashboard"`
	AssetContentType string    `json:"asset_content_type,omitempty" example:"text/html"`
	AssetThumbnail   string    `json:"asset_thumbnail_s3_key,omitempty"`
	AssetDescription string    `json:"asset_description,omitempty" example:"Interactive revenue breakdown"`
	CreatedAt        time.Time `json:"created_at"`
}

// CollectionFilter defines filtering criteria for listing collections.
type CollectionFilter struct {
	OwnerID string `json:"owner_id,omitempty"`
	Search  string `json:"search,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Offset  int    `json:"offset,omitempty"`
}

// EffectiveLimit returns the limit with defaults applied.
func (f *CollectionFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return defaultLimit
	}
	if f.Limit > maxLimit {
		return maxLimit
	}
	return f.Limit
}

// SharedCollection combines a Collection with share metadata.
type SharedCollection struct {
	Collection Collection      `json:"collection"`
	ShareID    string          `json:"share_id" example:"share_01HK7R9E"`
	SharedBy   string          `json:"shared_by" example:"alice@example.com"`
	SharedAt   time.Time       `json:"shared_at"`
	Permission SharePermission `json:"permission" example:"viewer"`
}

// maxCollectionDescriptionLength is the maximum length for collection descriptions.
const maxCollectionDescriptionLength = 50000

// maxSectionDescriptionLength is the maximum length for section descriptions.
const maxSectionDescriptionLength = 10000

// maxSectionTitleLength is the maximum length for section titles.
const maxSectionTitleLength = 255

// maxSections is the maximum number of sections per collection.
const maxSections = 50

// maxItemsPerSection is the maximum number of items per section.
const maxItemsPerSection = 100

// ValidateCollectionName checks that a collection name is valid.
func ValidateCollectionName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("name exceeds %d characters", maxNameLength)
	}
	return nil
}

// ValidateCollectionDescription checks collection description length.
func ValidateCollectionDescription(desc string) error {
	if len(desc) > maxCollectionDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", maxCollectionDescriptionLength)
	}
	return nil
}

// ValidateSectionTitle checks section title length.
func ValidateSectionTitle(title string) error {
	if len(title) > maxSectionTitleLength {
		return fmt.Errorf("title exceeds %d characters", maxSectionTitleLength)
	}
	return nil
}

// ValidateSectionDescription checks section description length.
func ValidateSectionDescription(desc string) error {
	if len(desc) > maxSectionDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", maxSectionDescriptionLength)
	}
	return nil
}

// ValidateSections checks sections count and content validity.
func ValidateSections(sections []CollectionSection) error {
	if len(sections) > maxSections {
		return fmt.Errorf("too many sections: %d (max %d)", len(sections), maxSections)
	}
	for i, s := range sections {
		if err := ValidateSectionTitle(s.Title); err != nil {
			return fmt.Errorf("section %d: %w", i, err)
		}
		if err := ValidateSectionDescription(s.Description); err != nil {
			return fmt.Errorf("section %d: %w", i, err)
		}
		if len(s.Items) > maxItemsPerSection {
			return fmt.Errorf("section %d: too many items: %d (max %d)", i, len(s.Items), maxItemsPerSection)
		}
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
