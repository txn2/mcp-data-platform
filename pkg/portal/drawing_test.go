package portal

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2302: a content route answers ?render=svg for a D2 source with the drawing
// the platform renders, on the asset page and a share link alike.

const d2Source = "vpc: {\n  web -> api\n}\nusers -> vpc.web\n"

func d2Asset() *Asset {
	return &Asset{ID: "a1", OwnerID: "u1", Name: "arch", ContentType: "text/vnd.d2", S3Bucket: "b", S3Key: "k", SizeBytes: int64(len(d2Source))}
}

func TestContentRouteDrawsAD2Source(t *testing.T) {
	s3 := &mockS3Client{getData: []byte(d2Source), getCT: "text/vnd.d2"}
	h := newTestHandler(&mockAssetStore{getAsset: d2Asset()}, &mockShareStore{}, s3, &User{UserID: "u1"})

	src := rangedGet(h, "/api/v1/portal/assets/a1/content", nil)
	require.Equal(t, http.StatusOK, src.Code)
	assert.Equal(t, d2Source, src.Body.String(), "without the parameter the source is served unchanged")

	light := rangedGet(h, "/api/v1/portal/assets/a1/content?render=svg", nil)
	require.Equal(t, http.StatusOK, light.Code, light.Body.String())
	assert.Equal(t, "image/svg+xml", light.Header().Get("Content-Type"))
	assert.Contains(t, light.Header().Get("Content-Security-Policy"), "sandbox")
	for _, label := range []string{"vpc", "web", "api", "users"} {
		assert.Contains(t, light.Body.String(), label)
	}
	dark := rangedGet(h, "/api/v1/portal/assets/a1/content?render=svg&theme=dark", nil)
	require.Equal(t, http.StatusOK, dark.Code)
	assert.NotEqual(t, light.Body.String(), dark.Body.String())
}

func TestContentRouteDrawsAD2SourceOnAShare(t *testing.T) {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: d2Asset()},
		ShareStore: &mockShareStore{getByTokenRes: &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}},
		S3Client:   &mockS3Client{getData: []byte(d2Source), getCT: "text/vnd.d2"},
	}, nil)
	w := rangedGet(h, "/portal/view/tok1/content?render=svg", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, strings.Contains(w.Body.String(), "<svg"))
}

func TestContentRouteRefusesAD2SourceItCannotDraw(t *testing.T) {
	asset := d2Asset()
	s3 := &mockS3Client{getData: []byte("a: {shape: nosuchshape}"), getCT: "text/vnd.d2"}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, s3, &User{UserID: "u1"})
	w := rangedGet(h, "/api/v1/portal/assets/a1/content?render=svg", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "could not be drawn")
}

func TestRenderParameterLeavesOtherTypesAlone(t *testing.T) {
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "n", ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", SizeBytes: 3}
	h := newTestHandler(&mockAssetStore{getAsset: asset}, &mockShareStore{}, &mockS3Client{getData: []byte("# x"), getCT: "text/markdown"}, &User{UserID: "u1"})
	w := rangedGet(h, "/api/v1/portal/assets/a1/content?render=svg", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "# x", w.Body.String())
}
