package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/contenturl"
)

// #2395: binary content is read by range whether or not the request names
// one, so a download of a large export is never held whole in memory.

const rangedBytes = "0123456789abcdefghij"

func rangedAsset() *Asset {
	return &Asset{
		ID: "a1", OwnerID: "u1", Name: "rows.parquet", ContentType: "application/vnd.apache.parquet",
		S3Bucket: "b", S3Key: "k", SizeBytes: int64(len(rangedBytes)), CurrentVersion: 1,
	}
}

func rangedVersion() *AssetVersion {
	return &AssetVersion{
		AssetID: "a1", Version: 1, S3Bucket: "b", S3Key: "k1",
		ContentType: "application/vnd.apache.parquet", SizeBytes: int64(len(rangedBytes)),
	}
}

func rangedGet(h http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func assertStreamed(t *testing.T, w *httptest.ResponseRecorder, s3 *mockS3Client) {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, rangedBytes, w.Body.String())
	assert.Positive(t, s3.rangeReads)
	assert.Empty(t, s3.getKey, "the object was not read whole")
	assert.NotEmpty(t, w.Header().Get("ETag"))
}

func TestGetAssetContentWholeBinaryIsReadByRange(t *testing.T) {
	s3 := &mockS3Client{getData: []byte(rangedBytes)}
	h := newTestHandler(&mockAssetStore{getAsset: rangedAsset()}, &mockShareStore{}, s3, &User{UserID: "u1"})

	w := rangedGet(h, "/api/v1/portal/assets/a1/content", nil)
	assertStreamed(t, w, s3)

	again := rangedGet(h, "/api/v1/portal/assets/a1/content", map[string]string{"If-None-Match": w.Header().Get("ETag")})
	assert.Equal(t, http.StatusNotModified, again.Code)
}

func TestGetAssetVersionContentWholeBinaryIsReadByRange(t *testing.T) {
	s3 := &mockS3Client{getData: []byte(rangedBytes)}
	h := newTestHandlerWithVersions(&mockAssetStore{getAsset: rangedAsset()}, &mockShareStore{},
		&mockVersionStore{getVersion: rangedVersion()}, s3, &User{UserID: "u1"})

	assertStreamed(t, rangedGet(h, "/api/v1/portal/assets/a1/versions/1/content", nil), s3)
}

func TestSignedContentWholeBinaryIsReadByRange(t *testing.T) {
	s3 := &mockS3Client{getData: []byte(rangedBytes)}
	h := contentURLHandler(&mockAssetStore{getAsset: rangedAsset()}, &mockVersionStore{getVersion: rangedVersion()}, s3, nil)
	token := contenturl.Sign(testContentURLKey, contenturl.Target{AssetID: "a1", Version: 1, Expires: time.Now().Add(time.Minute)})

	assertStreamed(t, rangedGet(h, contenturl.Path+token, nil), s3)
}

func TestPublicAssetContentWholeBinaryIsReadByRange(t *testing.T) {
	s3 := &mockS3Client{getData: []byte(rangedBytes)}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: rangedAsset()},
		ShareStore: &mockShareStore{getByTokenRes: &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}},
		S3Client:   s3,
	}, nil)

	assertStreamed(t, rangedGet(h, "/portal/view/tok1/content", nil), s3)
}

// TestGetAssetContentWholeBinaryMissingObjectFallsBack: a ranged read that
// cannot read the object leaves the answer to the whole-object path, so a
// storage fault is reported as one rather than a 200 cut off after headers.
func TestGetAssetContentWholeBinaryMissingObjectFallsBack(t *testing.T) {
	s3 := &mockS3Client{objects: map[string][]byte{}}
	h := newTestHandler(&mockAssetStore{getAsset: rangedAsset()}, &mockShareStore{}, s3, &User{UserID: "u1"})

	w := rangedGet(h, "/api/v1/portal/assets/a1/content", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "k", s3.getKey)
}

// TestGetMeReportsTheEmbedHosts: the asset frame reads the list it enforces
// from /me (#2069): the deployment's list, the default when none is wired,
// and [] rather than null when embeds are off.
func TestGetMeReportsTheEmbedHosts(t *testing.T) {
	for name, tc := range map[string]struct {
		hosts func(context.Context) []string
		want  string
	}{
		"the deployment's list": {func(context.Context) []string { return []string{"https://cdn.example.com"} }, `["https://cdn.example.com"]`},
		"turned off":            {func(context.Context) []string { return nil }, `[]`},
		"none wired":            {nil, `["https://fast.wistia.net","https://fast.wistia.com","https://www.youtube-nocookie.com","https://www.youtube.com","https://player.vimeo.com"]`},
	} {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(Deps{
				AssetStore: &mockAssetStore{}, ShareStore: &mockShareStore{}, S3Client: &mockS3Client{},
				EmbedHosts: tc.hosts,
			}, testAuthMiddleware(&User{UserID: "u1"}))
			w := rangedGet(h, "/api/v1/portal/me", nil)
			require.Equal(t, http.StatusOK, w.Code)
			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.JSONEq(t, tc.want, string(body["embed_hosts"]))
		})
	}
}

// TestPublicViewCarriesTheEmbedHosts: the share page's policy names the
// deployment's hosts in frame-src and media-src.
func TestPublicViewCarriesTheEmbedHosts(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "deck", ContentType: "text/html", S3Bucket: "b", S3Key: "k", SizeBytes: 5}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}},
		S3Client:   &mockS3Client{getData: []byte("<p/>"), getCT: "text/html"},
		EmbedHosts: func(context.Context) []string { return []string{"https://cdn.example.com"} },
	}, nil)
	w := rangedGet(h, "/portal/view/tok1", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	csp := w.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "frame-src blob: data: 'self' https://cdn.example.com;")
	assert.Contains(t, csp, "media-src 'self' blob: data: https://cdn.example.com;")
}
