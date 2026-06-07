package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchableAssetStore adds the AssetSearcher capability to the mock asset
// store so the ranked-search route is registered and exercisable.
type searchableAssetStore struct {
	*mockAssetStore
	gotQuery AssetSearchQuery
	result   []ScoredAsset
	err      error
}

func (s *searchableAssetStore) SearchAssets(_ context.Context, q AssetSearchQuery) ([]ScoredAsset, error) {
	s.gotQuery = q
	return s.result, s.err
}

var _ AssetSearcher = (*searchableAssetStore)(nil)

// searchableCollectionStore adds the CollectionSearcher capability to the mock
// collection store.
type searchableCollectionStore struct {
	*mockCollectionStore
	gotQuery CollectionSearchQuery
	result   []ScoredCollection
	err      error
}

func (s *searchableCollectionStore) SearchCollections(_ context.Context, q CollectionSearchQuery) ([]ScoredCollection, error) {
	s.gotQuery = q
	return s.result, s.err
}

var _ CollectionSearcher = (*searchableCollectionStore)(nil)

func newAssetSearchHandler(result []ScoredAsset) (*Handler, *searchableAssetStore) {
	store := &searchableAssetStore{mockAssetStore: &mockAssetStore{}, result: result}
	h := NewHandler(Deps{AssetStore: store, ShareStore: &mockShareStore{}}, nil)
	return h, store
}

func TestSearchMyAssets_Unauthenticated(t *testing.T) {
	h, _ := newAssetSearchHandler(nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search?q=x", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// withScopedUserID injects a portal user with the given owner_id (and a fixed
// email) so the search owner-scope guard, which keys on UserID, can be exercised.
func withScopedUserID(r *http.Request, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), portalUserKey, &User{UserID: userID, Email: "alice@example.com"}))
}

func TestSearchMyAssets_EmptyIdentityFailsClosed(t *testing.T) {
	h, _ := newAssetSearchHandler(nil)
	req := withScopedUserID(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search?q=x", http.NoBody), "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSearchMyAssets_WhitespaceIdentityFailsClosed(t *testing.T) {
	h, _ := newAssetSearchHandler(nil)
	req := withScopedUserID(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search?q=x", http.NoBody), "   ")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSearchMyAssets_MissingQuery(t *testing.T) {
	h, _ := newAssetSearchHandler(nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchMyAssets_Success(t *testing.T) {
	result := []ScoredAsset{{Asset: Asset{ID: "a-1", Name: "Cohort"}, Score: 0.88}}
	h, store := newAssetSearchHandler(result)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/assets/search?q=cohort+retention&limit=5", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data  []ScoredAsset `json:"data"`
		Total int           `json:"total"`
		Limit int           `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "a-1", resp.Data[0].Asset.ID)
	assert.InDelta(t, 0.88, resp.Data[0].Score, 1e-9)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, 5, resp.Limit)
	// Owner scope reaches the store as the caller's owner_id (withUser sets it).
	assert.Equal(t, "user-123", store.gotQuery.OwnerID)
	assert.Equal(t, "cohort retention", store.gotQuery.QueryText)
	assert.Equal(t, 5, store.gotQuery.Limit)
}

func TestSearchMyAssets_StoreError(t *testing.T) {
	h, store := newAssetSearchHandler(nil)
	store.err = assert.AnError
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search?q=x", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestSearchMyAssets_Unavailable hits the defensive 503 branch directly: a
// non-searcher asset store does not register the route, so the guard is only
// reachable by invoking the handler method.
func TestSearchMyAssets_Unavailable(t *testing.T) {
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}}, nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search?q=x", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.searchMyAssets(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestSearchMyCollections_Success(t *testing.T) {
	store := &searchableCollectionStore{
		mockCollectionStore: &mockCollectionStore{},
		result:              []ScoredCollection{{Collection: Collection{ID: "c-1", Name: "Q4"}, Score: 0.7}},
	}
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, CollectionStore: store}, nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/collections/search?q=quarter", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data  []ScoredCollection `json:"data"`
		Total int                `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "c-1", resp.Data[0].Collection.ID)
	assert.Equal(t, "user-123", store.gotQuery.OwnerID)
}

func TestSearchMyCollections_EmptyIdentityFailsClosed(t *testing.T) {
	store := &searchableCollectionStore{mockCollectionStore: &mockCollectionStore{}}
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, CollectionStore: store}, nil)
	req := withScopedUserID(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/search?q=x", http.NoBody), "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func newCollectionSearchHandler() (*Handler, *searchableCollectionStore) {
	store := &searchableCollectionStore{mockCollectionStore: &mockCollectionStore{}}
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, CollectionStore: store}, nil)
	return h, store
}

func TestSearchMyCollections_Unauthenticated(t *testing.T) {
	h, _ := newCollectionSearchHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/search?q=x", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSearchMyCollections_MissingQuery(t *testing.T) {
	h, _ := newCollectionSearchHandler()
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/search", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchMyCollections_StoreError(t *testing.T) {
	h, store := newCollectionSearchHandler()
	store.err = assert.AnError
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/search?q=x", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestSearchMyCollections_Unavailable hits the defensive 503 branch directly.
func TestSearchMyCollections_Unavailable(t *testing.T) {
	h := NewHandler(Deps{AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, CollectionStore: &mockCollectionStore{}}, nil)
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/search?q=x", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.searchMyCollections(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestIntegration_SearchMyAssets_RealStoreEnforcesOwnerScope wires the real
// portal Handler to a real postgresAssetStore (sqlmock at the DB boundary) and
// a configured embedder, then sends a real HTTP request through the real mux +
// auth middleware. It proves the #550 claim: the authenticated caller's owner_id
// arrives as the owner_id SQL predicate (the per-user search boundary, the same
// key the asset library uses), and the relevance score travels back to the JSON
// response.
func TestIntegration_SearchMyAssets_RealStoreEnforcesOwnerScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := NewPostgresAssetStore(db)
	const callerID = "u1"

	// Hybrid binds $1=vector, $2=query, $3=owner_id. If the handler failed to
	// scope by the caller, this expectation would not be met.
	rows := sqlmock.NewRows(append(append([]string{}, assetSearchCols...), "vec_score", "lex_match"))
	addAssetRow(rows, "a-1", "Cohort retention", driverValueList{0.9, true})
	mock.ExpectQuery("UNION ALL").
		WithArgs(sqlmock.AnyArg(), "retention", callerID).
		WillReturnRows(rows)
	mock.ExpectQuery("FROM portal_collection_items").
		WillReturnRows(sqlmock.NewRows([]string{"asset_id", "id", "name"}))

	h := NewHandler(Deps{
		AssetStore:        store,
		ShareStore:        &mockShareStore{},
		EmbeddingProvider: &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}},
	}, testAuthMiddleware(&User{UserID: callerID, Email: "alice@example.com"}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/assets/search?q=retention", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet(),
		"the real store must be queried with the caller's owner_id as the scope")

	var resp struct {
		Total int `json:"total"`
		Data  []struct {
			Asset Asset   `json:"asset"`
			Score float64 `json:"score"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "a-1", resp.Data[0].Asset.ID)
	assert.Positive(t, resp.Data[0].Score, "a real relevance score must reach the JSON response")
}
