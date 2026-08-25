package assetrefapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// refsPath is the asset's reference collection.
func refsPath(asset string) string { return "/api/v1/portal/assets/" + asset + "/resources" }

func TestListRefsUnauthenticated(t *testing.T) {
	h := newHarness()
	rec := h.do(t, nil, http.MethodGet, refsPath(assetID), "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListRefsUnknownAsset(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodGet, refsPath("asset-nope"), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A reader with no grant on the asset is refused the list, because the list
// names the files the asset is made of.
func TestListRefsDeniedToStranger(t *testing.T) {
	h := newHarness()
	rec := h.do(t, reader(), http.MethodGet, refsPath(assetID), "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// An asset with no references answers with an empty list rather than an error,
// which is what lets the panel render nothing instead of an empty panel.
func TestListRefsEmpty(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	assert.Empty(t, body.Data)
	assert.Zero(t, body.Total)
	assert.True(t, body.CanEdit)
	assert.Equal(t, portaldomain.MaxAssetResourceRefs, body.Max)
	assert.Equal(t, assetrefs.GrantNotice, body.Notice,
		"the person and the agent are told the same thing about what a reference gives away")
}

// The listed reference carries the resource behind it, the URL the reference is
// served under, and the lines the content still writes the URI on.
func TestListRefsWithReference(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	got := body.Data[0]
	assert.Equal(t, logoID, got.ResourceID)
	assert.Equal(t, logoURI, got.URI)
	assert.Equal(t, "Company logo", got.DisplayName)
	assert.Equal(t, "image/png", got.MIMEType)
	assert.Equal(t, "global", got.Scope)
	assert.False(t, got.Broken)
	assert.Equal(t, assetrefs.PathPrefix+assetID+"/"+logoToken, got.ContentURL,
		"the panel loads the picture through the grant the reference already makes")

	require.Len(t, got.Occurrences, 1)
	assert.Equal(t, 2, got.Occurrences[0].Line)
	assert.Contains(t, got.Occurrences[0].Snippet, logoURI)
}

// A reference whose resource was deleted is listed and flagged, never dropped:
// the row is the only place the owner learns the report is serving without it.
func TestListRefsBrokenReference(t *testing.T) {
	h := newHarness()
	delete(h.resources.byID, logoID)
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.True(t, body.Data[0].Broken)
	assert.Empty(t, body.Data[0].DisplayName, "a deleted resource discloses no metadata")
	assert.Equal(t, logoURI, body.Data[0].URI, "the URI is what the owner has to clean up")
}

// A viewer with read-only access sees the list and is told they may not change
// it, which is what the panel hides its add and remove controls on.
func TestListRefsViewerCannotEdit(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionViewer)
	h.declare(assetID, logoRef())

	rec := h.do(t, reader(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	assert.Len(t, body.Data, 1)
	assert.False(t, body.CanEdit)
}

// An editor on a shared asset may change its references, on the same authority
// that lets them change its content.
func TestListRefsSharedEditorCanEdit(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionEditor)

	rec := h.do(t, reader(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, decode[listResponse](t, rec).CanEdit)
}

// The audience the add confirmation names comes from the asset's live shares.
func TestListRefsReportsPublicAudience(t *testing.T) {
	h := newHarness()
	h.shares.summaries[assetID] = portaldomain.ShareSummary{HasPublicLink: true, HasUserShare: true}

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	assert.True(t, body.Audience.Public)
	assert.True(t, body.Audience.SharedWithUsers)
}

// A reference-store failure is a 500 rather than an empty list: telling an
// owner their report references nothing when the platform cannot say is worse
// than telling them it could not answer.
func TestListRefsStoreFailure(t *testing.T) {
	h := newHarness()
	h.refs.listErr = errStore

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// A content read that fails costs the occurrences and nothing else: the panel
// still lists the references, and says nothing about the markup.
func TestListRefsContentUnreadable(t *testing.T) {
	h := newHarness()
	h.blobs.getErr = errStore
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Empty(t, body.Data[0].Occurrences)
	assert.Equal(t, logoID, body.Data[0].ResourceID)
}

func TestAddRefRequiresResourceID(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID), `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// The add records the reference and answers with the asset's list, so the panel
// updates without a reload.
func TestAddRefRecordsReference(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, logoURI, body.Data[0].URI,
		"the panel shows the URI the markup has to name for the picture to render")

	stored := h.refs.byAsset[assetID]
	require.Len(t, stored, 1)
	assert.Equal(t, logoID, stored[0].ResourceID)
	assert.Equal(t, ownerEmail, stored[0].DeclaredBy)
	assert.NotEmpty(t, stored[0].RefToken)
	assert.Equal(t, 0, stored[0].Position)
}

// Adding a reference does not touch the asset's content, which is the whole
// reason the panel has to hand the author the URI.
func TestAddRefLeavesContentUnchanged(t *testing.T) {
	h := newHarness()
	before := string(h.blobs.byKey[assetKey])

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, before, string(h.blobs.byKey[assetKey]))
}

// A resource the caller cannot read is refused, and refused as not-found: being
// told a file exists but is out of reach is itself a disclosure.
func TestAddRefRefusesUnreadableResource(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, chartID))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, h.refs.byAsset[assetID])
}

// The persona that may read the chart may reference it.
func TestAddRefAllowsResourceInCallersPersona(t *testing.T) {
	h := newHarness()
	financeOwner := &access.User{UserID: ownerID, Email: ownerEmail, Roles: []string{"persona:finance"}}

	rec := h.do(t, financeOwner, http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, chartID))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

func TestAddRefRejectsDuplicate(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

// The cap refusal names the number, so the caller learns the limit.
func TestAddRefRejectsPastTheCap(t *testing.T) {
	h := newHarness()
	full := make([]portaldomain.AssetResourceRef, 0, portaldomain.MaxAssetResourceRefs)
	for i := range portaldomain.MaxAssetResourceRefs {
		full = append(full, portaldomain.AssetResourceRef{
			ResourceID: fmt.Sprintf("res-%d", i),
			URI:        fmt.Sprintf("mcp://global/f/%d.png", i),
			RefToken:   fmt.Sprintf("tok-%d", i),
		})
	}
	h.declare(assetID, full...)

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), fmt.Sprint(portaldomain.MaxAssetResourceRefs))
}

// A viewer is refused the add, matching what the list told them.
func TestAddRefRefusesViewer(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionViewer)

	rec := h.do(t, reader(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, h.refs.byAsset[assetID])
}

// An administrator manages the references on an asset they do not own, which is
// the console's whole reason for showing the panel.
func TestAddRefAllowsAdmin(t *testing.T) {
	h := newHarness()
	admin := &access.User{UserID: "user-admin", Email: "admin@example.com", Roles: []string{"admin"}}

	rec := h.do(t, admin, http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

func TestAddRefWriteFailure(t *testing.T) {
	h := newHarness()
	h.refs.attachErr = errStore

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// removePath addresses one reference on the report.
func removePath(res string) string { return refsPath(assetID) + "/" + res }

func TestRemoveRefDropsReference(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef(), portaldomain.AssetResourceRef{
		ResourceID: chartID, URI: chartURI, RefToken: "tok-chart",
	})

	rec := h.do(t, owner(), http.MethodDelete, removePath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	stored := h.refs.byAsset[assetID]
	require.Len(t, stored, 1)
	assert.Equal(t, chartID, stored[0].ResourceID)
	assert.Equal(t, 1, h.refs.detachCall, "one row is removed, not the whole list rewritten")
	assert.Zero(t, h.refs.replaceCall,
		"a person removing one file has decided nothing about the others")
}

// The removal is allowed while the content still names the URI; the panel warns
// with the lines the list reported and the caller decides.
func TestRemoveRefAllowedWhileContentNamesIt(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	listed := decode[listResponse](t, h.do(t, owner(), http.MethodGet, refsPath(assetID), ""))
	require.Len(t, listed.Data, 1)
	require.NotEmpty(t, listed.Data[0].Occurrences,
		"the warning is built from where the content writes the URI")

	rec := h.do(t, owner(), http.MethodDelete, removePath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, h.refs.byAsset[assetID])
}

func TestRemoveRefUnknownReference(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodDelete, removePath(logoID), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, h.refs.replaceCall, "a removal that names nothing rewrites nothing")
}

func TestRemoveRefRefusesViewer(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionViewer)
	h.declare(assetID, logoRef())

	rec := h.do(t, reader(), http.MethodDelete, removePath(logoID), "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

// A deployment with no managed-resource layer registers none of the routes, so
// the paths are unknown rather than present and always refusing.
func TestRegisterSkipsWithoutResourceLayer(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Config{})

	rec := serveBare(t, mux, refsPath(assetID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A share store that is not wired leaves the audience unstated rather than
// failing the read; the notice beside it states the rule regardless.
func TestListRefsWithoutShareStore(t *testing.T) {
	h := newHarness()
	h.cfg.Shares = nil
	h.cfg.Access = access.New(access.Config{Assets: h.assets, Shares: h.shares, AdminRoles: []string{"admin"}})

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	assert.False(t, body.Audience.Public)
	assert.False(t, body.Audience.SharedWithUsers)
}

// A share-summary failure costs the audience and nothing else.
func TestListRefsToleratesShareSummaryFailure(t *testing.T) {
	h := newHarness()
	h.shares.sumErr = errStore
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	assert.False(t, body.Audience.Public)
	assert.Len(t, body.Data, 1)
}

// A resource-store failure renders every row broken rather than dropping them:
// the honest answer when the platform cannot say whether the files are there.
func TestListRefsResourceReadFailure(t *testing.T) {
	h := newHarness()
	h.resources.getErr = errStore
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.True(t, body.Data[0].Broken)
}

// A soft-deleted asset is gone, and says so, rather than serving a list of the
// files a deleted report used to name.
func TestListRefsDeletedAsset(t *testing.T) {
	h := newHarness()
	deletedAt := time.Now()
	h.assets.byID[assetID].DeletedAt = &deletedAt

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	assert.Equal(t, http.StatusGone, rec.Code)
}

// An asset store that cannot answer is a not-found, matching the parent's own
// asset read rather than disclosing which ids exist.
func TestListRefsAssetStoreFailure(t *testing.T) {
	h := newHarness()
	h.assets.getErr = errStore

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A resource lookup that fails for a reason other than absence is a fault, not
// a decision about what the caller may reference.
func TestAddRefResourceStoreFailure(t *testing.T) {
	h := newHarness()
	h.resources.getErr = errStore

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// A malformed body is refused before anything is read or written.
func TestAddRefMalformedBody(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID), `{`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Zero(t, h.refs.attachCall)
}

// A reference list that cannot be read stops the add rather than writing a list
// built from a partial view of what the asset already names.
func TestAddRefListFailure(t *testing.T) {
	h := newHarness()
	h.refs.listErr = errStore

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Zero(t, h.refs.attachCall)
}

// An empty resource id in the path matches nothing rather than the first
// reference in the list.
func TestRemoveRefEmptyResourceID(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodDelete, removePath(""), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

func TestRemoveRefUnauthenticated(t *testing.T) {
	h := newHarness()
	rec := h.do(t, nil, http.MethodDelete, removePath(logoID), "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// A share lookup that fails is a fault rather than a denial: a reader who holds
// a share must not be told they do not, because the platform could not check.
func TestListRefsShareLookupFailure(t *testing.T) {
	h := newHarness()
	h.shares.listErr = errStore

	rec := h.do(t, reader(), http.MethodGet, refsPath(assetID), "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// A reader who could open the resource on its own is told so, which is what
// makes the row a link to the resource's page.
func TestListRefsMarksResourceReadable(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, decode[listResponse](t, rec).Data[0].Readable)
}

// A reader of a shared asset sees a file they have no direct access to. The row
// is not a link, because the resource's own page would answer them not-found.
func TestListRefsMarksResourceUnreadable(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionViewer)
	h.declare(assetID, portaldomain.AssetResourceRef{
		ResourceID: chartID, URI: chartURI, RefToken: "tok-chart",
	})

	rec := h.do(t, reader(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	got := decode[listResponse](t, rec).Data[0]
	assert.False(t, got.Readable)
	assert.Equal(t, "Revenue chart", got.DisplayName,
		"the reference already serves this reader the file, so its name is not a new disclosure")
	assert.NotEmpty(t, got.ContentURL)
}

// A person adding one file has decided nothing about the others, so the write
// touches one row. Rewriting the whole list from a read would drop whatever a
// concurrent save had just declared.
func TestAddRefWritesOneRowNotTheWholeList(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, chartID))
	require.Equal(t, http.StatusNotFound, rec.Code, "the chart is out of this caller's reach")

	financeOwner := &access.User{UserID: ownerID, Email: ownerEmail, Roles: []string{"persona:finance"}}
	rec = h.do(t, financeOwner, http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, chartID))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, 1, h.refs.attachCall)
	assert.Zero(t, h.refs.replaceCall)
	assert.Len(t, h.refs.byAsset[assetID], 2)
}

// A removal the store could not perform is a fault, not a silent success.
func TestRemoveRefStoreFailure(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())
	h.refs.detachErr = errStore

	rec := h.do(t, owner(), http.MethodDelete, removePath(logoID), "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// The response says whether the content was read, because "we did not look" and
// "the content does not name it" are opposite answers to someone about to
// withdraw a grant.
func TestListRefsReportsWhetherContentWasScanned(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, decode[listResponse](t, rec).ContentScanned)
}

func TestListRefsReportsAnUnscannedContent(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*harness)
	}{
		{"read failed", func(h *harness) { h.blobs.getErr = errStore }},
		{"no blob reader", func(h *harness) { h.cfg.Blobs = nil }},
		{"binary content", func(h *harness) { h.assets.byID[assetID].ContentType = "image/png" }},
		{"too large", func(h *harness) {
			h.assets.byID[assetID].SizeBytes = maxScanBytes + 1
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			h.declare(assetID, logoRef())
			tc.setup(h)

			rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
			require.Equal(t, http.StatusOK, rec.Code)

			body := decode[listResponse](t, rec)
			assert.False(t, body.ContentScanned)
			assert.Empty(t, body.Data[0].Occurrences)
		})
	}
}

// An asset with nothing declared is a scan that ran and found nothing, so the
// first reference someone adds and then removes needs no warning.
func TestListRefsEmptyAssetCountsAsScanned(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, decode[listResponse](t, rec).ContentScanned)
}

// A write that landed and a read-back that failed is a fault, and the reference
// stays: the caller is told the platform could not answer, not that nothing
// happened, and a refresh shows the file they added.
func TestAddRefReadBackFailure(t *testing.T) {
	h := newHarness()
	h.refs.listErrAfter = 1

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1, "the write is not undone by a failed read-back")
}

// A mutation addressed to an asset that is not there is refused before any
// authority is considered.
func TestAddRefUnknownAsset(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath("asset-nope"),
		fmt.Sprintf(`{"resource_id":%q}`, logoID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, h.refs.attachCall)
}

func TestRemoveRefUnknownAsset(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodDelete,
		refsPath("asset-nope")+"/"+logoID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, h.refs.detachCall)
}
