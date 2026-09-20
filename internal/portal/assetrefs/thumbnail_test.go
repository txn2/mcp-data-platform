package assetrefs_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// A reference's tile is served through the reference's own token rather than
// through the target's route, because the reader of a shared asset has no
// access to the target at all (#1794). These hold that: the same token that
// serves the bytes serves the picture, and nothing else about the answer
// changes.

const (
	pdfToken      = "tok-pdf"
	pdfURI        = "mcp://global/reports/q3.pdf"
	assetRefToken = "tok-asset"
)

// tilePath is the reference URL with the query the panel appends to ask for
// the tile instead of the file.
func tilePath(token, query string) string {
	return assetrefs.PathPrefix + testAssetID + "/" + token + "?thumbnail=1" + query
}

// pdfRefs is an asset referencing one managed PDF and one other asset.
func pdfRefs() *fakeRefs {
	refs := newFakeRefs()
	refs.byAsset[testAssetID] = []assetrefs.Ref{
		{
			AssetID: testAssetID, TargetKind: assetrefs.TargetResource,
			TargetID: "res-pdf", URI: pdfURI, RefToken: pdfToken,
		},
		{
			AssetID: testAssetID, TargetKind: assetrefs.TargetAsset,
			TargetID: "asset_target", URI: "mcp://asset/asset_target", RefToken: assetRefToken,
		},
	}
	return refs
}

// pdfResources adds a PDF carrying a drawn tile to the standard fixtures.
func pdfResources(darkKey string) *fakeResources {
	res := fixtureResources()
	res.byID["res-pdf"] = &resource.Resource{
		ID: "res-pdf", Scope: resource.ScopeGlobal, Filename: "q3.pdf",
		DisplayName: "Q3 report", MIMEType: "application/pdf",
		S3Key: "resources/global/q3.pdf", URI: pdfURI,
		ThumbnailS3Key:     "resources/global/.thumbnail.png",
		ThumbnailDarkS3Key: darkKey,
		UpdatedAt:          time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	return res
}

// pdfDeps wires the route over that library, with both tiles in the store.
func pdfDeps(refs *fakeRefs, res *fakeResources, blobs *fakeBlobs) assetrefs.Deps {
	return assetrefs.Deps{Refs: refs, Resources: res, Blobs: blobs, Bucket: resourcesBucket}
}

func tileBlobs() *fakeBlobs {
	return &fakeBlobs{byKey: map[string]string{
		"resources/global/.thumbnail.png":      "LIGHTPNG",
		"resources/global/.thumbnail_dark.png": "DARKPNG",
		"assets/asset_target/.thumbnail.png":   "ASSETPNG",
	}}
}

// TestThumbnailServesTheResourceTileOnTheReferenceToken is the whole point:
// the token that serves the PDF serves its picture, with no session.
func TestThumbnailServesTheResourceTileOnTheReferenceToken(t *testing.T) {
	blobs := tileBlobs()
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), blobs))

	rec := get(t, h, tilePath(pdfToken, ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "LIGHTPNG", rec.Body.String())
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"),
		"a tile is a PNG whatever the target's own type is")
	assert.Equal(t, resourcesBucket, blobs.bucket)
}

// TestThumbnailServesTheDarkCaptureWhenOneIsStored: a themeable family stores
// two tiles and a dark portal must get the dark one.
func TestThumbnailServesTheDarkCaptureWhenOneIsStored(t *testing.T) {
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources("resources/global/.thumbnail_dark.png"), tileBlobs()))

	rec := get(t, h, tilePath(pdfToken, "&variant=dark"))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "DARKPNG", rec.Body.String())
}

// TestThumbnailFallsBackToTheLightCapture: a PDF, an SVG and a raster image
// are drawn as stored and keep one tile. The panel asks for the dark variant
// in a dark portal whatever the family, so an empty dark key must answer with
// the light picture rather than with nothing.
func TestThumbnailFallsBackToTheLightCapture(t *testing.T) {
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), tileBlobs()))

	rec := get(t, h, tilePath(pdfToken, "&variant=dark"))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "LIGHTPNG", rec.Body.String())
}

// TestThumbnailIsNotFoundWhenNoneWasDrawn: a file too large to draw, one still
// queued, and one the renderer refused all answer the same way, which is what
// tells the panel to fall back to the content-type icon.
func TestThumbnailIsNotFoundWhenNoneWasDrawn(t *testing.T) {
	res := pdfResources("")
	res.byID["res-pdf"].ThumbnailS3Key = ""
	h := serveFixture(t, pdfDeps(pdfRefs(), res, tileBlobs()))

	rec := get(t, h, tilePath(pdfToken, ""))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestThumbnailIsNotFoundWhenTheStoredObjectIsGone: a key recorded on the row
// whose object has been swept is the missing picture, not a fault.
func TestThumbnailIsNotFoundWhenTheStoredObjectIsGone(t *testing.T) {
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), &fakeBlobs{byKey: map[string]string{}}))

	rec := get(t, h, tilePath(pdfToken, ""))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestThumbnailRefusesATokenFromAnotherAsset: the tile is behind the same
// capability the bytes are, so a token pasted onto another asset's path
// resolves to nothing here too.
func TestThumbnailRefusesATokenFromAnotherAsset(t *testing.T) {
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), tileBlobs()))

	rec := get(t, h, assetrefs.PathPrefix+"asset_other/"+pdfToken+"?thumbnail=1")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "res-pdf", "a refusal must disclose nothing")
}

// TestThumbnailServesAReferencedAssetsTile: a reference can point at another
// asset, and that asset's tile is served from the portal's own bucket.
func TestThumbnailServesAReferencedAssetsTile(t *testing.T) {
	blobs := tileBlobs()
	deps := pdfDeps(pdfRefs(), pdfResources(""), blobs)
	deps.Assets = &fakeAssets{byID: map[string]*portaldomain.Asset{
		"asset_target": {
			ID: "asset_target", Name: "Deck", ContentType: "application/pdf",
			S3Bucket: portalBucket, S3Key: "assets/asset_target/deck.pdf",
			ThumbnailS3Key: "assets/asset_target/.thumbnail.png",
		},
	}}
	deps.AssetBlobs = blobs
	h := serveFixture(t, deps)

	rec := get(t, h, tilePath(assetRefToken, ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ASSETPNG", rec.Body.String())
	assert.Equal(t, portalBucket, blobs.bucket,
		"a referenced asset's tile lives in the portal's bucket, not the resource layer's")
}

// TestThumbnailRefusesAnAssetTileWithoutTheAssetLayer: a deployment with no
// portal blob client says so rather than panicking, which is the rule the
// bytes path already follows.
func TestThumbnailRefusesAnAssetTileWithoutTheAssetLayer(t *testing.T) {
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), tileBlobs()))

	rec := get(t, h, tilePath(assetRefToken, ""))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestThumbnailIsNotFoundForADeletedAsset: the tile follows the target, and a
// deleted asset has none.
func TestThumbnailIsNotFoundForADeletedAsset(t *testing.T) {
	deleted := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	blobs := tileBlobs()
	deps := pdfDeps(pdfRefs(), pdfResources(""), blobs)
	deps.Assets = &fakeAssets{byID: map[string]*portaldomain.Asset{
		"asset_target": {ID: "asset_target", DeletedAt: &deleted, ThumbnailS3Key: "assets/asset_target/.thumbnail.png"},
	}}
	deps.AssetBlobs = blobs
	h := serveFixture(t, deps)

	rec := get(t, h, tilePath(assetRefToken, ""))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestThumbnailIsNotFoundForAMissingResource: the reference row outlives the
// file it points at, and the tile answer says the file is gone.
func TestThumbnailIsNotFoundForAMissingResource(t *testing.T) {
	res := pdfResources("")
	delete(res.byID, "res-pdf")
	h := serveFixture(t, pdfDeps(pdfRefs(), res, tileBlobs()))

	rec := get(t, h, tilePath(pdfToken, ""))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestWithoutTheQueryTheBytesAreStillServed: the tile is an opt-in on the same
// route, so every existing caller of it is unaffected.
func TestWithoutTheQueryTheBytesAreStillServed(t *testing.T) {
	blobs := tileBlobs()
	blobs.byKey["resources/global/q3.pdf"] = "PDFBYTES"
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), blobs))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+pdfToken)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PDFBYTES", rec.Body.String())
}

// TestAnyOtherThumbnailValueServesTheBytes: only the value the panel sends
// switches the route, so a stray "?thumbnail=true" reads as the bytes rather
// than as an unrecognized tile request.
func TestAnyOtherThumbnailValueServesTheBytes(t *testing.T) {
	blobs := tileBlobs()
	blobs.byKey["resources/global/q3.pdf"] = "PDFBYTES"
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), blobs))

	rec := get(t, h, assetrefs.PathPrefix+testAssetID+"/"+pdfToken+"?thumbnail=true")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PDFBYTES", rec.Body.String())
}

// TestThumbnailRefusesAResourceTileWithoutTheResourceLayer: a deployment with
// no managed-resource layer says so rather than panicking, which is the rule
// the bytes path follows.
func TestThumbnailRefusesAResourceTileWithoutTheResourceLayer(t *testing.T) {
	blobs := tileBlobs()
	deps := assetrefs.Deps{Refs: pdfRefs(), Assets: &fakeAssets{}, AssetBlobs: blobs}
	h := serveFixture(t, deps)

	rec := get(t, h, tilePath(pdfToken, ""))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestThumbnailRefusesAKindThisBuildDoesNotKnow: a reference kind written by a
// later version is answered as a missing target rather than guessed at, which
// is what the bytes path does with it. An asset rolled back onto an older
// binary shows an icon instead of the wrong picture.
func TestThumbnailRefusesAKindThisBuildDoesNotKnow(t *testing.T) {
	refs := newFakeRefs()
	refs.byAsset[testAssetID] = []assetrefs.Ref{{
		AssetID: testAssetID, TargetKind: "dataset-from-the-future",
		TargetID: "x1", URI: "mcp://future/x1", RefToken: pdfToken,
	}}
	h := serveFixture(t, pdfDeps(refs, pdfResources(""), tileBlobs()))

	rec := get(t, h, tilePath(pdfToken, ""))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestThumbnailReportsAStorageFault: an object store that is down is a fault,
// not a missing picture, and is answered as one rather than as "no tile".
func TestThumbnailReportsAStorageFault(t *testing.T) {
	blobs := tileBlobs()
	blobs.err = errors.New("the object store is unreachable")
	h := serveFixture(t, pdfDeps(pdfRefs(), pdfResources(""), blobs))

	rec := get(t, h, tilePath(pdfToken, ""))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "unreachable", "a fault must not disclose the store's own message")
}
