package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedview"
)

// stubProduced answers the produced listing, recording what it was asked.
type stubProduced struct {
	items    []producedview.Item
	err      error
	askedFor string
	limit    int
}

func (s *stubProduced) Produced(_ context.Context, scriptID string, limit int) ([]producedview.Item, error) {
	s.askedFor, s.limit = scriptID, limit
	return s.items, s.err
}

func producedFixture() *stubProduced {
	at := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	return &stubProduced{items: []producedview.Item{
		{
			TargetKind: "asset", TargetID: "asset-1", Name: "Daily sales",
			Created: true, FirstWriteAt: at, LastWriteAt: at.Add(time.Hour),
			WriteCount: 12, LastVersion: 12,
		},
		{
			TargetKind: "resource", TargetID: "res-1", Name: "Region map",
			FirstWriteAt: at, LastWriteAt: at, WriteCount: 1,
		},
	}}
}

// producedDeps assembles the portal handler with the produced reader wired.
func producedDeps(produced ProducedReader, user *PortalIdentity) Deps {
	deps := portalDeps(portalStore(), nil, nil, user)
	deps.Produced = produced
	return deps
}

// TestPortalListProduced is acceptance criterion 7: one list of everything the
// script has written, across runs, drawn from the producer relation.
func TestPortalListProduced(t *testing.T) {
	produced := producedFixture()
	rec := servePortal(t, producedDeps(produced, owner), "/api/v1/portal/scripts/script_1/produced")
	require.Equal(t, http.StatusOK, rec.Code)

	var body producedListResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 2)
	assert.Equal(t, 2, body.Total)
	assert.Equal(t, "script_1", produced.askedFor)

	assert.Equal(t, "Daily sales", body.Data[0].Name)
	assert.True(t, body.Data[0].Created)
	assert.Equal(t, 12, body.Data[0].WriteCount)
	assert.Equal(t, "resource", body.Data[1].TargetKind)
	assert.False(t, body.Data[1].Created)
}

// TestPortalListProducedIsAdministrable pins the same visibility rule the rest
// of the surface applies: an administrator sees every script's list.
func TestPortalListProducedIsAdministrable(t *testing.T) {
	rec := servePortal(t, producedDeps(producedFixture(), admin), "/api/v1/portal/scripts/script_2/produced")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestPortalListProducedRefusesAnotherOwner: what a script has written is part
// of the script, and not-yours reads as does-not-exist.
func TestPortalListProducedRefusesAnotherOwner(t *testing.T) {
	rec := servePortal(t, producedDeps(producedFixture(), stranger), "/api/v1/portal/scripts/script_1/produced")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalListProducedUnknownScript(t *testing.T) {
	rec := servePortal(t, producedDeps(producedFixture(), owner), "/api/v1/portal/scripts/nope/produced")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalListProducedReadFailure(t *testing.T) {
	produced := &stubProduced{err: errors.New("database down")}
	rec := servePortal(t, producedDeps(produced, owner), "/api/v1/portal/scripts/script_1/produced")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestPortalProducedRouteAbsentWithoutARecord keeps a deployment that records
// no producers from serving a route that always answers empty.
func TestPortalProducedRouteAbsentWithoutARecord(t *testing.T) {
	rec := servePortal(t, portalDeps(portalStore(), nil, nil, owner),
		"/api/v1/portal/scripts/script_1/produced")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
