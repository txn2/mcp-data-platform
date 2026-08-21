package scripthttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The set a connection parameter chooses from (#1361): the connections the
// caller's own persona reaches, narrowed to the kind the query binding
// reaches. The tests here are about what was served, how it was narrowed, and
// what it was labeled.

const connectionsPath = "/api/v1/portal/scripts/script_2/connections"

// reachable is what the caller's own persona enumerates in these tests: three
// connections, only two of a kind a connection parameter can name.
func reachable() []ConnectionChoice {
	return []ConnectionChoice{
		{Name: "warehouse", Kind: "trino", Description: "Production warehouse"},
		{Name: "reporting", Kind: "trino", Description: "Reporting cluster"},
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

// TestPortalScriptConnections_ServesThePersonaReach is the route's whole job:
// the caller's own enumeration, narrowed to the bindable kind, labeled with
// where the set came from.
func TestPortalScriptConnections_ServesThePersonaReach(t *testing.T) {
	deps, asked := connectionDeps(portalStore(), carol, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body connectionChoicesResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "persona", body.Source)
	require.Len(t, body.Data, 2,
		"the set is the caller's reach, of the kind a connection parameter names")
	for _, c := range body.Data {
		assert.Equal(t, "trino", c.Kind)
	}
	assert.Equal(t, "warehouse", body.Data[0].Name)
	assert.Equal(t, "Production warehouse", body.Data[0].Description)
	assert.Contains(t, body.Note, "persona")
	assert.Equal(t, "analyst", asked.Persona)
	assert.False(t, asked.Unrestricted)
}

// TestPortalScriptConnections_EnumeratesAnAdministratorUnrestricted keeps the
// admin surface unrestricted, which is what it is everywhere else.
func TestPortalScriptConnections_EnumeratesAnAdministratorUnrestricted(t *testing.T) {
	deps, asked := connectionDeps(portalStore(), admin, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, asked.Unrestricted)
}

// TestPortalScriptConnections_ServesAListRatherThanNull keeps the payload's
// shape stable when the enumeration answers nothing: a form iterates an empty
// list rather than guarding against null.
func TestPortalScriptConnections_ServesAListRatherThanNull(t *testing.T) {
	deps, _ := connectionDeps(portalStore(), carol, nil)
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`)
}

func TestPortalScriptConnections_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	deps, _ := connectionDeps(portalStore(), stranger, reachable())
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestPortalScriptConnections_IsUnmountedWithoutAnEnumerator keeps a deployment
// that cannot enumerate its connections from serving an empty set a form would
// render as "this script may reach nothing".
func TestPortalScriptConnections_IsUnmountedWithoutAnEnumerator(t *testing.T) {
	deps := portalDeps(portalStore(), nil, nil, carol)
	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "about:blank")
}

// TestOrEmptyChoices keeps a deployment whose enumeration answers nothing from
// putting a null where a form expects a list.
func TestOrEmptyChoices(t *testing.T) {
	assert.NotNil(t, orEmptyChoices(nil))
	assert.Empty(t, orEmptyChoices(nil))
	assert.Len(t, orEmptyChoices(reachable()), 3)
}

// TestBindableChoices narrows an enumeration to the kind a connection parameter
// can name, which is the kind the query binding reaches (#1384). Offering the
// others offered values the run refuses, and made a name carried by several
// kinds resolve to whichever the enumeration reached first.
func TestBindableChoices(t *testing.T) {
	got := bindableChoices(reachable())
	require.Len(t, got, 2)
	assert.Equal(t, "warehouse", got[0].Name)
	assert.Equal(t, "reporting", got[1].Name)

	assert.Empty(t, bindableChoices(nil))
	assert.Empty(t, bindableChoices([]ConnectionChoice{{Name: "lake", Kind: "s3"}}))
}

// TestPortalScriptConnections_ResolvesASharedNameToTheKindTheRunReaches is the
// stability the picker owes its reader (#1384). A deployment may carry one name
// across kinds, and a value bound here is passed to the query binding, so the
// name resolves to the connection that binding reaches — the same one on every
// call.
func TestPortalScriptConnections_ResolvesASharedNameToTheKindTheRunReaches(t *testing.T) {
	shared := []ConnectionChoice{
		{Name: "warehouse", Kind: "s3", Description: "Raw object store"},
		{Name: "warehouse", Kind: "datahub", Description: "Catalog"},
		{Name: "warehouse", Kind: "trino", Description: "Production warehouse"},
	}
	deps, _ := connectionDeps(portalStore(), carol, shared)

	rec := servePortalRequest(t, deps, http.MethodGet, connectionsPath, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body connectionChoicesResponse
	decodeInto(t, rec, &body)
	require.Len(t, body.Data, 1)
	assert.Equal(t, "warehouse", body.Data[0].Name)
	assert.Equal(t, "trino", body.Data[0].Kind)
	assert.Equal(t, "Production warehouse", body.Data[0].Description)
}
