package portal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// reachSearchStore adds ranking within a person's reach to the searchable
// test store.
type reachSearchStore struct {
	*searchableAssetStore
	gotReach portaldomain.RankedAssetQuery
	reached  bool
	total    int
}

func (s *reachSearchStore) SearchReach(_ context.Context, q portaldomain.RankedAssetQuery) (scored []portal.ScoredAsset, total int, err error) {
	s.gotReach, s.reached = q, true
	return s.result, s.total, s.err
}

func newReachSearchToolkit(result []portal.ScoredAsset, total int) (*Toolkit, *reachSearchStore) {
	store := &reachSearchStore{
		searchableAssetStore: &searchableAssetStore{inMemoryAssetStore: newInMemoryAssetStore(), result: result},
		total:                total,
	}
	return New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"}), store
}

// A person's search covers everything they can open by default, with every
// filter the call names, and reports the count of all results.
func TestHandleSearch_PersonRanksTheirReach(t *testing.T) {
	tk, store := newReachSearchToolkit([]portal.ScoredAsset{{Asset: portal.Asset{ID: "a-colleague"}, Score: 0.8}}, 42)
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u1", UserEmail: searchTestEmail, PersonaName: "analyst",
	})

	r, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{
		Action: actionSearch, Query: "revenue", Owners: []string{"Bob@Example.com"},
		Tags: []string{"finance"}, ContentType: "text/csv", Limit: 10, Offset: 10,
	})
	require.NoError(t, err)
	require.False(t, r.IsError, errText(t, r))
	require.True(t, store.reached)
	assert.False(t, store.searched, "the owner-only ranking is not used for a person")

	q := store.gotReach
	assert.Equal(t, portaldomain.ScopeAll, q.Reach.Scope)
	assert.Equal(t, "analyst", q.Reach.Persona)
	assert.Equal(t, []string{"bob@example.com"}, q.Owners)
	assert.Equal(t, []string{"finance"}, q.Tags)
	assert.Equal(t, "text/csv", q.ContentType)
	assert.Equal(t, 10, q.Offset)

	m := searchResultMap(t, r)
	assert.InDelta(t, 42, m["total"], 0)
	assert.Equal(t, "all", m["scope"])
}

func TestHandleSearch_ScopeNarrowsTheReach(t *testing.T) {
	tk, store := newReachSearchToolkit(nil, 0)
	r, _, err := tk.handleManageAsset(searchCtx(), nil, manageAssetInput{Action: actionSearch, Query: "x", Scope: "mine"})
	require.NoError(t, err)
	require.False(t, r.IsError)
	assert.Equal(t, portaldomain.ScopeMine, store.gotReach.Reach.Scope)
	assets, ok := searchResultMap(t, r)["assets"].([]any)
	require.True(t, ok, "an empty result is a list, never null")
	assert.Empty(t, assets)
}

// A managed-script run ranks what it produced; no scope widens it.
func TestHandleSearch_ScriptRunKeepsItsProducerScope(t *testing.T) {
	tk, store := newReachSearchToolkit(nil, 0)
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "script:daily", OnBehalfOfEmail: searchTestEmail,
	})
	assert.Nil(t, callerReach(ctx, "all", portaldomain.ScopeAll))
	_, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: actionSearch, Query: "x", Scope: "all"})
	require.NoError(t, err)
	assert.False(t, store.reached)
}

func TestHandleSearch_ReachStoreError(t *testing.T) {
	tk, store := newReachSearchToolkit(nil, 0)
	store.err = assert.AnError
	r, _, err := tk.handleManageAsset(searchCtx(), nil, manageAssetInput{Action: actionSearch, Query: "x"})
	require.NoError(t, err)
	assert.True(t, r.IsError)
}

// list keeps the caller's own assets by default and takes the scope, owner,
// tag, content type and offset the call names.
func TestHandleList_ReachAndFilters(t *testing.T) {
	store := &filterRecordingStore{inMemoryAssetStore: newInMemoryAssetStore()}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "bucket"})

	_, _, err := tk.handleManageAsset(searchCtx(), nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.NotNil(t, store.last.Reach)
	assert.Equal(t, portaldomain.ScopeMine, store.last.Reach.Scope)
	assert.False(t, store.last.Owner.Identified(), "the reach replaces the owner scope")

	_, _, err = tk.handleManageAsset(searchCtx(), nil, manageAssetInput{
		Action: actionList, Scope: "shared", Owners: []string{" B@example.com "}, Tags: []string{"t"}, ContentType: "text/csv", Offset: 5,
	})
	require.NoError(t, err)
	assert.Equal(t, portaldomain.ScopeShared, store.last.Reach.Scope)
	assert.Equal(t, []string{"b@example.com"}, store.last.Owners)
	assert.Equal(t, []string{"t"}, store.last.Tags)
	assert.Equal(t, "text/csv", store.last.ContentType)
	assert.Equal(t, 5, store.last.Offset)
}

type filterRecordingStore struct {
	*inMemoryAssetStore
	last portal.AssetFilter
}

func (s *filterRecordingStore) List(ctx context.Context, f portal.AssetFilter) (assets []portal.Asset, total int, err error) {
	s.last = f
	return s.inMemoryAssetStore.List(ctx, f)
}
