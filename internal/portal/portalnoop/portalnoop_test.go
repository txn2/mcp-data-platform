package portalnoop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func TestNoopAssetStore(t *testing.T) {
	store := NewAssetStore()
	ctx := context.Background()

	assert.NoError(t, store.Insert(ctx, portaldomain.Asset{}))

	_, err := store.Get(ctx, "any")
	assert.Error(t, err)

	assets, total, err := store.List(ctx, portaldomain.AssetFilter{})
	assert.NoError(t, err)
	assert.Nil(t, assets)
	assert.Equal(t, 0, total)

	assert.NoError(t, store.Update(ctx, "any", portaldomain.AssetUpdate{}))
	assert.NoError(t, store.SoftDelete(ctx, "any"))
}

func TestNoopShareStore(t *testing.T) {
	store := NewShareStore()
	ctx := context.Background()

	assert.NoError(t, store.Insert(ctx, portaldomain.Share{}))

	_, err := store.GetByID(ctx, "any")
	assert.Error(t, err)

	_, err = store.GetByToken(ctx, "any")
	assert.Error(t, err)

	shares, err := store.ListByAsset(ctx, "any")
	assert.NoError(t, err)
	assert.Nil(t, shares)

	shared, total, err := store.ListSharedWithUser(ctx, "any", "", 10, 0)
	assert.NoError(t, err)
	assert.Nil(t, shared)
	assert.Equal(t, 0, total)

	assert.NoError(t, store.Revoke(ctx, "any"))
	assert.NoError(t, store.IncrementAccess(ctx, "any"))

	summaries, err := store.ListActiveShareSummaries(ctx, []string{"a1"})
	assert.NoError(t, err)
	assert.Empty(t, summaries)
}

// TestNoopStoresAnswerEveryContractMethod walks the remaining reads. Each is a
// door a handler takes with no database configured: an empty answer must come
// back without an error (so the surface degrades to "nothing here"), and a
// lookup by id must report not found through the one shared sentinel.
func TestNoopStoresAnswerEveryContractMethod(t *testing.T) {
	ctx := context.Background()
	assets, shares := NewAssetStore(), NewShareStore()

	byIDs, err := assets.GetByIDs(ctx, []string{"a1"})
	require.NoError(t, err)
	assert.Empty(t, byIDs)

	_, err = assets.GetByIdempotencyKey(ctx, "u1", "key")
	require.ErrorIs(t, err, errNotFound)

	byColl, err := shares.ListByCollection(ctx, "c1")
	require.NoError(t, err)
	assert.Nil(t, byColl)

	byPrompt, err := shares.ListByPrompt(ctx, "p1")
	require.NoError(t, err)
	assert.Nil(t, byPrompt)

	promptRefs, err := shares.ListSharedPromptsWithUser(ctx, "u1", "u1@example.com")
	require.NoError(t, err)
	assert.Nil(t, promptRefs)

	_, err = shares.GetUserCollectionPermission(ctx, "c1", "u1", "u1@example.com")
	require.ErrorIs(t, err, errNotFound)

	sharedColls, total, err := shares.ListSharedCollectionsWithUser(ctx, "u1", "u1@example.com", 10, 0)
	require.NoError(t, err)
	assert.Nil(t, sharedColls)
	assert.Equal(t, 0, total)

	collSummaries, err := shares.ListActiveCollectionShareSummaries(ctx, []string{"c1"})
	require.NoError(t, err)
	assert.Empty(t, collSummaries)

	// No database means no collection grant, and that is a definite answer
	// rather than a failure: the caller must read it as "no access", not 500.
	perm, err := shares.GetUserAssetPermissionViaCollection(ctx, "a1", "u1", "u1@example.com")
	require.NoError(t, err)
	assert.Empty(t, perm)
}

func TestNoopVersionStore(t *testing.T) {
	store := NewVersionStore()
	ctx := context.Background()

	v, err := store.CreateVersion(ctx, portaldomain.AssetVersion{})
	assert.NoError(t, err)
	assert.Equal(t, 0, v)

	versions, total, err := store.ListByAsset(ctx, "any", 10, 0)
	assert.NoError(t, err)
	assert.Nil(t, versions)
	assert.Equal(t, 0, total)

	_, err = store.GetByVersion(ctx, "any", 1)
	assert.Error(t, err)

	_, err = store.GetLatest(ctx, "any")
	assert.Error(t, err)
}

func TestNoopCollectionStore(t *testing.T) {
	store := NewCollectionStore()

	ctx := context.Background()

	err := store.Insert(ctx, portaldomain.Collection{})
	assert.NoError(t, err)

	_, err = store.Get(ctx, "any")
	require.ErrorIs(t, err, errNotFound)

	collections, total, err := store.List(ctx, portaldomain.CollectionFilter{})
	assert.NoError(t, err)
	assert.Nil(t, collections)
	assert.Equal(t, 0, total)

	err = store.Update(ctx, "any", "name", "desc")
	assert.NoError(t, err)

	err = store.UpdateConfig(ctx, "any", portaldomain.CollectionConfig{})
	assert.NoError(t, err)

	err = store.UpdateThumbnail(ctx, "any", "key")
	assert.NoError(t, err)

	err = store.SoftDelete(ctx, "any")
	assert.NoError(t, err)

	err = store.SetSections(ctx, "any", nil)
	assert.NoError(t, err)
}
