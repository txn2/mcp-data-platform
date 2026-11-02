package assetrefs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// rangeBlobs is a store that can read ranges, the way the platform's S3
// adapter can, and counts both kinds of read so a test holds that a binary
// target was never read whole (#2395).
type rangeBlobs struct {
	fakeBlobs
	rangeReads int
	wholeReads int
}

func (f *rangeBlobs) GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error) {
	f.wholeReads++
	return f.fakeBlobs.GetObject(ctx, bucket, key)
}

func (f *rangeBlobs) GetObjectRange(
	_ context.Context, _, key string, offset, length int64,
) (body []byte, size int64, err error) {
	f.rangeReads++
	text, ok := f.byKey[key]
	if !ok {
		return nil, 0, errors.New("NoSuchKey: the specified key does not exist")
	}
	data := []byte(text)
	if offset >= int64(len(data)) {
		return nil, int64(len(data)), nil
	}
	return data[offset:min(offset+length, int64(len(data)))], int64(len(data)), nil
}

const (
	clipKey   = "resources/global/clip.mp4"
	clipBytes = "0123456789abcdefghij"
	clipToken = "tok-clip"
)

// clipDeps wires one reference to an uploaded video whose store reads ranges.
func clipDeps(t *testing.T) (assetrefs.Deps, *rangeBlobs) {
	t.Helper()
	res := fixtureResources()
	clip := *res.byID["res-logo"]
	clip.ID, clip.Filename, clip.MIMEType = "res-clip", "clip.mp4", "video/mp4"
	clip.S3Key, clip.URI, clip.SizeBytes = clipKey, "mcp://global/clip.mp4", int64(len(clipBytes))
	res.byID["res-clip"] = &clip

	refs := newFakeRefs()
	refs.byAsset[testAssetID] = []assetrefs.Ref{{
		AssetID: testAssetID, TargetKind: assetrefs.TargetResource,
		TargetID: "res-clip", URI: clip.URI, RefToken: clipToken,
	}}
	blobs := &rangeBlobs{fakeBlobs{byKey: map[string]string{clipKey: clipBytes}}, 0, 0}
	return assetrefs.Deps{Refs: refs, Resources: res, Blobs: blobs, Bucket: resourcesBucket}, blobs
}

func getWith(t *testing.T, h http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestServeReferencedVideoIsReadByRange is #2395's criterion on the refs route:
// a <video> in an HTML asset plays and seeks through range requests, and each
// is answered from a ranged read of the object rather than the whole file.
func TestServeReferencedVideoIsReadByRange(t *testing.T) {
	deps, blobs := clipDeps(t)
	h := serveFixture(t, deps)
	path := assetrefs.PathPrefix + testAssetID + "/" + clipToken

	seek := getWith(t, h, path, map[string]string{"Range": "bytes=10-14"})
	require.Equal(t, http.StatusPartialContent, seek.Code)
	assert.Equal(t, "abcde", seek.Body.String())
	assert.Equal(t, "bytes 10-14/20", seek.Header().Get("Content-Range"))

	whole := getWith(t, h, path, nil)
	require.Equal(t, http.StatusOK, whole.Code)
	assert.Equal(t, clipBytes, whole.Body.String())
	assert.Equal(t, "video/mp4", whole.Header().Get("Content-Type"))

	assert.Positive(t, blobs.rangeReads)
	assert.Zero(t, blobs.wholeReads, "the clip was never read whole")

	tag := whole.Header().Get("ETag")
	require.NotEmpty(t, tag)
	again := getWith(t, h, path, map[string]string{"If-None-Match": tag})
	assert.Equal(t, http.StatusNotModified, again.Code)
}

// TestServeReferencedVideoDownloadIsAnAttachment keeps #2127's download anchor
// on the ranged path.
func TestServeReferencedVideoDownloadIsAnAttachment(t *testing.T) {
	deps, _ := clipDeps(t)
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+clipToken+"?download=1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Equal(t, clipBytes, rec.Body.String())
}

// TestServeReferencedVideoMissingObjectIs404: a ranged store that cannot read
// the object leaves the answer to the whole-object path, which reports the
// file missing instead of a 200 cut off after its headers.
func TestServeReferencedVideoMissingObjectIs404(t *testing.T) {
	deps, blobs := clipDeps(t)
	delete(blobs.byKey, clipKey)
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+clipToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServeReferencedBinaryAssetIsReadByRange: a binary asset named by
// reference is read by range too; a textual one is still read whole and
// rewritten through its own references.
func TestServeReferencedBinaryAssetIsReadByRange(t *testing.T) {
	deps, assets, _ := assetRefDeps(t, assetRefFixture())
	blobs := &rangeBlobs{fakeBlobs{byKey: map[string]string{targetAssetKey: clipBytes}}, 0, 0}
	deps.AssetBlobs = blobs
	target := assets.byID[targetAssetID]
	target.ContentType, target.SizeBytes = "application/vnd.apache.parquet", int64(len(clipBytes))
	h := serveFixture(t, deps)

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, clipBytes, rec.Body.String())
	assert.Positive(t, blobs.rangeReads)
	assert.Zero(t, blobs.wholeReads)

	target.ContentType = "text/csv"
	blobs.byKey[targetAssetKey] = targetAssetBytes
	target.SizeBytes = int64(len(targetAssetBytes))
	reads := blobs.rangeReads
	text := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+assetToken)
	require.Equal(t, http.StatusOK, text.Code)
	assert.Equal(t, targetAssetBytes, text.Body.String())
	assert.Equal(t, reads, blobs.rangeReads, "textual content is read whole to be rewritten")
	assert.Equal(t, 1, blobs.wholeReads)
}
