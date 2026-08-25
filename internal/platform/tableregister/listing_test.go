package tableregister

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listingHarness is a registrar over a store already holding registrations,
// with a persona boundary that denies one connection. The registration path is
// not exercised here: what these tests are about is who may see what, which is
// decided entirely on records that already exist.
func listingHarness(t *testing.T, rows ...Registration) *Registrar {
	t.Helper()
	store := newMemStore()
	for _, r := range rows {
		require.NoError(t, store.Insert(context.Background(), r))
	}
	return New(Deps{
		Store:   store,
		Trino:   &fakeTrino{hasTarget: true},
		Objects: map[string]ObjectReader{KindAsset: &fakeObjects{}},
		Scope:   denyScope{denied: "restricted"},
	})
}

func listingRow(id, connection, kind, sourceID string, at time.Time) Registration {
	return Registration{
		ID: id, SourceKind: kind, SourceID: sourceID,
		Connection: connection, Catalog: "scratch", Schema: "uploads", Table: id,
		Location:     "s3://bucket/dir/" + sourceID + "/",
		Columns:      []Column{{Name: "store_id", Type: "VARCHAR"}},
		RegisteredBy: "alice@example.com", RegisteredAt: at,
	}
}

var listedAt = time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)

// TestList_SpansBothKindsNewestFirst is the acceptance criterion the whole
// section exists for: one read that answers what a deployment has registered,
// whichever kind of file each registration was built over.
func TestList_SpansBothKindsNewestFirst(t *testing.T) {
	reg := listingHarness(t,
		listingRow("older", "scratch", KindAsset, "asset_1", listedAt),
		listingRow("newer", "scratch", KindResource, "res_1", listedAt.Add(time.Hour)),
	)

	page, total, err := reg.List(context.Background(), Filter{AllConnections: true})
	require.NoError(t, err)

	assert.Equal(t, 2, total)
	require.Len(t, page, 2)
	assert.Equal(t, "newer", page[0].ID, "the most recent registration leads the page")
	assert.Equal(t, KindResource, page[0].SourceKind)
	assert.Equal(t, KindAsset, page[1].SourceKind)
}

// TestList_OnAnUnwiredDeploymentIsEmpty keeps a platform with no registration
// mechanism serving an empty listing rather than an error every page has to
// render.
func TestList_OnAnUnwiredDeploymentIsEmpty(t *testing.T) {
	page, total, err := New(Deps{}).List(context.Background(), Filter{AllConnections: true})

	require.NoError(t, err)
	assert.Empty(t, page)
	assert.Zero(t, total)
}

// TestVisible_AnswersARegistrationOutsideThePersonaAsNotFound: the caller
// cannot query the table and cannot act on it, so naming it would disclose a
// table somebody else registered in a schema they have no reach into.
func TestVisible_AnswersARegistrationOutsideThePersonaAsNotFound(t *testing.T) {
	reg := listingHarness(t,
		listingRow("reg_open", "scratch", KindAsset, "asset_1", listedAt),
		listingRow("reg_shut", "restricted", KindAsset, "asset_2", listedAt),
	)
	caller := Caller{UserID: "u1", Email: "alice@example.com", Persona: "analyst"}

	open, err := reg.Visible(context.Background(), caller, "reg_open")
	require.NoError(t, err)
	assert.Equal(t, "reg_open", open.ID)

	shut, err := reg.Visible(context.Background(), caller, "reg_shut")
	assert.Nil(t, shut)
	assert.ErrorIs(t, err, ErrNotFound,
		"a registration outside the persona is answered as absent, not as denied")
}

// TestVisible_AnAdministratorReachesEveryConnection holds the rule that runs
// everywhere else in the portal: scoping an administrator is a defect.
func TestVisible_AnAdministratorReachesEveryConnection(t *testing.T) {
	reg := listingHarness(t, listingRow("reg_shut", "restricted", KindAsset, "asset_2", listedAt))

	got, err := reg.Visible(context.Background(),
		Caller{UserID: "ops", Email: "ops@example.com", Persona: "analyst", IsAdmin: true}, "reg_shut")

	require.NoError(t, err)
	assert.Equal(t, "reg_shut", got.ID)
}

// TestVisible_ReportsAnUnknownIDAsNotFound, which is the same answer a
// registration outside the persona gets, so a probe cannot tell them apart.
func TestVisible_ReportsAnUnknownIDAsNotFound(t *testing.T) {
	reg := listingHarness(t)

	_, err := reg.Visible(context.Background(), Caller{Persona: "analyst"}, "reg_nothing")

	assert.ErrorIs(t, err, ErrNotFound)
}

// TestVisible_OnAnUnwiredDeploymentSaysSo, rather than reporting the record as
// missing: the difference matters to the surface, which has an action to hide
// in one case and a page to refuse in the other.
func TestVisible_OnAnUnwiredDeploymentSaysSo(t *testing.T) {
	_, err := New(Deps{}).Visible(context.Background(), Caller{}, "reg_1")

	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestVisible_WithNoPersonaRulesWiredShowsTheRegistration: a deployment with
// no persona registry has no boundary to apply, and denying there would hide
// every table on it.
func TestVisible_WithNoPersonaRulesWiredShowsTheRegistration(t *testing.T) {
	store := newMemStore()
	require.NoError(t, store.Insert(context.Background(),
		listingRow("reg_1", "scratch", KindAsset, "asset_1", listedAt)))
	reg := New(Deps{
		Store:   store,
		Trino:   &fakeTrino{hasTarget: true},
		Objects: map[string]ObjectReader{KindAsset: &fakeObjects{}},
	})

	got, err := reg.Visible(context.Background(), Caller{Persona: "analyst"}, "reg_1")

	require.NoError(t, err)
	assert.Equal(t, "reg_1", got.ID)
}
