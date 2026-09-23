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

	"github.com/txn2/mcp-data-platform/internal/portal/contenturl"
)

// Signed content URLs (#1848): a reader mints one, anybody holding it
// downloads that exact version until it expires, and it is 403 afterwards.

var testContentURLKey = []byte("0123456789abcdef0123456789abcdef")

func contentURLHandler(assets *mockAssetStore, versions *mockVersionStore, s3 *mockS3Client, user *User) *Handler {
	deps := Deps{
		AssetStore: assets, ShareStore: &mockShareStore{}, VersionStore: versions, S3Client: s3,
		S3Bucket: "test-bucket", PublicBaseURL: "https://example.com", ContentURLKey: testContentURLKey,
		RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

func serveContentURL(h *Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestContentURL_MintAndDownload(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "report.csv", CurrentVersion: 2}
	ver := &AssetVersion{AssetID: "a1", Version: 2, S3Key: "k2", S3Bucket: "b", ContentType: "text/csv"}
	h := contentURLHandler(&mockAssetStore{getAsset: asset}, &mockVersionStore{getVersion: ver},
		&mockS3Client{getData: []byte("a,b\n1,2\n"), getCT: "text/csv"}, &User{UserID: "u1"})

	w := serveContentURL(h, "/api/v1/portal/assets/a1/content-url?ttl=60")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var minted contentURLResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &minted))
	assert.Equal(t, 2, minted.Version, "the current version by default")
	assert.True(t, strings.HasPrefix(minted.Path, contenturl.Path))
	assert.Equal(t, "https://example.com"+minted.Path, minted.URL)
	assert.WithinDuration(t, time.Now().Add(time.Minute), minted.ExpiresAt, 2*time.Second)

	// The download takes no session: an anonymous handler serves it.
	anon := contentURLHandler(&mockAssetStore{getAsset: asset}, &mockVersionStore{getVersion: ver},
		&mockS3Client{getData: []byte("a,b\n1,2\n"), getCT: "text/csv"}, nil)
	w = serveContentURL(anon, minted.Path)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "a,b\n1,2\n", w.Body.String())
}

func TestContentURL_ExpiredOrAlteredIs403(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", CurrentVersion: 1}
	h := contentURLHandler(&mockAssetStore{getAsset: asset}, &mockVersionStore{getVersion: &AssetVersion{Version: 1}},
		&mockS3Client{}, nil)
	expired := contenturl.Sign(testContentURLKey, contenturl.Target{AssetID: "a1", Version: 1, Expires: time.Now().Add(-time.Second)})
	w := serveContentURL(h, contenturl.Path+expired)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "expired")

	w = serveContentURL(h, contenturl.Path+"not-a-token")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestContentURL_Refusals(t *testing.T) {
	live := contenturl.Sign(testContentURLKey, contenturl.Target{AssetID: "a1", Version: 1, Expires: time.Now().Add(time.Minute)})
	deleted := time.Now()
	for name, tc := range map[string]struct {
		assets   *mockAssetStore
		versions *mockVersionStore
		s3       *mockS3Client
		user     *User
		path     string
		want     int
	}{
		"mint anonymously":        {&mockAssetStore{}, &mockVersionStore{}, &mockS3Client{}, nil, "/api/v1/portal/assets/a1/content-url", http.StatusUnauthorized},
		"mint a missing asset":    {&mockAssetStore{getErr: errors.New("no")}, &mockVersionStore{}, &mockS3Client{}, &User{UserID: "u1"}, "/api/v1/portal/assets/a1/content-url", http.StatusNotFound},
		"mint another's asset":    {&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "other"}}, &mockVersionStore{}, &mockS3Client{}, &User{UserID: "u1"}, "/api/v1/portal/assets/a1/content-url", http.StatusForbidden},
		"mint a bad version":      {&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1"}}, &mockVersionStore{}, &mockS3Client{}, &User{UserID: "u1"}, "/api/v1/portal/assets/a1/content-url?version=x", http.StatusBadRequest},
		"mint a bad ttl":          {&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1"}}, &mockVersionStore{}, &mockS3Client{}, &User{UserID: "u1"}, "/api/v1/portal/assets/a1/content-url?ttl=-1", http.StatusBadRequest},
		"mint a missing version":  {&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1"}}, &mockVersionStore{getErr: errors.New("no")}, &mockS3Client{}, &User{UserID: "u1"}, "/api/v1/portal/assets/a1/content-url?version=4", http.StatusNotFound},
		"serve a deleted asset":   {&mockAssetStore{getAsset: &Asset{ID: "a1", DeletedAt: &deleted}}, &mockVersionStore{}, &mockS3Client{}, nil, contenturl.Path + live, http.StatusNotFound},
		"serve a removed version": {&mockAssetStore{getAsset: &Asset{ID: "a1"}}, &mockVersionStore{getErr: errors.New("no")}, &mockS3Client{}, nil, contenturl.Path + live, http.StatusNotFound},
		"serve a storage failure": {&mockAssetStore{getAsset: &Asset{ID: "a1"}}, &mockVersionStore{getVersion: &AssetVersion{Version: 1}}, &mockS3Client{getErr: errors.New("boom")}, nil, contenturl.Path + live, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			w := serveContentURL(contentURLHandler(tc.assets, tc.versions, tc.s3, tc.user), tc.path)
			assert.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}

// Without a signing key the routes are not there.
func TestContentURL_UnmountedWithoutAKey(t *testing.T) {
	h := newTestHandlerWithVersions(&mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: "u1"}}, &mockShareStore{},
		&mockVersionStore{}, &mockS3Client{}, &User{UserID: "u1"})
	w := serveContentURL(h, "/api/v1/portal/assets/a1/content-url")
	assert.NotEqual(t, http.StatusOK, w.Code)
}
