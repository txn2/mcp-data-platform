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
func refsPath(asset string) string { return "/api/v1/portal/assets/" + asset + "/references" }

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
	assert.Equal(t, assetrefs.MaxRefs, body.Max)
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
	assert.Equal(t, logoID, got.TargetID)
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
	assert.Equal(t, logoID, body.Data[0].TargetID)
}

func TestAddRefRequiresATarget(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID), `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// The add records the reference and answers with the asset's list, so the panel
// updates without a reload.
func TestAddRefRecordsReference(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, logoURI, body.Data[0].URI,
		"the panel shows the URI the markup has to name for the picture to render")

	stored := h.refs.byAsset[assetID]
	require.Len(t, stored, 1)
	assert.Equal(t, logoID, stored[0].TargetID)
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
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, before, string(h.blobs.byKey[assetKey]))
}

// A resource the caller cannot read is refused, and refused as not-found: being
// told a file exists but is out of reach is itself a disclosure.
func TestAddRefRefusesUnreadableResource(t *testing.T) {
	h := newHarness()
	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, chartID))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, h.refs.byAsset[assetID])
}

// The persona that may read the chart may reference it.
func TestAddRefAllowsResourceInCallersPersona(t *testing.T) {
	h := newHarness()
	financeOwner := &access.User{UserID: ownerID, Email: ownerEmail, Roles: []string{"persona:finance"}}

	rec := h.do(t, financeOwner, http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, chartID))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

func TestAddRefRejectsDuplicate(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef())

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

// The cap refusal names the number, so the caller learns the limit.
func TestAddRefRejectsPastTheCap(t *testing.T) {
	h := newHarness()
	full := make([]assetrefs.Ref, 0, assetrefs.MaxRefs)
	for i := range assetrefs.MaxRefs {
		full = append(full, assetrefs.Ref{
			TargetKind: assetrefs.TargetResource, TargetID: fmt.Sprintf("res-%d", i),
			URI:      fmt.Sprintf("mcp://global/f/%d.png", i),
			RefToken: fmt.Sprintf("tok-%d", i),
		})
	}
	h.declare(assetID, full...)

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), fmt.Sprint(assetrefs.MaxRefs))
}

// A viewer is refused the add, matching what the list told them.
func TestAddRefRefusesViewer(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionViewer)

	rec := h.do(t, reader(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, h.refs.byAsset[assetID])
}

// An administrator manages the references on an asset they do not own, which is
// the console's whole reason for showing the panel.
func TestAddRefAllowsAdmin(t *testing.T) {
	h := newHarness()
	admin := &access.User{UserID: "user-admin", Email: "admin@example.com", Roles: []string{"admin"}}

	rec := h.do(t, admin, http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1)
}

func TestAddRefWriteFailure(t *testing.T) {
	h := newHarness()
	h.refs.attachErr = errStore

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// removePath addresses one reference on the report.
// removePath addresses one resource reference for removal. The kind is in the
// path because it is part of the reference's identity.
func removePath(res string) string { return refsPath(assetID) + "/resource/" + res }

// removeAssetPath is the same for a reference to another asset.
func removeAssetPath(id string) string { return refsPath(assetID) + "/asset/" + id }

func TestRemoveRefDropsReference(t *testing.T) {
	h := newHarness()
	h.declare(assetID, logoRef(), assetrefs.Ref{
		TargetKind: assetrefs.TargetResource, TargetID: chartID, URI: chartURI, RefToken: "tok-chart",
	})

	rec := h.do(t, owner(), http.MethodDelete, removePath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	stored := h.refs.byAsset[assetID]
	require.Len(t, stored, 1)
	assert.Equal(t, chartID, stored[0].TargetID)
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
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
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
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Zero(t, h.refs.attachCall)
}

// An empty resource id in the path matches nothing rather than the first
// reference in the list.
func TestRemoveRefEmptyTargetID(t *testing.T) {
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
	h.declare(assetID, assetrefs.Ref{
		TargetKind: assetrefs.TargetResource, TargetID: chartID, URI: chartURI, RefToken: "tok-chart",
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
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, chartID))
	require.Equal(t, http.StatusNotFound, rec.Code, "the chart is out of this caller's reach")

	financeOwner := &access.User{UserID: ownerID, Email: ownerEmail, Roles: []string{"persona:finance"}}
	rec = h.do(t, financeOwner, http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, chartID))
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
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Len(t, h.refs.byAsset[assetID], 1, "the write is not undone by a failed read-back")
}

// A mutation addressed to an asset that is not there is refused before any
// authority is considered.
func TestAddRefUnknownAsset(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath("asset-nope"),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, h.refs.attachCall)
}

func TestRemoveRefUnknownAsset(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodDelete,
		refsPath("asset-nope")+"/resource/"+logoID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, h.refs.detachCall)
}

// The asset-reference half of the panel (#1488). otherID is an asset the owner
// does not own; a share is what makes it referenceable.

// TestAddAssetRefRecordsReference is the acceptance criterion for adding a
// reference to another asset: the caller's own read is the check, and the row
// is stored under the mcp:asset:<id> reference the content has to name.
func TestAddAssetRefRecordsReference(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(otherID, ownerEmail, portaldomain.PermissionViewer)

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"asset","target_id":%q}`, otherID))
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "asset", body.Data[0].TargetKind)
	assert.Equal(t, "mcp:asset:"+otherID, body.Data[0].URI,
		"the panel shows the reference the markup has to name")
	assert.Equal(t, "Someone else's memo", body.Data[0].DisplayName)
	assert.Equal(t, readerMail, body.Data[0].OwnerEmail)
	assert.True(t, body.Data[0].Readable, "the caller holds a share, so the row links to it")

	stored := h.refs.byAsset[assetID]
	require.Len(t, stored, 1)
	assert.Equal(t, assetrefs.TargetAsset, stored[0].TargetKind)
	assert.Equal(t, otherID, stored[0].TargetID)
	assert.NotEmpty(t, stored[0].RefToken)
}

// An asset the caller cannot open is refused as not found, so the refusal
// cannot be used to learn that an asset exists.
func TestAddAssetRefRefusesUnreadableAsset(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"asset","target_id":%q}`, otherID))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Someone else's memo")
	assert.Empty(t, h.refs.byAsset[assetID])
}

// An asset referencing itself is refused. The serving route answers such a
// reference rather than following it, so nothing breaks -- but it resolves to
// the very content it was written in, and there is no reading of that the
// author could have meant.
func TestAddAssetRefRefusesSelfReference(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"asset","target_id":%q}`, assetID))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot reference itself")
	assert.Empty(t, h.refs.byAsset[assetID])
}

// A kind the platform cannot resolve is refused before anything is read, and
// the refusal names what the field takes.
func TestAddRefRefusesUnknownKind(t *testing.T) {
	h := newHarness()

	rec := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"collection","target_id":%q}`, otherID))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "resource")
	assert.Contains(t, rec.Body.String(), "asset")
	assert.Zero(t, h.refs.attachCall)
}

// A reference to a deleted asset is listed and flagged broken, never dropped:
// the row is where the owner learns the report is serving without it.
func TestListRefsBrokenAssetReference(t *testing.T) {
	h := newHarness()
	deleted := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	h.assets.byID[otherID].DeletedAt = &deleted
	h.declare(assetID, assetRef(otherID, "tok-asset"))

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.True(t, body.Data[0].Broken)
	assert.Empty(t, body.Data[0].DisplayName, "a deleted asset discloses no metadata")
}

// A reader who can open the referencing asset sees the referenced asset's name
// even without access to it, and the row is not a link. That is not a widening:
// the reference already hands them its content through ContentURL.
func TestListRefsAssetRowUnreadableToViewer(t *testing.T) {
	h := newHarness()
	h.shares.shareWith(assetID, readerMail, portaldomain.PermissionViewer)
	h.assets.byID["asset-third"] = &portaldomain.Asset{
		ID: "asset-third", OwnerID: ownerID, OwnerEmail: ownerEmail, Name: "Weekly numbers",
	}
	h.declare(assetID, assetRef("asset-third", "tok-third"))

	rec := h.do(t, reader(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "Weekly numbers", body.Data[0].DisplayName)
	assert.False(t, body.Data[0].Readable)
	assert.NotEmpty(t, body.Data[0].ContentURL)
}

// Removing an asset reference addresses it by kind and id, and leaves a
// resource reference sharing that id alone.
func TestRemoveAssetRefLeavesTheResourceRefAlone(t *testing.T) {
	h := newHarness()
	sameID := assetRef(logoID, "tok-same") // an asset whose id matches the resource's
	h.declare(assetID, logoRef(), sameID)

	rec := h.do(t, owner(), http.MethodDelete, removeAssetPath(logoID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	stored := h.refs.byAsset[assetID]
	require.Len(t, stored, 1)
	assert.Equal(t, assetrefs.TargetResource, stored[0].TargetKind)
}

// TestRoutesRegisterWithoutAManagedResourceLayer proves the two kinds are
// independent on this surface as well as in the declaration path: a deployment
// with assets and no managed resources still manages asset references, and a
// resource target is answered as absent rather than taking the routes down.
func TestRoutesRegisterWithoutAManagedResourceLayer(t *testing.T) {
	h := newHarness()
	h.cfg.Resources = nil
	h.shares.shareWith(otherID, ownerEmail, portaldomain.PermissionViewer)

	added := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"asset","target_id":%q}`, otherID))
	require.Equal(t, http.StatusOK, added.Code)
	assert.Len(t, decode[listResponse](t, added).Data, 1)

	refused := h.do(t, owner(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, logoID))
	assert.Equal(t, http.StatusNotFound, refused.Code)
}

// TestListRefsRendersAssetRowsBrokenWhenTheAssetReadFails is the asset arm of
// the rule the resource arm already follows: the platform cannot say whether
// the targets are still there, so every row of that kind reads broken rather
// than reading whole.
func TestListRefsRendersAssetRowsBrokenWhenTheAssetReadFails(t *testing.T) {
	h := newHarness()
	h.declare(assetID, assetRef(otherID, "tok-asset"))
	// The asset behind the panel is loaded before the rows are, so the read
	// fails only once the route is past its own gate.
	h.assets.byIDsErr = errStore

	rec := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode[listResponse](t, rec)
	require.Len(t, body.Data, 1)
	assert.True(t, body.Data[0].Broken)
}

// TestAddRefAllowsAnAdministratorOutsideThePersona is the panel's half of
// #1584: the person's door to the act an agent performs at save time answers
// the same way the agent's does.
//
// An administrator belongs to no persona, so the membership rule refused them a
// reference to a file whose detail, content, replace and move routes all admit
// them. They now add it.
func TestAddRefAllowsAnAdministratorOutsideThePersona(t *testing.T) {
	h := newHarness()

	rec := h.do(t, admin(), http.MethodPost, refsPath(assetID),
		fmt.Sprintf(`{"target_kind":"resource","target_id":%q}`, chartID))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, h.refs.byAsset[assetID], 1)
	assert.Equal(t, chartID, h.refs.byAsset[assetID][0].TargetID)
	assert.Equal(t, chartURI, h.refs.byAsset[assetID][0].URI)
}

// TestListRefsMarksAPersonaFileReadableForAnAdministrator pins the flag the row
// links on (#1584). The administrator's own resource page opens the chart, so a
// panel that reported it unopenable sent them to a link it had just said would
// fail, or withheld the link to a page that would have worked.
func TestListRefsMarksAPersonaFileReadableForAnAdministrator(t *testing.T) {
	h := newHarness()
	h.declare(assetID, chartRef())

	admins := h.do(t, admin(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, admins.Code)
	assert.True(t, decode[listResponse](t, admins).Data[0].Readable)

	// The owner of the asset is in no persona and holds no authority over the
	// finance library, so the same row is still not a link for them.
	owners := h.do(t, owner(), http.MethodGet, refsPath(assetID), "")
	require.Equal(t, http.StatusOK, owners.Code)
	assert.False(t, decode[listResponse](t, owners).Data[0].Readable)
}
