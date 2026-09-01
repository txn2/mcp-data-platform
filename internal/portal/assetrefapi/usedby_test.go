package assetrefapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// usedByPath is the resource's "what is holding this up?" list.
func usedByPath(res string) string { return "/api/v1/portal/resources/" + res + "/used-by" }

// assetUsedByPath is the same question asked of an asset (#1488).
func assetUsedByPath(id string) string { return "/api/v1/portal/assets/" + id + "/used-by" }

func TestUsedByUnauthenticated(t *testing.T) {
	h := newHarness()
	rec := h.do(t, nil, http.MethodGet, usedByPath(logoID), "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// A resource the caller cannot read answers not found, so the list cannot be
// used to learn that a file exists.
func TestUsedByRefusesUnreadableResource(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodGet, usedByPath(chartID), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A resource nothing references answers an empty list, which is what lets the
// section render nothing rather than an empty panel.
func TestUsedByNoReferences(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	assert.Empty(t, body.Data)
	assert.Zero(t, body.Total)
	assert.Zero(t, body.Hidden)
}

// Two assets referencing one file are both listed, and the one carrying a
// public link is flagged: that flag is the reason the section exists.
func TestUsedByListsBothAndFlagsPublic(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())
	h.declare(otherID, logoRef())
	h.shares.shareWith(otherID, ownerEmail, portaldomain.PermissionViewer)
	h.shares.summaries[otherID] = portaldomain.ShareSummary{HasPublicLink: true}

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	require.Len(t, body.Data, 2)
	assert.Zero(t, body.Hidden)

	public := map[string]bool{}
	for _, a := range body.Data {
		public[a.ID] = a.Public
	}
	assert.False(t, public[assetID])
	assert.True(t, public[otherID])
}

// An asset the reader cannot open is counted and not named: someone deciding
// whether to delete a file has to know the list is not the whole of what would
// break, without being told whose report it is.
func TestUsedByCountsAssetsTheReaderCannotOpen(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())
	h.declare(otherID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, assetID, body.Data[0].ID)
	assert.Equal(t, 1, body.Hidden)
	assert.NotContains(t, rec.Body.String(), "Someone else's memo")
}

// An administrator sees every referencing asset, which is what makes the
// console's answer to "what would this delete break?" the whole answer.
func TestUsedByAdminSeesEveryAsset(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())
	h.declare(otherID, logoRef())
	admin := &access.User{UserID: "user-admin", Email: "admin@example.com", Roles: []string{"admin"}}

	rec := h.do(t, admin, http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	assert.Len(t, body.Data, 2)
	assert.Zero(t, body.Hidden)
}

// A reference whose asset is gone is neither listed nor counted: reporting it
// would say a file is holding up something that no longer exists.
func TestUsedBySkipsVanishedAssets(t *testing.T) {
	h := newHarness()
	h.declare("asset-gone", logoRef())

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	assert.Empty(t, body.Data)
	assert.Zero(t, body.Hidden)
}

func TestUsedByStoreFailure(t *testing.T) {
	h := newHarness()
	h.refs.byResErr = errStore

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// A share read that fails costs the flag and nothing else: the assets are still
// listed, because not knowing whether one is public is a smaller failure than
// hiding that the file is referenced at all.
func TestUsedByToleratesShareFailure(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())
	h.shares.sumErr = errStore

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.False(t, body.Data[0].Public)
}

// An asset read that fails is a 500, never an empty list. An empty list here
// reads as "nothing uses this file", which is the one wrong answer someone
// about to delete it must not be given.
func TestUsedByAssetReadFailure(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())
	h.assets.getErr = errStore

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// The answer is bounded, and says when the bound cut it. Narrowing to the
// assets a reader may open costs a share query per asset, so an unbounded read
// of a globally referenced file would turn one page view into a query per
// asset in the deployment.
func TestUsedByBoundsTheAnswerAndSaysSo(t *testing.T) {
	h := newHarness()
	for i := range maxReferencingAssets + 5 {
		id := fmt.Sprintf("asset-%03d", i)
		h.assets.byID[id] = &portaldomain.Asset{
			ID: id, OwnerID: ownerID, OwnerEmail: ownerEmail, Name: id,
			ContentType: "text/html", S3Bucket: assetBucket, S3Key: assetKey,
		}
		h.declare(id, logoRef())
	}

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	assert.Len(t, body.Data, maxReferencingAssets)
	assert.True(t, body.Truncated, "a short list must never read as the whole of it")
}

// Under the bound the answer is complete and says so by not saying otherwise.
func TestUsedByWithinTheBoundIsNotTruncated(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, usedByPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, decode[usedByResponse](t, rec).Truncated)
}

// TestAssetUsedByListsReferencingAssets is the acceptance criterion for the
// asset's Used by section (#1488): the assets reading this one's content, so
// the cost of editing or deleting it is visible before either.
func TestAssetUsedByListsReferencingAssets(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(otherID, ownerEmail, portaldomain.PermissionViewer)
	h.shares.summaries[otherID] = portaldomain.ShareSummary{HasPublicLink: true}
	h.declare(otherID, assetRef(assetID, "tok-a"))

	rec := h.do(t, owner(), http.MethodGet, assetUsedByPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, otherID, body.Data[0].ID)
	assert.True(t, body.Data[0].Public,
		"a public referencing asset is what makes this asset's content readable by anyone with that link")
}

// A referencing asset the reader cannot open is counted and not named: someone
// deciding whether to delete has to know the list is not the whole of what
// would break.
func TestAssetUsedByCountsHiddenReferences(t *testing.T) {
	h := newHarness()
	// otherID belongs to the reader and is shared with nobody, so the owner of
	// the referenced asset cannot open it.
	h.declare(otherID, assetRef(assetID, "tok-a"))

	rec := h.do(t, owner(), http.MethodGet, assetUsedByPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	assert.Empty(t, body.Data)
	assert.Equal(t, 1, body.Hidden,
		"an asset reading this one's content is still reading it, whoever owns it")
}

// The reader of the referencing asset is refused the question entirely when
// they cannot open the asset being asked about: who reads an asset is part of
// the asset, not public knowledge about it.
func TestAssetUsedByRefusesAStranger(t *testing.T) {
	h := newHarness()
	h.declare(otherID, assetRef(assetID, "tok-a"))

	rec := h.do(t, reader(), http.MethodGet, assetUsedByPath(assetID), "")

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// The two used-by routes answer about their own kind only: a resource and an
// asset that share an id do not appear in each other's list.
func TestUsedByIsScopedToTheKind(t *testing.T) {
	h := newHarness()
	h.declare(otherID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, assetUsedByPath(logoID), "")

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"logoID names no asset, so the asset route answers not found rather than listing the resource's readers")
}

// An asset nothing references answers an empty list rather than an error.
func TestAssetUsedByNoReferences(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodGet, assetUsedByPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[usedByResponse](t, rec)
	assert.Empty(t, body.Data)
	assert.Zero(t, body.Hidden)
}

func TestAssetUsedByUnauthenticated(t *testing.T) {
	h := newHarness()
	rec := h.do(t, nil, http.MethodGet, assetUsedByPath(assetID), "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestUsedByAllowsAnAdministratorOutsideThePersona is the target-end half of
// #1584. This panel is drawn on the resource viewer page, whose own routes
// admit an administrator on every library, so answering not-found here put the
// page and one of its panels in disagreement inside a single request cycle.
func TestUsedByAllowsAnAdministratorOutsideThePersona(t *testing.T) {
	h := newHarness()
	h.declare(assetID, chartRef())

	rec := h.do(t, admin(), http.MethodGet, usedByPath(chartID), "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode[usedByResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, assetID, body.Data[0].ID)
}
