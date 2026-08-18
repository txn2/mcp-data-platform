// Package portalnoop holds the portal's no-database store implementations.
//
// A deployment with no `database:` block still constructs the portal handler,
// so every store contract needs an implementation that answers without a
// connection: writes succeed silently, lists come back empty, and a lookup by
// id reports not found. They are grouped here rather than beside the
// PostgreSQL stores because they share nothing with them but the interface —
// no query builder, no column list, no scanner — and only each other's
// not-found contract.
package portalnoop

import (
	"context"
	"errors"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// errNotFound is what every no-database lookup by id reports. One sentinel
// across the four stores keeps the degraded path answering the same way,
// which is what lets a handler treat "no database" and "no such row"
// identically instead of matching on per-store error text.
var errNotFound = errors.New("not found: no database configured")

// --- AssetStore ---

type assetStore struct{}

// NewAssetStore returns an AssetStore for use when no database is available.
func NewAssetStore() portaldomain.AssetStore {
	return &assetStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*assetStore) Insert(_ context.Context, _ portaldomain.Asset) error { return nil }

func (*assetStore) Get(_ context.Context, _ string) (*portaldomain.Asset, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

func (*assetStore) GetByIDs(_ context.Context, _ []string) (map[string]*portaldomain.Asset, error) { //nolint:revive // interface impl
	return map[string]*portaldomain.Asset{}, nil
}

func (*assetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*portaldomain.Asset, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

func (*assetStore) List(_ context.Context, _ portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

//nolint:revive // interface implementation method on an unexported type
func (*assetStore) Update(_ context.Context, _ string, _ portaldomain.AssetUpdate) error {
	return nil
}

//nolint:revive // interface implementation method on an unexported type
func (*assetStore) AppendProvenanceCapture(_ context.Context, _ string, _ portaldomain.ProvenanceCapture) error {
	return nil
}

func (*assetStore) SoftDelete(_ context.Context, _ string) error { return nil } //nolint:revive // interface impl

// --- ShareStore ---

type shareStore struct{}

// NewShareStore returns a ShareStore for use when no database is available.
func NewShareStore() portaldomain.ShareStore {
	return &shareStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*shareStore) Insert(_ context.Context, _ portaldomain.Share) error { return nil }

func (*shareStore) GetByID(_ context.Context, _ string) (*portaldomain.Share, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

func (*shareStore) GetByToken(_ context.Context, _ string) (*portaldomain.Share, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

func (*shareStore) ListByAsset(_ context.Context, _ string) ([]portaldomain.Share, error) { //nolint:revive // interface impl
	return nil, nil
}

func (*shareStore) ListByCollection(_ context.Context, _ string) ([]portaldomain.Share, error) { //nolint:revive // interface impl
	return nil, nil
}

func (*shareStore) ListByPrompt(_ context.Context, _ string) ([]portaldomain.Share, error) { //nolint:revive // interface impl
	return nil, nil
}

func (*shareStore) ListSharedPromptsWithUser(_ context.Context, _, _ string) ([]portaldomain.SharedPromptRef, error) { //nolint:revive // interface impl
	return nil, nil
}

func (*shareStore) ListSharedWithUserSince(_ context.Context, _, _ string, _ time.Time, _ int) ([]portaldomain.SharedTargetRef, error) { //nolint:revive // interface impl
	return nil, nil
}

func (*shareStore) GetUserCollectionPermission(_ context.Context, _, _, _ string) (portaldomain.SharePermission, error) { //nolint:revive // interface impl
	return "", errNotFound
}

func (*shareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]portaldomain.SharedAsset, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*shareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]portaldomain.SharedCollection, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*shareStore) ListActiveShareSummaries(_ context.Context, _ []string) (map[string]portaldomain.ShareSummary, error) { //nolint:revive // interface impl
	return map[string]portaldomain.ShareSummary{}, nil
}

func (*shareStore) ListActiveCollectionShareSummaries(_ context.Context, _ []string) (map[string]portaldomain.ShareSummary, error) { //nolint:revive // interface impl
	return map[string]portaldomain.ShareSummary{}, nil
}

func (*shareStore) GetActiveShareForTarget(_ context.Context, _, _, _, _ string) (*portaldomain.Share, error) { //nolint:revive // interface impl
	return nil, nil //nolint:nilnil // noop: no shares
}

func (*shareStore) GetUserAssetPermissionViaCollection(_ context.Context, _, _, _ string) (portaldomain.SharePermission, error) { //nolint:revive // interface impl
	return "", nil
}

func (*shareStore) Revoke(_ context.Context, _ string) error          { return nil } //nolint:revive // interface impl
func (*shareStore) IncrementAccess(_ context.Context, _ string) error { return nil } //nolint:revive // interface impl

// --- VersionStore ---

type versionStore struct{}

// NewVersionStore returns a VersionStore for use when no database is available.
func NewVersionStore() portaldomain.VersionStore {
	return &versionStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*versionStore) CreateVersion(_ context.Context, _ portaldomain.AssetVersion) (int, error) {
	return 0, nil
}

func (*versionStore) ListByAsset(_ context.Context, _ string, _, _ int) ([]portaldomain.AssetVersion, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*versionStore) GetByVersion(_ context.Context, _ string, _ int) (*portaldomain.AssetVersion, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

func (*versionStore) GetLatest(_ context.Context, _ string) (*portaldomain.AssetVersion, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

// --- CollectionStore ---

type collectionStore struct{}

// NewCollectionStore returns a CollectionStore for use when no database is available.
func NewCollectionStore() portaldomain.CollectionStore {
	return &collectionStore{}
}

//nolint:revive // interface implementation methods on unexported type need no doc comments
func (*collectionStore) Insert(_ context.Context, _ portaldomain.Collection) error { return nil }

func (*collectionStore) Get(_ context.Context, _ string) (*portaldomain.Collection, error) { //nolint:revive // interface impl
	return nil, errNotFound
}

func (*collectionStore) List(_ context.Context, _ portaldomain.CollectionFilter) ([]portaldomain.Collection, int, error) { //nolint:revive // interface impl
	return nil, 0, nil
}

func (*collectionStore) Update(_ context.Context, _, _, _ string) error { return nil } //nolint:revive // interface impl
func (*collectionStore) UpdateConfig(_ context.Context, _ string, _ portaldomain.CollectionConfig) error { //nolint:revive // interface impl
	return nil
}

func (*collectionStore) UpdateThumbnail(_ context.Context, _, _ string) error { //nolint:revive // interface impl
	return nil
}
func (*collectionStore) SoftDelete(_ context.Context, _ string) error { return nil } //nolint:revive // interface impl
func (*collectionStore) SetSections(_ context.Context, _ string, _ []portaldomain.CollectionSection) error { //nolint:revive // interface impl
	return nil
}

// Verify interface compliance.
var (
	_ portaldomain.AssetStore      = (*assetStore)(nil)
	_ portaldomain.ShareStore      = (*shareStore)(nil)
	_ portaldomain.VersionStore    = (*versionStore)(nil)
	_ portaldomain.CollectionStore = (*collectionStore)(nil)
)
