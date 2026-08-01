package feedbackapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// activityShareStore extends mockShareStore so a test can inject collections
// shared with the caller (the base mock always returns none).
type activityShareStore struct {
	mockShareStore
	sharedCollections []portaldomain.SharedCollection
}

func (m *activityShareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]portaldomain.SharedCollection, int, error) {
	return m.sharedCollections, len(m.sharedCollections), nil
}

func newActivityHandler(cfg Config, user *access.User) http.Handler {
	return newTestServer(cfg, user)
}

func TestFeedbackActivityUnauthorized(t *testing.T) {
	h := newActivityHandler(Config{Threads: &mockThreadStore{}}, nil)
	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestFeedbackActivityNoArtifacts(t *testing.T) {
	store := &mockThreadStore{}
	h := newActivityHandler(Config{
		Threads:     store,
		Assets:      &mockAssetStore{},
		Shares:      &mockShareStore{},
		Collections: &mockCollectionStore{},
		Prompts:     newMockPromptStore(),
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	require.Equal(t, http.StatusOK, w.Code)

	// With no accessible artifacts the store is NOT queried unscoped: doing so
	// would disclose feedback on items the caller cannot see.
	assert.Empty(t, store.lastListFilter.TargetAssetIDs)
	assert.Empty(t, store.lastListFilter.TargetCollectionIDs)
	assert.Empty(t, store.lastListFilter.TargetPromptIDs)

	var resp pagedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Total)
}

// TestFeedbackActivityScopesToViewableTargets is the access-scoping gate: the
// feed must span every asset/collection/prompt the caller can VIEW (owned plus
// shared at any permission, including viewer), and nothing else. Unlike the
// practitioner worklist, viewer-only shares are included here.
func TestFeedbackActivityScopesToViewableTargets(t *testing.T) {
	store := &mockThreadStore{}
	assets := &mockAssetStore{listRes: []portaldomain.Asset{{ID: "asset_owned", OwnerID: "u1"}}}
	shares := &activityShareStore{
		mockShareStore: mockShareStore{
			sharedWithRes: []portaldomain.SharedAsset{
				{Asset: portaldomain.Asset{ID: "asset_view"}, Permission: portaldomain.PermissionViewer}, // included (viewer)
			},
			promptRefs: []portaldomain.SharedPromptRef{{PromptID: "prm_shared"}},
		},
		sharedCollections: []portaldomain.SharedCollection{
			{Collection: portaldomain.Collection{ID: "col_shared"}, Permission: portaldomain.PermissionViewer},
		},
	}
	colls := &mockCollectionStore{listResult: []portaldomain.Collection{{ID: "col_owned", OwnerID: "u1"}}}
	prompts := newMockPromptStore()
	prompts.prompts["mine"] = &prompt.Prompt{ID: "prm_owned", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "u1@example.com"}
	// A global prompt the user did not author must NOT enter the feed.
	prompts.prompts["lib"] = &prompt.Prompt{ID: "prm_global", Name: "lib", Scope: prompt.ScopeGlobal, OwnerEmail: "other@example.com"}

	h := newActivityHandler(Config{
		Threads: store, Assets: assets, Shares: shares,
		Collections: colls, Prompts: prompts,
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity?limit=25", nil)
	require.Equal(t, http.StatusOK, w.Code)

	f := store.lastListFilter
	assert.ElementsMatch(t, []string{"asset_owned", "asset_view"}, f.TargetAssetIDs)
	assert.ElementsMatch(t, []string{"col_owned", "col_shared"}, f.TargetCollectionIDs)
	assert.ElementsMatch(t, []string{"prm_owned", "prm_shared"}, f.TargetPromptIDs)
	assert.NotContains(t, f.TargetPromptIDs, "prm_global")
	assert.Equal(t, 25, f.Limit)
}

func TestFeedbackActivityTargetLabels(t *testing.T) {
	store := &mockThreadStore{listResult: []threads.ThreadWithMeta{
		{Thread: threads.Thread{ID: "thr_a", TargetType: portaldomain.TargetTypeAsset, AssetID: "a1"}},
		{Thread: threads.Thread{ID: "thr_gone", TargetType: portaldomain.TargetTypeAsset, AssetID: "a_missing"}},
		{Thread: threads.Thread{ID: "thr_c", TargetType: portaldomain.TargetTypeCollection, CollectionID: "c1"}},
		{Thread: threads.Thread{ID: "thr_p", TargetType: portaldomain.TargetTypePrompt, PromptID: "prm1"}},
	}, listTotal: 4}
	assets := &mockMultiAssetStore{
		mockAssetStore: mockAssetStore{listRes: []portaldomain.Asset{{ID: "a1", OwnerID: "u1"}, {ID: "a_missing", OwnerID: "u1"}}},
		assets:         map[string]*portaldomain.Asset{"a1": {ID: "a1", Name: "Revenue Dashboard"}},
	}
	colls := &mockCollectionStore{
		listResult: []portaldomain.Collection{{ID: "c1", OwnerID: "u1"}},
		getResult:  &portaldomain.Collection{ID: "c1", Name: "Q4 Review"},
	}
	prompts := newMockPromptStore()
	prompts.prompts["rep"] = &prompt.Prompt{ID: "prm1", Name: "rep", DisplayName: "Daily Report", Scope: prompt.ScopePersonal, OwnerEmail: "u1@example.com"}

	h := newActivityHandler(Config{
		Threads: store, Assets: assets, Shares: &mockShareStore{},
		Collections: colls, Prompts: prompts,
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

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
	h := newActivityHandler(Config{
		Threads: &mockThreadStore{}, Assets: assets,
		Shares: &mockShareStore{}, Collections: &mockCollectionStore{}, Prompts: newMockPromptStore(),
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// errPromptStore errors on List so the owned-prompt failure path is covered.
type errPromptStore struct{ *mockPromptStore }

func (errPromptStore) List(_ context.Context, _ prompt.ListFilter) ([]prompt.Prompt, error) {
	return nil, assert.AnError
}

func TestFeedbackActivityCollectionResolveError(t *testing.T) {
	assets := &mockAssetStore{listRes: []portaldomain.Asset{{ID: "a1", OwnerID: "u1"}}}
	colls := &mockCollectionStore{listErr: assert.AnError}
	h := newActivityHandler(Config{
		Threads: &mockThreadStore{}, Assets: assets,
		Shares: &mockShareStore{}, Collections: colls, Prompts: newMockPromptStore(),
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestFeedbackActivityPromptResolveError(t *testing.T) {
	assets := &mockAssetStore{listRes: []portaldomain.Asset{{ID: "a1", OwnerID: "u1"}}}
	h := newActivityHandler(Config{
		Threads: &mockThreadStore{}, Assets: assets,
		Shares: &mockShareStore{}, Collections: &mockCollectionStore{}, Prompts: errPromptStore{newMockPromptStore()},
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestTargetActivityLabel(t *testing.T) {
	assets := map[string]string{"a1": "Asset One"}
	colls := map[string]string{"c1": "Coll One"}
	prompts := map[string]string{"p1": "Prompt One"}
	cases := []struct {
		name string
		t    threads.ThreadWithMeta
		want string
	}{
		{"asset", threads.ThreadWithMeta{Thread: threads.Thread{TargetType: portaldomain.TargetTypeAsset, AssetID: "a1"}}, "Asset One"},
		{"asset-missing", threads.ThreadWithMeta{Thread: threads.Thread{TargetType: portaldomain.TargetTypeAsset, AssetID: "x"}}, "Asset"},
		{"collection", threads.ThreadWithMeta{Thread: threads.Thread{TargetType: portaldomain.TargetTypeCollection, CollectionID: "c1"}}, "Coll One"},
		{"prompt", threads.ThreadWithMeta{Thread: threads.Thread{TargetType: portaldomain.TargetTypePrompt, PromptID: "p1"}}, "Prompt One"},
		{"standalone", threads.ThreadWithMeta{Thread: threads.Thread{TargetType: portaldomain.TargetTypeStandalone}}, "General feedback"},
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
	store := &mockThreadStore{listErr: assert.AnError}
	assets := &mockAssetStore{listRes: []portaldomain.Asset{{ID: "asset_owned", OwnerID: "u1"}}}
	h := newActivityHandler(Config{
		Threads: store, Assets: assets,
		Shares: &mockShareStore{}, Collections: &mockCollectionStore{}, Prompts: newMockPromptStore(),
	}, &access.User{UserID: "u1", Email: "u1@example.com"})

	w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/feedback/activity", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
