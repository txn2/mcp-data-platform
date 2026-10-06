package producedview

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// openSet opens the items whose id it holds.
type openSet map[string]bool

func (o openSet) CanOpen(_ context.Context, it Item) bool { return o[it.TargetID] }

// TestProducedForNamesWhatTheReaderOpensAndCountsTheRest is the Used-by rule
// applied to a script's outputs (#2027): the files the reader can open are
// listed, the others are counted, and a file since deleted is neither.
func TestProducedForNamesWhatTheReaderOpensAndCountsTheRest(t *testing.T) {
	r := New(&fakeProducers{rows: []producedby.Row{assetRow("shared"), assetRow("private"), assetRow("gone"), resourceRow("res-1")}},
		&fakeAssets{found: map[string]*portaldomain.Asset{
			"shared":  {ID: "shared", Name: "Shared Report"},
			"private": {ID: "private", Name: "Private Report"},
		}},
		&fakeResources{res: &resource.Resource{ID: "res-1", DisplayName: "Extract"}}, nil, nil)

	got, err := r.ProducedFor(context.Background(), "script-1", 50, openSet{"shared": true, "res-1": true})
	require.NoError(t, err)
	require.Len(t, got.Open, 2)
	assert.Equal(t, "Shared Report", got.Open[0].Name)
	assert.Equal(t, "Extract", got.Open[1].Name)
	assert.Equal(t, 1, got.Hidden, "the private report is counted, the deleted one is not")
	assert.False(t, got.More)

	got, err = r.ProducedFor(context.Background(), "script-1", 50, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, got.Hidden, "with no rule to open by, nothing is named")
}

// TestProducedForSaysWhenTheScriptWroteMore keeps the count honest: past the
// limit, the files neither listed nor counted are reported as existing.
func TestProducedForSaysWhenTheScriptWroteMore(t *testing.T) {
	producers := &fakeProducers{rows: []producedby.Row{resourceRow("a"), resourceRow("b"), resourceRow("c")}}
	r := New(producers, nil, &fakeResources{res: &resource.Resource{ID: "x", DisplayName: "X"}}, nil, nil)

	got, err := r.ProducedFor(context.Background(), "script-1", 2, openSet{"a": true, "b": true, "c": true})
	require.NoError(t, err)
	assert.Equal(t, 3, producers.limit, "one more than the limit is read, to learn whether there are more")
	assert.Len(t, got.Open, 2)
	assert.True(t, got.More)
}

func TestProducedForPropagatesTheListingFailure(t *testing.T) {
	r := New(&fakeProducers{err: errors.New("down")}, nil, nil, nil, nil)
	_, err := r.ProducedFor(context.Background(), "script-1", 50, openSet{})
	require.ErrorContains(t, err, "for a reader")
}

// fakeAssetGetter resolves one asset by id.
type fakeAssetGetter struct {
	byID map[string]*portaldomain.Asset
	err  error
}

func (f fakeAssetGetter) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if f.err != nil {
		return nil, f.err
	}
	a, ok := f.byID[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return a, nil
}

// fakeShares answers the share-graph reads CanViewAsset and CanViewCollection
// make; every other ShareStore method is unused here.
type fakeShares struct {
	portaldomain.ShareStore
	byAsset      map[string][]portaldomain.Share
	byCollection map[string]portaldomain.SharePermission
}

func (f fakeShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	return f.byAsset[id], nil
}

func (fakeShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

func (f fakeShares) GetUserCollectionPermission(_ context.Context, id, _, _ string) (portaldomain.SharePermission, error) {
	return f.byCollection[id], nil
}

type fakeCollectionGetter map[string]*portaldomain.Collection

func (f fakeCollectionGetter) Get(_ context.Context, id string) (*portaldomain.Collection, error) {
	c, ok := f[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return c, nil
}

func item(kind, id string) Item { return Item{TargetKind: kind, TargetID: id} }

// TestAccessOpensByTheFilesOwnRules proves each kind is judged by the rule its
// own surface applies: an asset by ownership, administration and the share
// graph; a collection likewise; a resource by its scope.
func TestAccessOpensByTheFilesOwnRules(t *testing.T) {
	removed := time.Now()
	assets := fakeAssetGetter{byID: map[string]*portaldomain.Asset{
		"mine":    {ID: "mine", OwnerID: "u-bob", OwnerEmail: "bob@example.com"},
		"shared":  {ID: "shared", OwnerID: "u-jane", OwnerEmail: "jane@example.com"},
		"private": {ID: "private", OwnerID: "u-jane", OwnerEmail: "jane@example.com"},
		"deleted": {ID: "deleted", OwnerID: "u-bob", OwnerEmail: "bob@example.com", DeletedAt: &removed},
	}}
	collections := fakeCollectionGetter{
		"own-col":     {ID: "own-col", OwnerID: "u-bob"},
		"shared-col":  {ID: "shared-col", OwnerID: "u-jane"},
		"private-col": {ID: "private-col", OwnerID: "u-jane"},
		"gone-col":    {ID: "gone-col", OwnerID: "u-bob", DeletedAt: &removed},
	}
	shares := fakeShares{
		byAsset:      map[string][]portaldomain.Share{"shared": {{SharedWithEmail: "bob@example.com", Permission: portaldomain.PermissionViewer}}},
		byCollection: map[string]portaldomain.SharePermission{"shared-col": portaldomain.PermissionViewer},
	}
	resources := &fakeResources{res: &resource.Resource{ID: "res-1", Scope: resource.ScopeGlobal}}
	a := NewAccess(assets, collections, shares, resources)
	bob := a.For(Viewer{UserID: "u-bob", Email: "bob@example.com", Claims: resource.Claims{Sub: "u-bob", Email: "bob@example.com"}})
	ctx := context.Background()

	assert.True(t, bob.CanOpen(ctx, item(TargetAsset, "mine")))
	assert.True(t, bob.CanOpen(ctx, item(TargetAsset, "shared")))
	assert.False(t, bob.CanOpen(ctx, item(TargetAsset, "private")))
	assert.False(t, bob.CanOpen(ctx, item(TargetAsset, "deleted")))
	assert.False(t, bob.CanOpen(ctx, item(TargetAsset, "missing")))
	assert.True(t, bob.CanOpen(ctx, item(TargetCollection, "own-col")))
	assert.True(t, bob.CanOpen(ctx, item(TargetCollection, "shared-col")))
	assert.False(t, bob.CanOpen(ctx, item(TargetCollection, "private-col")))
	assert.False(t, bob.CanOpen(ctx, item(TargetCollection, "gone-col")))
	assert.False(t, bob.CanOpen(ctx, item(TargetCollection, "missing")))
	assert.True(t, bob.CanOpen(ctx, item(TargetResource, "res-1")), "a global resource opens for everyone")
	assert.False(t, bob.CanOpen(ctx, item("dataset", "x")), "a kind with no rule opens for no one")

	admin := a.For(Viewer{UserID: "u-root", Email: "root@example.com", Admin: true})
	assert.True(t, admin.CanOpen(ctx, item(TargetAsset, "private")))
	assert.True(t, admin.CanOpen(ctx, item(TargetCollection, "private-col")))

	// A run reads for the person it acts for: their own files, and neither the
	// share graph nor an administrator's reach.
	run := a.For(Viewer{UserID: "script:daily", Email: "bob@example.com", Admin: true, Unattended: true})
	assert.True(t, run.CanOpen(ctx, item(TargetAsset, "mine")))
	assert.False(t, run.CanOpen(ctx, item(TargetAsset, "shared")))
	assert.False(t, run.CanOpen(ctx, item(TargetAsset, "private")))
	assert.False(t, run.CanOpen(ctx, item(TargetCollection, "shared-col")))

	personal := NewAccess(nil, nil, nil, &fakeResources{res: &resource.Resource{ID: "res-2", Scope: resource.ScopeUser, ScopeID: "u-jane"}})
	assert.False(t, personal.For(Viewer{Claims: resource.Claims{Sub: "u-bob"}}).CanOpen(ctx, item(TargetResource, "res-2")),
		"another person's resource does not open")
}

// TestAccessWithoutStoresOpensNothingItCannotRead keeps a deployment missing a
// store from naming a file it cannot judge.
func TestAccessWithoutStoresOpensNothingItCannotRead(t *testing.T) {
	ctx := context.Background()
	none := NewAccess(nil, nil, nil, nil).For(Viewer{Admin: true})
	for _, kind := range []string{TargetAsset, TargetCollection, TargetResource} {
		assert.False(t, none.CanOpen(ctx, item(kind, "x")), kind)
	}

	// No share store: only owners and administrators open an asset.
	noShares := NewAccess(fakeAssetGetter{byID: map[string]*portaldomain.Asset{
		"shared": {ID: "shared", OwnerID: "u-jane", OwnerEmail: "jane@example.com"},
	}}, fakeCollectionGetter{"c": {ID: "c", OwnerID: "u-jane"}}, nil, nil).For(Viewer{UserID: "u-bob", Email: "bob@example.com"})
	assert.False(t, noShares.CanOpen(ctx, item(TargetAsset, "shared")))
	assert.False(t, noShares.CanOpen(ctx, item(TargetCollection, "c")))

	failing := NewAccess(fakeAssetGetter{err: errors.New("down")}, nil, nil, &fakeResources{err: errors.New("down")}).
		For(Viewer{Admin: true})
	assert.False(t, failing.CanOpen(ctx, item(TargetAsset, "a")), "a read that fails opens nothing")
	assert.False(t, failing.CanOpen(ctx, item(TargetResource, "r")))
}

func TestSharedWithReadsTheShareGraph(t *testing.T) {
	assert.Nil(t, SharedWith(nil))
	lookup := SharedWith(fakeShares{byAsset: map[string][]portaldomain.Share{
		"a1": {{SharedWithEmail: "bob@example.com", Permission: portaldomain.PermissionViewer}},
	}})
	asset := &portaldomain.Asset{ID: "a1", OwnerID: "u-jane", OwnerEmail: "jane@example.com"}
	assert.True(t, lookup(context.Background(), asset, "u-bob", "bob@example.com"))
	assert.False(t, lookup(context.Background(), asset, "u-carol", "carol@example.com"))
}
