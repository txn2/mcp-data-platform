package portaldomain

import (
	"context"
	"time"
)

// Target-type discriminators shared by shares and threads. Exactly one of the
// object targets (asset/collection/prompt/knowledge page) is set on a
// polymorphic row; a standalone thread has none.
const (
	TargetTypeAsset         = "asset"
	TargetTypeCollection    = "collection"
	TargetTypePrompt        = "prompt"
	TargetTypeKnowledgePage = "knowledge_page"
	TargetTypeStandalone    = "standalone"
)

// AssetStore persists and queries portal assets.
type AssetStore interface {
	Insert(ctx context.Context, asset Asset) error
	Get(ctx context.Context, id string) (*Asset, error)
	GetByIDs(ctx context.Context, ids []string) (map[string]*Asset, error)
	GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*Asset, error)
	List(ctx context.Context, filter AssetFilter) ([]Asset, int, error)
	Update(ctx context.Context, id string, updates AssetUpdate) error
	// AppendProvenanceCapture adds one capture to the end of the asset's
	// provenance, leaving the captures already recorded untouched. Updating an
	// asset's content is new work with its own sources, so each write appends
	// rather than replacing what the previous write recorded (#1320).
	AppendProvenanceCapture(ctx context.Context, id string, capture ProvenanceCapture) error
	SoftDelete(ctx context.Context, id string) error
}

// VersionStore persists and queries asset version history.
type VersionStore interface {
	// CreateVersion atomically assigns the next version number and records
	// the version. It returns the assigned version number. The Version field
	// in the input is ignored — the actual number is determined by locking
	// the asset row and incrementing current_version.
	CreateVersion(ctx context.Context, version AssetVersion) (int, error)
	ListByAsset(ctx context.Context, assetID string, limit, offset int) ([]AssetVersion, int, error)
	GetByVersion(ctx context.Context, assetID string, version int) (*AssetVersion, error)
	GetLatest(ctx context.Context, assetID string) (*AssetVersion, error)
}

// ShareStore persists and queries share links for assets, collections, and prompts.
type ShareStore interface {
	Insert(ctx context.Context, share Share) error
	GetByID(ctx context.Context, id string) (*Share, error)
	GetByToken(ctx context.Context, token string) (*Share, error)
	ListByAsset(ctx context.Context, assetID string) ([]Share, error)
	ListByCollection(ctx context.Context, collectionID string) ([]Share, error)
	ListByPrompt(ctx context.Context, promptID string) ([]Share, error)
	GetUserCollectionPermission(ctx context.Context, collectionID, userID, email string) (SharePermission, error)
	ListSharedWithUser(ctx context.Context, userID, email string, limit, offset int) ([]SharedAsset, int, error)
	ListSharedCollectionsWithUser(ctx context.Context, userID, email string, limit, offset int) ([]SharedCollection, int, error)
	ListSharedPromptsWithUser(ctx context.Context, userID, email string) ([]SharedPromptRef, error)
	// ListSharedWithUserSince returns the active share grants naming this user
	// -- assets, collections, and prompts alike -- created after since, newest
	// first, capped at limit. It answers "what was shared with me lately"
	// without loading each artifact's body, which is what the session-start
	// notice digest needs (#1278). A link share nobody was named on is not a
	// share with this user and never appears.
	ListSharedWithUserSince(ctx context.Context, userID, email string, since time.Time, limit int) ([]SharedTargetRef, error)
	ListActiveShareSummaries(ctx context.Context, assetIDs []string) (map[string]ShareSummary, error)
	ListActiveCollectionShareSummaries(ctx context.Context, collectionIDs []string) (map[string]ShareSummary, error)
	GetUserAssetPermissionViaCollection(ctx context.Context, assetID, userID, email string) (SharePermission, error)
	GetActiveShareForTarget(ctx context.Context, targetType, targetID, userID, email string) (*Share, error)
	Revoke(ctx context.Context, id string) error
	IncrementAccess(ctx context.Context, id string) error
}

// CollectionStore persists and queries portal collections.
type CollectionStore interface {
	Insert(ctx context.Context, c Collection) error
	Get(ctx context.Context, id string) (*Collection, error)
	List(ctx context.Context, filter CollectionFilter) ([]Collection, int, error)
	Update(ctx context.Context, id, name, description string) error
	UpdateConfig(ctx context.Context, id string, config CollectionConfig) error
	UpdateThumbnail(ctx context.Context, id, thumbnailS3Key string) error
	SoftDelete(ctx context.Context, id string) error
	SetSections(ctx context.Context, collectionID string, sections []CollectionSection) error
}
