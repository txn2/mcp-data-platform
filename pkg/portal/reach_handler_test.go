package portal

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// reachAssetStore adds the reach capabilities to the mock asset store.
type reachAssetStore struct {
	*mockAssetStore
	facets      portaldomain.AssetFacets
	facetErr    error
	gotFacet    portaldomain.AssetFilter
	perms       map[string]portaldomain.SharePermission
	gotPermIDs  []string
	gotPermFrom *portaldomain.Reach
}

func (s *reachAssetStore) AssetFacets(_ context.Context, f portaldomain.AssetFilter) (portaldomain.AssetFacets, error) {
	s.gotFacet = f
	return s.facets, s.facetErr
}

func (s *reachAssetStore) ReachPermissions(_ context.Context, ids []string, r *portaldomain.Reach) (map[string]portaldomain.SharePermission, error) {
	s.gotPermIDs, s.gotPermFrom = ids, r
	out := map[string]portaldomain.SharePermission{}
	maps.Copy(out, s.perms)
	return out, nil
}

// summaryShareStore records which ids the share summaries were asked for.
type summaryShareStore struct {
	mockShareStore
	askedAsset []string
}

func (s *summaryShareStore) ListActiveShareSummaries(_ context.Context, ids []string) (map[string]ShareSummary, error) {
	s.askedAsset = ids
	out := map[string]ShareSummary{}
	for _, id := range ids {
		out[id] = ShareSummary{HasPublicLink: true}
	}
	return out, nil
}

func getJSON(t *testing.T, h http.Handler, user *User, target string, into any) int {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	req = req.WithContext(ContextWithUser(req.Context(), user))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if into != nil && w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), into))
	}
	return w.Code
}

var reachCaller = &User{UserID: "u-alice", Email: "alice@example.com"}

// A listing in the all scope carries the caller's permission on each row they
// do not own, and a share summary only on the rows they own: whether a row
// has a public link is its owner's to know.
func TestListAssets_AllScopeDescribesEachRowForTheCaller(t *testing.T) {
	store := &reachAssetStore{
		mockAssetStore: &mockAssetStore{listRes: []Asset{
			{ID: "mine", OwnerID: "u-alice", OwnerEmail: "alice@example.com"},
			{ID: "theirs", OwnerID: "u-bob", OwnerEmail: "bob@example.com"},
		}, listTotal: 2},
		perms: map[string]portaldomain.SharePermission{"theirs": portaldomain.PermissionEditor},
	}
	shares := &summaryShareStore{}
	h := NewHandler(Deps{AssetStore: store, ShareStore: shares}, nil)

	var resp struct {
		Total          int                                     `json:"total"`
		ShareSummaries map[string]ShareSummary                 `json:"share_summaries"`
		Permissions    map[string]portaldomain.SharePermission `json:"permissions"`
	}
	code := getJSON(t, h, reachCaller, "/api/v1/portal/assets?scope=all&owner=bob@example.com", &resp)
	require.Equal(t, http.StatusOK, code)

	require.NotNil(t, store.lastFilter.Reach)
	assert.Equal(t, portaldomain.ScopeAll, store.lastFilter.Reach.Scope)
	assert.Equal(t, []string{"bob@example.com"}, store.lastFilter.Owners)
	assert.Equal(t, []string{"mine"}, shares.askedAsset, "summaries are asked only for owned rows")
	assert.Contains(t, resp.ShareSummaries, "mine")
	assert.NotContains(t, resp.ShareSummaries, "theirs")
	assert.Equal(t, []string{"theirs"}, store.gotPermIDs, "permissions are asked only for rows not owned")
	assert.Equal(t, portaldomain.PermissionEditor, resp.Permissions["theirs"])
}

// A person of an external persona is held to viewer on every row, whatever a
// share grants, as access.ResolveAssetPermission holds them.
func TestListAssets_ExternalPersonaIsCappedAtViewer(t *testing.T) {
	store := &reachAssetStore{
		mockAssetStore: &mockAssetStore{listRes: []Asset{{ID: "theirs", OwnerID: "u-bob"}}, listTotal: 1},
		perms:          map[string]portaldomain.SharePermission{"theirs": portaldomain.PermissionEditor},
	}
	h := NewHandler(Deps{
		AssetStore: store, ShareStore: &mockShareStore{},
		PersonaResolver: func([]string) *PersonaInfo {
			return &PersonaInfo{Name: "guest", Portal: persona.PortalAccess{External: true}}
		},
	}, nil)

	var resp struct {
		Permissions map[string]portaldomain.SharePermission `json:"permissions"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, h, reachCaller, "/api/v1/portal/assets?scope=shared", &resp))
	assert.Equal(t, portaldomain.PermissionViewer, resp.Permissions["theirs"])
}

// A caller naming nobody lists nothing rather than every owner's assets.
func TestListAssets_NobodyListsNothing(t *testing.T) {
	store := &reachAssetStore{mockAssetStore: &mockAssetStore{listRes: []Asset{{ID: "x"}}, listTotal: 1}}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}}, nil)
	var resp struct {
		Total int `json:"total"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, h, &User{}, "/api/v1/portal/assets?scope=all", &resp))
	assert.Zero(t, resp.Total)
	assert.Nil(t, store.lastFilter, "the store is never asked")
}

func TestAssetFacets_ReadsTheCallersReachAndFilters(t *testing.T) {
	store := &reachAssetStore{
		mockAssetStore: &mockAssetStore{},
		facets: portaldomain.AssetFacets{
			Tags:    []portaldomain.FacetCount{{Value: "finance", Count: 3}},
			Authors: []portaldomain.FacetCount{{Value: "bob@example.com", Count: 3}},
		},
	}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}}, nil)

	var resp struct {
		Tags    []portaldomain.FacetCount `json:"tags"`
		Authors []portaldomain.FacetCount `json:"authors"`
	}
	code := getJSON(t, h, reachCaller, "/api/v1/portal/assets/facets?scope=shared&tag=finance&owner=Bob@example.com&content_type=text/csv", &resp)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, store.facets.Tags, resp.Tags)
	assert.Equal(t, store.facets.Authors, resp.Authors)
	require.NotNil(t, store.gotFacet.Reach)
	assert.Equal(t, portaldomain.ScopeShared, store.gotFacet.Reach.Scope)
	assert.Equal(t, "u-alice", store.gotFacet.Reach.Owner.UserID)
	assert.Equal(t, []string{"finance"}, store.gotFacet.Tags)
	assert.Equal(t, []string{"bob@example.com"}, store.gotFacet.Owners)
	assert.Equal(t, "text/csv", store.gotFacet.ContentType)
}

func TestAssetFacets_DefaultScopeIsAll(t *testing.T) {
	store := &reachAssetStore{mockAssetStore: &mockAssetStore{}}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}}, nil)
	require.Equal(t, http.StatusOK, getJSON(t, h, reachCaller, "/api/v1/portal/assets/facets", nil))
	assert.Equal(t, portaldomain.ScopeAll, store.gotFacet.Reach.Scope)
}

func TestAssetFacets_Errors(t *testing.T) {
	store := &reachAssetStore{mockAssetStore: &mockAssetStore{}, facetErr: assert.AnError}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}}, nil)
	assert.Equal(t, http.StatusInternalServerError, getJSON(t, h, reachCaller, "/api/v1/portal/assets/facets", nil))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/facets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// A store that cannot facet answers with empty lists, never null (the UI
// reads both as arrays).
func TestAssetFacets_StoreWithoutFacetsAnswersEmptyLists(t *testing.T) {
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}}, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/facets", http.NoBody)
	req = req.WithContext(ContextWithUser(req.Context(), reachCaller))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"tags":[],"authors":[]}`, w.Body.String())
}

// reachCollectionStore adds the author facet to the mock collection store.
type reachCollectionStore struct {
	*mockCollectionStore
	authors  []portaldomain.FacetCount
	err      error
	gotReach *portaldomain.Reach
}

func (s *reachCollectionStore) CollectionAuthors(_ context.Context, r *portaldomain.Reach) ([]portaldomain.FacetCount, error) {
	s.gotReach = r
	return s.authors, s.err
}

func TestCollectionFacets(t *testing.T) {
	store := &reachCollectionStore{
		mockCollectionStore: &mockCollectionStore{},
		authors:             []portaldomain.FacetCount{{Value: "bob@example.com", Count: 2}},
	}
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, CollectionStore: store}, nil)

	var resp struct {
		Authors []portaldomain.FacetCount `json:"authors"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, h, reachCaller, "/api/v1/portal/collections/facets?scope=mine", &resp))
	assert.Equal(t, store.authors, resp.Authors)
	assert.Equal(t, portaldomain.ScopeMine, store.gotReach.Scope)

	store.err = assert.AnError
	assert.Equal(t, http.StatusInternalServerError, getJSON(t, h, reachCaller, "/api/v1/portal/collections/facets", nil))

	plain := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, CollectionStore: &mockCollectionStore{}}, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/facets", http.NoBody)
	req = req.WithContext(ContextWithUser(req.Context(), reachCaller))
	w := httptest.NewRecorder()
	plain.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"authors":[]}`, w.Body.String())

	anon := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/facets", http.NoBody)
	w = httptest.NewRecorder()
	plain.ServeHTTP(w, anon)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// A collection listing in the all scope gives the caller's permission on each
// collection they do not own, and summaries only for their own.
func TestListCollections_AllScopeDescribesEachRow(t *testing.T) {
	store := &mockCollectionStore{listResult: []Collection{
		{ID: "c-mine", OwnerID: "u-alice"},
		{ID: "c-theirs", OwnerID: "u-bob"},
		{ID: "c-unshared", OwnerID: "u-carol"},
	}, listTotal: 3}
	shares := &mockCollectionShareStore{collPermission: portaldomain.PermissionViewer}
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: shares, CollectionStore: store}, nil)

	var resp struct {
		Permissions map[string]portaldomain.SharePermission `json:"permissions"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, h, reachCaller, "/api/v1/portal/collections?scope=all", &resp))
	assert.Equal(t, portaldomain.PermissionViewer, resp.Permissions["c-theirs"])
	assert.NotContains(t, resp.Permissions, "c-mine")
}

func TestListCollections_UngrantedRowsCarryNoPermission(t *testing.T) {
	store := &mockCollectionStore{listResult: []Collection{{ID: "c", OwnerID: "u-bob"}}, listTotal: 1}
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockCollectionShareStore{}, CollectionStore: store}, nil)
	var resp struct {
		Permissions map[string]portaldomain.SharePermission `json:"permissions"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, h, reachCaller, "/api/v1/portal/collections?scope=all", &resp))
	assert.Empty(t, resp.Permissions)
}
