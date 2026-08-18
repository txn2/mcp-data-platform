// Package portaldomain holds the asset portal's domain vocabulary: the
// persisted entities (assets, versions, shares, collections), the identity a
// portal request carries, the store contracts over them, and the validation
// that guards every write door.
//
// It exists so pkg/portal and the handler seams under internal/portal can name
// the same types. pkg/portal must import any package it registers routes from,
// so a seam cannot import pkg/portal back; the shared vocabulary therefore has
// to sit below both. pkg/portal aliases every name defined here, which is why
// its public spelling (portal.Asset, portal.Collection, portal.User) is
// unchanged for callers.
package portaldomain

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
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

// Asset represents a persisted AI-generated asset.
type Asset struct {
	ID             string `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	OwnerID        string `json:"owner_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	OwnerEmail     string `json:"owner_email" example:"alice@example.com"`
	Name           string `json:"name" example:"Q4 Revenue Dashboard"`
	Description    string `json:"description,omitempty" example:"Interactive revenue breakdown by region"`
	ContentType    string `json:"content_type" example:"text/html"`
	S3Bucket       string `json:"s3_bucket" example:"portal-assets"`
	S3Key          string `json:"s3_key" example:"assets/01HK7R8Z/content.html"`
	ThumbnailS3Key string `json:"thumbnail_s3_key,omitempty" example:"assets/01HK7R8Z/thumb.png"`
	// ThumbnailDarkS3Key holds the dark-mode thumbnail variant. Only populated
	// for content types rendered on a forced background (markdown, CSV); types
	// with a built-in theme (HTML, JSX, SVG) reuse ThumbnailS3Key in both modes.
	// Empty means callers should fall back to ThumbnailS3Key.
	ThumbnailDarkS3Key string               `json:"thumbnail_dark_s3_key,omitempty" example:"assets/01HK7R8Z/thumbnail_dark.png"`
	SizeBytes          int64                `json:"size_bytes" example:"4200"`
	Tags               []string             `json:"tags"`
	Provenance         Provenance           `json:"provenance"`
	SessionID          string               `json:"session_id,omitempty" example:"sess_abc123"`
	IdempotencyKey     string               `json:"idempotency_key,omitempty" example:"export-2026-04-18-abc123"`
	CurrentVersion     int                  `json:"current_version" example:"1"`
	Collections        []AssetCollectionRef `json:"collections,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	DeletedAt          *time.Time           `json:"deleted_at,omitempty"`
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

// ExtensionForContentType returns the file extension used in an asset's object
// key for a content type. It delegates to the shared contenttype table, so a
// key stops defaulting to ".bin" for every family the platform can now detect
// (images, audio, video, PDF, YAML, NDJSON, ...).
func ExtensionForContentType(ct string) string {
	return contenttype.Extension(ct)
}

// ResolveContentType returns the content type to store for content whose
// author declared declared. A specific declaration is honored; a generic or
// absent one is replaced by the type detected from the bytes. Detection never
// produces an active type — see pkg/contenttype.
func ResolveContentType(declared string, content []byte) string {
	return contenttype.DetectBytes(declared, content)
}

// Provenance records which calls produced an asset.
//
// Captures is the live shape (issue #1320): one entry per time the asset was
// written, each naming the calls that fed that write by audit event id and
// carrying a snapshot of them. ToolCalls is what assets saved before that
// carry — the platform no longer writes it, and readers render both.
type Provenance struct {
	Captures  []ProvenanceCapture  `json:"captures,omitempty"`
	ToolCalls []ProvenanceToolCall `json:"tool_calls,omitempty"`
	SessionID string               `json:"session_id,omitempty" example:"sess_abc123"`
	UserID    string               `json:"user_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	// DeclaredContentType is the media type the writer declared, recorded only
	// when detection replaced it. It is the audit trail for a reclassified
	// asset: it answers "what did the upstream actually say" without which a
	// stored type that disagrees with the source is unexplainable.
	DeclaredContentType string `json:"declared_content_type,omitempty" example:"text/plain"`
}

// ProvenanceToolCall records a single tool invocation in the provenance chain.
// It is the pre-#1320 shape, kept because assets written under it still carry
// it. Nothing writes it any more.
type ProvenanceToolCall struct {
	ToolName   string         `json:"tool_name" example:"trino_query"`
	Timestamp  string         `json:"timestamp" example:"2026-04-15T14:30:00Z"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// Provenance call kinds. An asset is built from queries and from API
// invocations alike, so a captured call says which it was rather than leaving
// the reader to infer it from a tool name.
const (
	// ProvenanceKindSQL is a statement run against a query engine.
	ProvenanceKindSQL = "sql"
	// ProvenanceKindAPI is an HTTP invocation through the API gateway.
	ProvenanceKindAPI = "api"
	// ProvenanceKindTool is any other data-access call the platform serves
	// (catalog lookups, object reads, upstream MCP tools).
	ProvenanceKindTool = "tool"
)

// Provenance call outcomes, as recorded by the audit log.
const (
	// ProvenanceOutcomeSuccess is a call that returned normally.
	ProvenanceOutcomeSuccess = "success"
	// ProvenanceOutcomeError is a call that failed. A failed call is part of
	// how an answer was reached and is captured, not hidden.
	ProvenanceOutcomeError = "error"
)

// ProvenanceCapture is one recording of the calls an asset was built from,
// taken when the asset was written. An update appends another, so the captures
// read in order as the asset's history of what fed each of its versions.
type ProvenanceCapture struct {
	// Tool is the tool that performed this capture (save_asset, manage_asset,
	// trino_export, api_export).
	Tool string `json:"tool" example:"save_asset"`
	// CapturedAt is when the capture was taken.
	CapturedAt time.Time `json:"captured_at"`
	// Version is the asset version this capture produced, when known.
	Version int `json:"version,omitempty" example:"2"`
	// SessionID is the session whose calls were captured.
	SessionID string `json:"session_id,omitempty" example:"dps_abc123"`
	// EventIDs are the audit event ids of the captured calls, in call order.
	// They are the durable reference: the audit log holds the full record of
	// each call for as long as it is retained.
	EventIDs []string `json:"event_ids,omitempty"`
	// Explicit records that the caller named these sources rather than the
	// platform taking the session's calls since the previous capture.
	Explicit bool `json:"explicit,omitempty"`
	// Truncated records that more calls were eligible than the capture holds.
	Truncated bool `json:"truncated,omitempty"`
	// Calls is the snapshot of the captured calls, taken at write time.
	// Audit rows are retained for a fixed window and assets are not, so an
	// asset that outlives its audit rows still says what produced it.
	Calls []ProvenanceCall `json:"calls,omitempty"`
}

// ProvenanceCall is one captured call: what ran, against what, why, and how it
// ended. Fields that do not apply to a call's kind are empty.
type ProvenanceCall struct {
	// EventID is the audit event this call was read from. A call the capturing
	// tool recorded about itself carries the id minted for that call before
	// its handler ran, which is the same id the caller was handed back: the
	// audit row follows, but the identifier does not wait for it.
	EventID string `json:"event_id,omitempty"`
	// Cited records that this call was NAMED as a source of the write rather
	// than merely being in the session's default window when the write
	// happened. A caller's `sources` argument names calls, and so does the
	// capturing call's own record of itself; a window does not. It is what
	// separates a call an artifact was built from, whose catalog record reads
	// `satisfied`, from a call that was only in scope at the time (#1353).
	Cited bool `json:"cited,omitempty"`
	// Kind is one of the ProvenanceKind constants.
	Kind string `json:"kind" example:"sql"`
	// Tool is the tool that was called.
	Tool string `json:"tool" example:"trino_query"`
	// Connection is the named connection the call was routed to.
	Connection string `json:"connection,omitempty" example:"warehouse"`
	// Statement is the query text, for a sql call.
	Statement string `json:"statement,omitempty"`
	// Method and Path are the HTTP request line, for an api call.
	Method string `json:"method,omitempty" example:"GET"`
	Path   string `json:"path,omitempty" example:"/v1/orders"`
	// OperationID is the catalog operation invoked, for an api call.
	OperationID string `json:"operation_id,omitempty" example:"listOrders"`
	// Summary describes a call whose kind carries neither a statement nor a
	// request line (a catalog lookup, an object read).
	Summary string `json:"summary,omitempty"`
	// Purpose is the reason the caller stated for the call (#1317).
	Purpose string `json:"purpose,omitempty"`
	// Outcome is one of the ProvenanceOutcome constants.
	Outcome string `json:"outcome" example:"success"`
	// Error is the failure message, for a call that ended in error.
	Error string `json:"error,omitempty"`
	// DurationMS is how long the call took.
	DurationMS int64 `json:"duration_ms,omitempty" example:"143"`
	// Timestamp is when the call started.
	Timestamp time.Time `json:"timestamp"`
}

// ProvenanceRequest asks for a capture of the calls behind one asset write.
type ProvenanceRequest struct {
	// Tool is the tool performing the write.
	Tool string
	// SessionID and UserID identify the caller. They scope the capture: a
	// caller can only ever record its own calls as an asset's sources.
	SessionID string
	UserID    string
	// Sources are the event ids (bare, or in mcp:call:<id> form) the caller
	// named. When set they replace the default window entirely.
	Sources []string
	// Version is the asset version this write produces, when known.
	Version int
	// Own is the capturing call's record of itself, appended after the
	// resolved sources. An export tool knows the statement it just ran and
	// its own audit row does not exist yet, so it states it here.
	Own *ProvenanceCall
}

// ProvenanceCapturer resolves a write's sources into a capture. The platform
// implements it over the audit log; a deployment without one leaves it nil and
// assets record their session and owner but no calls.
type ProvenanceCapturer func(ctx context.Context, req ProvenanceRequest) ProvenanceCapture

// ParseCallReference returns the event id a source string names, accepting
// both the bare id and the mcp:call:<id> reference form, and reports whether
// anything was left after trimming.
func ParseCallReference(source string) (string, bool) {
	id := strings.TrimSpace(source)
	id = strings.TrimPrefix(id, "mcp:call:")
	return id, id != ""
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

// ShareOrigin records how a share came to exist.
type ShareOrigin string

const (
	// OriginExplicit is a share created deliberately by an owner/editor.
	OriginExplicit ShareOrigin = "explicit"
	// OriginPublicLinkLogin is a viewer share auto-created when a user signs
	// in through a public link and had no prior share for the target.
	OriginPublicLinkLogin ShareOrigin = "public_link_login"
)

// ShareAccessMode determines who may open a share token. The mode domain and
// the access decision live in pkg/portal/shareaccess; these aliases keep the
// portal's public API and its JSON payloads spelled as before.
type ShareAccessMode = shareaccess.Mode

const (
	// AccessModeRestricted admits only the named recipient
	// (SharedWithUserID or SharedWithEmail) and the share's creator.
	AccessModeRestricted = shareaccess.ModeRestricted
	// AccessModeAuthenticated admits any signed-in platform user.
	AccessModeAuthenticated = shareaccess.ModeAuthenticated
	// AccessModePublic admits anyone holding the token, without sign-in.
	AccessModePublic = shareaccess.ModePublic
)

// Share represents a share link for an asset, collection, or prompt.
// Exactly one of AssetID, CollectionID, or PromptID is set.
type Share struct {
	ID               string          `json:"id" example:"share_01HK7R9B"`
	AssetID          string          `json:"asset_id,omitempty" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	CollectionID     string          `json:"collection_id,omitempty"`
	PromptID         string          `json:"prompt_id,omitempty"`
	Token            string          `json:"token" example:"tk_a1b2c3d4e5f6"`
	CreatedBy        string          `json:"created_by" example:"alice@example.com"`
	SharedWithUserID string          `json:"shared_with_user_id,omitempty"`
	SharedWithEmail  string          `json:"shared_with_email,omitempty" example:"bob@example.com"`
	Permission       SharePermission `json:"permission" example:"viewer"`
	AccessMode       ShareAccessMode `json:"access_mode" example:"restricted"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
	Revoked          bool            `json:"revoked" example:"false"`
	HideExpiration   bool            `json:"hide_expiration" example:"false"`
	NoticeText       string          `json:"notice_text" example:"Proprietary & Confidential"`
	AccessCount      int             `json:"access_count" example:"3"`
	LastAccessedAt   *time.Time      `json:"last_accessed_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	Origin           ShareOrigin     `json:"origin,omitempty" example:"explicit"`
}

// SharedAsset combines an Asset with share metadata for "shared with me" results.
type SharedAsset struct {
	Asset      Asset           `json:"asset"`
	ShareID    string          `json:"share_id" example:"share_01HK7R9B"`
	SharedBy   string          `json:"shared_by" example:"alice@example.com"`
	SharedAt   time.Time       `json:"shared_at"`
	Permission SharePermission `json:"permission" example:"viewer"`
}

// SharedPromptRef references a prompt shared with a user along with share
// metadata. The prompt body is fetched separately from the prompt store so the
// share store stays decoupled from the prompt domain.
type SharedPromptRef struct {
	PromptID   string
	ShareID    string
	SharedBy   string
	SharedAt   time.Time
	Permission SharePermission
}

// SharedTargetRef is one active share grant to a named recipient, whatever kind
// of artifact it points at. portal_shares is a single polymorphic table, so the
// three per-kind list queries (assets, collections, prompts) collapse to one
// where the caller wants recency rather than the artifact body: the session-start
// notice digest names what was shared and when, and dereferences it through the
// portal. TargetName is the artifact's display name, resolved in the same query.
type SharedTargetRef struct {
	ShareID    string
	TargetType string // one of the TargetType* constants
	TargetID   string
	TargetName string
	SharedBy   string
	SharedAt   time.Time
	Permission SharePermission
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
	// SortBy names the ordering column. It must be a key of
	// AssetSortColumns; anything else falls back to SortUpdatedAt.
	SortBy string `json:"sort_by,omitempty"`
	// SortDir is SortAsc or SortDesc. Anything else falls back to SortDesc.
	SortDir string `json:"sort_dir,omitempty"`
}

// Order returns the ORDER BY clauses for this filter, with unknown columns and
// directions resolved to the defaults.
func (f *AssetFilter) Order() []string {
	return ResolveOrder(f.SortBy, f.SortDir, AssetSortColumns, SortUpdatedAt)
}

// DefaultLimit is the default page size for asset and collection listing.
const DefaultLimit = 50

// MaxLimit caps the maximum number of entities per page.
const MaxLimit = 200

// EffectiveLimit returns the limit with defaults applied.
func (f *AssetFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	if f.Limit > MaxLimit {
		return MaxLimit
	}
	return f.Limit
}

// AssetUpdate holds mutable fields for updating an asset.
// Pointer fields distinguish "no change" (nil) from "clear to empty" (pointer to "").
type AssetUpdate struct {
	Name               *string  `json:"name,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	ContentType        string   `json:"content_type,omitempty"`
	S3Key              string   `json:"s3_key,omitempty"`
	SizeBytes          int64    `json:"size_bytes,omitempty"`
	ThumbnailS3Key     *string  `json:"thumbnail_s3_key,omitempty"`
	ThumbnailDarkS3Key *string  `json:"thumbnail_dark_s3_key,omitempty"`
	HasContent         bool     `json:"-"` // set when content replacement provides SizeBytes (even if 0)
}

// MaxNameLength is the maximum length for asset names.
const MaxNameLength = 255

// MaxDescriptionLength is the maximum length for asset descriptions.
const MaxDescriptionLength = 2000

// MaxTags is the maximum number of tags per asset.
const MaxTags = 20

// MaxTagLength is the maximum length for a single tag.
const MaxTagLength = 100

// ValidateAssetName checks that a name is non-empty and within length limits.
func ValidateAssetName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("name exceeds %d characters", MaxNameLength)
	}
	return nil
}

// ValidateContentType checks that a content type is present and is one the
// platform stores for content that arrives as a string.
//
// Every door that takes a caller-declared type for string content applies it —
// the REST inline-create endpoint, save_asset and the manage_asset content
// update — so the three admit exactly the same set. The type accepted here is
// what the asset is stored under, and so what later drives its disposition and
// its viewer, which is why the check is an allowlist rather than a denylist.
func ValidateContentType(ct string) error {
	if ct == "" {
		return errors.New("content_type is required")
	}
	if !contenttype.IsStorableText(ct) {
		return fmt.Errorf("unsupported content_type %q; accepted: %s",
			ct, strings.Join(contenttype.StorableTextTypes(), ", "))
	}
	return nil
}

// ValidateContentTypeChange checks the type a content update resolved to
// against the type the asset already carries.
//
// An update may keep whatever the asset carries, including a type no door would
// accept today: an asset that predates the allowlist, or one an export wrote
// from an upstream response, must not become uneditable. Landing on a different
// type is a new declaration and goes through the same check as a create.
func ValidateContentTypeChange(existing, resolved string) error {
	if contenttype.Normalize(existing) == contenttype.Normalize(resolved) {
		return nil
	}
	return ValidateContentType(resolved)
}

// ValidateTags checks tag count and individual tag length.
func ValidateTags(tags []string) error {
	if len(tags) > MaxTags {
		return fmt.Errorf("too many tags: %d (max %d)", len(tags), MaxTags)
	}
	for _, t := range tags {
		if len(t) > MaxTagLength {
			return fmt.Errorf("tag exceeds %d characters", MaxTagLength)
		}
	}
	return nil
}

// ValidateDescription checks that a description is within length limits.
func ValidateDescription(desc string) error {
	if len(desc) > MaxDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", MaxDescriptionLength)
	}
	return nil
}

// DefaultNoticeText is the notice shown on public shares when no custom text
// is provided. It lives with the share domain so the portal handlers and the
// out-of-package share creators (e.g. the export adapters) apply one default
// rather than each re-declaring the literal.
const DefaultNoticeText = "Proprietary & Confidential. Only share with authorized viewers."

// MaxNoticeTextLength is the maximum length for share notice text.
const MaxNoticeTextLength = 500

// ValidateNoticeText checks that notice text is within length limits.
func ValidateNoticeText(text string) error {
	if len(text) > MaxNoticeTextLength {
		return fmt.Errorf("notice_text exceeds %d characters", MaxNoticeTextLength)
	}
	return nil
}

// MaxShareMessageLength is the maximum length for the personal note a sharer
// may attach to a share.
const MaxShareMessageLength = 500

// shareMessageTag matches the start of an HTML tag: "<" followed by an
// optional slash and a name or declaration character. It deliberately does not
// match a bare "<" or ">", so plain prose like "margin > 40%" stays legal.
var shareMessageTag = regexp.MustCompile(`<\s*/?\s*[a-zA-Z!]`)

// shareMessageLink matches the link forms mail clients turn into clickable
// anchors, plus the schemes that would be dangerous if one ever did: an
// explicit scheme separator, a "www." host, or a named scheme.
var shareMessageLink = regexp.MustCompile(`(?i)(://|\bwww\.|\b(?:https?|mailto|data|javascript|file|ftp)\s*:)`)

// ValidateShareMessage checks the optional personal note a sharer attaches to
// a share. The note is plain text and travels only in the notification email.
//
// The email is a message the recipient's employer sends them, so its body is
// trusted in a way arbitrary web content is not. Markup and links are rejected
// here rather than escaped alone: escaping stops a note from rendering as
// markup, but a plausible-looking link inside a platform email is a phishing
// vector regardless of how it is encoded. Rendering escapes as well, so the
// two defenses are independent.
func ValidateShareMessage(message string) error {
	if len(message) > MaxShareMessageLength {
		return fmt.Errorf("message exceeds %d characters", MaxShareMessageLength)
	}
	if shareMessageTag.MatchString(message) {
		return errors.New("message must be plain text: remove HTML tags")
	}
	if shareMessageLink.MatchString(message) {
		return errors.New("message must be plain text: remove links")
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
	// SortBy names the ordering column. It must be a key of
	// CollectionSortColumns; anything else falls back to SortUpdatedAt.
	SortBy string `json:"sort_by,omitempty"`
	// SortDir is SortAsc or SortDesc. Anything else falls back to SortDesc.
	SortDir string `json:"sort_dir,omitempty"`
}

// Order returns the ORDER BY clauses for this filter, with unknown columns and
// directions resolved to the defaults.
func (f *CollectionFilter) Order() []string {
	return ResolveOrder(f.SortBy, f.SortDir, CollectionSortColumns, SortUpdatedAt)
}

// EffectiveLimit returns the limit with defaults applied.
func (f *CollectionFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	if f.Limit > MaxLimit {
		return MaxLimit
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

// MaxCollectionDescriptionLength is the maximum length for collection descriptions.
const MaxCollectionDescriptionLength = 50000

// MaxSectionDescriptionLength is the maximum length for section descriptions.
const MaxSectionDescriptionLength = 10000

// MaxSectionTitleLength is the maximum length for section titles.
const MaxSectionTitleLength = 255

// MaxSections is the maximum number of sections per collection.
const MaxSections = 50

// MaxItemsPerSection is the maximum number of items per section.
const MaxItemsPerSection = 100

// ValidateCollectionName checks that a collection name is valid.
func ValidateCollectionName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("name exceeds %d characters", MaxNameLength)
	}
	return nil
}

// ValidateCollectionDescription checks collection description length.
func ValidateCollectionDescription(desc string) error {
	if len(desc) > MaxCollectionDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", MaxCollectionDescriptionLength)
	}
	return nil
}

// ValidateSectionTitle checks section title length.
func ValidateSectionTitle(title string) error {
	if len(title) > MaxSectionTitleLength {
		return fmt.Errorf("title exceeds %d characters", MaxSectionTitleLength)
	}
	return nil
}

// ValidateSectionDescription checks section description length.
func ValidateSectionDescription(desc string) error {
	if len(desc) > MaxSectionDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", MaxSectionDescriptionLength)
	}
	return nil
}

// ValidateSections checks sections count and content validity.
func ValidateSections(sections []CollectionSection) error {
	if len(sections) > MaxSections {
		return fmt.Errorf("too many sections: %d (max %d)", len(sections), MaxSections)
	}
	for i, s := range sections {
		if err := ValidateSectionTitle(s.Title); err != nil {
			return fmt.Errorf("section %d: %w", i, err)
		}
		if err := ValidateSectionDescription(s.Description); err != nil {
			return fmt.Errorf("section %d: %w", i, err)
		}
		if len(s.Items) > MaxItemsPerSection {
			return fmt.Errorf("section %d: too many items: %d (max %d)", i, len(s.Items), MaxItemsPerSection)
		}
	}
	return nil
}

// MaxEmailLength is the maximum length for an email address (RFC 5321).
const MaxEmailLength = 254

// ParseEmail reduces recipient input to the bare address it names, lowercased.
//
// It accepts both a plain address and the "Display Name <user@example.com>"
// form mail clients put on the clipboard, because that is what people paste
// into a share field. Storing the display form verbatim produced a share that
// matched no signed-in user and a notification addressed to a string no mail
// server would route, so the display name is stripped here rather than
// anywhere downstream: every door that accepts a recipient stores what this
// returns.
//
// The domain must contain a dot, which keeps single-label hosts
// ("user@localhost") out of a recipient field whose value is mailed to.
func ParseEmail(input string) (string, error) {
	input = strings.TrimSpace(input)
	if len(input) > MaxEmailLength {
		return "", fmt.Errorf("email exceeds %d characters", MaxEmailLength)
	}
	addr, err := mail.ParseAddress(input)
	if err != nil {
		return "", errors.New("invalid email address")
	}
	local, domain, ok := strings.Cut(addr.Address, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(addr.Address), nil
}

// ValidateEmail reports whether recipient input names a usable address. It is
// exactly ParseEmail's verdict: a value that cannot be reduced to a bare
// address is rejected rather than stored raw.
func ValidateEmail(email string) error {
	_, err := ParseEmail(email)
	return err
}
