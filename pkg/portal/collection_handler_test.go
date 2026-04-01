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

// --- Mock collection store for collection handler tests ---

type collHandlerMockCollStore struct {
	insertErr       error
	getColl         *Collection
	getErr          error
	listRes         []Collection
	listTotal       int
	listErr         error
	updateErr       error
	updateConfigErr error
	updateThumbErr  error
	softDeleteErr   error
	setSectionsErr  error
}

func (m *collHandlerMockCollStore) Insert(_ context.Context, _ Collection) error { return m.insertErr }
func (m *collHandlerMockCollStore) Get(_ context.Context, _ string) (*Collection, error) {
	return m.getColl, m.getErr
}

func (m *collHandlerMockCollStore) List(_ context.Context, _ CollectionFilter) ([]Collection, int, error) {
	return m.listRes, m.listTotal, m.listErr
}

func (m *collHandlerMockCollStore) Update(_ context.Context, _, _, _ string) error {
	return m.updateErr
}

func (m *collHandlerMockCollStore) UpdateConfig(_ context.Context, _ string, _ CollectionConfig) error {
	return m.updateConfigErr
}

func (m *collHandlerMockCollStore) UpdateThumbnail(_ context.Context, _, _ string) error {
	return m.updateThumbErr
}

func (m *collHandlerMockCollStore) SoftDelete(_ context.Context, _ string) error {
	return m.softDeleteErr
}

func (m *collHandlerMockCollStore) SetSections(_ context.Context, _ string, _ []CollectionSection) error {
	return m.setSectionsErr
}

// mockCollectionShareStore wraps mockShareStore with configurable collection-specific methods.
type mockCollectionShareStore struct {
	mockShareStore
	listByCollRes    []Share
	listByCollErr    error
	collPermission   SharePermission
	collPermErr      error
	sharedCollRes    []SharedCollection
	sharedCollTotal  int
	sharedCollErr    error
	collSummaries    map[string]ShareSummary
	collSummariesErr error
}

func (m *mockCollectionShareStore) ListByCollection(_ context.Context, _ string) ([]Share, error) {
	return m.listByCollRes, m.listByCollErr
}

func (m *mockCollectionShareStore) GetUserCollectionPermission(_ context.Context, _, _, _ string) (SharePermission, error) {
	return m.collPermission, m.collPermErr
}

func (m *mockCollectionShareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]SharedCollection, int, error) {
	return m.sharedCollRes, m.sharedCollTotal, m.sharedCollErr
}

func (m *mockCollectionShareStore) ListActiveCollectionShareSummaries(_ context.Context, _ []string) (map[string]ShareSummary, error) {
	return m.collSummaries, m.collSummariesErr
}

// newTestHandlerWithCollections creates a Handler with a CollectionStore for testing.
func newTestHandlerWithCollections(
	assets *mockAssetStore,
	shares ShareStore,
	collections *collHandlerMockCollStore,
	s3 *mockS3Client,
	user *User,
) *Handler {
	deps := Deps{
		AssetStore:      assets,
		ShareStore:      shares,
		CollectionStore: collections,
		S3Bucket:        "test-bucket",
		PublicBaseURL:   "https://example.com",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	if s3 != nil {
		deps.S3Client = s3
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

// --- Test helpers ---

var testUser = &User{UserID: "user-1", Email: "user@example.com"}

func baseCollection() *Collection {
	return &Collection{
		ID:         "coll-1",
		OwnerID:    testUser.UserID,
		OwnerEmail: testUser.Email,
		Name:       "My Collection",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

// --- CreateCollection tests ---

func TestCreateCollection(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"My Collection","description":"A test"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp Collection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "My Collection", resp.Name)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		body := `{"name":"Test"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid_name_empty", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":""}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid_body", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections", strings.NewReader("{bad"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("insert_error_returns_500", func(t *testing.T) {
		cs := &collHandlerMockCollStore{insertErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"Valid Name"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("get_after_insert_fails_returns_created_with_input", func(t *testing.T) {
		// Insert succeeds but re-read fails — handler still returns 201 with the original coll.
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"Fallback"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp Collection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "Fallback", resp.Name)
	})
}

// --- ListCollections tests ---

func TestListCollections(t *testing.T) {
	t.Run("success_with_results", func(t *testing.T) {
		coll := *baseCollection()
		cs := &collHandlerMockCollStore{listRes: []Collection{coll}, listTotal: 1}
		shares := &mockCollectionShareStore{
			collSummaries: map[string]ShareSummary{"coll-1": {HasPublicLink: true}},
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp listCollectionsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 1, resp.Total)
		assert.Len(t, resp.Data, 1)
		assert.Equal(t, defaultLimit, resp.Limit)
	})

	t.Run("empty_results", func(t *testing.T) {
		cs := &collHandlerMockCollStore{listRes: nil, listTotal: 0}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp listCollectionsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 0, resp.Total)
		assert.Equal(t, []Collection{}, resp.Data)
	})

	t.Run("with_search_limit_offset", func(t *testing.T) {
		cs := &collHandlerMockCollStore{listRes: []Collection{}, listTotal: 0}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections?search=test&limit=10&offset=5", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp listCollectionsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 10, resp.Limit)
		assert.Equal(t, 5, resp.Offset)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("list_error", func(t *testing.T) {
		cs := &collHandlerMockCollStore{listErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// --- GetCollection tests ---

func TestGetCollection(t *testing.T) {
	t.Run("owner_success", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp getCollectionResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.IsOwner)
		assert.Equal(t, "coll-1", resp.ID)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/missing", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("deleted_returns_410", func(t *testing.T) {
		now := time.Now()
		coll := baseCollection()
		coll.DeletedAt = &now
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("non_owner_with_share_access", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{
			collPermission: PermissionViewer,
		}
		otherUser := &User{UserID: "user-1", Email: "user@example.com"}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, otherUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp getCollectionResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.False(t, resp.IsOwner)
		assert.Equal(t, PermissionViewer, resp.SharePermission)
	})

	t.Run("non_owner_denied", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{
			collPermErr: fmt.Errorf("no shares"),
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("nil_sections_become_empty", func(t *testing.T) {
		coll := baseCollection()
		coll.Sections = nil
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp getCollectionResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotNil(t, resp.Sections)
		assert.Empty(t, resp.Sections)
	})

	t.Run("nil_section_items_become_empty", func(t *testing.T) {
		coll := baseCollection()
		coll.Sections = []CollectionSection{{ID: "s1", Items: nil}}
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp getCollectionResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.Len(t, resp.Sections, 1)
		assert.NotNil(t, resp.Sections[0].Items)
		assert.Empty(t, resp.Sections[0].Items)
	})
}

// --- UpdateCollection tests ---

func TestUpdateCollection(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		updated := *coll
		updated.Name = "Updated Name"
		cs := &collHandlerMockCollStore{getColl: &updated}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"Updated Name"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp Collection
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "Updated Name", resp.Name)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"Hack"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"X"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/missing", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid_body", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1", strings.NewReader("{bad"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid_name", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		emptyName := ""
		body := fmt.Sprintf(`{"name":%q}`, emptyName)
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("update_store_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll, updateErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"name":"Valid"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		body := `{"name":"X"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- DeleteCollection tests ---

func TestDeleteCollection(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/collections/missing", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("soft_delete_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll, softDeleteErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "DELETE", "/api/v1/portal/collections/coll-1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- UpdateCollectionConfig tests ---

func TestUpdateCollectionConfig(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		updated := *coll
		updated.Config = CollectionConfig{ThumbnailSize: "large"}
		cs := &collHandlerMockCollStore{getColl: &updated}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"thumbnail_size":"large"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"thumbnail_size":"small"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"thumbnail_size":"small"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/missing/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid_body", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/config", strings.NewReader("{bad"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("update_config_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll, updateConfigErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"thumbnail_size":"large"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		body := `{"thumbnail_size":"large"}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- SetCollectionSections tests ---

func TestSetCollectionSections(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		updated := *coll
		updated.Sections = []CollectionSection{{ID: "s1", Title: "Section 1"}}
		cs := &collHandlerMockCollStore{getColl: &updated}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"sections":[{"title":"Section 1","items":[{"asset_id":"a1"}]}]}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("too_many_items", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		// Build request with 101 items in one section.
		items := make([]string, 101)
		for i := range items {
			items[i] = fmt.Sprintf(`{"asset_id":"a%d"}`, i)
		}
		body := fmt.Sprintf(`{"sections":[{"title":"Big","items":[%s]}]}`, strings.Join(items, ","))
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"sections":[]}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"sections":[]}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/missing/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid_body", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader("{bad"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing_asset_id", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"sections":[{"title":"S","items":[{"asset_id":""}]}]}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("set_sections_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll, setSectionsErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"sections":[{"title":"S","items":[{"asset_id":"a1"}]}]}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		body := `{"sections":[]}`
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("too_many_sections", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		// Build 51 sections (max is 50).
		sections := make([]string, 51)
		for i := range sections {
			sections[i] = fmt.Sprintf(`{"title":"Section %d","items":[{"asset_id":"a1"}]}`, i)
		}
		body := fmt.Sprintf(`{"sections":[%s]}`, strings.Join(sections, ","))
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/sections", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// --- UploadCollectionThumbnail tests ---

func TestUploadCollectionThumbnail(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		s3 := &mockS3Client{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, s3, testUser)

		data := strings.NewReader("PNG-DATA")
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", data)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("too_large", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		s3 := &mockS3Client{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, s3, testUser)

		// Create data larger than MaxThumbnailUploadBytes (512 KB).
		data := strings.NewReader(strings.Repeat("x", int(MaxThumbnailUploadBytes)+1))
		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", data)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", strings.NewReader("data"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/missing/thumbnail", strings.NewReader("data"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("no_s3_client", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, nil, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", strings.NewReader("data"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("s3_put_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		s3 := &mockS3Client{putErr: fmt.Errorf("s3 error")}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, s3, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", strings.NewReader("data"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("update_thumbnail_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll, updateThumbErr: fmt.Errorf("db error")}
		shares := &mockCollectionShareStore{}
		s3 := &mockS3Client{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, s3, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", strings.NewReader("data"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/portal/collections/coll-1/thumbnail", strings.NewReader("data"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- GetCollectionThumbnail tests ---

func TestGetCollectionThumbnail(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		coll.ThumbnailS3Key = "portal/collections/coll-1/thumbnail.png"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		s3 := &mockS3Client{getData: []byte("PNG"), getCT: "image/png"}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, s3, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/thumbnail", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
		assert.Equal(t, "PNG", w.Body.String())
	})

	t.Run("no_thumbnail", func(t *testing.T) {
		coll := baseCollection()
		coll.ThumbnailS3Key = "" // no thumbnail
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/thumbnail", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("not_found_collection", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/missing/thumbnail", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("no_s3_client", func(t *testing.T) {
		coll := baseCollection()
		coll.ThumbnailS3Key = "portal/collections/coll-1/thumbnail.png"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, nil, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/thumbnail", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("s3_get_error", func(t *testing.T) {
		coll := baseCollection()
		coll.ThumbnailS3Key = "portal/collections/coll-1/thumbnail.png"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		s3 := &mockS3Client{getErr: fmt.Errorf("s3 error")}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, s3, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/thumbnail", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/thumbnail", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- CreateCollectionShare tests ---

func TestCreateCollectionShare(t *testing.T) {
	t.Run("success_public_link", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"expires_in":"24h"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/coll-1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.Share.Token)
		assert.Equal(t, "coll-1", resp.Share.CollectionID)
		assert.Contains(t, resp.ShareURL, "/portal/view/")
	})

	t.Run("success_user_share", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"shared_with_email":"other@example.com","permission":"viewer"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/coll-1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "other@example.com", resp.Share.SharedWithEmail)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"expires_in":"1h"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/coll-1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"expires_in":"1h"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/missing/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid_body", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/coll-1/shares", strings.NewReader("{bad"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("insert_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		shares.insertErr = fmt.Errorf("db error")
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		body := `{"expires_in":"1h"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/coll-1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		body := `{"expires_in":"1h"}`
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/portal/collections/coll-1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- ListCollectionShares tests ---

func TestListCollectionShares(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{
			listByCollRes: []Share{{ID: "sh-1", CollectionID: "coll-1", Token: "tok1"}},
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp []Share
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Len(t, resp, 1)
	})

	t.Run("not_owner", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "other-user"
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("not_found", func(t *testing.T) {
		cs := &collHandlerMockCollStore{getErr: fmt.Errorf("not found")}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/missing/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("list_error", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{
			listByCollErr: fmt.Errorf("db error"),
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("nil_shares_become_empty", func(t *testing.T) {
		coll := baseCollection()
		cs := &collHandlerMockCollStore{getColl: coll}
		shares := &mockCollectionShareStore{
			listByCollRes: nil,
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp []Share
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotNil(t, resp)
		assert.Empty(t, resp)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/collections/coll-1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// --- ListSharedCollections tests ---

func TestListSharedCollections(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{
			sharedCollRes: []SharedCollection{
				{
					Collection: Collection{ID: "coll-2", Name: "Shared Coll"},
					ShareID:    "sh-1",
					SharedBy:   "owner@example.com",
					SharedAt:   time.Now(),
					Permission: PermissionViewer,
				},
			},
			sharedCollTotal: 1,
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp listSharedCollectionsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 1, resp.Total)
		assert.Len(t, resp.Data, 1)
		assert.Equal(t, defaultLimit, resp.Limit)
	})

	t.Run("empty", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp listSharedCollectionsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 0, resp.Total)
		assert.Equal(t, []SharedCollection{}, resp.Data)
	})

	t.Run("with_limit_offset", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-collections?limit=5&offset=10", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp listSharedCollectionsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 5, resp.Limit)
		assert.Equal(t, 10, resp.Offset)
	})

	t.Run("store_error", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{
			sharedCollErr: fmt.Errorf("db error"),
		}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, testUser)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing_auth", func(t *testing.T) {
		cs := &collHandlerMockCollStore{}
		shares := &mockCollectionShareStore{}
		h := newTestHandlerWithCollections(&mockAssetStore{}, shares, cs, &mockS3Client{}, nil)

		r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/shared-collections", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
