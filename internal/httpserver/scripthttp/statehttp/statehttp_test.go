package statehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

const (
	statePath = "/api/v1/portal/scripts/script_2/state"
	carolID   = "carol@example.com"
	adminID   = "admin@example.com"
	// strangerID owns nothing; the gate refuses them.
	strangerID = "stranger@example.com"
)

// stubStates is the state store: one state per script, reset by SetState with
// the revision moved, and failing on demand.
type stubStates struct {
	states map[string]*script.State
	err    error
	// setBy records who the last reset was attributed to.
	setBy string
}

func newStubStates() *stubStates { return &stubStates{states: map[string]*script.State{}} }

func (s *stubStates) GetState(_ context.Context, scriptID string) (*script.State, error) {
	if s.err != nil {
		return nil, s.err
	}
	if st, ok := s.states[scriptID]; ok {
		return st, nil
	}
	return script.EmptyState(scriptID), nil
}

func (s *stubStates) SetState(_ context.Context, scriptID string, value map[string]any, by string) (*script.State, error) {
	if s.err != nil {
		return nil, s.err
	}
	var revision int64
	if prior, ok := s.states[scriptID]; ok {
		revision = prior.Revision
	}
	s.setBy = by
	st := &script.State{ScriptID: scriptID, Value: value, Revision: revision + 1, UpdatedBy: by, UpdatedAt: time.Now().UTC()}
	s.states[scriptID] = st
	return st, nil
}

// serve sends one request as caller through the mounted routes. The gate
// stands in for scripthttp's: carol and the administrator own script_2, and
// everybody else is answered not-found.
// call is one request as one caller.
type call struct{ caller, method, path, body string }

func serve(t *testing.T, states *stubStates, c call) *httptest.ResponseRecorder {
	t.Helper()
	owned := func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
		if c.caller == strangerID || r.PathValue("id") != "script_2" {
			http.Error(w, "script not found", http.StatusNotFound)
			return "", "", false
		}
		return r.PathValue("id"), c.caller, true
	}
	mux := http.NewServeMux()
	New(Deps{States: states, Owned: owned}).Register(mux, func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(context.Background(), c.method, c.path, strings.NewReader(c.body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestState_RefusesWhomTheGateRefuses(t *testing.T) {
	states := newStubStates()
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := serve(t, states, call{strangerID, method, statePath, `{"state":{}}`})
		assert.Equal(t, http.StatusNotFound, rec.Code, method)
	}
	assert.Empty(t, states.states, "nothing was written")
}

func TestGetState_ReportsAnEmptyObjectAtRevisionZero(t *testing.T) {
	rec := serve(t, newStubStates(), call{carolID, http.MethodGet, statePath, ""})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{}, body.State, "{} rather than null")
	assert.Zero(t, body.Revision)
	assert.Nil(t, body.UpdatedAt)
}

func TestGetState_NamesTheRunThatWroteIt(t *testing.T) {
	states := newStubStates()
	states.states["script_2"] = &script.State{
		ScriptID: "script_2", Value: map[string]any{"synced_through": "2026-08-28"}, Revision: 3,
		RunID: "dpx_9", UpdatedAt: time.Now().UTC(),
	}
	rec := serve(t, states, call{carolID, http.MethodGet, statePath, ""})
	require.Equal(t, http.StatusOK, rec.Code)
	var body stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "2026-08-28", body.State["synced_through"])
	assert.Equal(t, int64(3), body.Revision)
	assert.Equal(t, "dpx_9", body.RunID)
	assert.NotNil(t, body.UpdatedAt)
}

func TestSetState_ReplacesTheObjectAsTheCaller(t *testing.T) {
	states := newStubStates()
	rec := serve(t, states, call{carolID, http.MethodPut, statePath, `{"state":{"synced_through":"2026-08-01","count":2}}`})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(1), body.Revision)
	assert.Equal(t, carolID, body.UpdatedBy)
	assert.Contains(t, body.Message, "fails at its write")
	assert.Equal(t, carolID, states.setBy, "the reset is recorded with who did it")
	assert.Equal(t, "2026-08-01", states.states["script_2"].Value["synced_through"])
}

func TestClearState_ResetsToAnEmptyObject(t *testing.T) {
	states := newStubStates()
	states.states["script_2"] = &script.State{ScriptID: "script_2", Value: map[string]any{"k": "v"}, Revision: 4}
	rec := serve(t, states, call{adminID, http.MethodDelete, statePath, ""})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body stateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{}, body.State)
	assert.Equal(t, int64(5), body.Revision, "a clear moves the revision")
	assert.Contains(t, body.Message, "starts from {}")
	assert.Equal(t, adminID, states.setBy, "an administrator reaches every script's state")
}

func TestSetState_Refusals(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{
		{"not JSON", "{", http.StatusBadRequest},
		{"no object", `{}`, http.StatusBadRequest},
		{"over the bound", `{"state":{"blob":"` + strings.Repeat("x", script.MaxStateBytes) + `"}}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			states := newStubStates()
			rec := serve(t, states, call{carolID, http.MethodPut, statePath, tt.body})
			assert.Equal(t, tt.want, rec.Code, rec.Body.String())
			assert.Empty(t, states.states, "nothing was written")
		})
	}
}

func TestState_StoreFailures(t *testing.T) {
	states := newStubStates()
	states.err = errors.New("boom")
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := serve(t, states, call{carolID, method, statePath, `{"state":{}}`})
		assert.Equal(t, http.StatusInternalServerError, rec.Code, method)
		assert.NotContains(t, rec.Body.String(), "boom")
	}
}

func TestRenderState_RevisionZeroHasNoTimestamp(t *testing.T) {
	out := renderState(&script.State{ScriptID: "s"}, "")
	assert.Equal(t, map[string]any{}, out.State)
	assert.Nil(t, out.UpdatedAt)
}
