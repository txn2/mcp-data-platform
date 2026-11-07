package portal

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// searchableAssetStore adds the AssetSearcher capability to the mock asset
// store so the ranked-search route is registered and exercisable.
type searchableAssetStore struct {
	*mockAssetStore
	gotQuery AssetSearchQuery
	gotReach portaldomain.RankedAssetQuery
	result   []ScoredAsset
	total    int
	err      error
}

func (s *searchableAssetStore) SearchAssets(_ context.Context, q AssetSearchQuery) ([]ScoredAsset, error) {
	s.gotQuery = q
	return s.result, s.err
}

func (s *searchableAssetStore) SearchReach(_ context.Context, q portaldomain.RankedAssetQuery) (scored []ScoredAsset, total int, err error) {
	s.gotReach = q
	total = s.total
	if total == 0 {
		total = len(s.result)
	}
	return s.result, total, s.err
}

var _ AssetSearcher = (*searchableAssetStore)(nil)

// searchableCollectionStore adds the CollectionSearcher capability to the mock
// collection store.
type searchableCollectionStore struct {
	*mockCollectionStore
	gotQuery CollectionSearchQuery
	gotReach portaldomain.RankedCollectionQuery
	result   []ScoredCollection
	err      error
}

func (s *searchableCollectionStore) SearchCollections(_ context.Context, q CollectionSearchQuery) ([]ScoredCollection, error) {
	s.gotQuery = q
	return s.result, s.err
}

func (s *searchableCollectionStore) SearchCollectionReach(_ context.Context, q portaldomain.RankedCollectionQuery) (scored []ScoredCollection, total int, err error) {
	s.gotReach = q
	return s.result, len(s.result), s.err
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

// withScopedUserID injects a portal user with the given owner id and address,
// so the search guard, which refuses a caller naming nobody, can be exercised.
func withScopedUserID(r *http.Request, userID string) *http.Request {
	return r.WithContext(ContextWithUser(r.Context(), &User{UserID: userID, Email: userID}))
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
	// The caller's reach reaches the store (withUser sets the identity); the
	// route's default scope is the caller's own assets.
	require.NotNil(t, store.gotReach.Reach)
	assert.Equal(t, "user-123", store.gotReach.Reach.Owner.UserID)
	assert.Equal(t, portaldomain.ScopeMine, store.gotReach.Reach.Scope)
	assert.Equal(t, "cohort retention", store.gotReach.QueryText)
	assert.Equal(t, 5, store.gotReach.Limit)
}

// Every filter the gallery sends reaches the ranked query, and total is the
// store's count of all results, not the length of the page.
func TestSearchMyAssets_ScopeFiltersAndPaging(t *testing.T) {
	result := []ScoredAsset{{Asset: Asset{ID: "a-2", OwnerID: "someone-else"}, Score: 0.7}}
	h, store := newAssetSearchHandler(result)
	store.total = 37
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/assets/search?q=revenue&scope=all&owner=B@Example.com&owner=c@example.com&tag=finance&content_type=text/csv&limit=10&offset=20",
		http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Total  int `json:"total"`
		Offset int `json:"offset"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 37, resp.Total)
	assert.Equal(t, 20, resp.Offset)
	q := store.gotReach
	assert.Equal(t, portaldomain.ScopeAll, q.Reach.Scope)
	assert.Equal(t, []string{"b@example.com", "c@example.com"}, q.Owners)
	assert.Equal(t, []string{"finance"}, q.Tags)
	assert.Equal(t, "text/csv", q.ContentType)
	assert.Equal(t, 10, q.Limit)
	assert.Equal(t, 20, q.Offset)
}

func TestSearchMyAssets_StoreError(t *testing.T) {
	h, store := newAssetSearchHandler(nil)
	store.err = assert.AnError
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/assets/search?q=x", http.NoBody), "alice@example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
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
	require.NotNil(t, store.gotReach.Reach)
	assert.Equal(t, "user-123", store.gotReach.Reach.Owner.UserID)
	assert.Equal(t, portaldomain.ScopeMine, store.gotReach.Reach.Scope)
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

	store := NewPostgresAssetStore(db, nil)
	const callerID = "u1"

	// The reach's mine scope binds the caller's two identifiers first, then
	// the query text and the vector. If the handler failed to scope by the
	// caller, this expectation would not be met.
	mock.ExpectQuery("WITH c AS MATERIALIZED").
		WithArgs(callerID, "alice@example.com", "retention", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "score", "total"}).AddRow("a-1", 0.9, 1))
	// The page's rows are read through the listing itself.
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	rows := sqlmock.NewRows(rankedAssetCols)
	rows.AddRow(rankedAssetRow("a-1", callerID, "Cohort retention", 0.9, true)[:len(rankedAssetCols)]...)
	mock.ExpectQuery("FROM portal_assets").WillReturnRows(rows)
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

// rankedAssetCols is the asset projection the ranked-search scanner reads, in
// the store's scan order (sqlmock scans positionally). It is spelled here
// rather than imported because the store's own fixtures live with the store;
// a drift between the two shows up as a scan error in this test.
var rankedAssetCols = []string{
	"id", "owner_id", "owner_email", "name", "description", "content_type",
	"s3_bucket", "s3_key", "thumbnail_s3_key", "thumbnail_dark_s3_key", "thumbnail_version", "thumbnail_dark_version", "thumbnail_renderer", "thumbnail_failure", "thumbnail_failed_version", "size_bytes", "tags", "provenance",
	"session_id", "current_version", "created_at", "updated_at", "deleted_at", "idempotency_key", "max_versions", "released_version",
}

// rankedAssetRow builds one ranked-search row: the asset projection followed by
// the two per-arm score columns the hybrid query appends.
func rankedAssetRow(id, ownerID, name string, vecScore float64, lexMatch bool) []driver.Value {
	now := time.Now()
	return []driver.Value{
		id, ownerID, "alice@example.com", name, "desc", "text/html",
		"bucket", "key", "", "", 0, 0, 0, "", 0, int64(10), []byte(`["t"]`), []byte(`{}`),
		"", 1, now, now, nil, "", nil, 0,
		vecScore, lexMatch,
	}
}
