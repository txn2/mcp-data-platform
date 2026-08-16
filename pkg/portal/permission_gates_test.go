package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// These tests pin the two authorities the portal's owner gates were collapsing
// into a bare ID comparison: owner-or-admin over anything (#1293), and
// owner-or-admin-or-editor over a collection's own fields (#1294).

const gateAdminRole = "admin"

func gateOwner() *User { return &User{UserID: "u-owner", Email: "owner@example.com"} }
func gateAdmin() *User {
	return &User{UserID: "u-admin", Email: "admin@example.com", Roles: []string{gateAdminRole}}
}
func gateStranger() *User { return &User{UserID: "u-stranger", Email: "stranger@example.com"} }

func gateAssetHandler(assets *mockAssetStore, shares *mockShareStore, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:    assets,
		ShareStore:    shares,
		S3Client:      &mockS3Client{},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
		AdminRoles:    []string{gateAdminRole},
		RateLimit:     RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
}

func gateCollectionHandler(colls *collHandlerMockCollStore, shares ShareStore, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:      &mockAssetStore{},
		ShareStore:      shares,
		CollectionStore: colls,
		S3Client:        &mockS3Client{},
		S3Bucket:        "test-bucket",
		PublicBaseURL:   "https://example.com",
		AdminRoles:      []string{gateAdminRole},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
}

// gateAsset is an asset owned by someone other than the caller under test.
func gateAsset() *Asset {
	return &Asset{
		ID: "a1", OwnerID: gateOwner().UserID, Name: "Report",
		S3Bucket: "b", S3Key: "portal/u-owner/a1/content.md", ContentType: "text/markdown",
	}
}

// gateCollection is a collection owned by someone other than the caller.
func gateCollection() *Collection {
	return &Collection{ID: "coll-1", OwnerID: gateOwner().UserID, OwnerEmail: gateOwner().Email, Name: "Q4"}
}

// assetManageRoute is one owner-authority route on an asset, with the body and
// success status it answers with when the caller is allowed.
type assetManageRoute struct {
	name        string
	method      string
	path        string
	body        string
	contentType string
	wantOK      int
}

var assetManageRoutes = []assetManageRoute{
	{"delete", http.MethodDelete, "/api/v1/portal/assets/a1", "", "", http.StatusOK},
	{"create share", http.MethodPost, "/api/v1/portal/assets/a1/shares", `{"permission":"viewer"}`, "application/json", http.StatusCreated},
	{"list shares", http.MethodGet, "/api/v1/portal/assets/a1/shares", "", "", http.StatusOK},
	{"upload thumbnail", http.MethodPut, "/api/v1/portal/assets/a1/thumbnail", strings.Repeat("x", 100), "image/png", http.StatusOK},
}

func (rt assetManageRoute) do(h *Handler) *httptest.ResponseRecorder {
	var body *strings.Reader
	if rt.body == "" {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(rt.body)
	}
	r := httptest.NewRequestWithContext(context.Background(), rt.method, rt.path, body)
	if rt.contentType != "" {
		r.Header.Set("Content-Type", rt.contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestAssetOwnerGatesAdmitAdmin is the reported defect: an admin who can already
// read, edit and delete any asset through the admin API was refused the weaker
// right to share it or read its share list.
func TestAssetOwnerGatesAdmitAdmin(t *testing.T) {
	for _, rt := range assetManageRoutes {
		t.Run(rt.name+"/admin", func(t *testing.T) {
			h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, &mockShareStore{}, gateAdmin())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
		t.Run(rt.name+"/owner", func(t *testing.T) {
			h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, &mockShareStore{}, gateOwner())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
		t.Run(rt.name+"/stranger", func(t *testing.T) {
			h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, &mockShareStore{}, gateStranger())
			assert.Equal(t, http.StatusForbidden, rt.do(h).Code,
				"a caller who is neither owner nor admin is still refused")
		})
	}
}

// TestCreateShareByAdminIsRecorded checks the share an admin mints is a real
// share attributed to the admin, not a silently dropped write.
func TestCreateShareByAdminIsRecorded(t *testing.T) {
	shares := &mockShareStore{}
	h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, shares, gateAdmin())

	body := `{"shared_with_email":"bob@example.com","permission":"editor"}`
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusCreated, w.Code)
	require.NotNil(t, shares.inserted)
	assert.Equal(t, "a1", shares.inserted.AssetID)
	assert.Equal(t, "bob@example.com", shares.inserted.SharedWithEmail)
	assert.Equal(t, PermissionEditor, shares.inserted.Permission)
	assert.Equal(t, gateAdmin().Email, shares.inserted.CreatedBy,
		"the share records who actually created it")
}

// TestRevokeShareAdmitsAdmin covers all three arms of the revoke switch: the
// asset arm, the collection arm, and the prompt arm.
func TestRevokeShareAdmitsAdmin(t *testing.T) {
	t.Run("asset share", func(t *testing.T) {
		shares := &mockShareStore{getByIDShare: &Share{ID: "s1", AssetID: "a1"}}
		h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, shares, gateAdmin())
		r := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("asset share, stranger", func(t *testing.T) {
		shares := &mockShareStore{getByIDShare: &Share{ID: "s1", AssetID: "a1"}}
		h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, shares, gateStranger())
		r := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("collection share", func(t *testing.T) {
		shares := &mockCollectionShareStore{}
		shares.getByIDShare = &Share{ID: "s1", CollectionID: "coll-1"}
		h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateAdmin())
		r := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("collection share, editor is refused", func(t *testing.T) {
		shares := &mockCollectionShareStore{collPermission: PermissionEditor}
		shares.getByIDShare = &Share{ID: "s1", CollectionID: "coll-1"}
		h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateStranger())
		r := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusForbidden, w.Code,
			"revoking is share management, which an Editor share never grants")
	})

	t.Run("prompt share", func(t *testing.T) {
		pstore := newMockPromptStore()
		pstore.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: gateOwner().Email}
		shares := &mockShareStore{getByIDShare: &Share{ID: "s1", PromptID: "p1"}}
		h := NewHandler(Deps{
			AssetStore:  NewNoopAssetStore(),
			ShareStore:  shares,
			PromptStore: pstore,
			AdminRoles:  []string{gateAdminRole},
		}, testAuthMiddleware(gateAdmin()))
		r := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/portal/shares/s1", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// collectionRoute is one write route on a collection's own fields.
type collectionRoute struct {
	name   string
	method string
	path   string
	body   string
	ctype  string
	wantOK int
}

// collectionEditRoutes are the four operations that shape a collection as a
// document, which an Editor share now grants (#1294).
var collectionEditRoutes = []collectionRoute{
	{"update", http.MethodPut, "/api/v1/portal/collections/coll-1", `{"name":"Renamed"}`, "application/json", http.StatusOK},
	{"update config", http.MethodPut, "/api/v1/portal/collections/coll-1/config", `{"thumbnail_size":"small"}`, "application/json", http.StatusOK},
	{"set sections", http.MethodPut, "/api/v1/portal/collections/coll-1/sections", `{"sections":[]}`, "application/json", http.StatusOK},
	{"upload thumbnail", http.MethodPut, "/api/v1/portal/collections/coll-1/thumbnail", strings.Repeat("x", 100), "image/png", http.StatusNoContent},
}

// collectionManageRoutes stay with the owner and admins: destruction and
// re-granting access are not editing.
var collectionManageRoutes = []collectionRoute{
	{"delete", http.MethodDelete, "/api/v1/portal/collections/coll-1", "", "", http.StatusNoContent},
	{"create share", http.MethodPost, "/api/v1/portal/collections/coll-1/shares", `{"permission":"viewer"}`, "application/json", http.StatusCreated},
	{"list shares", http.MethodGet, "/api/v1/portal/collections/coll-1/shares", "", "", http.StatusOK},
}

func (rt collectionRoute) do(h *Handler) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), rt.method, rt.path, strings.NewReader(rt.body))
	if rt.ctype != "" {
		r.Header.Set("Content-Type", rt.ctype)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestCollectionEditRoutesHonourEditorShare is the reported defect: an Editor
// who may rewrite every asset in a collection could not rename the collection.
func TestCollectionEditRoutesHonourEditorShare(t *testing.T) {
	for _, rt := range collectionEditRoutes {
		t.Run(rt.name+"/editor", func(t *testing.T) {
			shares := &mockCollectionShareStore{collPermission: PermissionEditor}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateStranger())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
		t.Run(rt.name+"/viewer", func(t *testing.T) {
			shares := &mockCollectionShareStore{collPermission: PermissionViewer}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateStranger())
			w := rt.do(h)
			assert.Equal(t, http.StatusForbidden, w.Code, "a Viewer share never edits")
			assert.Contains(t, w.Body.String(), "only the owner or an editor",
				"the refusal names the permission that would have worked")
		})
		t.Run(rt.name+"/admin", func(t *testing.T) {
			shares := &mockCollectionShareStore{}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateAdmin())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
		t.Run(rt.name+"/owner", func(t *testing.T) {
			shares := &mockCollectionShareStore{}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateOwner())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
	}
}

// TestCollectionManageRoutesStayWithTheOwner records the deliberate half of
// #1294: an Editor holds none of these, while an admin holds all of them.
func TestCollectionManageRoutesStayWithTheOwner(t *testing.T) {
	for _, rt := range collectionManageRoutes {
		t.Run(rt.name+"/editor is refused", func(t *testing.T) {
			shares := &mockCollectionShareStore{collPermission: PermissionEditor}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateStranger())
			assert.Equal(t, http.StatusForbidden, rt.do(h).Code)
		})
		t.Run(rt.name+"/admin", func(t *testing.T) {
			shares := &mockCollectionShareStore{}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateAdmin())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
		t.Run(rt.name+"/owner", func(t *testing.T) {
			shares := &mockCollectionShareStore{}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateOwner())
			assert.Equal(t, rt.wantOK, rt.do(h).Code)
		})
	}
}

// TestGetCollectionReportsResolvedAuthority checks the page is told what it may
// do, rather than inferring it from ownership alone.
func TestGetCollectionReportsResolvedAuthority(t *testing.T) {
	tests := []struct {
		name          string
		user          *User
		perm          SharePermission
		wantOwner     bool
		wantCanEdit   bool
		wantCanManage bool
	}{
		{"owner holds both", gateOwner(), "", true, true, true},
		{"editor edits but does not manage", gateStranger(), PermissionEditor, false, true, false},
		{"viewer holds neither", gateStranger(), PermissionViewer, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shares := &mockCollectionShareStore{collPermission: tt.perm}
			h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, tt.user)
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/collections/coll-1", http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Code)
			var resp struct {
				IsOwner   bool `json:"is_owner"`
				CanEdit   bool `json:"can_edit"`
				CanManage bool `json:"can_manage"`
			}
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, tt.wantOwner, resp.IsOwner)
			assert.Equal(t, tt.wantCanEdit, resp.CanEdit)
			assert.Equal(t, tt.wantCanManage, resp.CanManage)
		})
	}
}

// TestPromptShareGatesAdmitAdmin covers the prompt arm, whose ownership is an
// email rather than a user ID.
func TestPromptShareGatesAdmitAdmin(t *testing.T) {
	newHandler := func(user *User) (*Handler, *mockShareStore) {
		pstore := newMockPromptStore()
		pstore.prompts["report"] = &prompt.Prompt{
			ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: gateOwner().Email,
		}
		sstore := &mockShareStore{}
		return NewHandler(Deps{
			AssetStore:  NewNoopAssetStore(),
			ShareStore:  sstore,
			PromptStore: pstore,
			AdminRoles:  []string{gateAdminRole},
		}, testAuthMiddleware(user)), sstore
	}

	t.Run("admin shares a prompt they do not own", func(t *testing.T) {
		h, sstore := newHandler(gateAdmin())
		body := `{"shared_with_email":"bob@example.com","permission":"viewer"}`
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)
		require.NotNil(t, sstore.inserted)
		assert.Equal(t, "p1", sstore.inserted.PromptID)
	})

	t.Run("admin cannot address the share to the prompt's own owner", func(t *testing.T) {
		h, _ := newHandler(gateAdmin())
		body := fmt.Sprintf(`{"shared_with_email":%q}`, gateOwner().Email)
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "already owns this prompt")
	})

	t.Run("stranger is still refused", func(t *testing.T) {
		h, _ := newHandler(gateStranger())
		body := `{"shared_with_email":"bob@example.com"}`
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("admin reads the share list", func(t *testing.T) {
		h, _ := newHandler(gateAdmin())
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("stranger cannot read the share list", func(t *testing.T) {
		h, _ := newHandler(gateStranger())
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/prompts/p1/shares", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

// TestCanEditAssetAdmitsAdmin covers the shared asset-edit wrapper, which the
// content-update route runs through.
func TestCanEditAssetAdmitsAdmin(t *testing.T) {
	h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, &mockShareStore{}, gateAdmin())
	user := gateAdmin()
	r := httptest.NewRequestWithContext(ContextWithUser(context.Background(), user), http.MethodGet, "/api/v1/portal/assets/a1", http.NoBody)
	w := httptest.NewRecorder()
	assert.True(t, h.canEditAsset(w, r, "a1", gateAsset(), user))
	assert.Equal(t, http.StatusOK, w.Code, "an allowed check writes no error")
}
