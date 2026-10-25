package portal

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Who reads an asset's earlier versions (#2226, #2290): three versions,
// shared with a person whose persona withholds earlier versions, read as
// version 3 with no history whether the share is viewer or editor; with the
// setting on, read in full either way. The owner always reads them.

func historyHandler(perm SharePermission, user *User, history bool) *Handler {
	asset := &Asset{ID: "a1", OwnerID: "owner", OwnerEmail: "owner@example.com", Name: "r.md", CurrentVersion: 3}
	shares := &mockShareStore{listByAsset: []Share{{
		ID: "s1", AssetID: "a1", SharedWithUserID: "reader", Permission: perm, AccessMode: AccessModeRestricted,
	}}}
	ver := &AssetVersion{AssetID: "a1", Version: 1, S3Key: "k1", S3Bucket: "b", ContentType: "text/markdown"}
	deps := Deps{
		AssetStore: &mockAssetStore{getAsset: asset}, ShareStore: shares,
		VersionStore: &mockVersionStore{getVersion: ver, listVersions: []AssetVersion{*ver}, listTotal: 3},
		S3Client:     &mockS3Client{getData: []byte("v"), getCT: "text/markdown"}, S3Bucket: "b",
		PublicBaseURL: "https://example.com", ContentURLKey: testContentURLKey,
		RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		PersonaResolver: func([]string) *PersonaInfo {
			return &PersonaInfo{Name: "readers", Portal: persona.PortalAccess{History: &history}}
		},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

func historyGet(h *Handler, path string) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	return w.Code
}

func TestHistory_ThePersonaSettingDecides(t *testing.T) {
	reader := &User{UserID: "reader", Email: "reader@example.com"}
	for _, perm := range []SharePermission{PermissionViewer, PermissionEditor} {
		off := historyHandler(perm, reader, false)
		assert.Equal(t, http.StatusForbidden, historyGet(off, "/api/v1/portal/assets/a1/versions"), perm)
		assert.Equal(t, http.StatusForbidden, historyGet(off, "/api/v1/portal/assets/a1/versions/1/content"), perm)
		assert.Equal(t, http.StatusOK, historyGet(off, "/api/v1/portal/assets/a1/versions/3/content"), perm)
		assert.Equal(t, http.StatusForbidden, historyGet(off, "/api/v1/portal/assets/a1/content-url?version=1"), perm)
		assert.Equal(t, http.StatusOK, historyGet(off, "/api/v1/portal/assets/a1/content-url"), perm)

		on := historyHandler(perm, reader, true)
		assert.Equal(t, http.StatusOK, historyGet(on, "/api/v1/portal/assets/a1/versions"), perm)
		assert.Equal(t, http.StatusOK, historyGet(on, "/api/v1/portal/assets/a1/versions/1/content"), perm)
		assert.Equal(t, http.StatusOK, historyGet(on, "/api/v1/portal/assets/a1/content-url?version=1"), perm)
	}

	owner := historyHandler(PermissionViewer, &User{UserID: "owner", Email: "owner@example.com"}, false)
	assert.Equal(t, http.StatusOK, historyGet(owner, "/api/v1/portal/assets/a1/versions"))
}

func TestSharing_APersonaWithoutItCreatesNoShare(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "r"}
	deps := Deps{
		AssetStore: &mockAssetStore{getAsset: asset}, ShareStore: &mockShareStore{},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		PersonaResolver: func([]string) *PersonaInfo { return &PersonaInfo{Name: "viewer", SharingDenied: true} },
	}
	h := NewHandler(deps, testAuthMiddleware(&User{UserID: "u1", Roles: []string{"viewer"}}))
	for _, path := range []string{
		"/api/v1/portal/assets/a1/shares",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, path,
			bytes.NewBufferString(`{"access_mode":"authenticated"}`)))
		assert.Equal(t, http.StatusForbidden, w.Code, path)
		assert.Contains(t, w.Body.String(), access.ErrSharingNotAllowed)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/me", http.NoBody))
	assert.Contains(t, w.Body.String(), `"sharing":false`)

	allowed := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset}, ShareStore: &mockShareStore{},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		PersonaResolver: func([]string) *PersonaInfo { return &PersonaInfo{Name: "analyst"} },
	}, testAuthMiddleware(&User{UserID: "u1"}))
	w = httptest.NewRecorder()
	allowed.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/me", http.NoBody))
	assert.Contains(t, w.Body.String(), `"sharing":true`)
}

func TestSharing_ACollectionAndAPromptRefuseAPersonaWithoutIt(t *testing.T) {
	denied := func([]string) *PersonaInfo { return &PersonaInfo{Name: "viewer", SharingDenied: true} }

	coll := baseCollection()
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{}, ShareStore: &mockCollectionShareStore{},
		CollectionStore: &collHandlerMockCollStore{getColl: coll},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		PersonaResolver: denied,
	}, testAuthMiddleware(testUser))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/portal/collections/coll-1/shares", bytes.NewBufferString(`{"access_mode":"authenticated"}`)))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), access.ErrSharingNotAllowed)

	ph, store, sstore := newTestPortalPromptShareHandler()
	ph.deps.PersonaResolver = denied
	store.prompts["report"] = &prompt.Prompt{ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"}
	req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares",
		bytes.NewBufferString(`{"shared_with_email":"bob@example.com"}`)), "alice@example.com")
	w = httptest.NewRecorder()
	ph.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Nil(t, sstore.inserted, "a refused prompt share was stored")
}

func TestSharingAllowed_TheAdminRuleAndNoUser(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		PersonaResolver: func([]string) *PersonaInfo { return &PersonaInfo{Name: "admin", SharingDenied: true} },
		AdminRule:       func(roles []string) bool { return len(roles) > 0 && roles[0] == "root" },
	}, nil)
	assert.False(t, h.sharingAllowed(nil))
	assert.True(t, h.sharingAllowed(&User{Roles: []string{"root"}}), "an administrator shares whatever the persona says")
	assert.False(t, h.sharingAllowed(&User{Roles: []string{"x"}}))
}
