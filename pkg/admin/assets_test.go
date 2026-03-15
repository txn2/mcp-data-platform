package admin

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

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// --- mock stores for admin asset tests ---

type mockAdminAssetStore struct {
	insertErr  error
	getAsset   *portal.Asset
	getErr     error
	listRes    []portal.Asset
	listTotal  int
	listErr    error
	updateErr  error
	deleteErr  error
	lastUpdate *portal.AssetUpdate // captures the most recent Update call
}

func (m *mockAdminAssetStore) Insert(_ context.Context, _ portal.Asset) error { return m.insertErr }
func (m *mockAdminAssetStore) Get(_ context.Context, _ string) (*portal.Asset, error) {
	return m.getAsset, m.getErr
}

func (m *mockAdminAssetStore) List(_ context.Context, _ portal.AssetFilter) ([]portal.Asset, int, error) {
	return m.listRes, m.listTotal, m.listErr
}

func (m *mockAdminAssetStore) Update(_ context.Context, _ string, u portal.AssetUpdate) error {
	m.lastUpdate = &u
	return m.updateErr
}
func (m *mockAdminAssetStore) SoftDelete(_ context.Context, _ string) error { return m.deleteErr }

type mockAdminShareStore struct {
	summaries    map[string]portal.ShareSummary
	summariesErr error
}

func (*mockAdminShareStore) Insert(_ context.Context, _ portal.Share) error { return nil }
func (*mockAdminShareStore) GetByID(_ context.Context, _ string) (*portal.Share, error) {
	return &portal.Share{}, nil
}

func (*mockAdminShareStore) GetByToken(_ context.Context, _ string) (*portal.Share, error) {
	return &portal.Share{}, nil
}

func (*mockAdminShareStore) ListByAsset(_ context.Context, _ string) ([]portal.Share, error) {
	return []portal.Share{}, nil
}

func (*mockAdminShareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]portal.SharedAsset, int, error) {
	return nil, 0, nil
}
func (*mockAdminShareStore) Revoke(_ context.Context, _ string) error          { return nil }
func (*mockAdminShareStore) IncrementAccess(_ context.Context, _ string) error { return nil }
func (m *mockAdminShareStore) ListActiveShareSummaries(_ context.Context, _ []string) (map[string]portal.ShareSummary, error) {
	return m.summaries, m.summariesErr
}

type mockAdminS3Client struct {
	getData   []byte
	getCT     string
	getErr    error
	putErr    error
	deleteErr error
}

func (m *mockAdminS3Client) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return m.putErr
}

func (m *mockAdminS3Client) GetObject(_ context.Context, _, _ string) (body []byte, contentType string, err error) {
	return m.getData, m.getCT, m.getErr
}
func (m *mockAdminS3Client) DeleteObject(_ context.Context, _, _ string) error { return m.deleteErr }
func (*mockAdminS3Client) Close() error                                        { return nil }

type mockAdminVersionStore struct {
	createVersion int
	createErr     error
	listVersions  []portal.AssetVersion
	listTotal     int
	listErr       error
	getVersion    *portal.AssetVersion
	getErr        error
	latestVer     *portal.AssetVersion
	latestErr     error
	lastCreated   *portal.AssetVersion // captures the most recent CreateVersion call
}

func (m *mockAdminVersionStore) CreateVersion(_ context.Context, av portal.AssetVersion) (int, error) {
	m.lastCreated = &av
	return m.createVersion, m.createErr
}

func (m *mockAdminVersionStore) ListByAsset(_ context.Context, _ string, _, _ int) ([]portal.AssetVersion, int, error) {
	return m.listVersions, m.listTotal, m.listErr
}

func (m *mockAdminVersionStore) GetByVersion(_ context.Context, _ string, _ int) (*portal.AssetVersion, error) {
	return m.getVersion, m.getErr
}

func (m *mockAdminVersionStore) GetLatest(_ context.Context, _ string) (*portal.AssetVersion, error) {
	return m.latestVer, m.latestErr
}

func newAdminTestHandler(assets portal.AssetStore, shares portal.ShareStore, s3 portal.S3Client) *Handler {
	return newAdminTestHandlerWithVersions(assets, shares, nil, s3)
}

func newAdminTestHandlerWithVersions(assets portal.AssetStore, shares portal.ShareStore, versions portal.VersionStore, s3 portal.S3Client) *Handler {
	return NewHandler(Deps{
		AssetStore:   assets,
		ShareStore:   shares,
		VersionStore: versions,
		S3Client:     s3,
		S3Bucket:     "test-bucket",
	}, nil)
}

// --- registerAssetRoutes ---

func TestAssetRoutesRegistered(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "test-id", OwnerID: "u1", Name: "Test", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminS3Client{getData: []byte("data"), getCT: "text/plain"},
	)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/assets"},
		{http.MethodGet, "/api/v1/admin/assets/test-id"},
		{http.MethodGet, "/api/v1/admin/assets/test-id/content"},
		{http.MethodDelete, "/api/v1/admin/assets/test-id"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), rt.method, rt.path, http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusNotFound, w.Code,
				"route %s %s should be registered", rt.method, rt.path)
		})
	}
}

func TestAssetRoutesNotRegisteredWithoutStore(t *testing.T) {
	h := NewHandler(Deps{}, nil) // no AssetStore

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Without an asset store, the route should not be registered (404 or 405).
	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed,
		"expected 404/405 without asset store, got %d", w.Code)
}

// --- listAllAssets ---

func TestListAllAssetsSuccess(t *testing.T) {
	now := time.Now()
	assets := &mockAdminAssetStore{
		listRes: []portal.Asset{{
			ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
			Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
		}},
		listTotal: 1,
	}
	h := newAdminTestHandler(assets, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/admin/assets?limit=10&offset=0&search=Test", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp adminAssetListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "a1", resp.Data[0].ID)
}

func TestListAllAssetsWithShareSummaries(t *testing.T) {
	now := time.Now()
	assets := &mockAdminAssetStore{
		listRes: []portal.Asset{{
			ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
			Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
		}},
		listTotal: 1,
	}
	shares := &mockAdminShareStore{
		summaries: map[string]portal.ShareSummary{
			"a1": {HasUserShare: true, HasPublicLink: true},
		},
	}
	h := newAdminTestHandler(assets, shares, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp adminAssetListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp.ShareSummaries, "a1")
	assert.True(t, resp.ShareSummaries["a1"].HasUserShare)
	assert.True(t, resp.ShareSummaries["a1"].HasPublicLink)
}

func TestListAllAssetsStoreError(t *testing.T) {
	assets := &mockAdminAssetStore{listErr: fmt.Errorf("db error")}
	h := newAdminTestHandler(assets, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListAllAssetsNilResult(t *testing.T) {
	assets := &mockAdminAssetStore{listRes: nil, listTotal: 0}
	h := newAdminTestHandler(assets, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp adminAssetListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Empty(t, resp.Data)
}

// --- getAdminAsset ---

func TestGetAdminAssetSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp portal.Asset
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "a1", resp.ID)
}

func TestGetAdminAssetNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- getAdminAssetContent ---

func TestGetAdminAssetContentSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{getData: []byte("<html>Hello</html>"), getCT: "text/html"}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html", w.Header().Get("Content-Type"))
	assert.Equal(t, "<html>Hello</html>", w.Body.String())
}

func TestGetAdminAssetContentNoS3(t *testing.T) {
	h := newAdminTestHandler(&mockAdminAssetStore{}, &mockAdminShareStore{}, nil)

	// With nil S3Client, routes that need S3 should still be registered
	// but the content endpoint should return 503
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetAdminAssetContentAssetNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAdminAssetContentS3Error(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{getErr: fmt.Errorf("s3 error")}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAdminAssetContentDefaultContentType(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{getData: []byte("binary"), getCT: ""} // empty content type
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
}

// --- updateAdminAsset ---

func TestUpdateAdminAssetSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	body := `{"name": "Updated Name", "description": "New desc", "tags": ["tag1"]}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "updated", resp.Status)
}

func TestUpdateAdminAssetNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	body := `{"name": "Updated"}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/missing",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateAdminAssetInvalidBody(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1",
		strings.NewReader("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAdminAssetValidationError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	// Empty name should fail validation
	body := `{"name": ""}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateAdminAssetStoreError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset, updateErr: fmt.Errorf("db error")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	body := `{"name": "Valid Name"}`
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- updateAdminAssetContent ---

func TestUpdateAdminAssetContentSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminVersionStore{createVersion: 2}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("<html>Updated</html>"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "updated", resp.Status)
}

func TestUpdateAdminAssetContentChangeSummaryHeader(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}

	t.Run("with header", func(t *testing.T) {
		vs := &mockAdminVersionStore{createVersion: 2}
		h := newAdminTestHandlerWithVersions(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, vs, &mockAdminS3Client{})

		req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
			strings.NewReader("updated content"))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("X-Change-Summary", "Admin fix: corrected data table")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, vs.lastCreated)
		assert.Equal(t, "Admin fix: corrected data table", vs.lastCreated.ChangeSummary)
	})

	t.Run("without header uses default", func(t *testing.T) {
		vs := &mockAdminVersionStore{createVersion: 2}
		h := newAdminTestHandlerWithVersions(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, vs, &mockAdminS3Client{})

		req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
			strings.NewReader("updated content"))
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, vs.lastCreated)
		assert.Equal(t, "Content updated (admin)", vs.lastCreated.ChangeSummary)
	})
}

func TestUpdateAdminAssetContentNoVersionStore(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", Name: "Test", ContentType: "text/html",
		S3Bucket: "b", S3Key: "k", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("<html>Updated</html>"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateAdminAssetContentNoS3(t *testing.T) {
	h := newAdminTestHandler(&mockAdminAssetStore{}, &mockAdminShareStore{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateAdminAssetContentAssetNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateAdminAssetContentDeleted(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
		DeletedAt: &now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestUpdateAdminAssetContentTooLarge(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	// Send content that exceeds 10 MB
	oversize := strings.Repeat("x", portal.MaxContentUploadBytes+1)
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader(oversize))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestUpdateAdminAssetContentS3Error(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{putErr: fmt.Errorf("s3 error")}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateAdminAssetContentUpdateError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{createErr: fmt.Errorf("db error")},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- deleteAdminAsset ---

func TestDeleteAdminAssetSuccess(t *testing.T) {
	h := newAdminTestHandler(&mockAdminAssetStore{}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/admin/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp statusResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "deleted", resp.Status)
}

func TestDeleteAdminAssetNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{deleteErr: fmt.Errorf("not found")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/admin/assets/missing", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- validateAdminAssetUpdate ---

func TestValidateAdminAssetUpdate(t *testing.T) {
	validName := "Valid Name"
	emptyName := ""
	validDesc := "A description"
	longDesc := string(make([]byte, 2001))
	longTag := string(make([]byte, 101))

	tooManyTags := make([]string, 21)
	for i := range tooManyTags {
		tooManyTags[i] = fmt.Sprintf("tag%d", i)
	}

	tests := []struct {
		name    string
		updates portal.AssetUpdate
		wantErr bool
	}{
		{
			name:    "valid update with name",
			updates: portal.AssetUpdate{Name: &validName},
			wantErr: false,
		},
		{
			name:    "empty name fails",
			updates: portal.AssetUpdate{Name: &emptyName},
			wantErr: true,
		},
		{
			name:    "valid description",
			updates: portal.AssetUpdate{Description: &validDesc},
			wantErr: false,
		},
		{
			name:    "description too long fails",
			updates: portal.AssetUpdate{Description: &longDesc},
			wantErr: true,
		},
		{
			name:    "valid tags",
			updates: portal.AssetUpdate{Tags: []string{"tag1", "tag2"}},
			wantErr: false,
		},
		{
			name:    "too many tags fails",
			updates: portal.AssetUpdate{Tags: tooManyTags},
			wantErr: true,
		},
		{
			name:    "tag too long fails",
			updates: portal.AssetUpdate{Tags: []string{longTag}},
			wantErr: true,
		},
		{
			name:    "no fields is valid",
			updates: portal.AssetUpdate{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAdminAssetUpdate(tt.updates)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- DeriveThumbnailKey (portal package) ---

func TestAdminDeriveThumbnailKeyUsesPortal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"portal/owner/asset/content.html", "portal/owner/asset/thumbnail.png"},
		{"simple.html", "thumbnail.png"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, portal.DeriveThumbnailKey(tt.input))
		})
	}
}

// --- uploadAdminThumbnail ---

func TestUploadAdminThumbnailSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "portal/u1/a1/content.html",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	body := strings.NewReader("PNG-DATA")
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail", body)
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUploadAdminThumbnailNoS3(t *testing.T) {
	h := newAdminTestHandler(&mockAdminAssetStore{}, &mockAdminShareStore{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUploadAdminThumbnailNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUploadAdminThumbnailWrongContentType(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/jpeg")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadAdminThumbnailTooLarge(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	oversize := strings.Repeat("x", portal.MaxThumbnailUploadBytes+1)
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader(oversize))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestUploadAdminThumbnailDeleted(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", DeletedAt: &now,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestUploadAdminThumbnailS3Error(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "portal/u1/a1/c.html",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{putErr: fmt.Errorf("s3 fail")}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUploadAdminThumbnailUpdateError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "portal/u1/a1/c.html",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset, updateErr: fmt.Errorf("db fail")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/thumbnail",
		strings.NewReader("data"))
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- getAdminThumbnail ---

func TestGetAdminThumbnailSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "thumb.png",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{getData: []byte("PNG-DATA"), getCT: "image/png"}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Equal(t, "PNG-DATA", w.Body.String())
}

func TestGetAdminThumbnailDeleted(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "thumb.png", DeletedAt: &now,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestGetAdminThumbnailNoThumbnail(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAdminThumbnailNoS3(t *testing.T) {
	h := newAdminTestHandler(&mockAdminAssetStore{}, &mockAdminShareStore{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetAdminThumbnailNotFound(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAdminThumbnailS3Error(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", ThumbnailS3Key: "thumb.png",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	s3 := &mockAdminS3Client{getErr: fmt.Errorf("s3 fail")}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, s3)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/thumbnail", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- listAllAssets with nil ShareStore ---

func TestListAllAssetsNilShareStore(t *testing.T) {
	now := time.Now()
	assets := &mockAdminAssetStore{
		listRes: []portal.Asset{{
			ID: "a1", OwnerID: "u1", Name: "Test",
			Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
		}},
		listTotal: 1,
	}
	// nil ShareStore — should still return assets without summaries
	h := newAdminTestHandler(assets, nil, &mockAdminS3Client{})

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp adminAssetListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp.Data, 1)
	assert.Nil(t, resp.ShareSummaries)
}

// --- Admin version handler tests ---

func TestListAdminVersionsSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", CurrentVersion: 2,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	versions := []portal.AssetVersion{
		{ID: "v2", AssetID: "a1", Version: 2, S3Key: "k2", S3Bucket: "b"},
		{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b"},
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{listVersions: versions, listTotal: 2},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp adminVersionListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Total)
	assert.Len(t, resp.Data, 2)
}

func TestListAdminVersionsNoStore(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetAdminVersionContentSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", CurrentVersion: 2,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getVersion: ver},
		&mockAdminS3Client{getData: []byte("<html>v1</html>"), getCT: "text/html"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html", w.Header().Get("Content-Type"))
}

func TestRevertAdminVersionSuccess(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getVersion: ver, createVersion: 3},
		&mockAdminS3Client{getData: []byte("<html>v1</html>"), getCT: "text/html"},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRevertAdminVersionDeleted(t *testing.T) {
	now := time.Now()
	deleted := now.Add(-time.Hour)
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", CurrentVersion: 2, DeletedAt: &deleted,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
}

func TestListAdminVersionsAssetNotFound(t *testing.T) {
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{},
		&mockAdminVersionStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListAdminVersionsError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{listErr: fmt.Errorf("db error")},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListAdminVersionsWithPagination(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{listVersions: []portal.AssetVersion{}, listTotal: 0},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions?limit=5&offset=10", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp adminVersionListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 5, resp.Limit)
	assert.Equal(t, 10, resp.Offset)
}

func TestGetAdminVersionContentNoStore(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetAdminVersionContentAssetNotFound(t *testing.T) {
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{},
		&mockAdminVersionStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAdminVersionContentInvalidVersion(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions/abc/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetAdminVersionContentVersionNotFound(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getErr: fmt.Errorf("not found")},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions/99/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAdminVersionContentS3Error(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", CurrentVersion: 2,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b"}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getVersion: ver},
		&mockAdminS3Client{getErr: fmt.Errorf("s3 error")},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/assets/a1/versions/1/content", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRevertAdminVersionNoStore(t *testing.T) {
	h := newAdminTestHandler(
		&mockAdminAssetStore{},
		&mockAdminShareStore{}, &mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRevertAdminVersionAssetNotFound(t *testing.T) {
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getErr: fmt.Errorf("not found")},
		&mockAdminShareStore{},
		&mockAdminVersionStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevertAdminVersionInvalidVersion(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/abc/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRevertAdminVersionVersionNotFound(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getErr: fmt.Errorf("not found")},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/99/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRevertAdminVersionS3ReadError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getVersion: ver},
		&mockAdminS3Client{getErr: fmt.Errorf("s3 error")},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRevertAdminVersionS3PutError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getVersion: ver},
		&mockAdminS3Client{getData: []byte("data"), putErr: fmt.Errorf("s3 error")},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRevertAdminVersionCreateVersionError(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	ver := &portal.AssetVersion{ID: "v1", AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/html"}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{getVersion: ver, createErr: fmt.Errorf("db error")},
		&mockAdminS3Client{getData: []byte("data")},
	)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/admin/assets/a1/versions/1/revert", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateAdminAssetContentCreateVersionErrorCleansUpS3(t *testing.T) {
	now := time.Now()
	asset := &portal.Asset{
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k", ContentType: "text/html", CurrentVersion: 1,
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandlerWithVersions(
		&mockAdminAssetStore{getAsset: asset},
		&mockAdminShareStore{},
		&mockAdminVersionStore{createErr: fmt.Errorf("db error")},
		&mockAdminS3Client{},
	)

	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/admin/assets/a1/content",
		strings.NewReader("new content"))
	req.Header.Set("Content-Type", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAdminIntParam(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		param      string
		defaultVal int
		want       int
	}{
		{"valid", "/test?limit=5", "limit", 10, 5},
		{"missing", "/test", "limit", 10, 10},
		{"invalid", "/test?limit=abc", "limit", 10, 10},
		{"empty", "/test?limit=", "limit", 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), "GET", tt.url, http.NoBody)
			assert.Equal(t, tt.want, adminIntParam(req, tt.param, tt.defaultVal))
		})
	}
}

func TestAdminUserEmail(t *testing.T) {
	t.Run("with email", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), adminUserKey, &User{UserID: "uid", Email: "admin@example.com"})
		req := httptest.NewRequestWithContext(ctx, "GET", "/", http.NoBody)
		assert.Equal(t, "admin@example.com", adminUserEmail(req))
	})
	t.Run("no email", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), adminUserKey, &User{UserID: "uid"})
		req := httptest.NewRequestWithContext(ctx, "GET", "/", http.NoBody)
		assert.Equal(t, "admin", adminUserEmail(req))
	})
	t.Run("no user", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		assert.Equal(t, "admin", adminUserEmail(req))
	})
}

func TestExtensionForContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want string
	}{
		{"text/html", ".html"},
		{"image/svg+xml", ".svg"},
		{"text/markdown", ".md"},
		{"application/json", ".json"},
		{"text/csv", ".csv"},
		{"application/octet-stream", ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			assert.Equal(t, tt.want, portal.ExtensionForContentType(tt.ct))
		})
	}
}
