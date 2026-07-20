package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mw "github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// withGate returns r carrying what publicShareGate would have put in the
// context, for tests that drive a public handler directly instead of through
// the mux.
func withGate(r *http.Request, share *Share, viewer *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), gateCtxKey{}, gateResult{Share: share, Viewer: viewer}))
}

// --- End-to-end route matrix ---

// publicRoutes are every path registered on publicMux. Each one serves asset
// bytes, collection bytes, or a viewer page, so each one must be gated (#999).
func publicRoutes() []string {
	return []string{
		"/portal/view/tok1",
		"/portal/view/tok1/content",
		"/portal/view/tok1/thumbnail",
		"/portal/view/tok1/collection-thumbnail",
		"/portal/view/tok1/items/a1/content",
		"/portal/view/tok1/items/a1/thumbnail",
		"/portal/view/tok1/items/a1/view",
	}
}

// shareGateHandler assembles a handler whose stores answer for both an asset
// share and a collection share, so one fixture drives all seven routes. The
// authenticator resolves any request carrying X-API-Key to viewer.
func shareGateHandler(share *Share, viewer *User) *Handler {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "owner", Name: "Doc", ContentType: "text/plain",
		S3Bucket: "b1", S3Key: "assets/a1", ThumbnailS3Key: "thumbs/a1",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	coll := &Collection{
		ID: "c1", Name: "Coll", OwnerID: "owner", ThumbnailS3Key: "thumbs/c1",
		Sections:  []CollectionSection{{Items: []CollectionItem{{AssetID: "a1"}}}},
		CreatedAt: now, UpdatedAt: now,
	}

	deps := Deps{
		AssetStore:      &mockAssetStore{getAsset: asset},
		ShareStore:      &mockShareStore{getByTokenRes: share},
		CollectionStore: &mockCollectionStore{getResult: coll},
		S3Client:        &mockS3Client{getData: []byte("file content"), getCT: "text/plain"},
		S3Bucket:        "b1",
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	if viewer != nil {
		deps.Authenticator = NewAuthenticator(&mockAuthenticator{
			info: &mw.UserInfo{UserID: viewer.UserID, Email: viewer.Email},
		})
	}
	return NewHandler(deps, nil)
}

// requestRoute drives one public route. A non-nil viewer means the request
// carries credentials the handler's authenticator will resolve.
func requestRoute(h *Handler, path string, signedIn bool) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	if signedIn {
		req.Header.Set("X-API-Key", "key")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// assetShare and collectionShare produce a share of each shape with the given
// access mode, so a route matrix can be run against whichever shape the route
// under test needs.
func assetShare(mode ShareAccessMode) *Share {
	return &Share{
		ID: "s1", Token: "tok1", AssetID: "a1", CreatedBy: "alice@example.com",
		SharedWithEmail: "bob@example.com", AccessMode: mode, NoticeText: defaultNoticeText,
	}
}

func collectionShare(mode ShareAccessMode) *Share {
	return &Share{
		ID: "s2", Token: "tok1", CollectionID: "c1", CreatedBy: "alice@example.com",
		SharedWithEmail: "bob@example.com", AccessMode: mode, NoticeText: defaultNoticeText,
	}
}

// shareForRoute picks the share shape a route needs: the two item routes and
// the collection thumbnail only resolve against a collection share.
func shareForRoute(path string, mode ShareAccessMode) *Share {
	switch path {
	case "/portal/view/tok1/collection-thumbnail",
		"/portal/view/tok1/items/a1/content",
		"/portal/view/tok1/items/a1/thumbnail",
		"/portal/view/tok1/items/a1/view":
		return collectionShare(mode)
	default:
		return assetShare(mode)
	}
}

func TestPublicRoutesRefuseAnonymousOnRestrictedShare(t *testing.T) {
	for _, path := range publicRoutes() {
		t.Run(path, func(t *testing.T) {
			h := shareGateHandler(shareForRoute(path, AccessModeRestricted), nil)
			w := requestRoute(h, path, false)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "sign in")
			assert.NotContains(t, w.Body.String(), "file content")
		})
	}
}

func TestPublicRoutesRefuseAnonymousOnAuthenticatedShare(t *testing.T) {
	for _, path := range publicRoutes() {
		t.Run(path, func(t *testing.T) {
			h := shareGateHandler(shareForRoute(path, AccessModeAuthenticated), nil)
			w := requestRoute(h, path, false)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.NotContains(t, w.Body.String(), "file content")
		})
	}
}

func TestPublicRoutesRefuseNonRecipientOnRestrictedShare(t *testing.T) {
	stranger := &User{UserID: "u-eve", Email: "eve@example.com"}

	for _, path := range publicRoutes() {
		t.Run(path, func(t *testing.T) {
			h := shareGateHandler(shareForRoute(path, AccessModeRestricted), stranger)
			w := requestRoute(h, path, true)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), shareaccess.MsgNotRecipient)
			assert.NotContains(t, w.Body.String(), "file content")
		})
	}
}

func TestPublicRoutesAdmitRecipientOnRestrictedShare(t *testing.T) {
	recipient := &User{UserID: "u-bob", Email: "bob@example.com"}

	for _, path := range publicRoutes() {
		t.Run(path, func(t *testing.T) {
			h := shareGateHandler(shareForRoute(path, AccessModeRestricted), recipient)
			w := requestRoute(h, path, true)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestPublicRoutesAdmitAnySignedInUserOnAuthenticatedShare(t *testing.T) {
	stranger := &User{UserID: "u-eve", Email: "eve@example.com"}

	for _, path := range publicRoutes() {
		t.Run(path, func(t *testing.T) {
			h := shareGateHandler(shareForRoute(path, AccessModeAuthenticated), stranger)
			w := requestRoute(h, path, true)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestPublicRoutesAdmitAnonymousOnPublicShare(t *testing.T) {
	for _, path := range publicRoutes() {
		t.Run(path, func(t *testing.T) {
			h := shareGateHandler(shareForRoute(path, AccessModePublic), nil)
			w := requestRoute(h, path, false)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// TestPublicContentRouteIsGatedIndependently pins the failure mode #999
// describes: /content reads S3 without going through the page handler, so a
// gate applied only to the page would leave the bytes readable.
func TestPublicContentRouteIsGatedIndependently(t *testing.T) {
	h := shareGateHandler(assetShare(AccessModeRestricted), nil)

	page := requestRoute(h, "/portal/view/tok1", false)
	require.Equal(t, http.StatusForbidden, page.Code)

	content := requestRoute(h, "/portal/view/tok1/content", false)
	assert.Equal(t, http.StatusForbidden, content.Code)
	assert.NotContains(t, content.Body.String(), "file content")
}

func TestPublicShareGateRevokedAndExpiredStillGone(t *testing.T) {
	past := time.Now().Add(-time.Hour)

	revoked := assetShare(AccessModePublic)
	revoked.Revoked = true
	assert.Equal(t, http.StatusGone, requestRoute(shareGateHandler(revoked, nil), "/portal/view/tok1", false).Code)

	expired := assetShare(AccessModePublic)
	expired.ExpiresAt = &past
	assert.Equal(t, http.StatusGone, requestRoute(shareGateHandler(expired, nil), "/portal/view/tok1", false).Code)
}

func TestPublicShareGateUnknownTokenIs404(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{getByTokenErr: assert.AnError},
		S3Client:   &mockS3Client{},
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	assert.Equal(t, http.StatusNotFound, requestRoute(h, "/portal/view/nope", false).Code)
}

// TestPublicShareGateNilShareIs404 covers a store that returns (nil, nil):
// the gate must refuse rather than hand a nil share to a handler.
func TestPublicShareGateNilShareIs404(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, nil)

	assert.Equal(t, http.StatusNotFound, requestRoute(h, "/portal/view/tok1", false).Code)
}

// --- Share creation ---

// createAssetShare posts a create-share request as the asset owner and returns
// the recorder plus the decoded response.
func createAssetShare(t *testing.T, body string) (*httptest.ResponseRecorder, shareResponse) {
	t.Helper()
	h := newTestHandler(
		&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1"}},
		&mockShareStore{},
		&mockS3Client{},
		&User{UserID: "u1", Email: "alice@example.com"},
	)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/portal/assets/a1/shares", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp shareResponse
	if w.Code == http.StatusCreated {
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	}
	return w, resp
}

func TestCreateShareAccessModeDefaults(t *testing.T) {
	tests := []struct {
		name string
		body string
		want ShareAccessMode
	}{
		{"named recipient defaults to restricted", `{"shared_with_email":"bob@example.com"}`, AccessModeRestricted},
		{"user id recipient defaults to restricted", `{"shared_with_user_id":"u2"}`, AccessModeRestricted},
		{"no recipient defaults to authenticated", `{"expires_in":"24h"}`, AccessModeAuthenticated},
		{"public is honored when asked for", `{"access_mode":"public"}`, AccessModePublic},
		{
			"public is honored for a named recipient",
			`{"shared_with_email":"bob@example.com","access_mode":"public"}`,
			AccessModePublic,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, resp := createAssetShare(t, tc.body)
			require.Equal(t, http.StatusCreated, w.Code)
			assert.Equal(t, tc.want, resp.Share.AccessMode)
		})
	}
}

func TestCreateShareRejectsIncoherentAccessMode(t *testing.T) {
	t.Run("restricted without a recipient", func(t *testing.T) {
		w, _ := createAssetShare(t, `{"access_mode":"restricted"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "requires shared_with_email")
	})

	t.Run("unknown mode", func(t *testing.T) {
		w, _ := createAssetShare(t, `{"access_mode":"everyone"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "invalid access_mode")
	})
}
