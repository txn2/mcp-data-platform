package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The set a connection parameter chooses from (#1361). There are two of them
// and they are genuinely different sets, so every test here is about which one
// was served and what it was labeled.

const connectionsPath = "/api/v1/portal/scripts/script_2/connections"

// reachable is what the caller's own persona enumerates in these tests: two
// connections, only one of which the fixture's grant covers.
func reachable() []ConnectionChoice {
	return []ConnectionChoice{
		{Name: "warehouse", Kind: "trino", Description: "Production warehouse"},
		{Name: "lake", Kind: "s3", Description: "Raw object store"},
	}
}

// connectionDeps assembles the portal deps with an enumerator that records the
// scope it was asked for, since narrowing to the caller is the whole contract
// between this package and the composition root.
func connectionDeps(store *stubStore, user *PortalIdentity, choices []ConnectionChoice) (Deps, *ConnectionScope) {
	asked := &ConnectionScope{}
	deps := portalDeps(store, nil, nil, user)
	deps.Connections = func(_ context.Context, caller ConnectionScope) []ConnectionChoice {
		*asked = caller
		return choices
	}
	return deps, asked
}

// TestPortalScriptConnections_ServesTheGrantForARun is the load-bearing case:
// an approved run is confined to what its approval granted, so offering the
// caller's own connections would offer values the run then refuses.
func TestPortalScriptConnections_ServesTheGrantForARun(t *testing.T) {
	deps, _ := connectionDeps(grantedStore(), carol, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body connectionChoicesResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "grant", body.Source)
	require.Len(t, body.Data, 1, "only the granted connection may be offered")
	assert.Equal(t, "warehouse", body.Data[0].Name)
	assert.Equal(t, "Production warehouse", body.Data[0].Description,
		"the description is borrowed from the caller's own enumeration")
	assert.Contains(t, body.Note, "refused")
}

// TestPortalScriptConnections_ServesThePersonaForADraft is the other set: a
// dry run executes as its caller with no grant, so the grant has nothing to
// say about it.
func TestPortalScriptConnections_ServesThePersonaForADraft(t *testing.T) {
	deps, asked := connectionDeps(grantedStore(), carol, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath+"?audience=draft", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body connectionChoicesResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "persona", body.Source)
	assert.Len(t, body.Data, 2, "a dry run reaches what its caller reaches")
	assert.Equal(t, "analyst", asked.Persona)
	assert.False(t, asked.Unrestricted)
}

// TestPortalScriptConnections_EnumeratesAnAdministratorUnrestricted keeps the
// admin surface unrestricted, which is what it is everywhere else.
func TestPortalScriptConnections_EnumeratesAnAdministratorUnrestricted(t *testing.T) {
	deps, asked := connectionDeps(grantedStore(), admin, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath+"?audience=draft", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, asked.Unrestricted)
}

// TestPortalScriptConnections_ListsAGrantedNameTheCallerCannotEnumerate keeps
// the offered set equal to what the run accepts: the grant is what a run is
// checked against, so a name it covers must be offerable even when this reader
// cannot enumerate it themselves.
func TestPortalScriptConnections_ListsAGrantedNameTheCallerCannotEnumerate(t *testing.T) {
	deps, _ := connectionDeps(grantedStore(), carol, nil)
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusOK, rec.Code)
	var body connectionChoicesResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "warehouse", body.Data[0].Name)
	assert.Empty(t, body.Data[0].Kind, "there was nothing to borrow a kind from")
}

// TestPortalScriptConnections_SaysSoWhenNothingIsApproved states the empty set
// as a fact about the script rather than rendering a blank picker.
func TestPortalScriptConnections_SaysSoWhenNothingIsApproved(t *testing.T) {
	deps, _ := connectionDeps(portalStore(), carol, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusOK, rec.Code)
	var body connectionChoicesResponse
	decodeInto(t, rec, &body)
	assert.Empty(t, body.Data)
	assert.Contains(t, body.Note, "can name none")
}

func TestPortalScriptConnections_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	deps, _ := connectionDeps(grantedStore(), stranger, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalScriptConnections_ReportsAnUnreadableApprovedVersion(t *testing.T) {
	store := grantedStore()
	store.versionErr = errors.New("the version store is unavailable")
	deps, _ := connectionDeps(store, carol, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestPortalScriptConnections_IsUnmountedWithoutAnEnumerator keeps a deployment
// that cannot enumerate its connections from serving an empty set a form would
// render as "this script may reach nothing".
func TestPortalScriptConnections_IsUnmountedWithoutAnEnumerator(t *testing.T) {
	deps := portalDeps(grantedStore(), nil, nil, carol)
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "about:blank")
}

// TestOrEmptyChoices keeps a deployment whose enumeration answers nothing from
// putting a null where a form expects a list.
func TestOrEmptyChoices(t *testing.T) {
	assert.NotNil(t, orEmptyChoices(nil))
	assert.Empty(t, orEmptyChoices(nil))
	assert.Len(t, orEmptyChoices(reachable()), 2)
}
