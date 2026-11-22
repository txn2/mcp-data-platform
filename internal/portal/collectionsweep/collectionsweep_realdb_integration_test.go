//go:build integration

package collectionsweep

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/portal/threadreach"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// realWorld is the stores, checker and reach the platform assembles, over a
// migrated database.
type realWorld struct {
	db          *sql.DB
	assets      portaldomain.AssetStore
	collections portaldomain.CollectionStore
	shares      portaldomain.ShareStore
	sweep       *Sweeper
}

func newRealWorld(t *testing.T) realWorld {
	t.Helper()
	db := testdb.New(t)
	w := realWorld{
		db:          db,
		assets:      portalstore.NewPostgresAssetStore(db, nil),
		collections: portalstore.NewPostgresCollectionStore(db, nil),
		shares:      portalstore.NewPostgresShareStore(db),
	}
	checker := access.New(access.Config{Assets: w.assets, Collections: w.collections, Shares: w.shares, AdminRoles: []string{"admin"}})
	people := user.NewPostgresStore(db)
	for _, who := range []string{"alice", "bob", "carol", "dave", "zed"} {
		require.NoError(t, people.Observe(context.Background(), who+"@example.com", who, "", []string{"analyst"}))
	}
	require.NoError(t, people.Observe(context.Background(), "root@example.com", "root", "", []string{"admin"}))
	reach := threadreach.New(threadreach.Config{Access: checker, Assets: w.assets, Collections: w.collections, Directory: people})
	grants, ok := w.shares.(Grants)
	require.True(t, ok, "the share store lists the collections granting an asset")
	w.sweep = New(db, reach, reach.Access(), grants)
	require.NotNil(t, w.sweep)
	return w
}

func (w realWorld) asset(ctx context.Context, t *testing.T, id, owner string) {
	t.Helper()
	require.NoError(t, w.assets.Insert(ctx, portaldomain.Asset{
		ID: id, OwnerID: owner, OwnerEmail: owner + "@example.com", Name: "asset " + id, ContentType: "text/markdown",
		S3Bucket: "portal-assets", S3Key: "k/" + id + "/v1/content.md", SizeBytes: 1, Tags: []string{}, CurrentVersion: 1,
	}))
}

// collection stores a collection whose one section holds assetIDs in order,
// written straight to the store, as a deployment before the add rule holds it.
func (w realWorld) collection(ctx context.Context, t *testing.T, id, owner string, assetIDs ...string) {
	t.Helper()
	require.NoError(t, w.collections.Insert(ctx, portaldomain.Collection{ID: id, OwnerID: owner, OwnerEmail: owner + "@example.com", Name: id}))
	items := make([]portaldomain.CollectionItem, len(assetIDs))
	for i, a := range assetIDs {
		items[i] = portaldomain.CollectionItem{ID: id + "-" + a, AssetID: a}
	}
	require.NoError(t, w.collections.SetSections(ctx, id, []portaldomain.CollectionSection{{ID: id + "-s", Title: "S", Items: items}}))
}

func (w realWorld) share(ctx context.Context, t *testing.T, id string, s portaldomain.Share) {
	t.Helper()
	s.ID, s.Token, s.CreatedBy, s.Permission = id, "tok-"+id, "x@example.com", portaldomain.PermissionViewer
	require.NoError(t, w.shares.Insert(ctx, &s))
}

func (w realWorld) items(ctx context.Context, t *testing.T, collectionID string) []string {
	t.Helper()
	got, err := w.collections.Get(ctx, collectionID)
	require.NoError(t, err)
	out := []string{}
	for _, s := range got.Sections {
		for _, it := range s.Items {
			out = append(out, it.AssetID)
		}
	}
	return out
}

// TestRealDB_SweepRemovesWhatTheOwnerCannotOpen is #2752 through the real
// stores and checker: an item its owner owns or was shared stays in place and
// order; one they cannot open is removed and recorded; one reached only
// through a chain of collection shares from someone who can open it stays;
// two collections that each hold an asset only because the other's share
// opens it lose it; a second pass removes nothing.
func TestRealDB_SweepRemovesWhatTheOwnerCannotOpen(t *testing.T) {
	ctx := context.Background()
	w := newRealWorld(t)
	for id, owner := range map[string]string{"mine": "alice", "shared": "zed", "private": "zed", "withdrawn": "zed", "chained": "zed", "circled": "zed"} {
		w.asset(ctx, t, id, owner)
	}
	// alice's collection: her own asset, one shared with her, one she cannot
	// open, one whose share was withdrawn, and one reached through bob.
	w.collection(ctx, t, "c-alice", "alice", "mine", "private", "shared", "withdrawn", "chained")
	w.share(ctx, t, "s-shared", portaldomain.Share{AssetID: "shared", SharedWithUserID: "alice"})
	w.share(ctx, t, "s-withdrawn", portaldomain.Share{AssetID: "withdrawn", SharedWithUserID: "alice"})
	require.NoError(t, w.shares.Revoke(ctx, "s-withdrawn"))
	// bob was shared "chained" and put it in his collection, shared with alice.
	w.collection(ctx, t, "c-bob", "bob", "chained")
	w.share(ctx, t, "s-chained", portaldomain.Share{AssetID: "chained", SharedWithUserID: "bob"})
	w.share(ctx, t, "s-c-bob", portaldomain.Share{CollectionID: "c-bob", SharedWithUserID: "alice"})
	// carol and dave each hold "circled", each opening it to the other.
	w.collection(ctx, t, "c-carol", "carol", "circled")
	w.collection(ctx, t, "c-dave", "dave", "circled")
	w.share(ctx, t, "s-c-carol", portaldomain.Share{CollectionID: "c-carol", SharedWithUserID: "dave"})
	w.share(ctx, t, "s-c-dave", portaldomain.Share{CollectionID: "c-dave", SharedWithUserID: "carol"})

	// An administrator's collection may hold anything; an owner the platform
	// holds no roles for is judged with none.
	w.collection(ctx, t, "c-root", "root", "private")
	w.collection(ctx, t, "c-stranger", "stranger", "private")

	removed, err := w.sweep.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), removed, "private and withdrawn from alice, circled from carol and dave, private from the stranger")
	assert.Equal(t, []string{"private"}, w.items(ctx, t, "c-root"))
	assert.Empty(t, w.items(ctx, t, "c-stranger"))
	assert.Equal(t, []string{"mine", "shared", "chained"}, w.items(ctx, t, "c-alice"), "what the owner can open stays, in order")
	assert.Equal(t, []string{"chained"}, w.items(ctx, t, "c-bob"))
	assert.Empty(t, w.items(ctx, t, "c-carol"))
	assert.Empty(t, w.items(ctx, t, "c-dave"))

	rows, err := w.db.QueryContext(ctx, `SELECT collection_id, asset_id, items FROM portal_collection_item_removals ORDER BY collection_id, asset_id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var c, a string
		var n int
		require.NoError(t, rows.Scan(&c, &a, &n))
		assert.Equal(t, 1, n)
		got = append(got, c+"/"+a)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"c-alice/private", "c-alice/withdrawn", "c-carol/circled", "c-dave/circled", "c-stranger/private"}, got)

	for _, id := range []string{"private", "withdrawn", "circled"} {
		a, err := w.assets.Get(ctx, id)
		require.NoError(t, err, "the asset itself is untouched")
		assert.Nil(t, a.DeletedAt)
	}

	again, err := w.sweep.Run(ctx)
	require.NoError(t, err)
	assert.Zero(t, again, "a second pass removes nothing")
}
