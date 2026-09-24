package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptgrant"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Run grants and caller-bound parameters (#1846): a grantee runs a script it
// does not own and reads the run it started; everybody else is answered as if
// the script did not exist; a bound parameter is the caller's.

// appKey is a non-admin API key carrying a tenant attribute.
var appKey = &PortalIdentity{
	UserID: "apikey:reporting-app", Email: "", Persona: "service", Roles: []string{"dp_service"},
	Claims: map[string]any{"tenant": "acme"},
}

// memGrants is the grant store: the grants it holds, and a failure on demand.
type memGrants struct {
	grants []scriptgrant.Grant
	err    error
}

func (*memGrants) matches(g scriptgrant.Grant, c scriptgrant.Caller) bool {
	switch g.Kind {
	case scriptgrant.KindAPIKey:
		return "apikey:"+g.Principal == c.UserID
	case scriptgrant.KindPersona:
		return g.Principal == c.Persona
	default:
		return slices.Contains(c.Roles, g.Principal)
	}
}

func (m *memGrants) List(_ context.Context, scriptID string) ([]scriptgrant.Grant, error) {
	out := []scriptgrant.Grant{}
	for _, g := range m.grants {
		if g.ScriptID == scriptID {
			out = append(out, g)
		}
	}
	return out, m.err
}

func (m *memGrants) Add(_ context.Context, g scriptgrant.Grant) error {
	if m.err != nil {
		return m.err
	}
	m.grants = append(m.grants, g)
	return nil
}

func (m *memGrants) Remove(_ context.Context, g scriptgrant.Grant) (bool, error) {
	for i, have := range m.grants {
		if have.ScriptID == g.ScriptID && have.Kind == g.Kind && have.Principal == g.Principal {
			m.grants = append(m.grants[:i], m.grants[i+1:]...)
			return true, m.err
		}
	}
	return false, m.err
}

func (m *memGrants) Allows(_ context.Context, scriptID string, c scriptgrant.Caller) (bool, error) {
	for _, g := range m.grants {
		if g.ScriptID == scriptID && m.matches(g, c) {
			return true, m.err
		}
	}
	return false, m.err
}

func (m *memGrants) GrantedScriptIDs(_ context.Context, c scriptgrant.Caller) ([]string, error) {
	ids := []string{}
	for _, g := range m.grants {
		if m.matches(g, c) {
			ids = append(ids, g.ScriptID)
		}
	}
	return ids, m.err
}

// grantedRunDeps is runDeps with script_2 granted to the app key.
func grantedRunDeps(store *stubStore, user *PortalIdentity) (Deps, *queueingRuns, *memGrants) {
	deps, runs := runDeps(store, user)
	grants := &memGrants{grants: []scriptgrant.Grant{{ScriptID: "script_2", Kind: scriptgrant.KindAPIKey, Principal: "reporting-app"}}}
	deps.Grants = grants
	return deps, runs, grants
}

func TestPortalRunScript_AGranteeRunsAScriptItDoesNotOwn(t *testing.T) {
	deps, runs, _ := grantedRunDeps(runnableStore(), appKey)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{"params":{"source":"warehouse"}}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotNil(t, runs.queued)
	assert.Equal(t, "apikey:reporting-app", runs.queued.RequestedBy, "the run is the grantee's to follow")

	// A grant opens running, not the script's run history, which is still the
	// owner's.
	rec = servePortalRequest(t, deps, http.MethodGet, "/api/v1/portal/scripts/script_2/runs", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPortalRunScript_ANonGranteeIsAnsweredNotFound(t *testing.T) {
	for name, deps := range map[string]Deps{
		"not granted":    func() Deps { d, _, _ := grantedRunDeps(runnableStore(), stranger); return d }(),
		"no grant store": func() Deps { d, _ := runDeps(runnableStore(), appKey); return d }(),
		"grant read fails": func() Deps {
			d, _, g := grantedRunDeps(runnableStore(), appKey)
			g.err = errors.New("boom")
			return d
		}(),
	} {
		rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{"params":{"source":"warehouse"}}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, name)
	}
	rec := servePortalRequest(t, func() Deps { d, _, _ := grantedRunDeps(runnableStore(), appKey); return d }(),
		http.MethodPost, "/api/v1/portal/scripts/missing/runs", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a missing script")
}

func TestPortalRunScript_ABoundParameterIsTheCallers(t *testing.T) {
	store := runnableStore()
	store.version.Params = []script.Param{{Name: "tenant", Type: script.ParamTypeString, Bind: "caller.tenant"}}

	deps, runs, _ := grantedRunDeps(store, appKey)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.Equal(t, "acme", runs.queued.Params["tenant"], "the run records the bound value")

	deps, runs, _ = grantedRunDeps(store, appKey)
	rec = servePortalRequest(t, deps, http.MethodPost, runPath, `{"params":{"tenant":"globex"}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a body value for a bound parameter is refused")
	assert.Nil(t, runs.queued)

	bare := *appKey
	bare.Claims = nil
	deps, runs, _ = grantedRunDeps(store, &bare)
	rec = servePortalRequest(t, deps, http.MethodPost, runPath, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "a caller without the claim cannot run it")
	assert.Nil(t, runs.queued)
}

func TestPortalListScripts_ScopeGrantedIsTheCallersCatalog(t *testing.T) {
	store := runnableStore()
	deps, _, grants := grantedRunDeps(store, appKey)
	rec := servePortalRequest(t, deps, http.MethodGet, "/api/v1/portal/scripts?scope=granted", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"script_2"}, store.lastFilter.IDs)
	assert.Empty(t, store.lastFilter.OwnerEmail, "a catalog is not the caller's own scripts")
	var body portalScriptListResponse
	decodeInto(t, rec, &body)
	for _, row := range body.Data {
		assert.Equal(t, row.Script.ID == "script_2", row.Granted, row.Script.ID)
		assert.Empty(t, row.Script.Source, "a grantee never reads the source")
	}

	grants.err = errors.New("boom")
	rec = servePortalRequest(t, deps, http.MethodGet, "/api/v1/portal/scripts?scope=granted", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "boom")

	deps, _ = runDeps(store, appKey)
	rec = servePortalRequest(t, deps, http.MethodGet, "/api/v1/portal/scripts?scope=granted", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{}, store.lastFilter.IDs, "no grant store has granted nothing")
}

// The grant routes are mounted with a grant store, gated to the owner, and
// every grant and withdrawal is audited under the caller.
func TestPortalGrants_OwnerGrantsAndWithdrawsAudited(t *testing.T) {
	deps, _, grants := grantedRunDeps(runnableStore(), carol)
	grants.grants = nil
	rec := &recordingAudit{}
	deps.Audit = rec

	resp := servePortalRequest(t, deps, http.MethodPost, "/api/v1/portal/scripts/script_2/grants",
		`{"principal_kind":"api_key","principal":"reporting-app"}`)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	require.Len(t, grants.grants, 1)
	assert.Equal(t, carol.owner(), grants.grants[0].GrantedBy)

	resp = servePortalRequest(t, deps, http.MethodDelete, "/api/v1/portal/scripts/script_2/grants/api_key/reporting-app", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Empty(t, grants.grants)

	require.Len(t, rec.events, 2)
	assert.Equal(t, "script_grant", rec.events[0].ToolName)
	assert.Equal(t, "script_revoke", rec.events[1].ToolName)
	assert.Equal(t, "script_2", rec.events[0].Parameters[keyScriptID])
	assert.Equal(t, carol.Email, rec.events[0].UserEmail)

	stranger := servePortalRequest(t, func() Deps { d, _, _ := grantedRunDeps(runnableStore(), appKey); return d }(),
		http.MethodGet, "/api/v1/portal/scripts/script_2/grants", "")
	assert.Equal(t, http.StatusNotFound, stranger.Code, "a grantee does not manage the grants")
}
