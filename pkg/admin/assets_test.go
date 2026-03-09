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
	insertErr error
	getAsset  *portal.Asset
	getErr    error
	listRes   []portal.Asset
	listTotal int
	listErr   error
	updateErr error
	deleteErr error
}

func (m *mockAdminAssetStore) Insert(_ context.Context, _ portal.Asset) error { return m.insertErr }
func (m *mockAdminAssetStore) Get(_ context.Context, _ string) (*portal.Asset, error) {
	return m.getAsset, m.getErr
}

func (m *mockAdminAssetStore) List(_ context.Context, _ portal.AssetFilter) ([]portal.Asset, int, error) {
	return m.listRes, m.listTotal, m.listErr
}

func (m *mockAdminAssetStore) Update(_ context.Context, _ string, _ portal.AssetUpdate) error {
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

func newAdminTestHandler(assets portal.AssetStore, shares portal.ShareStore, s3 portal.S3Client) *Handler {
	return NewHandler(Deps{
		AssetStore: assets,
		ShareStore: shares,
		S3Client:   s3,
		S3Bucket:   "test-bucket",
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
		S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(&mockAdminAssetStore{getAsset: asset}, &mockAdminShareStore{}, &mockAdminS3Client{})

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
		ID: "a1", OwnerID: "u1", S3Bucket: "b", S3Key: "k",
		Tags: []string{}, Provenance: portal.Provenance{}, CreatedAt: now, UpdatedAt: now,
	}
	h := newAdminTestHandler(
		&mockAdminAssetStore{getAsset: asset, updateErr: fmt.Errorf("db error")},
		&mockAdminShareStore{}, &mockAdminS3Client{},
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
