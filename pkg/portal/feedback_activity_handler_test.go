package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// activityShareStore extends mockShareStore so a test can inject collections
// shared with the caller (the base mock always returns none).
type activityShareStore struct {
	mockShareStore
	sharedCollections []SharedCollection
}

func (m *activityShareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]SharedCollection, int, error) {
	return m.sharedCollections, len(m.sharedCollections), nil
}

func newActivityHandler(deps Deps, user *User) *Handler {
	deps.AdminRoles = []string{"admin"}
	deps.RateLimit = RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100}
	return NewHandler(deps, testAuthMiddleware(user))
}

func TestFeedbackActivityUnauthorized(t *testing.T) {
	h := newActivityHandler(Deps{ThreadStore: &mockThreadStore{}}, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestFeedbackActivityNoArtifacts(t *testing.T) {
	threads := &mockThreadStore{}
	h := newActivityHandler(Deps{
		ThreadStore:     threads,
		AssetStore:      &mockAssetStore{},
		ShareStore:      &mockShareStore{},
		CollectionStore: &mockCollectionStore{},
		PromptStore:     newMockPromptStore(),
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	require.Equal(t, http.StatusOK, w.Code)

	// With no accessible artifacts the store is NOT queried unscoped: doing so
	// would disclose feedback on items the caller cannot see.
	assert.Empty(t, threads.lastListFilter.TargetAssetIDs)
	assert.Empty(t, threads.lastListFilter.TargetCollectionIDs)
	assert.Empty(t, threads.lastListFilter.TargetPromptIDs)

	var resp paginatedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Total)
}

// TestFeedbackActivityScopesToViewableTargets is the access-scoping gate: the
// feed must span every asset/collection/prompt the caller can VIEW (owned plus
// shared at any permission, including viewer), and nothing else. Unlike the
// practitioner worklist, viewer-only shares are included here.
func TestFeedbackActivityScopesToViewableTargets(t *testing.T) {
	threads := &mockThreadStore{}
	assets := &mockAssetStore{listRes: []Asset{{ID: "asset_owned", OwnerID: "u1"}}}
	shares := &activityShareStore{
		mockShareStore: mockShareStore{
			sharedWithRes: []SharedAsset{
				{Asset: Asset{ID: "asset_view"}, Permission: PermissionViewer}, // included (viewer)
			},
			promptRefs: []SharedPromptRef{{PromptID: "prm_shared"}},
		},
		sharedCollections: []SharedCollection{
			{Collection: Collection{ID: "col_shared"}, Permission: PermissionViewer},
		},
	}
	colls := &mockCollectionStore{listResult: []Collection{{ID: "col_owned", OwnerID: "u1"}}}
	prompts := newMockPromptStore()
	prompts.prompts["mine"] = &prompt.Prompt{ID: "prm_owned", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "u1@example.com"}
	// A global prompt the user did not author must NOT enter the feed.
	prompts.prompts["lib"] = &prompt.Prompt{ID: "prm_global", Name: "lib", Scope: prompt.ScopeGlobal, OwnerEmail: "other@example.com"}

	h := newActivityHandler(Deps{
		ThreadStore: threads, AssetStore: assets, ShareStore: shares,
		CollectionStore: colls, PromptStore: prompts,
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity?limit=25", nil)
	require.Equal(t, http.StatusOK, w.Code)

	f := threads.lastListFilter
	assert.ElementsMatch(t, []string{"asset_owned", "asset_view"}, f.TargetAssetIDs)
	assert.ElementsMatch(t, []string{"col_owned", "col_shared"}, f.TargetCollectionIDs)
	assert.ElementsMatch(t, []string{"prm_owned", "prm_shared"}, f.TargetPromptIDs)
	assert.NotContains(t, f.TargetPromptIDs, "prm_global")
	assert.Equal(t, 25, f.Limit)
}

func TestFeedbackActivityTargetLabels(t *testing.T) {
	threads := &mockThreadStore{listResult: []ThreadWithMeta{
		{Thread: Thread{ID: "thr_a", TargetType: targetTypeAsset, AssetID: "a1"}},
		{Thread: Thread{ID: "thr_gone", TargetType: targetTypeAsset, AssetID: "a_missing"}},
		{Thread: Thread{ID: "thr_c", TargetType: targetTypeCollection, CollectionID: "c1"}},
		{Thread: Thread{ID: "thr_p", TargetType: targetTypePrompt, PromptID: "prm1"}},
	}, listTotal: 4}
	assets := &mockMultiAssetStore{
		mockAssetStore: mockAssetStore{listRes: []Asset{{ID: "a1", OwnerID: "u1"}, {ID: "a_missing", OwnerID: "u1"}}},
		assets:         map[string]*Asset{"a1": {ID: "a1", Name: "Revenue Dashboard"}},
	}
	colls := &mockCollectionStore{
		listResult: []Collection{{ID: "c1", OwnerID: "u1"}},
		getResult:  &Collection{ID: "c1", Name: "Q4 Review"},
	}
	prompts := newMockPromptStore()
	prompts.prompts["rep"] = &prompt.Prompt{ID: "prm1", Name: "rep", DisplayName: "Daily Report", Scope: prompt.ScopePersonal, OwnerEmail: "u1@example.com"}

	h := newActivityHandler(Deps{
		ThreadStore: threads, AssetStore: assets, ShareStore: &mockShareStore{},
		CollectionStore: colls, PromptStore: prompts,
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []threadActivityItem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	labels := map[string]string{}
	for _, it := range resp.Data {
		labels[it.ID] = it.TargetLabel
	}
	assert.Equal(t, "Revenue Dashboard", labels["thr_a"])
	assert.Equal(t, "Asset", labels["thr_gone"]) // unresolved name falls back to type
	assert.Equal(t, "Q4 Review", labels["thr_c"])
	assert.Equal(t, "Daily Report", labels["thr_p"])
}

func TestFeedbackActivityTargetResolveError(t *testing.T) {
	assets := &mockAssetStore{listErr: assert.AnError}
	h := newActivityHandler(Deps{
		ThreadStore: &mockThreadStore{}, AssetStore: assets,
		ShareStore: &mockShareStore{}, CollectionStore: &mockCollectionStore{}, PromptStore: newMockPromptStore(),
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// errPromptStore errors on List so the owned-prompt failure path is covered.
type errPromptStore struct{ *mockPromptStore }

func (errPromptStore) List(_ context.Context, _ prompt.ListFilter) ([]prompt.Prompt, error) {
	return nil, assert.AnError
}

func TestFeedbackActivityCollectionResolveError(t *testing.T) {
	assets := &mockAssetStore{listRes: []Asset{{ID: "a1", OwnerID: "u1"}}}
	colls := &mockCollectionStore{listErr: assert.AnError}
	h := newActivityHandler(Deps{
		ThreadStore: &mockThreadStore{}, AssetStore: assets,
		ShareStore: &mockShareStore{}, CollectionStore: colls, PromptStore: newMockPromptStore(),
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestFeedbackActivityPromptResolveError(t *testing.T) {
	assets := &mockAssetStore{listRes: []Asset{{ID: "a1", OwnerID: "u1"}}}
	h := newActivityHandler(Deps{
		ThreadStore: &mockThreadStore{}, AssetStore: assets,
		ShareStore: &mockShareStore{}, CollectionStore: &mockCollectionStore{}, PromptStore: errPromptStore{newMockPromptStore()},
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestTargetActivityLabel(t *testing.T) {
	assets := map[string]string{"a1": "Asset One"}
	colls := map[string]string{"c1": "Coll One"}
	prompts := map[string]string{"p1": "Prompt One"}
	cases := []struct {
		name string
		t    ThreadWithMeta
		want string
	}{
		{"asset", ThreadWithMeta{Thread: Thread{TargetType: targetTypeAsset, AssetID: "a1"}}, "Asset One"},
		{"asset-missing", ThreadWithMeta{Thread: Thread{TargetType: targetTypeAsset, AssetID: "x"}}, "Asset"},
		{"collection", ThreadWithMeta{Thread: Thread{TargetType: targetTypeCollection, CollectionID: "c1"}}, "Coll One"},
		{"prompt", ThreadWithMeta{Thread: Thread{TargetType: targetTypePrompt, PromptID: "p1"}}, "Prompt One"},
		{"standalone", ThreadWithMeta{Thread: Thread{TargetType: targetTypeStandalone}}, "General feedback"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, targetActivityLabel(c.t, assets, colls, prompts))
		})
	}
}

func TestPromptDisplayName(t *testing.T) {
	assert.Equal(t, "Display", promptDisplayName(&prompt.Prompt{Name: "name", DisplayName: "Display"}))
	assert.Equal(t, "name", promptDisplayName(&prompt.Prompt{Name: "name"}))
}

func TestFeedbackActivityListError(t *testing.T) {
	threads := &mockThreadStore{listErr: assert.AnError}
	assets := &mockAssetStore{listRes: []Asset{{ID: "asset_owned", OwnerID: "u1"}}}
	h := newActivityHandler(Deps{
		ThreadStore: threads, AssetStore: assets,
		ShareStore: &mockShareStore{}, CollectionStore: &mockCollectionStore{}, PromptStore: newMockPromptStore(),
	}, &User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
