package granthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptgrant"
)

const (
	grantsPath = "/api/v1/portal/scripts/script_2/grants"
	ownerID    = "carol@example.com"
	strangerID = "stranger@example.com"
)

// fakeGrants is the store, with a failure per operation.
type fakeGrants struct {
	grants                       []scriptgrant.Grant
	listErr, addErr, removeErr   error
	scriptgrant.Store            // Allows and GrantedScriptIDs are not these routes'
	removed                      bool
	lastAdded, lastRemoveRequest scriptgrant.Grant
}

func (f *fakeGrants) List(context.Context, string) ([]scriptgrant.Grant, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]scriptgrant.Grant{}, f.grants...), nil
}

func (f *fakeGrants) Add(_ context.Context, g scriptgrant.Grant) error {
	f.lastAdded = g
	if f.addErr != nil {
		return f.addErr
	}
	f.grants = append(f.grants, g)
	return nil
}

func (f *fakeGrants) Remove(_ context.Context, g scriptgrant.Grant) (bool, error) {
	f.lastRemoveRequest = g
	return f.removed, f.removeErr
}

// audited is one recorded act.
type audited struct {
	tool   string
	params map[string]any
	failed bool
}

// call is one request as one caller.
type call struct{ caller, method, path, body string }

func serve(t *testing.T, grants *fakeGrants, acts *[]audited, c call) *httptest.ResponseRecorder {
	t.Helper()
	deps := Deps{
		Grants: grants,
		Owned: func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
			if c.caller == strangerID {
				http.Error(w, "script not found", http.StatusNotFound)
				return "", "", false
			}
			return r.PathValue("id"), c.caller, true
		},
	}
	if acts != nil {
		deps.Audit = func(_ *http.Request, tool, _ string, params map[string]any, opErr error) {
			*acts = append(*acts, audited{tool: tool, params: params, failed: opErr != nil})
		}
	}
	mux := http.NewServeMux()
	New(deps).Register(mux, func(h http.Handler) http.Handler { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), c.method, c.path, strings.NewReader(c.body)))
	return rec
}

func TestAddListRemove(t *testing.T) {
	grants := &fakeGrants{}
	var acts []audited
	rec := serve(t, grants, &acts, call{ownerID, http.MethodPost, grantsPath, `{"principal_kind":"role","principal":"dp_reports"}`})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, scriptgrant.Grant{ScriptID: "script_2", Kind: "role", Principal: "dp_reports", GrantedBy: ownerID}, grants.lastAdded)
	var body grantListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Total)

	rec = serve(t, grants, &acts, call{ownerID, http.MethodGet, grantsPath, ""})
	require.Equal(t, http.StatusOK, rec.Code)

	grants.removed = true
	rec = serve(t, grants, &acts, call{ownerID, http.MethodDelete, grantsPath + "/role/dp_reports", ""})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "dp_reports", grants.lastRemoveRequest.Principal)

	require.Len(t, acts, 2)
	assert.Equal(t, audited{tool: auditToolGrant, params: map[string]any{"principal_kind": "role", "principal": "dp_reports"}}, acts[0])
	assert.Equal(t, auditToolRevoke, acts[1].tool)
}

func TestEmptyListIsAnArray(t *testing.T) {
	rec := serve(t, &fakeGrants{}, nil, call{ownerID, http.MethodGet, grantsPath, ""})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`)
}

func TestRefusals(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		grants       *fakeGrants
		caller, verb string
		path, body   string
		want         int
	}{
		"stranger lists":       {&fakeGrants{}, strangerID, http.MethodGet, grantsPath, "", http.StatusNotFound},
		"stranger grants":      {&fakeGrants{}, strangerID, http.MethodPost, grantsPath, `{}`, http.StatusNotFound},
		"stranger withdraws":   {&fakeGrants{}, strangerID, http.MethodDelete, grantsPath + "/role/r", "", http.StatusNotFound},
		"not json":             {&fakeGrants{}, ownerID, http.MethodPost, grantsPath, "{", http.StatusBadRequest},
		"unknown kind":         {&fakeGrants{}, ownerID, http.MethodPost, grantsPath, `{"principal_kind":"user","principal":"x"}`, http.StatusBadRequest},
		"add fails":            {&fakeGrants{addErr: boom}, ownerID, http.MethodPost, grantsPath, `{"principal_kind":"role","principal":"r"}`, http.StatusInternalServerError},
		"list fails":           {&fakeGrants{listErr: boom}, ownerID, http.MethodGet, grantsPath, "", http.StatusInternalServerError},
		"no such grant":        {&fakeGrants{}, ownerID, http.MethodDelete, grantsPath + "/role/r", "", http.StatusNotFound},
		"withdraw fails":       {&fakeGrants{removeErr: boom}, ownerID, http.MethodDelete, grantsPath + "/role/r", "", http.StatusInternalServerError},
		"list after add fails": {&fakeGrants{listErr: boom}, ownerID, http.MethodPost, grantsPath, `{"principal_kind":"role","principal":"r"}`, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, tc.grants, nil, call{tc.caller, tc.verb, tc.path, tc.body})
			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
			assert.NotContains(t, rec.Body.String(), "boom")
		})
	}
}

// A failed grant is still audited, as a failure.
func TestFailedGrantIsAudited(t *testing.T) {
	var acts []audited
	serve(t, &fakeGrants{addErr: errors.New("boom")}, &acts, call{ownerID, http.MethodPost, grantsPath, `{"principal_kind":"role","principal":"r"}`})
	require.Len(t, acts, 1)
	assert.True(t, acts[0].failed)
}
