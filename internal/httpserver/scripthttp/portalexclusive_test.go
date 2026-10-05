package scripthttp

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/openrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// exclusivePath is the route under test, on the global script carol owns.
const exclusivePath = "/api/v1/portal/scripts/script_2/exclusive"

// TestPortalSetExclusive_SavesTheSetting is #1986's portal setting through the
// route as RegisterPortal mounts it: the owner sets it, the live row carries
// it, and the answer says what it now means.
func TestPortalSetExclusive_SavesTheSetting(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
		msg  string
	}{
		{`{"exclusive":true}`, true, "A run cannot start while another run of this script is pending or running."},
		{`{"exclusive":false}`, false, "Runs of this script may overlap."},
	} {
		store := newEditStore()
		rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, exclusivePath, tc.body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, store.updated)
		assert.Equal(t, tc.want, store.updated.Exclusive)
		var body struct {
			Exclusive bool   `json:"exclusive"`
			Message   string `json:"message"`
		}
		decodeInto(t, rec, &body)
		assert.Equal(t, tc.want, body.Exclusive)
		assert.Contains(t, body.Message, tc.msg)
	}
}

// TestPortalSetExclusive_RefusesABodyWithoutTheSetting keeps an empty or
// malformed body from reading as "turn it off".
func TestPortalSetExclusive_RefusesABodyWithoutTheSetting(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"exclusive":"yes"}`} {
		store := newEditStore()
		rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, exclusivePath, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.Nil(t, store.updated, body)
	}
}

// TestPortalSetExclusive_IsTheOwnersAlone answers a stranger as if the
// script did not exist, as every owner route does.
func TestPortalSetExclusive_IsTheOwnersAlone(t *testing.T) {
	store := newEditStore()
	rec := servePortalRequest(t, editDeps(store, stranger), http.MethodPut, exclusivePath, `{"exclusive":true}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Nil(t, store.updated)
}

// TestPortalSetExclusive_RefusedWhileRunsAreOpen answers 409 with the reason
// when more than one run is open.
func TestPortalSetExclusive_RefusedWhileRunsAreOpen(t *testing.T) {
	store := newEditStore()
	store.updateErr = fmt.Errorf("updating script: %w: %s", openrun.ErrOpen, openrun.BlockedMessage)
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, exclusivePath, `{"exclusive":true}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "more than one run of this script is open")
}

// TestPortalRunScript_RefusedNamingTheOpenRun is #1986's portal run criterion:
// a run of an exclusive script while another is open is answered 409 naming
// it, and nothing is queued.
func TestPortalRunScript_RefusedNamingTheOpenRun(t *testing.T) {
	deps, runs := runDeps(runnableStore(), carol)
	runs.enqueueErr = &openrun.Error{Open: openrun.Run{ID: "run_open", Trigger: script.TriggerTool}}
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "run run_open (started by run_script")
	assert.Nil(t, runs.queued)
}
