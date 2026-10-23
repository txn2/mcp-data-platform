package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// appenderCollStore is the collection store with the single-item append the
// add-to-collection routes need (#2260).
type appenderCollStore struct {
	collHandlerMockCollStore
	appendRes     portaldomain.AppendedItem
	appendErr     error
	summaries     map[string][]portaldomain.SectionSummary
	summariesErr  error
	lastAppend    [3]string
	lastSummaries []string
	setSections   []CollectionSection
	softDeleted   bool
}

func (m *appenderCollStore) AppendItem(_ context.Context, collectionID, sectionID, assetID string) (portaldomain.AppendedItem, error) {
	m.lastAppend = [3]string{collectionID, sectionID, assetID}
	return m.appendRes, m.appendErr
}

func (m *appenderCollStore) SectionSummaries(_ context.Context, ids []string, _ string) (map[string][]portaldomain.SectionSummary, error) {
	m.lastSummaries = ids
	return m.summaries, m.summariesErr
}

func (m *appenderCollStore) SetSections(_ context.Context, _ string, s []CollectionSection) error {
	m.setSections = s
	return m.setSectionsErr
}

func (m *appenderCollStore) SoftDelete(_ context.Context, _ string) error {
	m.softDeleted = true
	return m.softDeleteErr
}

func newAppendHandler(assets *mockAssetStore, shares ShareStore, colls CollectionStore, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:      assets,
		ShareStore:      shares,
		CollectionStore: colls,
		S3Bucket:        "test-bucket",
		PublicBaseURL:   "https://example.com",
		AdminRoles:      []string{"admin"},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
}

func ownedAsset() *Asset {
	return &Asset{ID: "asset-1", OwnerID: testUser.UserID, OwnerEmail: testUser.Email, Name: "Revenue"}
}

func doJSON(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAppendCollectionItem(t *testing.T) {
	const path = "/api/v1/portal/collections/coll-1/items"

	t.Run("adds to the named section", func(t *testing.T) {
		cs := &appenderCollStore{
			getColl: baseCollection(),
			appendRes: portaldomain.AppendedItem{
				SectionID: "sec-2", SectionTitle: "Findings",
				Item: portaldomain.CollectionItem{ID: "item-9", AssetID: "asset-1", Position: 3},
			},
		}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, testUser)

		w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1","section_id":"sec-2"}`)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.Equal(t, [3]string{"coll-1", "sec-2", "asset-1"}, cs.lastAppend)
		var got portaldomain.AppendedItem
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		assert.Equal(t, "Findings", got.SectionTitle)
		assert.Equal(t, 3, got.Item.Position)
	})

	refusals := []struct {
		name   string
		err    error
		status int
	}{
		{"duplicate in section", portaldomain.ErrItemAlreadyInSection, http.StatusConflict},
		{"section full", portaldomain.ErrSectionFull, http.StatusConflict},
		{"section gone", portaldomain.ErrSectionNotFound, http.StatusConflict},
		{"store failure", errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			cs := &appenderCollStore{getColl: baseCollection(), appendErr: tc.err}
			h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, testUser)
			w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`)
			assert.Equal(t, tc.status, w.Code)
		})
	}

	t.Run("viewer share is refused", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "someone-else"
		cs := &appenderCollStore{getColl: coll}
		shares := &mockCollectionShareStore{collPermission: PermissionViewer}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, shares, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, cs.lastAppend[0], "nothing is written for a viewer")
	})

	t.Run("editor share may add", func(t *testing.T) {
		coll := baseCollection()
		coll.OwnerID = "someone-else"
		cs := &appenderCollStore{getColl: coll}
		shares := &mockCollectionShareStore{collPermission: PermissionEditor}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, shares, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`)
		assert.Equal(t, http.StatusCreated, w.Code)
	})

	t.Run("asset the caller cannot open is refused", func(t *testing.T) {
		asset := ownedAsset()
		asset.OwnerID, asset.OwnerEmail = "other", "other@example.com"
		cs := &appenderCollStore{getColl: baseCollection()}
		h := newAppendHandler(&mockAssetStore{getAsset: asset}, &mockCollectionShareStore{}, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("deleted asset is not found", func(t *testing.T) {
		asset := ownedAsset()
		now := time.Now()
		asset.DeletedAt = &now
		cs := &appenderCollStore{getColl: baseCollection()}
		h := newAppendHandler(&mockAssetStore{getAsset: asset}, &mockCollectionShareStore{}, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("missing asset id", func(t *testing.T) {
		cs := &appenderCollStore{getColl: baseCollection()}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, testUser)
		assert.Equal(t, http.StatusBadRequest, doJSON(h, http.MethodPost, path, `{}`).Code)
	})

	t.Run("unknown collection", func(t *testing.T) {
		cs := &appenderCollStore{getErr: errors.New("no row")}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, testUser)
		assert.Equal(t, http.StatusNotFound, doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`).Code)
	})

	t.Run("signed out", func(t *testing.T) {
		cs := &appenderCollStore{}
		h := newAppendHandler(&mockAssetStore{}, &mockCollectionShareStore{}, cs, nil)
		assert.Equal(t, http.StatusUnauthorized, doJSON(h, http.MethodPost, path, `{"asset_id":"a"}`).Code)
	})

	t.Run("store without append mounts no route", func(t *testing.T) {
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{},
			&collHandlerMockCollStore{getColl: baseCollection()}, testUser)
		w := doJSON(h, http.MethodPost, path, `{"asset_id":"asset-1"}`)
		assert.NotEqual(t, http.StatusCreated, w.Code)
	})
}

func TestListCollectionTargets(t *testing.T) {
	const path = "/api/v1/portal/assets/asset-1/collection-targets"
	older := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(48 * time.Hour)
	owned := []Collection{{ID: "mine", OwnerID: testUser.UserID, Name: "Mine", UpdatedAt: older}}
	shared := []SharedCollection{{Collection: Collection{ID: "theirs", OwnerID: "bob", OwnerEmail: "bob@example.com", Name: "Theirs", UpdatedAt: newer}}}

	t.Run("owned and editor-shared, newest first, with sections", func(t *testing.T) {
		cs := &appenderCollStore{
			listRes: owned,
			summaries: map[string][]portaldomain.SectionSummary{
				"mine": {{ID: "s1", Title: "Overview", ItemCount: 2, HasAsset: true}},
			},
		}
		shares := &mockCollectionShareStore{sharedCollRes: shared, collPermission: PermissionEditor}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, shares, cs, testUser)

		w := doJSON(h, http.MethodGet, path, "")

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var got collectionTargetsResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
		require.Len(t, got.Data, 2)
		assert.Equal(t, "theirs", got.Data[0].ID, "most recently updated first")
		assert.True(t, got.Data[0].Shared)
		assert.Equal(t, []portaldomain.SectionSummary{}, got.Data[0].Sections, "no sections is [] not null")
		assert.Equal(t, "mine", got.Data[1].ID)
		assert.True(t, got.Data[1].Sections[0].HasAsset)
		assert.Equal(t, testUser.UserID, cs.lastFilter.OwnerID, "a person lists their own collections")
		assert.ElementsMatch(t, []string{"mine", "theirs"}, cs.lastSummaries)
	})

	t.Run("viewer shares are left out", func(t *testing.T) {
		cs := &appenderCollStore{listRes: owned}
		shares := &mockCollectionShareStore{sharedCollRes: shared, collPermission: PermissionViewer}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, shares, cs, testUser)
		var got collectionTargetsResponse
		require.NoError(t, json.NewDecoder(doJSON(h, http.MethodGet, path, "").Body).Decode(&got))
		require.Len(t, got.Data, 1)
		assert.Equal(t, "mine", got.Data[0].ID)
	})

	t.Run("search narrows the shared half too", func(t *testing.T) {
		cs := &appenderCollStore{}
		shares := &mockCollectionShareStore{sharedCollRes: shared, collPermission: PermissionEditor}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, shares, cs, testUser)
		var got collectionTargetsResponse
		require.NoError(t, json.NewDecoder(doJSON(h, http.MethodGet, path+"?search=nothing-like-it", "").Body).Decode(&got))
		assert.Equal(t, "nothing-like-it", cs.lastFilter.Search)
		assert.Equal(t, []portaldomain.CollectionTarget{}, got.Data)

		require.NoError(t, json.NewDecoder(doJSON(h, http.MethodGet, path+"?search=BOB@", "").Body).Decode(&got))
		require.Len(t, got.Data, 1, "the owner's address matches, as it does on the owned half")
	})

	t.Run("an administrator lists every collection", func(t *testing.T) {
		admin := &User{UserID: "admin-1", Email: "admin@example.com", Roles: []string{"admin"}}
		cs := &appenderCollStore{listRes: owned}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, admin)
		var got collectionTargetsResponse
		require.NoError(t, json.NewDecoder(doJSON(h, http.MethodGet, path, "").Body).Decode(&got))
		assert.Empty(t, cs.lastFilter.OwnerID)
		require.Len(t, got.Data, 1)
		assert.True(t, got.Data[0].Shared, "a collection the administrator does not own is marked")
	})

	failures := []struct {
		name string
		cs   *appenderCollStore
		sh   *mockCollectionShareStore
	}{
		{"list fails", &appenderCollStore{listErr: errors.New("x")}, &mockCollectionShareStore{}},
		{"shared fails", &appenderCollStore{}, &mockCollectionShareStore{sharedCollErr: errors.New("x")}},
		{"sections fail", &appenderCollStore{summariesErr: errors.New("x")}, &mockCollectionShareStore{}},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, tc.sh, tc.cs, testUser)
			assert.Equal(t, http.StatusInternalServerError, doJSON(h, http.MethodGet, path, "").Code)
		})
	}

	t.Run("asset the caller cannot open", func(t *testing.T) {
		asset := ownedAsset()
		asset.OwnerID, asset.OwnerEmail = "other", "other@example.com"
		h := newAppendHandler(&mockAssetStore{getAsset: asset}, &mockCollectionShareStore{}, &appenderCollStore{}, testUser)
		assert.Equal(t, http.StatusForbidden, doJSON(h, http.MethodGet, path, "").Code)
	})

	t.Run("signed out", func(t *testing.T) {
		h := newAppendHandler(&mockAssetStore{}, &mockCollectionShareStore{}, &appenderCollStore{}, nil)
		assert.Equal(t, http.StatusUnauthorized, doJSON(h, http.MethodGet, path, "").Code)
	})
}

func TestCreateCollection_WithAsset(t *testing.T) {
	const path = "/api/v1/portal/collections"

	t.Run("the asset is the first item of one untitled section", func(t *testing.T) {
		cs := &appenderCollStore{getColl: baseCollection()}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"name":"Board pack","asset_id":"asset-1"}`)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		require.Len(t, cs.setSections, 1)
		assert.Empty(t, cs.setSections[0].Title)
		require.Len(t, cs.setSections[0].Items, 1)
		assert.Equal(t, "asset-1", cs.setSections[0].Items[0].AssetID)
	})

	t.Run("a failed seed removes the new collection", func(t *testing.T) {
		cs := &appenderCollStore{setSectionsErr: errors.New("x")}
		h := newAppendHandler(&mockAssetStore{getAsset: ownedAsset()}, &mockCollectionShareStore{}, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"name":"Board pack","asset_id":"asset-1"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.True(t, cs.softDeleted)
	})

	t.Run("an asset the caller cannot open creates nothing", func(t *testing.T) {
		asset := ownedAsset()
		asset.OwnerID, asset.OwnerEmail = "other", "other@example.com"
		cs := &appenderCollStore{}
		h := newAppendHandler(&mockAssetStore{getAsset: asset}, &mockCollectionShareStore{}, cs, testUser)
		w := doJSON(h, http.MethodPost, path, `{"name":"Board pack","asset_id":"asset-1"}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Nil(t, cs.setSections)
	})

	t.Run("without an asset nothing is seeded", func(t *testing.T) {
		cs := &appenderCollStore{getColl: baseCollection()}
		h := newAppendHandler(&mockAssetStore{}, &mockCollectionShareStore{}, cs, testUser)
		assert.Equal(t, http.StatusCreated, doJSON(h, http.MethodPost, path, `{"name":"Empty"}`).Code)
		assert.Nil(t, cs.setSections)
	})
}
