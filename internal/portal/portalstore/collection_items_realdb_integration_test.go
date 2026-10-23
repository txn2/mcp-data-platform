//go:build integration

package portalstore

// Real-Postgres tests for adding one asset to a collection (#2260). The append
// is the add-to-collection picker's only write, and what it must leave alone
// (the other sections, the order of the items already there, the collection's
// name and config) is only visible in the stored rows.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestCollectionAppendItem_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	assets := &postgresAssetStore{db: db}
	store, ok := NewPostgresCollectionStore(db, nil).(*postgresCollectionStore)
	require.True(t, ok)

	for _, id := range []string{"a1", "a2", "a3", "a4"} {
		require.NoError(t, assets.Insert(ctx, newStampAsset(id, "asset "+id)))
	}
	coll := portaldomain.Collection{ID: "coll-append", OwnerID: stampOwner, OwnerEmail: "u@example.com",
		Name: "Board pack", Description: "kept", Config: portaldomain.CollectionConfig{ThumbnailSize: "small"}}
	require.NoError(t, store.Insert(ctx, coll))
	require.NoError(t, store.SetSections(ctx, coll.ID, []portaldomain.CollectionSection{
		{ID: "s-first", Title: "First", Items: []portaldomain.CollectionItem{{ID: "i1", AssetID: "a1"}, {ID: "i2", AssetID: "a2"}}},
		{ID: "s-last", Title: "Last", Items: []portaldomain.CollectionItem{{ID: "i3", AssetID: "a1"}}},
	}))

	t.Run("named section: appended at the end, nothing else moves", func(t *testing.T) {
		added, err := store.AppendItem(ctx, coll.ID, "s-first", "a3")
		require.NoError(t, err)
		assert.Equal(t, "s-first", added.SectionID)
		assert.Equal(t, "First", added.SectionTitle)
		assert.Equal(t, 2, added.Item.Position)

		got, err := store.Get(ctx, coll.ID)
		require.NoError(t, err)
		assert.Equal(t, "Board pack", got.Name)
		assert.Equal(t, "kept", got.Description)
		assert.Equal(t, "small", got.Config.ThumbnailSize)
		require.Len(t, got.Sections, 2)
		assert.Equal(t, []string{"a1", "a2", "a3"}, itemAssets(got.Sections[0]))
		assert.Equal(t, []string{"a1"}, itemAssets(got.Sections[1]))
	})

	t.Run("no section named: the last section", func(t *testing.T) {
		added, err := store.AppendItem(ctx, coll.ID, "", "a4")
		require.NoError(t, err)
		assert.Equal(t, "s-last", added.SectionID)
	})

	t.Run("twice in one section is refused", func(t *testing.T) {
		_, err := store.AppendItem(ctx, coll.ID, "s-first", "a2")
		assert.ErrorIs(t, err, portaldomain.ErrItemAlreadyInSection)
	})

	t.Run("a section of another collection is not found", func(t *testing.T) {
		_, err := store.AppendItem(ctx, coll.ID, "no-such-section", "a2")
		assert.ErrorIs(t, err, portaldomain.ErrSectionNotFound)
	})

	t.Run("a deleted collection is not found", func(t *testing.T) {
		gone := portaldomain.Collection{ID: "coll-gone", OwnerID: stampOwner, Name: "Gone"}
		require.NoError(t, store.Insert(ctx, gone))
		require.NoError(t, store.SoftDelete(ctx, gone.ID))
		_, err := store.AppendItem(ctx, gone.ID, "", "a1")
		assert.Error(t, err)
	})

	t.Run("a collection with no sections gets an untitled first one", func(t *testing.T) {
		empty := portaldomain.Collection{ID: "coll-empty", OwnerID: stampOwner, Name: "Empty"}
		require.NoError(t, store.Insert(ctx, empty))
		added, err := store.AppendItem(ctx, empty.ID, "", "a1")
		require.NoError(t, err)
		assert.Empty(t, added.SectionTitle)
		got, err := store.Get(ctx, empty.ID)
		require.NoError(t, err)
		require.Len(t, got.Sections, 1)
		assert.Equal(t, []string{"a1"}, itemAssets(got.Sections[0]))
	})

	t.Run("a full section is refused", func(t *testing.T) {
		full := portaldomain.Collection{ID: "coll-full", OwnerID: stampOwner, Name: "Full"}
		require.NoError(t, store.Insert(ctx, full))
		items := make([]portaldomain.CollectionItem, portaldomain.MaxItemsPerSection)
		for i := range items {
			items[i] = portaldomain.CollectionItem{ID: fmt.Sprintf("full-%d", i), AssetID: "a1"}
		}
		require.NoError(t, store.SetSections(ctx, full.ID, []portaldomain.CollectionSection{{ID: "s-full", Items: items}}))
		_, err := store.AppendItem(ctx, full.ID, "s-full", "a2")
		assert.ErrorIs(t, err, portaldomain.ErrSectionFull)
	})

	t.Run("section summaries mark where the asset already is", func(t *testing.T) {
		sums, err := store.SectionSummaries(ctx, []string{coll.ID, "coll-empty", "coll-unknown"}, "a3")
		require.NoError(t, err)
		require.Len(t, sums[coll.ID], 2)
		assert.Equal(t, portaldomain.SectionSummary{ID: "s-first", Title: "First", ItemCount: 3, HasAsset: true}, sums[coll.ID][0])
		assert.Equal(t, portaldomain.SectionSummary{ID: "s-last", Title: "Last", ItemCount: 2, HasAsset: false}, sums[coll.ID][1])
		assert.Len(t, sums["coll-empty"], 1)
		assert.NotContains(t, sums, "coll-unknown")

		none, err := store.SectionSummaries(ctx, nil, "a3")
		require.NoError(t, err)
		assert.Empty(t, none)
	})
}

func itemAssets(s portaldomain.CollectionSection) []string {
	out := make([]string, len(s.Items))
	for i, it := range s.Items {
		out[i] = it.AssetID
	}
	return out
}
