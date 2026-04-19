package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// --- Mock stores for handler tests ---

type mockAssetStore struct {
	insertErr error
	getAsset  *Asset
	getErr    error
	listRes   []Asset
	listTotal int
	listErr   error
	updateErr error
	deleteErr error
}

func (m *mockAssetStore) Insert(_ context.Context, _ Asset) error { return m.insertErr }
func (m *mockAssetStore) Get(_ context.Context, _ string) (*Asset, error) {
	return m.getAsset, m.getErr
}

func (m *mockAssetStore) GetByIDs(_ context.Context, ids []string) (map[string]*Asset, error) {
	result := make(map[string]*Asset)
	if m.getAsset != nil {
		for _, id := range ids {
			if id == m.getAsset.ID {
				result[id] = m.getAsset
			}
		}
	}
	return result, m.getErr
}

func (*mockAssetStore) GetByIdempotencyKey(_ context.Context, _, _ string) (*Asset, error) {
	return nil, fmt.Errorf("asset not found")
}

func (m *mockAssetStore) List(_ context.Context, _ AssetFilter) ([]Asset, int, error) {
	return m.listRes, m.listTotal, m.listErr
}

func (m *mockAssetStore) Update(_ context.Context, _ string, _ AssetUpdate) error {
	return m.updateErr
}
func (m *mockAssetStore) SoftDelete(_ context.Context, _ string) error { return m.deleteErr }

type mockShareStore struct {
	insertErr     error
	getByIDShare  *Share
	getByIDErr    error
	getByTokenRes *Share
	getByTokenErr error
	listByAsset   []Share
	listByAssetE  error
	sharedWithRes []SharedAsset
	sharedWithTot int
	sharedWithErr error
	revokeErr     error
	incrementErr  error
	summaries     map[string]ShareSummary
	summariesErr  error
}

func (m *mockShareStore) Insert(_ context.Context, _ Share) error { return m.insertErr }
func (m *mockShareStore) GetByID(_ context.Context, _ string) (*Share, error) {
	return m.getByIDShare, m.getByIDErr
}

func (m *mockShareStore) GetByToken(_ context.Context, _ string) (*Share, error) {
	return m.getByTokenRes, m.getByTokenErr
}

func (m *mockShareStore) ListByAsset(_ context.Context, _ string) ([]Share, error) {
	return m.listByAsset, m.listByAssetE
}

func (m *mockShareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]SharedAsset, int, error) {
	return m.sharedWithRes, m.sharedWithTot, m.sharedWithErr
}
func (m *mockShareStore) Revoke(_ context.Context, _ string) error          { return m.revokeErr }
func (m *mockShareStore) IncrementAccess(_ context.Context, _ string) error { return m.incrementErr }
func (m *mockShareStore) ListActiveShareSummaries(_ context.Context, _ []string) (map[string]ShareSummary, error) {
	return m.summaries, m.summariesErr
}

func (*mockShareStore) ListByCollection(_ context.Context, _ string) ([]Share, error) {
	return nil, nil
}

func (*mockShareStore) GetUserCollectionPermission(_ context.Context, _, _, _ string) (SharePermission, error) {
	return "", fmt.Errorf("no shares")
}

func (*mockShareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]SharedCollection, int, error) {
	return nil, 0, nil
}

func (*mockShareStore) ListActiveCollectionShareSummaries(_ context.Context, _ []string) (map[string]ShareSummary, error) {
	return map[string]ShareSummary{}, nil
}

type mockS3Client struct {
	getData   []byte
	getCT     string
	getErr    error
	putErr    error
	deleteErr error
}

func (m *mockS3Client) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return m.putErr
}

func (m *mockS3Client) GetObject(_ context.Context, _, _ string) (body []byte, contentType string, err error) {
	return m.getData, m.getCT, m.getErr
}
func (m *mockS3Client) DeleteObject(_ context.Context, _, _ string) error { return m.deleteErr }
func (*mockS3Client) Close() error                                        { return nil }

type mockVersionStore struct {
	createVersion int
	createErr     error
	listVersions  []AssetVersion
	listTotal     int
	listErr       error
	getVersion    *AssetVersion
	getErr        error
	latestVer     *AssetVersion
	latestErr     error
	lastCreated   *AssetVersion // captures the most recent CreateVersion call
}

func (m *mockVersionStore) CreateVersion(_ context.Context, av AssetVersion) (int, error) {
	m.lastCreated = &av
	return m.createVersion, m.createErr
}

func (m *mockVersionStore) ListByAsset(_ context.Context, _ string, _, _ int) ([]AssetVersion, int, error) {
	return m.listVersions, m.listTotal, m.listErr
}

func (m *mockVersionStore) GetByVersion(_ context.Context, _ string, _ int) (*AssetVersion, error) {
	return m.getVersion, m.getErr
}

func (m *mockVersionStore) GetLatest(_ context.Context, _ string) (*AssetVersion, error) {
	return m.latestVer, m.latestErr
}

// captureShareStore wraps a mockShareStore and captures the Share passed to Insert.
type captureShareStore struct {
	inner    *mockShareStore
	captured *Share
}

func (c *captureShareStore) Insert(ctx context.Context, share Share) error {
	*c.captured = share
	return c.inner.Insert(ctx, share)
}

func (c *captureShareStore) GetByID(ctx context.Context, id string) (*Share, error) {
	return c.inner.GetByID(ctx, id)
}

func (c *captureShareStore) GetByToken(ctx context.Context, token string) (*Share, error) {
	return c.inner.GetByToken(ctx, token)
}

func (c *captureShareStore) ListByAsset(ctx context.Context, assetID string) ([]Share, error) {
	return c.inner.ListByAsset(ctx, assetID)
}

func (c *captureShareStore) ListSharedWithUser(ctx context.Context, userID, email string, limit, offset int) ([]SharedAsset, int, error) {
	return c.inner.ListSharedWithUser(ctx, userID, email, limit, offset)
}

func (c *captureShareStore) Revoke(ctx context.Context, id string) error {
	return c.inner.Revoke(ctx, id)
}

func (c *captureShareStore) IncrementAccess(ctx context.Context, id string) error {
	return c.inner.IncrementAccess(ctx, id)
}

func (c *captureShareStore) ListActiveShareSummaries(ctx context.Context, ids []string) (map[string]ShareSummary, error) {
	return c.inner.ListActiveShareSummaries(ctx, ids)
}

func (c *captureShareStore) ListByCollection(ctx context.Context, id string) ([]Share, error) {
	return c.inner.ListByCollection(ctx, id)
}

func (c *captureShareStore) GetUserCollectionPermission(ctx context.Context, collectionID, userID, email string) (SharePermission, error) {
	return c.inner.GetUserCollectionPermission(ctx, collectionID, userID, email)
}

func (c *captureShareStore) ListSharedCollectionsWithUser(ctx context.Context, userID, email string, limit, offset int) ([]SharedCollection, int, error) {
	return c.inner.ListSharedCollectionsWithUser(ctx, userID, email, limit, offset)
}

func (c *captureShareStore) ListActiveCollectionShareSummaries(ctx context.Context, ids []string) (map[string]ShareSummary, error) {
	return c.inner.ListActiveCollectionShareSummaries(ctx, ids)
}

// authMiddleware injects a User into the context for testing.
func testAuthMiddleware(user *User) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user != nil {
				ctx := context.WithValue(r.Context(), portalUserKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func newTestHandler(assets *mockAssetStore, shares *mockShareStore, s3 *mockS3Client, user *User) *Handler {
	return newTestHandlerWithVersions(assets, shares, nil, s3, user)
}

func newTestHandlerWithVersions(assets *mockAssetStore, shares *mockShareStore, versions *mockVersionStore, s3 *mockS3Client, user *User) *Handler {
	deps := Deps{
		AssetStore:    assets,
		ShareStore:    shares,
		S3Client:      s3,
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
		RateLimit:     RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	if versions != nil {
		deps.VersionStore = versions
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

// --- NewHandler tests ---

func TestNewHandler(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
	}, nil)
	require.NotNil(t, h)
	assert.NotNil(t, h.mux)
	assert.NotNil(t, h.publicMux)
	assert.NotNil(t, h.rateLimiter)
}

// --- ServeHTTP routing tests ---

func TestServeHTTPRoutesToPublicMux(t *testing.T) {
	assets := &mockAssetStore{}
	shares := &mockShareStore{getByTokenErr: fmt.Errorf("not found")}
	h := newTestHandler(assets, shares, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/sometoken", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should reach the public mux (404 because token doesn't exist)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServeHTTPRoutesToAuthMux(t *testing.T) {
	assets := &mockAssetStore{
		listRes:   []Asset{},
		listTotal: 0,
	}
	shares := &mockShareStore{}
	h := newTestHandler(assets, shares, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServeHTTPNoAuthMiddleware(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{listRes: []Asset{}, listTotal: 0},
		ShareStore: &mockShareStore{},
	}, nil)

	// Without auth middleware, no user in context → 401
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- listAssets ---

func TestListAssetsSuccess(t *testing.T) {
	now := time.Now()
	assets := &mockAssetStore{
		listRes: []Asset{{
			ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
			Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
		}},
		listTotal: 1,
	}
	h := newTestHandler(assets, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets?limit=10&offset=0&content_type=text/html&tag=dashboard", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var resp paginatedResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
}

func TestListAssetsNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListAssetsStoreError(t *testing.T) {
	assets := &mockAssetStore{listErr: fmt.Errorf("db error")}
	h := newTestHandler(assets, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListAssetsIncludesShareSummaries(t *testing.T) {
	now := time.Now()
	assets := &mockAssetStore{
		listRes: []Asset{{
			ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
			Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
		}},
		listTotal: 1,
	}
	shares := &mockShareStore{
		summaries: map[string]ShareSummary{
			"a1": {HasUserShare: true, HasPublicLink: false},
		},
	}
	h := newTestHandler(assets, shares, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var raw map[string]json.RawMessage
	err := json.NewDecoder(w.Body).Decode(&raw)
	require.NoError(t, err)

	// share_summaries key must be present
	_, hasSummaries := raw["share_summaries"]
	assert.True(t, hasSummaries, "response should include share_summaries")

	var summaries map[string]ShareSummary
	require.NoError(t, json.Unmarshal(raw["share_summaries"], &summaries))
	assert.True(t, summaries["a1"].HasUserShare)
	assert.False(t, summaries["a1"].HasPublicLink)
}

func TestListAssetsNilResult(t *testing.T) {
	assets := &mockAssetStore{listRes: nil, listTotal: 0}
	h := newTestHandler(assets, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp paginatedResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	// nil slice replaced with empty array in JSON
	data, ok := resp.Data.([]any)
	require.True(t, ok)
	assert.Empty(t, data)
}

// --- getAsset ---

func TestGetAssetSuccess(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "Test", Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAssetDeleted(t *testing.T) {
	now := time.Now()
	deleted := now.Add(-time.Hour)
	asset := &Asset{ID: "a1", OwnerID: "u1", DeletedAt: &deleted}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestGetAssetForbiddenNotOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other-user"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{}}, // no shares
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetAssetSharedWithUser(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "other-user", Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp assetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.IsOwner)
	assert.Equal(t, PermissionViewer, resp.SharePermission)
}

func TestGetAssetNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- getAssetContent ---

func TestGetAssetContentSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{getData: []byte("hello"), getCT: "text/plain"},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
	assert.Equal(t, "hello", w.Body.String())
}

func TestGetAssetContentS3Error(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{getErr: fmt.Errorf("s3 fail")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAssetContentEmptyContentType(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{getData: []byte("data"), getCT: ""},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
}

func TestGetAssetContentNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAssetContentDeleted(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", DeletedAt: &now}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestGetAssetContentForbidden(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{}},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetAssetContentNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetAssetContentNilS3Client(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{},
		S3Client:   nil, // no S3 configured
		S3Bucket:   "test-bucket",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "content storage not configured")
}

// --- updateAssetContent ---

func TestUpdateAssetContentSuccess(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", CurrentVersion: 1,
	}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{createVersion: 2},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("<html>Updated</html>"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "updated", resp.Status)
}

func TestUpdateAssetContentNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUpdateAssetContentNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateAssetContentDeleted(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", DeletedAt: &now}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestUpdateAssetContentNotOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u2", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpdateAssetContentTooLarge(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset}, &mockShareStore{},
		&mockVersionStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	oversize := strings.Repeat("x", MaxContentUploadBytes+1)
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader(oversize))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestUpdateAssetContentNilS3(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{},
		S3Client:   nil,
		S3Bucket:   "test-bucket",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateAssetContentS3Error(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", CurrentVersion: 1}
	s3 := &mockS3Client{putErr: fmt.Errorf("s3 error")}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset}, &mockShareStore{},
		&mockVersionStore{}, s3, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateAssetContentUpdateError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{createErr: fmt.Errorf("db error")},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- updateAsset ---

func TestUpdateAssetSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"name":"New Name","description":"New desc","tags":["tag1"]}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "updated", resp.Status)
}

func TestUpdateAssetNotOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"name":"x"}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpdateAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"name":"x"}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateAssetInvalidBody(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader("{bad json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAssetInvalidName(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"name":""}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAssetInvalidDescription(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	longDesc := strings.Repeat("x", maxDescriptionLength+1)
	body := fmt.Sprintf(`{"description":%q}`, longDesc)
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAssetInvalidTags(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	tags := make([]string, maxTags+1)
	for i := range tags {
		tags[i] = "t"
	}
	tagsJSON, _ := json.Marshal(tags)
	body := fmt.Sprintf(`{"tags":%s}`, tagsJSON)
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAssetStoreError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, updateErr: fmt.Errorf("db error")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"name":"Valid Name"}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateAssetNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1", strings.NewReader(`{"name":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- deleteAsset ---

func TestDeleteAssetSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "deleted", resp.Status)
}

func TestDeleteAssetNotOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/assets/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteAssetStoreError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, deleteErr: fmt.Errorf("db error")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteAssetNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- createShare ---

func TestCreateShareSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"expires_in":"24h"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp shareResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Share.Token)
	assert.Contains(t, resp.ShareURL, "/portal/view/")
}

func TestCreateShareWithUserID(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"shared_with_user_id":"u2"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateShareNotOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateShareAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/missing/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreateShareInvalidBody(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateShareInvalidDuration(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"expires_in":"not-a-duration"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateShareStoreError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{insertErr: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateShareNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateShareWithEmail(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"shared_with_email":"user@example.com"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateShareWithInvalidEmail(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"shared_with_email":"not-an-email"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateShareEmailNormalized(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	var insertedShare Share
	shares := &mockShareStore{}
	shares.insertErr = nil
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &captureShareStore{inner: shares, captured: &insertedShare},
		S3Client:      &mockS3Client{},
		S3Bucket:      "test",
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	body := `{"shared_with_email":"  User@Example.COM  "}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "user@example.com", insertedShare.SharedWithEmail)
}

func TestCreateShareNoPublicBaseURL(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
	}, testAuthMiddleware(&User{UserID: "u1"}))

	body := `{}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp shareResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Empty(t, resp.ShareURL) // no base URL → no share URL
}

func TestCreateShareWithHideExpiration(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"expires_in":"24h","hide_expiration":true}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp shareResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Share.HideExpiration)
	assert.NotNil(t, resp.Share.ExpiresAt)
}

func TestCreateShareWithCustomNoticeText(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"notice_text":"Internal use only."}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp shareResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Internal use only.", resp.Share.NoticeText)
}

func TestCreateShareWithEmptyNoticeText(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{"notice_text":""}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp shareResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "", resp.Share.NoticeText)
}

func TestCreateShareOmittedNoticeTextUsesDefault(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	body := `{}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp shareResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, defaultNoticeText, resp.Share.NoticeText)
}

func TestCreateShareNoticeTextTooLong(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	longText := strings.Repeat("a", maxNoticeTextLength+1)
	body := fmt.Sprintf(`{"notice_text":%q}`, longText)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- listShares ---

func TestListSharesSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	shares := []Share{{ID: "s1", AssetID: "a1", Token: "tok"}}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: shares},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListSharesNotOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListSharesAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/missing/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListSharesStoreError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListSharesNilResult(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: nil},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// nil replaced with []
	var result []any
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestListSharesNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/shares", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- revokeShare ---

func TestRevokeShareSuccess(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1"}
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{getByIDShare: share},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/shares/s1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "revoked", resp.Status)
}

func TestRevokeShareNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{getByIDErr: fmt.Errorf("not found")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/shares/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevokeShareNotOwner(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1"}
	asset := &Asset{ID: "a1", OwnerID: "other"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{getByIDShare: share},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/shares/s1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRevokeShareAssetNotFound(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1"}
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{getByIDShare: share},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/shares/s1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevokeShareStoreError(t *testing.T) {
	share := &Share{ID: "s1", AssetID: "a1"}
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{getByIDShare: share, revokeErr: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/shares/s1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRevokeShareNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/shares/s1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- listSharedWithMe ---

func TestListSharedWithMeSuccess(t *testing.T) {
	now := time.Now()
	shared := []SharedAsset{
		{
			Asset:   Asset{ID: "a1", Tags: []string{}, CreatedAt: now, UpdatedAt: now},
			ShareID: "s1", SharedBy: "other", SharedAt: now,
		},
	}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{sharedWithRes: shared, sharedWithTot: 1},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-with-me?limit=5&offset=0", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp paginatedResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
}

func TestListSharedWithMeStoreError(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{sharedWithErr: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-with-me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListSharedWithMeNilResult(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{sharedWithRes: nil, sharedWithTot: 0},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-with-me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListSharedWithMeNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-with-me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- hasAnyRole ---

func TestHasAnyRole(t *testing.T) {
	tests := []struct {
		name        string
		userRoles   []string
		targetRoles []string
		expected    bool
	}{
		{"match", []string{"dp_admin", "dp_analyst"}, []string{"dp_admin"}, true},
		{"no match", []string{"dp_analyst"}, []string{"dp_admin"}, false},
		{"empty user roles", nil, []string{"dp_admin"}, false},
		{"empty target roles", []string{"dp_admin"}, nil, false},
		{"both empty", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasAnyRole(tt.userRoles, tt.targetRoles))
		})
	}
}

// --- intParam ---

func TestIntParam(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		param    string
		fallback int
		expected int
	}{
		{"present", "?limit=10", "limit", 50, 10},
		{"missing", "", "limit", 50, 50},
		{"invalid", "?limit=abc", "limit", 50, 50},
		{"negative", "?limit=-1", "limit", 50, 50},
		{"zero", "?limit=0", "limit", 50, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/test"+tt.query, http.NoBody)
			assert.Equal(t, tt.expected, intParam(req, tt.param, tt.fallback))
		})
	}
}

// --- generateToken ---

func TestGenerateToken(t *testing.T) {
	tok1, err := generateToken()
	require.NoError(t, err)
	assert.Len(t, tok1, tokenBytes*2) // hex encoding doubles length

	tok2, err := generateToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok1, tok2) // two tokens should be unique
}

// --- isSharedWithUser ---

func TestIsShareActive(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	assert.True(t, isShareActive(Share{Revoked: false}))
	assert.False(t, isShareActive(Share{Revoked: true}))
	assert.False(t, isShareActive(Share{Revoked: false, ExpiresAt: &past}))
	assert.True(t, isShareActive(Share{Revoked: false, ExpiresAt: &future}))
}

func TestCanViewAssetOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.True(t, h.canViewAsset(w, req, "a1", asset, &User{UserID: "u1"}))
}

func TestCanViewAssetShared(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1"}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.True(t, h.canViewAsset(w, req, "a1", asset, &User{UserID: "u1"}))
}

func TestCanViewAssetDenied(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1"}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{}},
		&mockS3Client{}, nil,
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.canViewAsset(w, req, "a1", asset, &User{UserID: "u1"}))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCanViewAssetDBError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1"}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{}, nil,
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.canViewAsset(w, req, "a1", asset, &User{UserID: "u1"}))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCanEditAssetOwner(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.True(t, h.canEditAsset(w, req, "a1", asset, &User{UserID: "u1"}))
}

func TestCanEditAssetEditor(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1"}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionEditor, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.True(t, h.canEditAsset(w, req, "a1", asset, &User{UserID: "u1"}))
}

func TestCanEditAssetViewerDenied(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1"}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.canEditAsset(w, req, "a1", asset, &User{UserID: "u1"}))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCanEditAssetDBError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1"}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{}, nil,
	)
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.canEditAsset(w, req, "a1", asset, &User{UserID: "u1"}))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestResolveSharePermission(t *testing.T) {
	perm, err := resolveSharePermission(createShareRequest{}, "user@example.com")
	assert.NoError(t, err)
	assert.Equal(t, PermissionViewer, perm)

	perm, err = resolveSharePermission(createShareRequest{Permission: "editor", SharedWithUserID: "u1"}, "")
	assert.NoError(t, err)
	assert.Equal(t, PermissionEditor, perm)

	_, err = resolveSharePermission(createShareRequest{Permission: "admin"}, "")
	assert.Error(t, err)

	// Public link forced to viewer even if editor requested.
	perm, err = resolveSharePermission(createShareRequest{Permission: "editor"}, "")
	assert.NoError(t, err)
	assert.Equal(t, PermissionViewer, perm)
}

// --- Me handler tests ---

func TestGetMeSuccess(t *testing.T) {
	user := &User{UserID: "user-42", Roles: []string{"dp_admin", "analyst"}}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
		S3Bucket:   "test-bucket",
		AdminRoles: []string{"dp_admin"},
	}, testAuthMiddleware(user))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp meResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "user-42", resp.UserID)
	assert.True(t, resp.IsAdmin)
	assert.Contains(t, resp.Roles, "dp_admin")
}

func TestGetMeNonAdminWithPrefixedRoles(t *testing.T) {
	// Verify that roles like "dp_analyst" do NOT match admin when AdminRoles is ["dp_admin"]
	user := &User{UserID: "user-99", Roles: []string{"dp_analyst"}}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
		AdminRoles: []string{"dp_admin"},
	}, testAuthMiddleware(user))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp meResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.IsAdmin)
}

func TestGetMeNonAdmin(t *testing.T) {
	user := &User{UserID: "user-99", Roles: []string{"analyst"}}
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp meResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "user-99", resp.UserID)
	assert.False(t, resp.IsAdmin)
}

func TestGetMeNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Activity handler tests ---

type mockAuditMetrics struct {
	overviewResult       *audit.Overview
	overviewErr          error
	timeseriesResult     []audit.TimeseriesBucket
	timeseriesErr        error
	breakdownResult      []audit.BreakdownEntry
	breakdownErr         error
	lastOverviewFilter   audit.MetricsFilter
	lastTimeseriesFilter audit.TimeseriesFilter
	lastBreakdownFilter  audit.BreakdownFilter
}

func (m *mockAuditMetrics) Overview(_ context.Context, f audit.MetricsFilter) (*audit.Overview, error) {
	m.lastOverviewFilter = f
	return m.overviewResult, m.overviewErr
}

func (m *mockAuditMetrics) Timeseries(_ context.Context, f audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error) {
	m.lastTimeseriesFilter = f
	return m.timeseriesResult, m.timeseriesErr
}

func (m *mockAuditMetrics) Breakdown(_ context.Context, f audit.BreakdownFilter) ([]audit.BreakdownEntry, error) {
	m.lastBreakdownFilter = f
	return m.breakdownResult, m.breakdownErr
}

var _ AuditMetrics = (*mockAuditMetrics)(nil)

func newActivityTestHandler(metrics *mockAuditMetrics, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:   &mockAssetStore{},
		ShareStore:   &mockShareStore{},
		AuditMetrics: metrics,
		RateLimit:    RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
}

func TestActivityOverview_Success(t *testing.T) {
	metrics := &mockAuditMetrics{
		overviewResult: &audit.Overview{
			TotalCalls:  42,
			SuccessRate: 0.95,
		},
	}
	user := &User{UserID: "user-1", Email: "test@example.com"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/overview?start_time=2024-01-01T00:00:00Z", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "user-1", metrics.lastOverviewFilter.UserID)
	assert.NotNil(t, metrics.lastOverviewFilter.StartTime)

	var result audit.Overview
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	assert.Equal(t, 42, result.TotalCalls)
}

func TestActivityOverview_Unauthenticated(t *testing.T) {
	metrics := &mockAuditMetrics{}
	h := newActivityTestHandler(metrics, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/overview", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestActivityOverview_Error(t *testing.T) {
	metrics := &mockAuditMetrics{overviewErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/overview", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestActivityTimeseries_Success(t *testing.T) {
	now := time.Now()
	metrics := &mockAuditMetrics{
		timeseriesResult: []audit.TimeseriesBucket{
			{Bucket: now, Count: 10, SuccessCount: 9, ErrorCount: 1},
		},
	}
	user := &User{UserID: "user-2"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/timeseries?resolution=hour", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "user-2", metrics.lastTimeseriesFilter.UserID)
	assert.Equal(t, audit.ResolutionHour, metrics.lastTimeseriesFilter.Resolution)
}

func TestActivityTimeseries_DefaultResolution(t *testing.T) {
	metrics := &mockAuditMetrics{timeseriesResult: []audit.TimeseriesBucket{}}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/timeseries", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, audit.ResolutionHour, metrics.lastTimeseriesFilter.Resolution)
}

func TestActivityTimeseries_Error(t *testing.T) {
	metrics := &mockAuditMetrics{timeseriesErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/timeseries", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestActivityTimeseries_InvalidResolution(t *testing.T) {
	metrics := &mockAuditMetrics{}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/timeseries?resolution=invalid", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestActivityBreakdown_Success(t *testing.T) {
	metrics := &mockAuditMetrics{
		breakdownResult: []audit.BreakdownEntry{
			{Dimension: "trino_query", Count: 20, SuccessRate: 1.0},
		},
	}
	user := &User{UserID: "user-3"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/breakdown?group_by=tool_name&limit=5", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "user-3", metrics.lastBreakdownFilter.UserID)
	assert.Equal(t, audit.BreakdownByToolName, metrics.lastBreakdownFilter.GroupBy)
	assert.Equal(t, 5, metrics.lastBreakdownFilter.Limit)
}

func TestActivityBreakdown_Error(t *testing.T) {
	metrics := &mockAuditMetrics{breakdownErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/breakdown", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestActivityBreakdown_DefaultGroupBy(t *testing.T) {
	metrics := &mockAuditMetrics{breakdownResult: []audit.BreakdownEntry{}}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/breakdown", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, audit.BreakdownByToolName, metrics.lastBreakdownFilter.GroupBy)
}

func TestActivityBreakdown_InvalidGroupBy(t *testing.T) {
	metrics := &mockAuditMetrics{}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/breakdown?group_by=invalid", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestActivityTimeseries_Unauthenticated(t *testing.T) {
	metrics := &mockAuditMetrics{}
	h := newActivityTestHandler(metrics, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/timeseries", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestActivityBreakdown_Unauthenticated(t *testing.T) {
	metrics := &mockAuditMetrics{}
	h := newActivityTestHandler(metrics, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/breakdown", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestActivityOverview_WithTimeParams(t *testing.T) {
	metrics := &mockAuditMetrics{overviewResult: &audit.Overview{TotalCalls: 5}}
	user := &User{UserID: "user-1"}
	h := newActivityTestHandler(metrics, user)

	// Valid time param + invalid time param (should be treated as nil).
	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/activity/overview?start_time=2026-01-01T00:00:00Z&end_time=not-a-time", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, metrics.lastOverviewFilter.StartTime)
	assert.Nil(t, metrics.lastOverviewFilter.EndTime) // invalid string → nil
}

func TestActivityNotRegisteredWithoutMetrics(t *testing.T) {
	// When AuditMetrics is nil, activity routes should not be registered.
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "user-1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/activity/overview", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Without activity routes, the mux should return 404.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- Knowledge handler tests ---

type mockInsightStore struct {
	listResult  []knowledge.Insight
	listTotal   int
	listErr     error
	statsResult *knowledge.InsightStats
	statsErr    error
	lastFilter  knowledge.InsightFilter
}

func (m *mockInsightStore) List(_ context.Context, f knowledge.InsightFilter) ([]knowledge.Insight, int, error) {
	m.lastFilter = f
	return m.listResult, m.listTotal, m.listErr
}

func (m *mockInsightStore) Stats(_ context.Context, f knowledge.InsightFilter) (*knowledge.InsightStats, error) {
	m.lastFilter = f
	return m.statsResult, m.statsErr
}

var _ InsightReader = (*mockInsightStore)(nil)

func newKnowledgeTestHandler(store *mockInsightStore, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:   &mockAssetStore{},
		ShareStore:   &mockShareStore{},
		InsightStore: store,
	}, testAuthMiddleware(user))
}

func TestListMyInsights_Success(t *testing.T) {
	store := &mockInsightStore{
		listResult: []knowledge.Insight{{ID: "ins-1", CapturedBy: "user-1", Status: "pending"}},
		listTotal:  1,
	}
	user := &User{UserID: "user-1"}
	h := newKnowledgeTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights?status=pending&limit=10", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "user-1", store.lastFilter.CapturedBy)
	assert.Equal(t, "pending", store.lastFilter.Status)
	assert.Equal(t, 10, store.lastFilter.Limit)
}

func TestListMyInsights_Unauthenticated(t *testing.T) {
	store := &mockInsightStore{}
	h := newKnowledgeTestHandler(store, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListMyInsights_Error(t *testing.T) {
	store := &mockInsightStore{listErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1"}
	h := newKnowledgeTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListMyInsights_EmptyResult(t *testing.T) {
	store := &mockInsightStore{listResult: nil, listTotal: 0}
	user := &User{UserID: "user-1"}
	h := newKnowledgeTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp paginatedResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Total)
}

func TestGetMyInsightStats_Success(t *testing.T) {
	store := &mockInsightStore{
		statsResult: &knowledge.InsightStats{
			TotalPending: 3,
			ByStatus:     map[string]int{"pending": 3, "approved": 1},
			ByCategory:   map[string]int{"correction": 2},
			ByConfidence: map[string]int{"high": 1},
		},
	}
	user := &User{UserID: "user-1"}
	h := newKnowledgeTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights/stats", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "user-1", store.lastFilter.CapturedBy)
}

func TestGetMyInsightStats_Unauthenticated(t *testing.T) {
	store := &mockInsightStore{}
	h := newKnowledgeTestHandler(store, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights/stats", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetMyInsightStats_Error(t *testing.T) {
	store := &mockInsightStore{statsErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1"}
	h := newKnowledgeTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights/stats", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestKnowledgeNotRegisteredWithoutStore(t *testing.T) {
	// When InsightStore is nil, knowledge routes should not be registered.
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "user-1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/knowledge/insights", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetMe_WithPersonaResolver(t *testing.T) {
	user := &User{UserID: "user-1", Roles: []string{"analyst"}}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		PersonaResolver: func(roles []string) *PersonaInfo {
			if len(roles) > 0 && roles[0] == "analyst" {
				return &PersonaInfo{Name: "analyst", Tools: []string{"trino_query", "datahub_search"}}
			}
			return nil
		},
	}, testAuthMiddleware(user))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/me", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp meResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "analyst", resp.Persona)
	assert.Contains(t, resp.Tools, "trino_query")
	assert.Contains(t, resp.Tools, "datahub_search")
}

// --- DeriveThumbnailKey ---

func TestDeriveThumbnailKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"portal/owner/asset/content.html", "portal/owner/asset/thumbnail.png"},
		{"portal/owner/asset/dashboard.jsx", "portal/owner/asset/thumbnail.png"},
		{"simple.html", "thumbnail.png"},
		{"a/b/c", "a/b/thumbnail.png"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, DeriveThumbnailKey(tt.input))
		})
	}
}

// --- uploadThumbnail ---

func TestUploadThumbnailSuccess(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", S3Bucket: "b", S3Key: "portal/u1/a1/content.html",
		ContentType: "text/html", Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockS3Client{}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, s3, &User{UserID: "u1"})

	body := strings.NewReader(strings.Repeat("x", 100))
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail", body)
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUploadThumbnailUnauth(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUploadThumbnailNotOwner(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "other-user", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUploadThumbnailWrongContentType(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/jpeg")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadThumbnailTooLarge(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	oversize := strings.Repeat("x", MaxThumbnailUploadBytes+1)
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader(oversize))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestUploadThumbnailAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUploadThumbnailNoS3(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	user := &User{UserID: "u1"}
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{},
		S3Client:      nil, // true nil interface
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
		RateLimit:     RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUploadThumbnailS3Error(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "portal/u1/a1/c.html",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockS3Client{putErr: fmt.Errorf("s3 fail")}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, s3, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUploadThumbnailUpdateError(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "portal/u1/a1/c.html",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, updateErr: fmt.Errorf("db fail")},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUploadThumbnailDeletedAsset(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", DeletedAt: &now,
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

// --- getThumbnail ---

func TestGetThumbnailSuccess(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "portal/u1/a1/thumbnail.png",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockS3Client{getData: []byte("PNG-DATA"), getCT: "image/png"}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, s3, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "private, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Equal(t, "PNG-DATA", w.Body.String())
}

func TestGetThumbnailNoThumbnail(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetThumbnailUnauth(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetThumbnailNotOwnerNotShared(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "other-user", S3Bucket: "b", ThumbnailS3Key: "thumb.png",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{}},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetThumbnailAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetThumbnailNoS3(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "thumb.png",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	user := &User{UserID: "u1"}
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{},
		S3Client:      nil, // true nil interface
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
		RateLimit:     RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetThumbnailS3Error(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "thumb.png",
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockS3Client{getErr: fmt.Errorf("s3 fail")}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, s3, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetThumbnailDeletedAsset(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "thumb.png", DeletedAt: &now,
		Tags: []string{}, Provenance: Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

// --- Permission tests ---

func TestValidSharePermission(t *testing.T) {
	assert.True(t, ValidSharePermission("viewer"))
	assert.True(t, ValidSharePermission("editor"))
	assert.False(t, ValidSharePermission("admin"))
	assert.False(t, ValidSharePermission(""))
}

func TestCreateShareWithPermission(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	var captured Share
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &captureShareStore{inner: &mockShareStore{}, captured: &captured},
		S3Client:      &mockS3Client{},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	body := `{"shared_with_email":"user@example.com","permission":"editor"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, PermissionEditor, captured.Permission)
}

func TestCreateShareDefaultPermission(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	var captured Share
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &captureShareStore{inner: &mockShareStore{}, captured: &captured},
		S3Client:      &mockS3Client{},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	body := `{"shared_with_email":"user@example.com"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, PermissionViewer, captured.Permission)
}

func TestCreateShareInvalidPermission(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	body := `{"shared_with_email":"user@example.com","permission":"admin"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateSharePublicLinkAlwaysViewer(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	var captured Share
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &captureShareStore{inner: &mockShareStore{}, captured: &captured},
		S3Client:      &mockS3Client{},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	body := `{"expires_in":"24h","permission":"editor"}`
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, PermissionViewer, captured.Permission, "public links must always be viewer")
}

// --- updateAssetContent with editor permission ---

func TestUpdateAssetContentEditor(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1", S3Bucket: "b", S3Key: "k", ContentType: "text/html", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionEditor, Revoked: false},
		}},
		&mockVersionStore{createVersion: 2},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content", strings.NewReader("new content"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateAssetContentViewerDenied(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1", S3Bucket: "b", S3Key: "k", ContentType: "text/html"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content", strings.NewReader("new content"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// --- sharePermissionForUser ---

func TestSharePermissionForUserEditor(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false},
			{ID: "s2", SharedWithUserID: "u1", Permission: PermissionEditor, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "u1"})
	assert.NoError(t, err)
	assert.Equal(t, PermissionEditor, perm)
}

func TestSharePermissionForUserNotShared(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "u1"})
	assert.NoError(t, err)
	assert.Equal(t, SharePermission(""), perm)
}

func TestSharePermissionForUserByEmail(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithEmail: "u1@example.com", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "other", Email: "u1@example.com"})
	assert.NoError(t, err)
	assert.Equal(t, PermissionViewer, perm)
}

func TestSharePermissionForUserEditorByEmail(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithEmail: "u1@example.com", Permission: PermissionEditor, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "other", Email: "u1@example.com"})
	assert.NoError(t, err)
	assert.Equal(t, PermissionEditor, perm)
}

func TestSharePermissionForUserEmailCaseInsensitive(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithEmail: "User@Example.COM", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "other", Email: "user@example.com"})
	assert.NoError(t, err)
	assert.Equal(t, PermissionViewer, perm)
}

func TestSharePermissionForUserEmptyEmailNoMatch(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithEmail: "user@example.com", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "other", Email: ""})
	assert.NoError(t, err)
	assert.Equal(t, SharePermission(""), perm)
}

func TestSharePermissionForUserError(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "u1"})
	assert.Error(t, err)
	assert.Equal(t, SharePermission(""), perm)
}

func TestSharePermissionForUserSkipsRevoked(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionEditor, Revoked: true},
			{ID: "s2", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "u1"})
	assert.NoError(t, err)
	assert.Equal(t, PermissionViewer, perm)
}

func TestSharePermissionForUserSkipsExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionEditor, Revoked: false, ExpiresAt: &past},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "u1"})
	assert.NoError(t, err)
	assert.Equal(t, SharePermission(""), perm)
}

func TestSharePermissionForUserSkipsWrongUser(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u2", Permission: PermissionEditor, Revoked: false},
		}},
		&mockS3Client{}, nil,
	)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	perm, err := h.sharePermissionForUser(req, "a1", &User{UserID: "u1"})
	assert.NoError(t, err)
	assert.Equal(t, SharePermission(""), perm)
}

// --- copyAsset ---

func TestCopyAssetSuccess(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "owner1", Name: "Test", Description: "desc",
		ContentType: "text/html", S3Bucket: "b", S3Key: "k", SizeBytes: 5,
		Tags: []string{"tag1"}, Provenance: Provenance{SessionID: "s1"},
		CreatedAt: now, UpdatedAt: now,
	}
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{listByAsset: []Share{{ID: "s1", SharedWithUserID: "u1", Permission: PermissionViewer, Revoked: false}}},
		S3Client:      &mockS3Client{getData: []byte("hello"), getCT: "text/html"},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1", Email: "u1@example.com"}))

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var result Asset
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, "u1", result.OwnerID)
	assert.Equal(t, "u1@example.com", result.OwnerEmail)
	assert.Equal(t, "Test (copy)", result.Name)
	assert.Equal(t, "desc", result.Description)
	assert.Equal(t, "text/html", result.ContentType)
	assert.Equal(t, "test-bucket", result.S3Bucket)
	assert.Contains(t, result.S3Key, "portal/u1/")
	assert.Equal(t, int64(5), result.SizeBytes)
}

func TestCopyAssetOwnerCanCopy(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "Mine", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", Tags: []string{}, Provenance: Provenance{},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{},
		S3Client:      &mockS3Client{getData: []byte("data"), getCT: "text/html"},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCopyAssetAccessDenied(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{}},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCopyAssetNoUser(t *testing.T) {
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCopyAssetNotFound(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCopyAssetDeleted(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", DeletedAt: &now}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestCopyAssetNoS3(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := NewHandler(Deps{
		AssetStore:    &mockAssetStore{getAsset: asset},
		ShareStore:    &mockShareStore{},
		PublicBaseURL: "https://example.com",
	}, testAuthMiddleware(&User{UserID: "u1"}))

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestCopyAssetS3ReadError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{getErr: fmt.Errorf("s3 fail")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCopyAssetS3WriteError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{getData: []byte("data"), getCT: "text/html", putErr: fmt.Errorf("s3 fail")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestCopyAssetInsertError(t *testing.T) {
	asset := &Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: Provenance{},
	}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset, insertErr: fmt.Errorf("db fail")},
		&mockShareStore{},
		&mockS3Client{getData: []byte("data"), getCT: "text/html"},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCopyAssetTooLarge(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", SizeBytes: MaxContentUploadBytes + 1}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestCopyAssetDBErrorOnShareCheck(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/copy", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAssetContentDBErrorOnShareCheck(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1", S3Bucket: "b", S3Key: "k"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateAssetContentDBErrorOnShareCheck(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "owner1", S3Bucket: "b", S3Key: "k", ContentType: "text/html"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content", strings.NewReader("content"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAssetDBErrorOnShareCheck(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "owner1", Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- getAsset response enrichment ---

func TestGetAssetOwnerResponse(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp assetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.IsOwner)
	assert.Equal(t, SharePermission(""), resp.SharePermission)
}

func TestGetAssetSharedEditorResponse(t *testing.T) {
	now := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "other", Tags: []string{}, CreatedAt: now, UpdatedAt: now}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{listByAsset: []Share{
			{ID: "s1", SharedWithUserID: "u1", Permission: PermissionEditor, Revoked: false},
		}},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp assetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.IsOwner)
	assert.Equal(t, PermissionEditor, resp.SharePermission)
}

// --- Version handler tests ---

func TestListVersionsSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 2}
	versions := []AssetVersion{
		{ID: "v2", AssetID: "a1", Version: 2, S3Key: "k2", S3Bucket: "b"},
		{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b"},
	}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{listVersions: versions, listTotal: 2},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var pr paginatedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &pr))
	assert.Equal(t, 2, pr.Total)
}

func TestListVersionsNoStore(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1"}
	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{}, &mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetVersionContentSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 2}
	ver := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getVersion: ver},
		&mockS3Client{getData: []byte("<html>v1</html>"), getCT: "text/html"},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html", w.Header().Get("Content-Type"))
	assert.Equal(t, "<html>v1</html>", w.Body.String())
}

func TestGetVersionContentNotFound(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 2}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getErr: fmt.Errorf("not found")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/99/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevertToVersionSuccess(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2}
	targetVer := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getVersion: targetVer, createVersion: 3},
		&mockS3Client{getData: []byte("<html>v1</html>"), getCT: "text/html"},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, "reverted", result["status"])
	assert.Equal(t, float64(3), result["version"])
}

func TestRevertToVersionNotFound(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getErr: fmt.Errorf("not found")},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/99/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevertToVersionForbidden(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "other-user", S3Bucket: "b", CurrentVersion: 2}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestUpdateAssetContentCreatesVersion(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", ContentType: "text/html", CurrentVersion: 1}
	vs := &mockVersionStore{}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		vs,
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("new content"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateAssetContentChangeSummaryHeader(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", ContentType: "text/html", CurrentVersion: 1}

	t.Run("with header", func(t *testing.T) {
		vs := &mockVersionStore{}
		h := newTestHandlerWithVersions(
			&mockAssetStore{getAsset: asset},
			&mockShareStore{},
			vs,
			&mockS3Client{},
			&User{UserID: "u1", Email: "user@example.com"},
		)

		req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
			strings.NewReader("updated"))
		req.Header.Set("X-Change-Summary", "Fixed typo in heading")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, vs.lastCreated)
		assert.Equal(t, "Fixed typo in heading", vs.lastCreated.ChangeSummary)
	})

	t.Run("without header uses default", func(t *testing.T) {
		vs := &mockVersionStore{}
		h := newTestHandlerWithVersions(
			&mockAssetStore{getAsset: asset},
			&mockShareStore{},
			vs,
			&mockS3Client{},
			&User{UserID: "u1", Email: "user@example.com"},
		)

		req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
			strings.NewReader("updated"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, vs.lastCreated)
		assert.Equal(t, "Content updated", vs.lastCreated.ChangeSummary)
	})

	t.Run("long header truncated", func(t *testing.T) {
		vs := &mockVersionStore{}
		h := newTestHandlerWithVersions(
			&mockAssetStore{getAsset: asset},
			&mockShareStore{},
			vs,
			&mockS3Client{},
			&User{UserID: "u1", Email: "user@example.com"},
		)

		longSummary := strings.Repeat("x", MaxChangeSummaryLength+100)
		req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
			strings.NewReader("updated"))
		req.Header.Set("X-Change-Summary", longSummary)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, vs.lastCreated)
		assert.Equal(t, MaxChangeSummaryLength, len(vs.lastCreated.ChangeSummary))
	})

	t.Run("whitespace-only header uses default", func(t *testing.T) {
		vs := &mockVersionStore{}
		h := newTestHandlerWithVersions(
			&mockAssetStore{getAsset: asset},
			&mockShareStore{},
			vs,
			&mockS3Client{},
			&User{UserID: "u1", Email: "user@example.com"},
		)

		req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
			strings.NewReader("updated"))
		req.Header.Set("X-Change-Summary", "   ")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, vs.lastCreated)
		assert.Equal(t, "Content updated", vs.lastCreated.ChangeSummary)
	})
}

func TestListVersionsAssetNotFound(t *testing.T) {
	h := newTestHandlerWithVersions(
		&mockAssetStore{getErr: fmt.Errorf("not found")},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListVersionsDeleted(t *testing.T) {
	deleted := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", DeletedAt: &deleted}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusGone, w.Code)
}

func TestListVersionsError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{listErr: fmt.Errorf("db error")},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListVersionsUnauthorized(t *testing.T) {
	h := newTestHandlerWithVersions(
		&mockAssetStore{},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetVersionContentDeleted(t *testing.T) {
	deleted := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", DeletedAt: &deleted}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusGone, w.Code)
}

func TestGetVersionContentNoStorage(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		nil,
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetVersionContentInvalidVersion(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/abc/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersionContentS3Error(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 2}
	ver := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b"}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getVersion: ver},
		&mockS3Client{getErr: fmt.Errorf("s3 error")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetVersionContentUnauthorized(t *testing.T) {
	h := newTestHandlerWithVersions(
		&mockAssetStore{},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRevertToVersionDeleted(t *testing.T) {
	deleted := time.Now()
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", DeletedAt: &deleted}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusGone, w.Code)
}

func TestRevertToVersionNoStorage(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		nil,
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRevertToVersionInvalidVersion(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 1}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, &User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/abc/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRevertToVersionUnauthorized(t *testing.T) {
	h := newTestHandlerWithVersions(
		&mockAssetStore{},
		&mockShareStore{},
		&mockVersionStore{},
		&mockS3Client{}, nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRevertToVersionS3ReadError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2}
	targetVer := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getVersion: targetVer},
		&mockS3Client{getErr: fmt.Errorf("s3 error")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRevertToVersionS3PutError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2}
	targetVer := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getVersion: targetVer},
		&mockS3Client{getData: []byte("data"), putErr: fmt.Errorf("s3 put error")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRevertToVersionCreateVersionError(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2}
	targetVer := &AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{getVersion: targetVer, createErr: fmt.Errorf("db error")},
		&mockS3Client{getData: []byte("data")},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateAssetContentCreateVersionErrorCleansUpS3(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", ContentType: "text/html", CurrentVersion: 1}
	s3 := &mockS3Client{}
	h := newTestHandlerWithVersions(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockVersionStore{createErr: fmt.Errorf("db error")},
		s3,
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/assets/a1/content",
		strings.NewReader("new content"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestVersionedExtension(t *testing.T) {
	tests := []struct {
		ct   string
		want string
	}{
		{"text/html", ".html"},
		{"text/jsx", ".html"},
		{"image/svg+xml", ".svg"},
		{"text/markdown", ".md"},
		{"application/json", ".json"},
		{"text/csv", ".csv"},
		{"application/octet-stream", ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			assert.Equal(t, tt.want, ExtensionForContentType(tt.ct))
		})
	}
}

// --- Memory mock and tests ---

type mockMemoryStore struct {
	listResult []memory.Record
	listTotal  int
	listErr    error
	lastFilter memory.Filter
}

func (m *mockMemoryStore) List(_ context.Context, f memory.Filter) ([]memory.Record, int, error) {
	m.lastFilter = f
	return m.listResult, m.listTotal, m.listErr
}

var _ MemoryReader = (*mockMemoryStore)(nil)

func newMemoryTestHandler(store *mockMemoryStore, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:  &mockAssetStore{},
		ShareStore:  &mockShareStore{},
		MemoryStore: store,
	}, testAuthMiddleware(user))
}

func TestListMyMemories_Success(t *testing.T) {
	store := &mockMemoryStore{
		listResult: []memory.Record{
			{ID: "mem-1", CreatedBy: "alice@example.com", Dimension: "knowledge", Category: "correction", Status: "active"},
		},
		listTotal: 1,
	}
	user := &User{UserID: "user-1", Email: "alice@example.com"}
	h := newMemoryTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/memory/records?dimension=knowledge&category=correction&status=active&source=user&limit=10", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "alice@example.com", store.lastFilter.CreatedBy)
	assert.Equal(t, "knowledge", store.lastFilter.Dimension)
	assert.Equal(t, "correction", store.lastFilter.Category)
	assert.Equal(t, "active", store.lastFilter.Status)
	assert.Equal(t, "user", store.lastFilter.Source)
	assert.Equal(t, 10, store.lastFilter.Limit)
}

func TestListMyMemories_Unauthenticated(t *testing.T) {
	store := &mockMemoryStore{}
	h := newMemoryTestHandler(store, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListMyMemories_Error(t *testing.T) {
	store := &mockMemoryStore{listErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1", Email: "alice@example.com"}
	h := newMemoryTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListMyMemories_EmptyResult(t *testing.T) {
	store := &mockMemoryStore{listResult: nil, listTotal: 0}
	user := &User{UserID: "user-1", Email: "alice@example.com"}
	h := newMemoryTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp paginatedResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Total)
}

func TestGetMyMemoryStats_Success(t *testing.T) {
	store := &mockMemoryStore{
		listResult: []memory.Record{
			{Dimension: "knowledge", Category: "correction", Status: "active"},
			{Dimension: "knowledge", Category: "business_context", Status: "active"},
			{Dimension: "event", Category: "correction", Status: "stale"},
		},
		listTotal: 3,
	}
	user := &User{UserID: "user-1", Email: "alice@example.com"}
	h := newMemoryTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records/stats", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "alice@example.com", store.lastFilter.CreatedBy)

	var stats memoryStatsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stats))
	assert.Equal(t, 3, stats.Total)
	assert.Equal(t, 2, stats.ByDimension["knowledge"])
	assert.Equal(t, 1, stats.ByDimension["event"])
	assert.Equal(t, 2, stats.ByCategory["correction"])
	assert.Equal(t, 1, stats.ByCategory["business_context"])
	assert.Equal(t, 2, stats.ByStatus["active"])
	assert.Equal(t, 1, stats.ByStatus["stale"])
}

func TestGetMyMemoryStats_Unauthenticated(t *testing.T) {
	store := &mockMemoryStore{}
	h := newMemoryTestHandler(store, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records/stats", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetMyMemoryStats_Error(t *testing.T) {
	store := &mockMemoryStore{listErr: fmt.Errorf("db error")}
	user := &User{UserID: "user-1", Email: "alice@example.com"}
	h := newMemoryTestHandler(store, user)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records/stats", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestMemoryNotRegisteredWithoutStore(t *testing.T) {
	// When MemoryStore is nil, memory routes should not be registered.
	h := newTestHandler(&mockAssetStore{}, &mockShareStore{}, &mockS3Client{}, &User{UserID: "user-1"})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/memory/records", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
