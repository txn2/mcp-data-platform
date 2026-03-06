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

func (m *mockShareStore) ListSharedWithUser(_ context.Context, _ string, _, _ int) ([]SharedAsset, int, error) {
	return m.sharedWithRes, m.sharedWithTot, m.sharedWithErr
}
func (m *mockShareStore) Revoke(_ context.Context, _ string) error          { return m.revokeErr }
func (m *mockShareStore) IncrementAccess(_ context.Context, _ string) error { return m.incrementErr }

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
	return NewHandler(Deps{
		AssetStore:    assets,
		ShareStore:    shares,
		S3Client:      s3,
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
		RateLimit:     RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
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
			{ID: "s1", SharedWithUserID: "u1", Revoked: false},
		}},
		&mockS3Client{},
		&User{UserID: "u1"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
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

func TestIsSharedWithUserTrue(t *testing.T) {
	shares := []Share{
		{ID: "s1", SharedWithUserID: "u1", Revoked: false},
	}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: shares},
		&mockS3Client{},
		nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.True(t, h.isSharedWithUser(req, "a1", "u1"))
}

func TestIsSharedWithUserRevoked(t *testing.T) {
	shares := []Share{
		{ID: "s1", SharedWithUserID: "u1", Revoked: true},
	}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: shares},
		&mockS3Client{},
		nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.isSharedWithUser(req, "a1", "u1"))
}

func TestIsSharedWithUserExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	shares := []Share{
		{ID: "s1", SharedWithUserID: "u1", Revoked: false, ExpiresAt: &past},
	}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: shares},
		&mockS3Client{},
		nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.isSharedWithUser(req, "a1", "u1"))
}

func TestIsSharedWithUserWrongUser(t *testing.T) {
	shares := []Share{
		{ID: "s1", SharedWithUserID: "u2", Revoked: false},
	}
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAsset: shares},
		&mockS3Client{},
		nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.isSharedWithUser(req, "a1", "u1"))
}

func TestIsSharedWithUserError(t *testing.T) {
	h := newTestHandler(
		&mockAssetStore{},
		&mockShareStore{listByAssetE: fmt.Errorf("db error")},
		&mockS3Client{},
		nil,
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	assert.False(t, h.isSharedWithUser(req, "a1", "u1"))
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
