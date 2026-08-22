// Package portal provides the asset portal data layer for persisting
// AI-generated assets (JSX dashboards, HTML reports, SVG charts).
//
// The portal's domain vocabulary — the persisted entities, the authenticated
// user, the store contracts and the validation over them — is defined in
// internal/portal/portaldomain and aliased here. The split exists because this
// package must import every handler seam it registers routes from, so a seam
// cannot import this package back; the shared types therefore sit below both.
// Every name below keeps the spelling callers already use.
package portal

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// Upload ceilings for content and thumbnails.
const (
	// MaxContentUploadBytes is the maximum size for content uploads (10 MB).
	MaxContentUploadBytes = portaldomain.MaxContentUploadBytes
	// MaxThumbnailUploadBytes is the maximum size for thumbnail uploads (512 KB).
	MaxThumbnailUploadBytes = portaldomain.MaxThumbnailUploadBytes
	// MaxChangeSummaryLength is the maximum length for a version change summary.
	MaxChangeSummaryLength = portaldomain.MaxChangeSummaryLength
)

// Page-size defaults shared by the asset, collection and knowledge-page lists.
const (
	defaultLimit = portaldomain.DefaultLimit
	maxLimit     = portaldomain.MaxLimit
)

// Target-type discriminators shared by shares and threads.
const (
	targetTypeAsset         = portaldomain.TargetTypeAsset
	targetTypeCollection    = portaldomain.TargetTypeCollection
	targetTypePrompt        = portaldomain.TargetTypePrompt
	targetTypeKnowledgePage = portaldomain.TargetTypeKnowledgePage
	targetTypeStandalone    = portaldomain.TargetTypeStandalone
)

// Asset and version types.
type (
	// AssetCollectionRef is a lightweight reference to a collection that contains an asset.
	AssetCollectionRef = portaldomain.AssetCollectionRef
	// Asset represents a persisted AI-generated asset.
	Asset = portaldomain.Asset
	// AssetVersion records a single version of an asset's content.
	AssetVersion = portaldomain.AssetVersion
	// Provenance records which calls produced an asset.
	Provenance = portaldomain.Provenance
	// ProvenanceToolCall records a single tool invocation in the provenance
	// chain. Pre-#1320 shape, still read from assets written under it.
	ProvenanceToolCall = portaldomain.ProvenanceToolCall
	// ProvenanceCapture is one recording of the calls an asset was built from.
	ProvenanceCapture = portaldomain.ProvenanceCapture
	// ProvenanceCall is one captured call in a capture's snapshot.
	ProvenanceCall = portaldomain.ProvenanceCall
	// ProvenanceRequest asks for a capture of the calls behind one asset write.
	ProvenanceRequest = portaldomain.ProvenanceRequest
	// ProvenanceCapturer resolves a write's sources into a capture.
	ProvenanceCapturer = portaldomain.ProvenanceCapturer
	// AssetFilter defines filtering criteria for listing assets.
	AssetFilter = portaldomain.AssetFilter
	// AssetUpdate holds mutable fields for updating an asset.
	AssetUpdate = portaldomain.AssetUpdate
)

// Provenance call kinds and outcomes.
const (
	// ProvenanceKindSQL is a statement run against a query engine.
	ProvenanceKindSQL = portaldomain.ProvenanceKindSQL
	// ProvenanceKindAPI is an HTTP invocation through the API gateway.
	ProvenanceKindAPI = portaldomain.ProvenanceKindAPI
	// ProvenanceKindTool is any other data-access call the platform serves.
	ProvenanceKindTool = portaldomain.ProvenanceKindTool
	// ProvenanceOutcomeSuccess is a call that returned normally.
	ProvenanceOutcomeSuccess = portaldomain.ProvenanceOutcomeSuccess
	// ProvenanceOutcomeError is a call that failed.
	ProvenanceOutcomeError = portaldomain.ProvenanceOutcomeError
)

// ParseCallReference returns the event id a source string names, accepting the
// bare id and the mcp:call:<id> reference form alike.
func ParseCallReference(source string) (string, bool) {
	return portaldomain.ParseCallReference(source)
}

// Share types.
type (
	// SharePermission defines the access level for a share recipient.
	SharePermission = portaldomain.SharePermission
	// ShareOrigin records how a share came to exist.
	ShareOrigin = portaldomain.ShareOrigin
	// ShareAccessMode determines who may open a share token.
	ShareAccessMode = portaldomain.ShareAccessMode
	// Share represents a share link for an asset, collection, or prompt.
	Share = portaldomain.Share
	// SharedAsset combines an Asset with share metadata for "shared with me" results.
	SharedAsset = portaldomain.SharedAsset
	// SharedPromptRef references a prompt shared with a user along with share metadata.
	SharedPromptRef = portaldomain.SharedPromptRef
	// SharedTargetRef is one share grant to a named recipient, of any artifact kind.
	SharedTargetRef = portaldomain.SharedTargetRef
	// ShareSummary indicates what kinds of active shares exist for an asset.
	ShareSummary = portaldomain.ShareSummary
)

// Share permission levels.
const (
	// PermissionViewer allows read-only access.
	PermissionViewer = portaldomain.PermissionViewer
	// PermissionEditor allows read and write access.
	PermissionEditor = portaldomain.PermissionEditor
)

// Share origins.
const (
	// OriginExplicit is a share created deliberately by an owner/editor.
	OriginExplicit = portaldomain.OriginExplicit
	// OriginPublicLinkLogin is a viewer share auto-created when a user signs
	// in through a public link and had no prior share for the target.
	OriginPublicLinkLogin = portaldomain.OriginPublicLinkLogin
)

// Share access modes.
const (
	// AccessModeRestricted admits only the named recipient
	// (SharedWithUserID or SharedWithEmail) and the share's creator.
	AccessModeRestricted = portaldomain.AccessModeRestricted
	// AccessModeAuthenticated admits any signed-in platform user.
	AccessModeAuthenticated = portaldomain.AccessModeAuthenticated
	// AccessModePublic admits anyone holding the token, without sign-in.
	AccessModePublic = portaldomain.AccessModePublic
)

// Collection types.
type (
	// CollectionConfig holds extensible per-collection settings.
	CollectionConfig = portaldomain.CollectionConfig
	// Collection represents a curated, ordered group of assets organized into sections.
	Collection = portaldomain.Collection
	// CollectionSection is an ordered section within a collection.
	CollectionSection = portaldomain.CollectionSection
	// CollectionItem is an ordered reference to an asset within a section.
	CollectionItem = portaldomain.CollectionItem
	// CollectionFilter defines filtering criteria for listing collections.
	CollectionFilter = portaldomain.CollectionFilter
	// SharedCollection combines a Collection with share metadata.
	SharedCollection = portaldomain.SharedCollection
)

// Store contracts and the object-storage port.
type (
	// AssetStore persists and queries portal assets.
	AssetStore = portaldomain.AssetStore
	// VersionStore persists and queries asset version history.
	VersionStore = portaldomain.VersionStore
	// ShareStore persists and queries share links for assets, collections, and prompts.
	ShareStore = portaldomain.ShareStore
	// CollectionStore persists and queries portal collections.
	CollectionStore = portaldomain.CollectionStore
	// S3Client abstracts the S3 operations needed by the portal toolkit.
	S3Client = portaldomain.S3Client
)

// Relevance-search contracts over assets and collections.
type (
	// AssetSearchQuery describes a relevance ranking request over saved assets.
	AssetSearchQuery = portaldomain.AssetSearchQuery
	// ScoredAsset pairs an asset with its relevance score in [0,1].
	ScoredAsset = portaldomain.ScoredAsset
	// AssetSearcher ranks the caller's assets by relevance to a query.
	AssetSearcher = portaldomain.AssetSearcher
	// CollectionSearchQuery describes a relevance ranking request over collections.
	CollectionSearchQuery = portaldomain.CollectionSearchQuery
	// ScoredCollection pairs a collection with its relevance score in [0,1].
	ScoredCollection = portaldomain.ScoredCollection
	// CollectionSearcher ranks the caller's collections by relevance to a query.
	CollectionSearcher = portaldomain.CollectionSearcher
)

// DefaultSearchLimit is the top-K returned when a ranked query does not
// specify a limit.
const DefaultSearchLimit = portaldomain.DefaultSearchLimit

// User holds information about the authenticated portal user. It is defined
// with the authorization core because it is the subject of every permission
// check the portal makes.
type User = access.User

// GetUser returns the User from context, or nil if not set.
func GetUser(ctx context.Context) *User { return access.GetUser(ctx) }

// ContextWithUser returns a copy of ctx carrying the authenticated user, the
// value GetUser reads. Exported so handlers split into sibling packages (e.g.
// internal/httpserver/datahubapi) can be exercised with an authenticated principal.
func ContextWithUser(ctx context.Context, user *User) context.Context {
	return access.ContextWithUser(ctx, user)
}

// ExtensionForContentType returns the file extension used in an asset's object
// key for a content type.
func ExtensionForContentType(ct string) string { return portaldomain.ExtensionForContentType(ct) }

// ResolveContentType returns the content type to store for content whose
// author declared declared.
func ResolveContentType(declared string, content []byte) string {
	return portaldomain.ResolveContentType(declared, content)
}

// ValidSharePermission checks whether a permission string is valid.
func ValidSharePermission(p string) bool { return portaldomain.ValidSharePermission(p) }

// ValidateAssetName checks that a name is non-empty and within length limits.
func ValidateAssetName(name string) error { return portaldomain.ValidateAssetName(name) }

// ValidateContentType checks that a content type is present and is one the
// platform stores for content that arrives as a string.
func ValidateContentType(ct string) error { return portaldomain.ValidateContentType(ct) }

// ValidateContentTypeChange checks the type a content update resolved to
// against the type the asset already carries.
func ValidateContentTypeChange(existing, resolved string) error {
	return portaldomain.ValidateContentTypeChange(existing, resolved)
}

// ValidateTags checks tag count and individual tag length.
func ValidateTags(tags []string) error { return portaldomain.ValidateTags(tags) }

// ValidateDescription checks that a description is within length limits.
func ValidateDescription(desc string) error { return portaldomain.ValidateDescription(desc) }

// ValidateMaxVersions checks a per-asset version-retention cap: 0 asks for
// unlimited history, any positive number is a cap, and a negative number is
// refused.
func ValidateMaxVersions(n int) error { return portaldomain.ValidateMaxVersions(n) }

// ValidateNoticeText checks that notice text is within length limits.
func ValidateNoticeText(text string) error { return portaldomain.ValidateNoticeText(text) }

// ValidateShareMessage checks the sharer's optional plain-text note.
func ValidateShareMessage(message string) error { return portaldomain.ValidateShareMessage(message) }

// ValidateChangeSummary checks that a change summary is within length limits.
func ValidateChangeSummary(s string) error { return portaldomain.ValidateChangeSummary(s) }

// ValidateCollectionName checks that a collection name is valid.
func ValidateCollectionName(name string) error { return portaldomain.ValidateCollectionName(name) }

// ValidateCollectionDescription checks collection description length.
func ValidateCollectionDescription(desc string) error {
	return portaldomain.ValidateCollectionDescription(desc)
}

// ValidateSectionTitle checks section title length.
func ValidateSectionTitle(title string) error { return portaldomain.ValidateSectionTitle(title) }

// ValidateSectionDescription checks section description length.
func ValidateSectionDescription(desc string) error {
	return portaldomain.ValidateSectionDescription(desc)
}

// ValidateSections checks sections count and content validity.
func ValidateSections(sections []CollectionSection) error {
	return portaldomain.ValidateSections(sections)
}

// ValidateEmail checks that an email address has a basic valid format.
func ValidateEmail(email string) error { return portaldomain.ValidateEmail(email) }

// AssetIndexText composes the text an asset is embedded and lexically indexed
// on for relevance search: its name, description, and tags.
func AssetIndexText(name, description string, tags []string) string {
	return portaldomain.AssetIndexText(name, description, tags)
}

// CollectionIndexText composes the text a collection is embedded and lexically
// indexed on: its name, description, and the denormalized section text.
func CollectionIndexText(name, description, sectionsText string) string {
	return portaldomain.CollectionIndexText(name, description, sectionsText)
}

// SectionsText flattens a collection's sections into the denormalized
// sections_text column.
func SectionsText(sections []CollectionSection) string {
	return portaldomain.SectionsText(sections)
}
