package feedbackapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// undoingStore is the edit mock plus the status undo.
type undoingStore struct {
	*editingStore
	asked    *threads.StatusUndo
	restored string
	err      error
}

func (s *undoingStore) UndoStatusChange(_ context.Context, u threads.StatusUndo) (string, error) {
	s.asked = &u
	return s.restored, s.err
}

const undoPath = "/api/v1/portal/threads/thr_1/events/evt_1/undo"

func newUndoServer(t *testing.T, restored string, err error) (http.Handler, *editFixture, *undoingStore) {
	t.Helper()
	_, f := newEditServer(t, authorUser, commentEvent())
	u := &undoingStore{editingStore: f.store, restored: restored, err: err}
	assets := &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: ownerID, OwnerEmail: "owner@example.com"}}
	shares := &mockShareStore{listByAsset: []portaldomain.Share{
		{ID: "s1", AssetID: "asset_1", SharedWithEmail: authorAddr, Permission: portaldomain.PermissionViewer},
	}}
	h := newTestServer(Config{
		Assets: assets, Shares: shares, Threads: u, Notifier: f.notifier, Audit: f.audit,
		PersonaName: func([]string) string { return "analyst" },
	}, authorUser)
	return h, f, u
}

// TestUndoStatusChange is #2532's route half: the undo restores the status,
// notifies nobody and is audited; each refusal answers its own status.
func TestUndoStatusChange(t *testing.T) {
	h, f, u := newUndoServer(t, threads.ThreadStatusOpen, nil)
	rec := doThreadReq(t, h, http.MethodPost, undoPath, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got undoStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, undoStatusResponse{ThreadID: "thr_1", Status: threads.ThreadStatusOpen}, got)
	require.NotNil(t, u.asked)
	assert.Equal(t, "evt_1", u.asked.EventID)
	assert.Equal(t, authorAddr, u.asked.ActorEmail)
	assert.False(t, u.asked.Now.IsZero())
	assert.Empty(t, f.notifier.messages, "an undo notifies nobody")
	require.Len(t, f.audit.events, 1)
	assert.Equal(t, "undo_status", f.audit.events[0].Parameters["action"])
	assert.True(t, f.audit.events[0].Success)

	for err, want := range map[error]int{
		threads.ErrUndoNotAuthor:  http.StatusForbidden,
		threads.ErrUndoExpired:    http.StatusConflict,
		threads.ErrUndoSuperseded: http.StatusConflict,
		threads.ErrUndoNotStatus:  http.StatusConflict,
		threads.ErrEventNotFound:  http.StatusNotFound,
		errors.New("db down"):     http.StatusInternalServerError,
	} {
		h, f, _ := newUndoServer(t, "", err)
		rec := doThreadReq(t, h, http.MethodPost, undoPath, nil)
		assert.Equal(t, want, rec.Code, err.Error())
		require.Len(t, f.audit.events, 1, "a refused undo is audited too")
		assert.False(t, f.audit.events[0].Success)
	}
}

func TestUndoStatusChange_UnavailableWithoutTheStore(t *testing.T) {
	h, _ := newEditServer(t, authorUser, commentEvent())
	rec := doThreadReq(t, h, http.MethodPost, undoPath, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestUpdateThread_AuditsAStatusChange: the change the undo takes back is in
// the audit log, with both statuses.
func TestUpdateThread_AuditsAStatusChange(t *testing.T) {
	h, f, _ := newUndoServer(t, "", nil)
	f.store.getResult.Status = threads.ThreadStatusOpen
	rec := doThreadReq(t, h, http.MethodPatch, "/api/v1/portal/threads/thr_1", map[string]any{"status": "acknowledged"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, f.audit.events, 1)
	p := f.audit.events[0].Parameters
	assert.Equal(t, "status", p["action"])
	assert.Equal(t, threads.ThreadStatusOpen, p["old_status"])
	assert.Equal(t, "acknowledged", p["new_status"])
}
