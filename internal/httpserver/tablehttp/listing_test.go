package tablehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The cross-source listing (#1472). What these tests hold is that a caller
// sees the registrations on the connections they reach and no others, that the
// two things only a cross-source read can answer -- which file this is, and
// whether the table is still reading its current contents -- are on every row,
// and that the unregister action is offered exactly where it would be accepted.

var listedAt = time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)

// seed puts a registration in the store directly. The registration path has its
// own tests; these are about reading back what is already recorded.
func seed(t *testing.T, h *harness, reg tableregister.Registration) {
	t.Helper()
	require.NoError(t, h.store.Insert(context.Background(), reg))
}

func listedRow(id, connection, kind, sourceID, location string) tableregister.Registration {
	return tableregister.Registration{
		ID: id, SourceKind: kind, SourceID: sourceID,
		Connection: connection, Catalog: "scratch", Schema: "uploads", Table: id,
		Location:     location,
		Columns:      []tableregister.Column{{Name: "store_id", Type: "VARCHAR"}},
		RegisteredBy: "alice@example.com", RegisteredAt: listedAt,
	}
}

// currentLocation is the directory the asset fixture's head key sits in, so a
// registration built on it is current.
const currentLocation = "s3://portal-assets/artifacts/u1/asset_1/"

func listing(t *testing.T, h *harness, query string) scratchTableList {
	t.Helper()
	res := h.do(http.MethodGet, "/api/v1/tables"+query, "")
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	var got scratchTableList
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &got))
	return got
}

// TestListing_SpansBothKinds is the acceptance criterion: a deployment with a
// registered asset and a registered resource shows both in one table, without
// opening either source.
func TestListing_SpansBothKinds(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("reg_asset", "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	seed(t, h, listedRow("reg_res", "scratch", tableregister.KindResource, "res_1", "s3://resources/r/res_1/"))

	got := listing(t, h, "")

	assert.Equal(t, 2, got.Total)
	require.Len(t, got.Data, 2)
	kinds := []string{got.Data[0].Source.Kind, got.Data[1].Source.Kind}
	assert.ElementsMatch(t, []string{tableregister.KindAsset, tableregister.KindResource}, kinds)
}

// TestListing_CarriesWhatOnlyACrossSourceReadCanAnswer: the qualified name and
// the sample SQL a reader queries with, the file each row came from, and
// whether the table is behind that file.
func TestListing_CarriesWhatOnlyACrossSourceReadCanAnswer(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("reg_current", "scratch", tableregister.KindAsset, "asset_1", currentLocation))

	row := listing(t, h, "").Data[0]

	assert.Equal(t, "scratch.uploads.reg_current", row.QueryTable)
	assert.Contains(t, row.SampleSQL, "scratch.uploads.reg_current")
	assert.Equal(t, "Vendor keys", row.Source.Name)
	assert.False(t, row.Source.Missing)
	assert.False(t, row.Stale)
	assert.True(t, row.CanUnregister)
}

// TestListing_FlagsARegistrationLeftBehindByANewVersion, without the reader
// opening the source. Staleness is the second thing only the source's own page
// could answer before this listing existed.
func TestListing_FlagsARegistrationLeftBehindByANewVersion(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("reg_stale", "scratch", tableregister.KindAsset, "asset_1",
		"s3://portal-assets/artifacts/u1/asset_1/v1/"))

	row := listing(t, h, "").Data[0]

	assert.True(t, row.Stale, "the head key moved to a new directory, so the table serves the old one")
	assert.False(t, row.Source.Missing)
}

// TestListing_ShowsOnlyTheConnectionsTheCallerReaches is the boundary. A
// registration on a connection this persona is not granted is one they cannot
// query, so it is not theirs to see.
func TestListing_ShowsOnlyTheConnectionsTheCallerReaches(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("reg_open", "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	seed(t, h, listedRow("reg_shut", "restricted", tableregister.KindAsset, "asset_1", currentLocation))

	got := listing(t, h, "")

	assert.Equal(t, 1, got.Total)
	require.Len(t, got.Data, 1)
	assert.Equal(t, "reg_open", got.Data[0].ID)
}

// TestListing_ShowsAnAdministratorEveryRegistration: an operator's whole
// reason for opening this page is to see what is in the shared schema.
func TestListing_ShowsAnAdministratorEveryRegistration(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.visibleFn = func(context.Context, tableregister.Caller) ([]string, bool) {
			return nil, true
		}
	})
	seed(t, h, listedRow("reg_open", "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	seed(t, h, listedRow("reg_shut", "restricted", tableregister.KindAsset, "asset_1", currentLocation))

	assert.Equal(t, 2, listing(t, h, "").Total)
}

// TestListing_WithNoVisibilityWiredShowsNothingToANonAdministrator is the
// fail-closed reading of a deployment that cannot enumerate its connections:
// a persona granted nothing and a platform that cannot say what a persona
// reaches are answered the same way, and neither is answered with everything.
func TestListing_WithNoVisibilityWiredShowsNothingToANonAdministrator(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.noVisibility = true })
	seed(t, h, listedRow("reg_open", "scratch", tableregister.KindAsset, "asset_1", currentLocation))

	got := listing(t, h, "")

	assert.Zero(t, got.Total)
	assert.Empty(t, got.Data)
}

// TestListing_WithNoVisibilityWiredStillShowsAnAdministratorEverything, since
// the boundary that could not be enumerated is not one that applies to them.
func TestListing_WithNoVisibilityWiredStillShowsAnAdministratorEverything(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.noVisibility = true
		h.user = &portal.User{UserID: "ops", Email: "ops@example.com", Roles: []string{"admin"}}
	})
	seed(t, h, listedRow("reg_open", "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	seed(t, h, listedRow("reg_shut", "restricted", tableregister.KindAsset, "asset_1", currentLocation))

	assert.Equal(t, 2, listing(t, h, "").Total)
}

// TestListing_NarrowsByKindAndByName are the facets, and a kind that is not
// one of the two is dropped rather than emptying the listing.
func TestListing_NarrowsByKindAndByName(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("vendor_keys", "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	seed(t, h, listedRow("rebates", "scratch", tableregister.KindResource, "res_1", "s3://resources/r/res_1/"))

	assert.Equal(t, 1, listing(t, h, "?kind=resource").Total)
	assert.Equal(t, 1, listing(t, h, "?q=vendor").Total)
	assert.Equal(t, 2, listing(t, h, "?kind=nonsense").Total,
		"a kind that names neither source is dropped, not passed through to empty the listing")
}

// TestListing_ConnectionFacetOutsideTheCallersReachIsEmptyNotRefused: the
// parameter is a facet of a listing they may read, and a refusal on it would
// confirm the connection exists.
func TestListing_ConnectionFacetOutsideTheCallersReachIsEmptyNotRefused(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("reg_shut", "restricted", tableregister.KindAsset, "asset_1", currentLocation))

	got := listing(t, h, "?connection=restricted")

	assert.Zero(t, got.Total)
	assert.Empty(t, got.Data)
}

// TestListing_ConnectionFacetInsideTheCallersReachNarrowsToIt: a connection
// the caller reaches narrows the listing to it rather than being ignored.
func TestListing_ConnectionFacetInsideTheCallersReachNarrowsToIt(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.visibleFn = func(context.Context, tableregister.Caller) ([]string, bool) {
			return []string{"scratch", "other"}, false
		}
	})
	seed(t, h, listedRow("here", "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	seed(t, h, listedRow("there", "other", tableregister.KindAsset, "asset_1", currentLocation))

	got := listing(t, h, "?connection=other")

	require.Len(t, got.Data, 1)
	assert.Equal(t, "there", got.Data[0].ID)
}

// TestListing_PagesTheResult reports the page it served alongside the total,
// so a pager has what it needs without recomputing it.
func TestListing_PagesTheResult(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"a", "b", "c"} {
		seed(t, h, listedRow(id, "scratch", tableregister.KindAsset, "asset_1", currentLocation))
	}

	got := listing(t, h, "?per_page=2&page=2")

	assert.Equal(t, 3, got.Total)
	assert.Equal(t, 2, got.Page)
	assert.Equal(t, 2, got.PerPage)
	assert.Len(t, got.Data, 1)
}

// TestListing_WithholdsUnregisterFromSomebodyElsesRegistration, which is the
// rule the DELETE route applies: the person who registered the table, or an
// administrator.
func TestListing_WithholdsUnregisterFromSomebodyElsesRegistration(t *testing.T) {
	h := newHarness(t)
	mine := listedRow("mine", "scratch", tableregister.KindAsset, "asset_1", currentLocation)
	theirs := listedRow("theirs", "scratch", tableregister.KindAsset, "asset_1", currentLocation)
	theirs.RegisteredBy = "bob@example.com"
	seed(t, h, mine)
	seed(t, h, theirs)

	byID := map[string]scratchTableView{}
	for _, row := range listing(t, h, "").Data {
		byID[row.ID] = row
	}

	assert.True(t, byID["mine"].CanUnregister)
	assert.False(t, byID["theirs"].CanUnregister,
		"a table someone else registered is listed, and is not theirs to drop")
}

// TestListing_WithholdsUnregisterWithoutAuthorityOverTheSource: the caller may
// see and query the table because they reach the connection, which is not
// authority over the file it was built from.
func TestListing_WithholdsUnregisterWithoutAuthorityOverTheSource(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.sourcesFn = func(
			_ context.Context, _ string, ids []string, _ tableregister.Caller,
		) map[string]tableregister.SourceRef {
			out := make(map[string]tableregister.SourceRef, len(ids))
			for _, id := range ids {
				out[id] = tableregister.SourceRef{
					Name: "Vendor keys", Bucket: "portal-assets",
					HeadKey: "artifacts/u1/asset_1/content.csv", CanModify: false,
				}
			}
			return out
		}
	})
	seed(t, h, listedRow("reg_1", "scratch", tableregister.KindAsset, "asset_1", currentLocation))

	row := listing(t, h, "").Data[0]

	assert.Equal(t, "Vendor keys", row.Source.Name, "the row still says which file it is")
	assert.False(t, row.CanUnregister)
}

// TestListing_MarksARegistrationWhoseSourceIsGone. Deleting a file unregisters
// its tables, so this is the residue of a cleanup that did not complete, and a
// reader has to be told that rather than shown an ordinary row.
func TestListing_MarksARegistrationWhoseSourceIsGone(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("orphan", "scratch", tableregister.KindAsset, "asset_gone", currentLocation))

	row := listing(t, h, "").Data[0]

	assert.True(t, row.Source.Missing)
	assert.Empty(t, row.Source.Name)
	assert.False(t, row.CanUnregister)
}

// TestListing_WithNoSourceLookupStillListsTheRegistrations, degraded rather
// than absent: the registrations are the page, and the source names are what
// it adds to them.
func TestListing_WithNoSourceLookupStillListsTheRegistrations(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.noSources = true })
	seed(t, h, listedRow("reg_1", "scratch", tableregister.KindAsset, "asset_1", currentLocation))

	row := listing(t, h, "").Data[0]

	assert.Equal(t, "scratch.uploads.reg_1", row.QueryTable, "the registration is the page")
	assert.True(t, row.Source.Missing, "with nothing to resolve it against, the source is unknown")
	assert.False(t, row.CanUnregister, "and an action that cannot be authorized is not offered")
}

// TestListing_RequiresAuthentication, like every other route in this package.
func TestListing_RequiresAuthentication(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.user = nil })

	assert.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/api/v1/tables", "").Code)
	assert.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/api/v1/tables/reg_1", "").Code)
}

// TestListing_ReportsAStoreFailureAsAPlatformFailure rather than as an empty
// listing, which would read as "nothing is registered".
func TestListing_ReportsAStoreFailureAsAPlatformFailure(t *testing.T) {
	h := newHarness(t)
	h.store.err = assert.AnError

	assert.Equal(t, http.StatusInternalServerError, h.do(http.MethodGet, "/api/v1/tables", "").Code)
}

// --- one registration ---

// TestGetOne_OpensTheRegistration is the detail the row click lands on.
func TestGetOne_OpensTheRegistration(t *testing.T) {
	h := newHarness(t)
	seed(t, h, listedRow("reg_1", "scratch", tableregister.KindAsset, "asset_1", currentLocation))

	res := h.do(http.MethodGet, "/api/v1/tables/reg_1", "")
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())

	var got scratchTableView
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &got))
	assert.Equal(t, "scratch.uploads.reg_1", got.QueryTable)
	assert.Equal(t, "Vendor keys", got.Source.Name)
	assert.Equal(t, currentLocation, got.Location)
	require.Len(t, got.Columns, 1)
	assert.Equal(t, "VARCHAR", got.Columns[0].Type)
	assert.True(t, got.CanUnregister)
}

// TestGetOne_AnswersARegistrationOutsideThePersonaAsNotFound, which is the
// same answer an id that never existed gets.
func TestGetOne_AnswersARegistrationOutsideThePersonaAsNotFound(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.scope = denyConnection("restricted") })
	seed(t, h, listedRow("reg_shut", "restricted", tableregister.KindAsset, "asset_1", currentLocation))

	assert.Equal(t, http.StatusNotFound, h.do(http.MethodGet, "/api/v1/tables/reg_shut", "").Code)
	assert.Equal(t, http.StatusNotFound, h.do(http.MethodGet, "/api/v1/tables/reg_nothing", "").Code)
}

// denyConnection refuses one named connection and allows the rest, standing in
// for the persona boundary the registrar applies to a read.
type denyConnection string

func (d denyConnection) AllowConnection(_, connection string) bool {
	return connection != string(d)
}

// TestGetOne_ForAnAdministratorReachesEveryConnection: scoping an
// administrator is a defect everywhere else in the portal, and here too.
func TestGetOne_ForAnAdministratorReachesEveryConnection(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.scope = denyConnection("restricted")
		h.user = &portal.User{UserID: "ops", Email: "ops@example.com", Roles: []string{"admin"}}
	})
	seed(t, h, listedRow("reg_shut", "restricted", tableregister.KindAsset, "asset_1", currentLocation))

	res := h.do(http.MethodGet, "/api/v1/tables/reg_shut", "")
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())

	var got scratchTableView
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &got))
	assert.Equal(t, "reg_shut", got.ID)
	assert.True(t, got.CanUnregister, "an administrator may drop any registration")
}
