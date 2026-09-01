package scripthttp

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Removing a script was the one verb in its life that the portal could not do
// (#1575), so a person who wanted their automation gone had to ask an agent.
// These cases hold the route to the authority the tool's delete already
// carries: the script's owner, an administrator over any script, and nobody
// else — with a caller who may not see the script answered exactly as one who
// named a script that does not exist.

// deletePath is the route under test, on the script carol owns.
const deletePath = "/api/v1/portal/scripts/script_2"

// ownDeletePath is the same route on jane's own script.
const ownDeletePath = "/api/v1/portal/scripts/script_1"

// TestPortalDeleteScript_OwnerRemovesTheirOwnScript is the load-bearing case:
// the person the script belongs to removes it from their own page, the store
// was asked to remove that script, and the answer states both halves of the
// consequence — what went with it, and what did not.
func TestPortalDeleteScript_OwnerRemovesTheirOwnScript(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{}

	rec := servePortalRequest(t, ownerDeps(store, owner, log), http.MethodDelete, ownDeletePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body deleteResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "deleted", body.Status)
	assert.Equal(t, "daily", body.Name)
	assert.Contains(t, body.Message, "saved versions")
	assert.Contains(t, body.Message, "run history")
	assert.Contains(t, body.Message, "remain",
		"the message says what survives the delete, which is what a person is most likely to be wrong about")
	assert.Equal(t, []string{"script_1"}, store.deletedIDs)
	assert.Len(t, store.scripts, 1, "carol's script is untouched")
}

// TestPortalDeleteScript_AdminRemovesSomebodyElses proves the administrator's
// unrestricted reach applies here as it does to every other script route: a
// script that is not theirs is still theirs to remove.
func TestPortalDeleteScript_AdminRemovesSomebodyElses(t *testing.T) {
	store := portalStore()

	rec := servePortalRequest(t, ownerDeps(store, admin, nil), http.MethodDelete, deletePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"script_2"}, store.deletedIDs)
}

// TestPortalDeleteScript_RefusesEverybodyElse is the criterion a stranger is
// held to. The refusal is a 404 rather than a 403 for the reason every read on
// this surface is: the difference would confirm that a script they may not see
// exists. Nothing was removed.
func TestPortalDeleteScript_RefusesEverybodyElse(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{}

	rec := servePortalRequest(t, ownerDeps(store, stranger, log), http.MethodDelete, deletePath, "")

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Empty(t, store.deletedIDs)
	assert.Len(t, store.scripts, 2)
	assert.Empty(t, log.events, "a refusal that never reached the store is not an act on a script")
}

// TestPortalDeleteScript_UnauthenticatedIsRefused proves the route sits behind
// the same identity gate as every other portal script route rather than being
// reachable by an anonymous caller.
func TestPortalDeleteScript_UnauthenticatedIsRefused(t *testing.T) {
	store := portalStore()

	rec := servePortalRequest(t, ownerDeps(store, nil, nil), http.MethodDelete, deletePath, "")

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Empty(t, store.deletedIDs)
}

// TestPortalDeleteScript_SecondDeleteIsNotFound is the criterion about deleting
// a script that is already gone, and about one that never existed: both are the
// answer the caller can act on rather than a server error.
func TestPortalDeleteScript_SecondDeleteIsNotFound(t *testing.T) {
	store := portalStore()
	deps := ownerDeps(store, owner, nil)

	first := servePortalRequest(t, deps, http.MethodDelete, ownDeletePath, "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	second := servePortalRequest(t, deps, http.MethodDelete, ownDeletePath, "")
	assert.Equal(t, http.StatusNotFound, second.Code, second.Body.String())

	absent := servePortalRequest(t, deps, http.MethodDelete, "/api/v1/portal/scripts/script_404", "")
	assert.Equal(t, http.StatusNotFound, absent.Code, absent.Body.String())
}

// TestPortalDeleteScript_RemovedByAnotherCallerFirst is the race the route
// cannot avoid and must answer honestly: the caller's page was open, the script
// resolved, and somebody else removed it — a second tab, an administrator, an
// agent calling manage_script — before the write landed. The script is gone,
// which is what the caller wanted, so the answer is the not-found a second
// delete gets rather than a server error over a delete that had already
// happened.
func TestPortalDeleteScript_RemovedByAnotherCallerFirst(t *testing.T) {
	store := portalStore()
	store.deleteErr = fmt.Errorf("delete script script_1: %w", script.ErrNotFound)
	log := &recordingAudit{}

	rec := servePortalRequest(t, ownerDeps(store, owner, log), http.MethodDelete, ownDeletePath, "")

	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Len(t, log.events, 1, "the attempt is still an act on a script, and it did not do anything")
	assert.False(t, log.events[0].Success)
}

// TestPortalDeleteScript_RecordsTheAct proves the removal is in the audit log
// naming what was removed and whose it was. The tool surface's delete is
// already recorded as a manage_script call; without this the portal would be
// the one way to remove somebody's automation without a trace.
func TestPortalDeleteScript_RecordsTheAct(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{}

	rec := servePortalRequest(t, ownerDeps(store, admin, log), http.MethodDelete, deletePath, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Len(t, log.events, 1)
	ev := log.events[0]
	assert.Equal(t, auditToolDelete, ev.ToolName)
	assert.Equal(t, audit.EventTypeAdmin, ev.EventKind)
	assert.Equal(t, "admin@example.com", ev.UserEmail)
	assert.True(t, ev.Success)
	assert.Equal(t, "carols-report", ev.Parameters["script"])
	assert.Equal(t, "script_2", ev.Parameters["script_id"])
	assert.Equal(t, "carol@example.com", ev.Parameters["owner"],
		"who lost the automation is the part of an administrator's delete a log has to carry")
}

// TestPortalDeleteScript_RecordsARefusedAttempt holds the same rule the
// transfer is held to: an attempt on somebody's automation that the store
// refused is exactly as much of an act as one it performed, and a log that
// carried only the successes would not show it.
func TestPortalDeleteScript_RecordsARefusedAttempt(t *testing.T) {
	store := portalStore()
	store.deleteErr = errors.New("the database is unreachable")
	log := &recordingAudit{}

	rec := servePortalRequest(t, ownerDeps(store, admin, log), http.MethodDelete, deletePath, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "unreachable",
		"the platform's own failure detail stays in the log")
	require.Len(t, log.events, 1)
	assert.False(t, log.events[0].Success)
	assert.Equal(t, "the database is unreachable", log.events[0].ErrorMessage)
}

// TestPortalDeleteScript_WithoutAnAuditStore proves the delete still works on a
// deployment that keeps no audit log, which is what every other write on this
// surface does.
func TestPortalDeleteScript_WithoutAnAuditStore(t *testing.T) {
	store := portalStore()

	rec := servePortalRequest(t, ownerDeps(store, owner, nil), http.MethodDelete, ownDeletePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"script_1"}, store.deletedIDs)
}

// TestPortalDeleteScript_AuditFailureDoesNotFailTheDelete proves the record is
// best effort: the script is gone either way, and refusing to say so because
// the log could not be written would be a worse answer than a warned log line.
func TestPortalDeleteScript_AuditFailureDoesNotFailTheDelete(t *testing.T) {
	store := portalStore()
	log := &recordingAudit{err: errors.New("audit store down")}

	rec := servePortalRequest(t, ownerDeps(store, owner, log), http.MethodDelete, ownDeletePath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"script_1"}, store.deletedIDs)
}
