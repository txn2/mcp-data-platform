//go:build integration

package portal

// Real-Postgres acceptance for the Phase 3 validation/signoff lifecycle (#603).
// Exercises behavior sqlmock cannot: the real RespondValidation transaction
// (state + re-open + event), the DISTINCT approval-author count, and the
// worklist filters (author/validation_state and the asset-OR-collection set).
// Verified by read-back. Run under `make test-realdb`.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// Guards the expires_at filter added to ListSharedWithUser (so an expired editor
// share cannot leak back into the worklist or "shared with me").
func TestRealDB_ExpiredEditorShareExcluded(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedAsset(t, db, "asset_active", realdbThreadOwner)
	seedAsset(t, db, "asset_expired", realdbThreadOwner)
	shareStore := NewPostgresShareStore(db)
	const grantee = "grantee-user"
	past := time.Now().Add(-time.Hour)

	require.NoError(t, shareStore.Insert(ctx, Share{
		ID: "shr_active", AssetID: "asset_active", Token: "tok_active", CreatedBy: "owner@example.com",
		SharedWithUserID: grantee, Permission: PermissionEditor,
	}))
	require.NoError(t, shareStore.Insert(ctx, Share{
		ID: "shr_expired", AssetID: "asset_expired", Token: "tok_expired", CreatedBy: "owner@example.com",
		SharedWithUserID: grantee, Permission: PermissionEditor, ExpiresAt: &past,
	}))

	shared, _, err := shareStore.ListSharedWithUser(ctx, grantee, "grantee@example.com", 100, 0)
	require.NoError(t, err)
	var ids []string
	for _, s := range shared {
		ids = append(ids, s.Asset.ID)
	}
	assert.Contains(t, ids, "asset_active")
	assert.NotContains(t, ids, "asset_expired", "expired editor share must be excluded")
}

func TestRealDB_ValidationSignoffWorklist(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	store := NewPostgresThreadStore(db)
	seedAsset(t, db, "asset_p3", realdbThreadOwner)
	const sme = "sme-user"

	mk := func(id string, status string) {
		_, err := store.CreateThread(ctx, Thread{
			ID: id, Kind: ThreadKindCorrection, TargetType: targetTypeAsset, AssetID: "asset_p3",
			RequiresResolution: true, AuthorID: sme, AuthorEmail: "sme@example.com", Status: status,
		}, ThreadEvent{ID: id + "_e1", ThreadID: id, EventType: EventTypeComment, AuthorID: sme, AuthorEmail: "sme@example.com", Body: "x"})
		require.NoError(t, err)
	}
	mk("thr_disp", ThreadStatusResolved)
	mk("thr_val", ThreadStatusResolved)

	// 1. request validation -> pending (read-back).
	require.NoError(t, store.RequestValidation(ctx, "thr_disp", sme, "sme@example.com"))
	got, err := store.GetThread(ctx, "thr_disp")
	require.NoError(t, err)
	assert.Equal(t, ValidationStatePending, got.ValidationState)

	// 2. dispute -> disputed + re-opened + validation_result event (read-back).
	require.NoError(t, store.RespondValidation(ctx, "thr_disp",
		ValidationResponse{Result: ValidationStateDisputed, Reason: "still wrong"}, sme, "sme@example.com"))
	got, err = store.GetThread(ctx, "thr_disp")
	require.NoError(t, err)
	assert.Equal(t, ValidationStateDisputed, got.ValidationState)
	assert.Equal(t, ThreadStatusOpen, got.Status, "dispute must re-open the thread")
	events, err := store.ListEvents(ctx, "thr_disp")
	require.NoError(t, err)
	assert.True(t, hasEvent(events, EventTypeValidationResult), "expected a validation_result event")

	// 3. responding to a thread that was never submitted for validation is
	//    rejected (the state machine requires a pending request first).
	require.Error(t, store.RespondValidation(ctx, "thr_val",
		ValidationResponse{Result: ValidationStateValidated}, sme, "sme@example.com"),
		"respond must require validation_state=pending")

	// ...so request it, then validate -> validated, status unchanged (read-back).
	require.NoError(t, store.RequestValidation(ctx, "thr_val", sme, "sme@example.com"))
	require.NoError(t, store.RespondValidation(ctx, "thr_val",
		ValidationResponse{Result: ValidationStateValidated}, sme, "sme@example.com"))
	got, err = store.GetThread(ctx, "thr_val")
	require.NoError(t, err)
	assert.Equal(t, ValidationStateValidated, got.ValidationState)
	assert.Equal(t, ThreadStatusResolved, got.Status, "validating must not re-open")

	// 4. sign-off: two DISTINCT approvers on the asset's threads -> N = 2.
	for _, a := range []struct{ id, author string }{{"ap1", "a1"}, {"ap2", "a2"}, {"ap3", "a1"}} {
		_, err := store.AppendEvent(ctx, ThreadEvent{ID: a.id, ThreadID: "thr_val", EventType: EventTypeApproval, AuthorID: a.author, AuthorEmail: a.author + "@x"})
		require.NoError(t, err)
	}
	n, err := store.CountSignoffs(ctx, targetTypeAsset, "asset_p3")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// 5. SME worklist: pending validation requests authored by the SME.
	mk("thr_pending", ThreadStatusResolved)
	require.NoError(t, store.RequestValidation(ctx, "thr_pending", sme, "sme@example.com"))
	smeList, _, err := store.ListThreads(ctx, ThreadFilter{AuthorID: sme, ValidationState: ValidationStatePending})
	require.NoError(t, err)
	smeIDs := idsOf(smeList)
	assert.Contains(t, smeIDs, "thr_pending")
	assert.NotContains(t, smeIDs, "thr_val", "validated thread is not awaiting validation")

	// 6. practitioner worklist: open + requires_resolution on the owned asset.
	requires := true
	pracList, _, err := store.ListThreads(ctx, ThreadFilter{
		TargetAssetIDs: []string{"asset_p3"}, Status: ThreadStatusOpen, RequiresResolution: &requires,
	})
	require.NoError(t, err)
	pracIDs := idsOf(pracList)
	assert.Contains(t, pracIDs, "thr_disp", "disputed thread re-opened and still requires resolution")
	assert.NotContains(t, pracIDs, "thr_val", "resolved thread is not open")
}

func hasEvent(events []ThreadEvent, eventType string) bool {
	for _, e := range events {
		if e.EventType == eventType {
			return true
		}
	}
	return false
}

func idsOf(ts []ThreadWithMeta) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
