//go:build integration

package portalstore_test

// Real-Postgres proof that a person's reach (#2500) is the access rule: for
// every way an asset can and cannot be opened, the set a listing, a ranked
// search and a facet count cover is exactly the set access.CanViewAsset grants,
// narrowed by scope. The query that ranks and counts is the authorization
// boundary, so it is held to the per-row check rather than to a second
// hand-written list of expectations.

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const (
	reachUserID  = "user-a"
	reachEmail   = "a@example.com"
	reachPersona = "analyst"
	otherID      = "user-b"
	otherEmail   = "b@example.com"
)

type reachFixture struct {
	db          *sql.DB
	assets      portaldomain.AssetStore
	collections portaldomain.CollectionStore
	shares      portaldomain.ShareStore
}

func newReachFixture(t *testing.T) *reachFixture {
	t.Helper()
	db := testdb.New(t)
	return &reachFixture{
		db:          db,
		assets:      portalstore.NewPostgresAssetStore(db, nil),
		collections: portalstore.NewPostgresCollectionStore(db, nil),
		shares:      portalstore.NewPostgresShareStore(db),
	}
}

func (f *reachFixture) asset(t *testing.T, id, ownerID, ownerEmail string, tags ...string) {
	t.Helper()
	if tags == nil {
		tags = []string{}
	}
	require.NoError(t, f.assets.Insert(context.Background(), portaldomain.Asset{
		ID: id, OwnerID: ownerID, OwnerEmail: ownerEmail, Name: "asset " + id,
		Description: "quarterly revenue for " + id, ContentType: "text/html",
		S3Bucket: "portal-assets", S3Key: id, Tags: tags, CurrentVersion: 1,
	}))
}

func (f *reachFixture) share(t *testing.T, s portaldomain.Share) {
	t.Helper()
	s.Token = "tok-" + s.ID
	s.CreatedBy = otherEmail
	require.NoError(t, f.shares.Insert(context.Background(), &s))
}

// collectionHolding makes a collection of other's holding assetID, and returns
// its id.
func (f *reachFixture) collectionHolding(t *testing.T, id, assetID string) string {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.collections.Insert(ctx, portaldomain.Collection{
		ID: id, OwnerID: otherID, OwnerEmail: otherEmail, Name: "collection " + id,
	}))
	_, err := f.db.ExecContext(ctx, `INSERT INTO portal_collection_sections (id, collection_id) VALUES ($1, $2)`, id+"-s", id)
	require.NoError(t, err)
	_, err = f.db.ExecContext(ctx, `INSERT INTO portal_collection_items (id, section_id, asset_id) VALUES ($1, $2, $3)`, id+"-i", id+"-s", assetID)
	require.NoError(t, err)
	return id
}

// seed lays out one asset per way of being, or not being, reachable.
func (f *reachFixture) seed(t *testing.T) {
	t.Helper()
	past := time.Now().Add(-time.Hour)

	f.asset(t, "own_by_id", reachUserID, "", "mine")
	f.asset(t, "own_by_email", "script:daily", reachEmail, "mine")
	f.asset(t, "direct_user", otherID, otherEmail, "finance")
	f.share(t, portaldomain.Share{ID: "s1", AssetID: "direct_user", SharedWithUserID: reachUserID, Permission: portaldomain.PermissionEditor})
	f.asset(t, "direct_email", otherID, otherEmail, "finance")
	f.share(t, portaldomain.Share{ID: "s2", AssetID: "direct_email", SharedWithEmail: "A@Example.com"})
	f.asset(t, "persona", otherID, otherEmail, "finance")
	f.share(t, portaldomain.Share{ID: "s3", AssetID: "persona", SharedWithPersona: reachPersona})
	f.asset(t, "via_collection", otherID, otherEmail, "finance")
	f.share(t, portaldomain.Share{ID: "s4", CollectionID: f.collectionHolding(t, "c_shared", "via_collection"), SharedWithEmail: reachEmail})

	// Unreachable, each for its own reason. Their tag and owner exist on no
	// reachable asset, so a facet leaking either is visible.
	const hiddenOwner = "hidden-owner@example.com"
	f.asset(t, "other_persona", "user-c", hiddenOwner, "secret")
	f.share(t, portaldomain.Share{ID: "s5", AssetID: "other_persona", SharedWithPersona: "finance-team"})
	f.asset(t, "revoked", "user-c", hiddenOwner, "secret")
	f.share(t, portaldomain.Share{ID: "s6", AssetID: "revoked", SharedWithUserID: reachUserID})
	require.NoError(t, f.shares.Revoke(context.Background(), "s6"))
	f.asset(t, "expired", "user-c", hiddenOwner, "secret")
	f.share(t, portaldomain.Share{ID: "s7", AssetID: "expired", SharedWithUserID: reachUserID, ExpiresAt: &past})
	f.asset(t, "public_link", "user-c", hiddenOwner, "secret")
	f.share(t, portaldomain.Share{ID: "s8", AssetID: "public_link"})
	f.asset(t, "collection_to_other", "user-c", hiddenOwner, "secret")
	f.share(t, portaldomain.Share{ID: "s9", CollectionID: f.collectionHolding(t, "c_other", "collection_to_other"), SharedWithEmail: "c@example.com"})
	f.asset(t, "unshared", "user-c", hiddenOwner, "secret")
	// Shared through a collection that was then deleted: deleting it leaves
	// the share row in place, and it must grant nothing.
	f.asset(t, "deleted_collection", "user-c", hiddenOwner, "secret")
	f.share(t, portaldomain.Share{ID: "s10", CollectionID: f.collectionHolding(t, "c_deleted", "deleted_collection"), SharedWithUserID: reachUserID})
	require.NoError(t, f.collections.SoftDelete(context.Background(), "c_deleted"))
}

var (
	reachOwned  = []string{"own_by_email", "own_by_id"}
	reachShared = []string{"direct_email", "direct_user", "persona", "via_collection"}
	reachAll    = []string{
		"direct_email", "direct_user", "own_by_email", "own_by_id", "persona", "via_collection",
	}
	reachHidden = []string{"collection_to_other", "deleted_collection", "expired", "other_persona", "public_link", "revoked", "unshared"}
)

func reachOf(scope portaldomain.Scope) *portaldomain.Reach {
	return portaldomain.NewReach(reachUserID, reachEmail, reachPersona, scope)
}

func listIDs(t *testing.T, f *reachFixture, filter portaldomain.AssetFilter) ([]string, int) {
	t.Helper()
	filter.Limit = portaldomain.MaxLimit
	assets, total, err := f.assets.List(context.Background(), filter)
	require.NoError(t, err)
	ids := make([]string, 0, len(assets))
	for _, a := range assets {
		ids = append(ids, a.ID)
	}
	slices.Sort(ids)
	return ids, total
}

func TestReach_RealDB_ListingIsTheAccessRule(t *testing.T) {
	f := newReachFixture(t)
	f.seed(t)

	for scope, want := range map[portaldomain.Scope][]string{
		portaldomain.ScopeMine: reachOwned, portaldomain.ScopeShared: reachShared, portaldomain.ScopeAll: reachAll,
	} {
		got, total := listIDs(t, f, portaldomain.AssetFilter{Reach: reachOf(scope)})
		assert.Equal(t, want, got, "scope %s", scope)
		assert.Equal(t, len(want), total, "scope %s total counts only the reach", scope)
	}

	// The per-row check, asked of every asset, grants exactly the All scope.
	ctx := portaldomain.WithCallerPersona(context.Background(), reachPersona)
	checker := access.New(access.Config{Assets: f.assets, Shares: f.shares})
	user := &access.User{UserID: reachUserID, Email: reachEmail}
	var granted []string
	for _, id := range append(slices.Clone(reachAll), reachHidden...) {
		a, err := f.assets.Get(ctx, id)
		require.NoError(t, err)
		if checker.CanViewAsset(ctx, id, a, user) {
			granted = append(granted, id)
		}
	}
	slices.Sort(granted)
	assert.Equal(t, reachAll, granted, "the reach and access.CanViewAsset agree on every asset")
}

func TestReach_RealDB_SearchAndFacetsStayInReach(t *testing.T) {
	f := newReachFixture(t)
	f.seed(t)
	ctx := context.Background()
	searcher := f.assets.(portaldomain.ReachSearcher)

	scored, total, err := searcher.SearchReach(ctx, portaldomain.RankedAssetQuery{
		QueryText: "quarterly revenue", Reach: reachOf(portaldomain.ScopeAll), Limit: 50,
	})
	require.NoError(t, err)
	ids := make([]string, 0, len(scored))
	for _, s := range scored {
		ids = append(ids, s.Asset.ID)
	}
	slices.Sort(ids)
	assert.Equal(t, reachAll, ids, "every reachable asset matches the description, and nothing else is ranked")
	assert.Equal(t, len(reachAll), total)

	// Paging walks the same ranked set.
	first, _, err := searcher.SearchReach(ctx, portaldomain.RankedAssetQuery{
		QueryText: "quarterly revenue", Reach: reachOf(portaldomain.ScopeAll), Limit: 4,
	})
	require.NoError(t, err)
	second, _, err := searcher.SearchReach(ctx, portaldomain.RankedAssetQuery{
		QueryText: "quarterly revenue", Reach: reachOf(portaldomain.ScopeAll), Limit: 4, Offset: 4,
	})
	require.NoError(t, err)
	assert.Len(t, first, 4)
	assert.Len(t, second, len(reachAll)-4)

	shared, _, err := searcher.SearchReach(ctx, portaldomain.RankedAssetQuery{
		QueryText: "revenue", Reach: reachOf(portaldomain.ScopeShared), Owners: []string{"B@example.com"}, Tags: []string{"finance"}, Limit: 50,
	})
	require.NoError(t, err)
	assert.Len(t, shared, len(reachShared), "filters narrow the shared scope")

	facets, err := f.assets.(portaldomain.AssetFaceter).AssetFacets(ctx, portaldomain.AssetFilter{Reach: reachOf(portaldomain.ScopeAll)})
	require.NoError(t, err)
	assert.Equal(t, []portaldomain.FacetCount{{Value: "finance", Count: 4}, {Value: "mine", Count: 2}}, facets.Tags,
		"a tag only an unreachable asset carries is not a facet value")
	authors := map[string]int{}
	for _, a := range facets.Authors {
		authors[a.Value] = a.Count
	}
	assert.Equal(t, map[string]int{otherEmail: 4, reachEmail: 1}, authors,
		"the owner of only unreachable assets is not an author value")
}

func TestReach_RealDB_PermissionsMatchTheCheck(t *testing.T) {
	f := newReachFixture(t)
	f.seed(t)
	ctx := portaldomain.WithCallerPersona(context.Background(), reachPersona)
	perms, err := f.assets.(portaldomain.ReachPermissions).ReachPermissions(ctx,
		append(slices.Clone(reachAll), reachHidden...), reachOf(portaldomain.ScopeAll))
	require.NoError(t, err)

	checker := access.New(access.Config{Assets: f.assets, Shares: f.shares})
	user := &access.User{UserID: reachUserID, Email: reachEmail}
	for _, id := range append(slices.Clone(reachShared), reachHidden...) {
		want, err := checker.ResolveAssetPermission(ctx, id, user)
		require.NoError(t, err)
		assert.Equal(t, want, perms[id], "permission on %s", id)
	}
}

func TestReach_RealDB_CollectionsInReach(t *testing.T) {
	f := newReachFixture(t)
	f.seed(t)
	ctx := context.Background()
	require.NoError(t, f.collections.Insert(ctx, portaldomain.Collection{
		ID: "c_mine", OwnerID: reachUserID, OwnerEmail: reachEmail, Name: "my quarterly revenue",
	}))

	for scope, want := range map[portaldomain.Scope][]string{
		portaldomain.ScopeMine:   {"c_mine"},
		portaldomain.ScopeShared: {"c_shared"},
		portaldomain.ScopeAll:    {"c_mine", "c_shared"},
	} {
		colls, total, err := f.collections.List(ctx, portaldomain.CollectionFilter{Reach: reachOf(scope), Limit: 50})
		require.NoError(t, err)
		ids := make([]string, 0, len(colls))
		for _, c := range colls {
			ids = append(ids, c.ID)
		}
		slices.Sort(ids)
		assert.Equal(t, want, ids, "scope %s", scope)
		assert.Equal(t, len(want), total)
	}

	scored, _, err := f.collections.(portaldomain.CollectionReachSearcher).SearchCollectionReach(ctx,
		portaldomain.RankedCollectionQuery{QueryText: "collection", Reach: reachOf(portaldomain.ScopeAll), Limit: 50})
	require.NoError(t, err)
	ids := make([]string, 0, len(scored))
	for _, s := range scored {
		ids = append(ids, s.Collection.ID)
	}
	assert.Equal(t, []string{"c_shared"}, ids, "the shared collection matches by name; c_other is not in reach")
}
