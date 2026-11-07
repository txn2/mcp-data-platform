package reachapi

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

func serve(h http.HandlerFunc, user *access.User, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	if user != nil {
		req = req.WithContext(access.ContextWithUser(req.Context(), user))
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

var caller = &access.User{UserID: "u1", Email: "a@example.com"}

// A store that cannot rank or facet: the search routes answer 503 when
// reached and the facets answer empty lists, never null.
func TestRoutesOverAStoreWithoutTheCapabilities(t *testing.T) {
	h := New(Config{Access: access.New(access.Config{})})
	assert.Equal(t, http.StatusServiceUnavailable, serve(h.searchAssets, caller, "/x?q=a").Code)
	assert.Equal(t, http.StatusServiceUnavailable, serve(h.searchCollections, caller, "/x?q=a").Code)

	w := serve(h.assetFacets, caller, "/x")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"tags":[],"authors":[]}`, w.Body.String())
	w = serve(h.collectionFacets, caller, "/x")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"authors":[]}`, w.Body.String())

	assert.Equal(t, http.StatusUnauthorized, serve(h.assetFacets, nil, "/x").Code)
	assert.Equal(t, http.StatusUnauthorized, serve(h.collectionFacets, nil, "/x").Code)
}

type rankingStore struct {
	got   portaldomain.RankedAssetQuery
	gotC  portaldomain.RankedCollectionQuery
	err   error
	calls int
}

func (s *rankingStore) SearchReach(_ context.Context, q portaldomain.RankedAssetQuery) ([]portaldomain.ScoredAsset, int, error) {
	s.got, s.calls = q, s.calls+1
	return nil, 0, s.err
}

func (s *rankingStore) SearchCollectionReach(_ context.Context, q portaldomain.RankedCollectionQuery) ([]portaldomain.ScoredCollection, int, error) {
	s.gotC, s.calls = q, s.calls+1
	return nil, 0, s.err
}

// The search routes refuse before reaching the store: no caller, a caller
// naming nobody, no query. A store failure is a 500 and an empty result is a
// list.
func TestSearchRefusalsAndResults(t *testing.T) {
	store := &rankingStore{}
	h := New(Config{Assets: store, Collections: store, Access: access.New(access.Config{})})
	for _, route := range []http.HandlerFunc{h.searchAssets, h.searchCollections} {
		assert.Equal(t, http.StatusUnauthorized, serve(route, nil, "/x?q=a").Code)
		assert.Equal(t, http.StatusForbidden, serve(route, &access.User{}, "/x?q=a").Code)
		assert.Equal(t, http.StatusBadRequest, serve(route, caller, "/x?q=%20").Code)
	}
	assert.Zero(t, store.calls)

	w := serve(h.searchAssets, caller, "/x?q=rev&scope=shared&limit=bad&offset=-3")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"data":[],"total":0,"limit":20,"offset":0}`, w.Body.String())
	assert.Equal(t, portaldomain.ScopeShared, store.got.Reach.Scope)
	assert.Nil(t, store.got.Embedding, "no embedder ranks lexically")

	w = serve(h.searchCollections, caller, "/x?q=rev&owner=B@example.com")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"b@example.com"}, store.gotC.Owners)

	store.err = assert.AnError
	assert.Equal(t, http.StatusInternalServerError, serve(h.searchAssets, caller, "/x?q=rev").Code)
	assert.Equal(t, http.StatusInternalServerError, serve(h.searchCollections, caller, "/x?q=rev").Code)
}

func TestRegisterMountsWhatTheStoresSupport(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{Assets: &rankingStore{}, Access: access.New(access.Config{})}).Register(mux)
	for path, want := range map[string]bool{
		"/api/v1/portal/assets/search":      true,
		"/api/v1/portal/assets/facets":      true,
		"/api/v1/portal/collections/search": false,
		"/api/v1/portal/collections/facets": false,
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
		_, pattern := mux.Handler(req)
		assert.Equal(t, want, pattern != "", path)
	}
}

func TestOwnedCollectionIDs(t *testing.T) {
	got := OwnedCollectionIDs([]portaldomain.Collection{{ID: "a", OwnerID: "u1"}, {ID: "b", OwnerID: "u2"}}, caller)
	assert.Equal(t, []string{"a"}, got)
}

type describeStore struct {
	perms map[string]portaldomain.SharePermission
	asked []string
}

func (s *describeStore) ReachPermissions(_ context.Context, ids []string, _ *portaldomain.Reach) (map[string]portaldomain.SharePermission, error) {
	s.asked = ids
	out := map[string]portaldomain.SharePermission{}
	maps.Copy(out, s.perms)
	return out, nil
}

type summaryShares struct {
	portaldomain.ShareStore
	asked []string
	perm  portaldomain.SharePermission
}

func (s *summaryShares) ListActiveShareSummaries(_ context.Context, ids []string) (map[string]portaldomain.ShareSummary, error) {
	s.asked = ids
	return map[string]portaldomain.ShareSummary{}, nil
}

func (s *summaryShares) GetUserCollectionPermission(_ context.Context, _, _, _ string) (portaldomain.SharePermission, error) {
	return s.perm, nil
}

// A page's owned rows get share summaries and its other rows the caller's
// permission; a person of an external persona is held to viewer.
func TestDescribeAssets(t *testing.T) {
	store := &describeStore{perms: map[string]portaldomain.SharePermission{"theirs": portaldomain.PermissionEditor}}
	shares := &summaryShares{}
	assets := []portaldomain.Asset{{ID: "mine", OwnerID: "u1"}, {ID: "theirs", OwnerID: "u2"}}
	reach := portaldomain.NewReach("u1", "a@example.com", "", portaldomain.ScopeAll)

	h := New(Config{Assets: store, Shares: shares, Access: access.New(access.Config{})})
	rows := h.DescribeAssets(context.Background(), assets, reach, caller)
	assert.Equal(t, []string{"mine"}, shares.asked)
	assert.Equal(t, []string{"theirs"}, store.asked)
	assert.Equal(t, portaldomain.PermissionEditor, rows.Permissions["theirs"])

	external := New(Config{Assets: store, Shares: shares, Access: access.New(access.Config{
		PortalPermissions: func([]string) persona.PortalAccess { return persona.PortalAccess{External: true} },
	})})
	rows = external.DescribeAssets(context.Background(), assets, reach, caller)
	assert.Equal(t, portaldomain.PermissionViewer, rows.Permissions["theirs"])
}

func TestCollectionPermissions(t *testing.T) {
	shares := &summaryShares{perm: portaldomain.PermissionViewer}
	h := New(Config{Shares: shares, Access: access.New(access.Config{Shares: shares})})
	got := h.CollectionPermissions(context.Background(),
		[]portaldomain.Collection{{ID: "mine", OwnerID: "u1"}, {ID: "theirs", OwnerID: "u2"}}, caller)
	assert.Equal(t, map[string]portaldomain.SharePermission{"theirs": portaldomain.PermissionViewer}, got)

	shares.perm = ""
	assert.Nil(t, h.CollectionPermissions(context.Background(), []portaldomain.Collection{{ID: "theirs", OwnerID: "u2"}}, caller))
}

type facetStores struct {
	gotFilter portaldomain.AssetFilter
	gotReach  *portaldomain.Reach
	err       error
}

func (s *facetStores) AssetFacets(_ context.Context, f portaldomain.AssetFilter) (portaldomain.AssetFacets, error) {
	s.gotFilter = f
	return portaldomain.AssetFacets{Tags: []portaldomain.FacetCount{{Value: "t", Count: 1}}, Authors: []portaldomain.FacetCount{}}, s.err
}

func (s *facetStores) CollectionAuthors(_ context.Context, r *portaldomain.Reach) ([]portaldomain.FacetCount, error) {
	s.gotReach = r
	return []portaldomain.FacetCount{{Value: "b@example.com", Count: 2}}, s.err
}

func TestFacets(t *testing.T) {
	store := &facetStores{}
	h := New(Config{Assets: store, Collections: store, Access: access.New(access.Config{})})

	w := serve(h.assetFacets, caller, "/x?scope=shared&tag=t&owner=b@example.com&content_type=text/csv")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"tags":[{"value":"t","count":1}],"authors":[]}`, w.Body.String())
	assert.Equal(t, portaldomain.ScopeShared, store.gotFilter.Reach.Scope)
	assert.Equal(t, []string{"t"}, store.gotFilter.Tags)
	assert.Equal(t, "text/csv", store.gotFilter.ContentType)

	w = serve(h.collectionFacets, caller, "/x")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"authors":[{"value":"b@example.com","count":2}]}`, w.Body.String())
	assert.Equal(t, portaldomain.ScopeAll, store.gotReach.Scope)

	store.err = assert.AnError
	assert.Equal(t, http.StatusInternalServerError, serve(h.assetFacets, caller, "/x").Code)
	assert.Equal(t, http.StatusInternalServerError, serve(h.collectionFacets, caller, "/x").Code)
}

func TestEmbedIsUsedWhenConfigured(t *testing.T) {
	store := &rankingStore{}
	h := New(Config{Assets: store, Access: access.New(access.Config{}), Embed: func(context.Context, string) []float32 { return []float32{1} }})
	require.Equal(t, http.StatusOK, serve(h.searchAssets, caller, "/x?q=rev").Code)
	assert.Equal(t, []float32{1}, store.got.Embedding)
}
