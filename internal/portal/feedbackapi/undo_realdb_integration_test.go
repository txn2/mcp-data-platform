//go:build integration

package feedbackapi

// Real-Postgres test for undoing a status change (#2532): the row-value
// comparison that finds a later event by someone else, the status restored
// and the line removed together, and the refusals read from what is stored.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

func TestRealDB_UndoStatusChange(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedAsset(t, db, "asset_undo", realdbThreadOwner)
	store := threads.NewPostgresThreadStore(db)
	undoer := store.(threads.StatusUndoer)

	_, err := store.CreateThread(ctx, threads.Thread{
		ID: "thr_undo", Kind: threads.ThreadKindQuestion, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_undo",
		AuthorID: "b", AuthorEmail: "b@example.com",
	}, threads.ThreadEvent{
		ID: "evt_open", ThreadID: "thr_undo", EventType: threads.EventTypeComment,
		AuthorID: "b", AuthorEmail: "b@example.com", Body: "why is March missing?",
	})
	require.NoError(t, err)
	ack := threads.ThreadStatusAcknowledged
	require.NoError(t, store.UpdateThread(ctx, "thr_undo", threads.ThreadUpdate{Status: &ack}, "o", "owner@example.com"))
	statusEvent := func() string {
		events, err := store.ListEvents(ctx, "thr_undo")
		require.NoError(t, err)
		for _, e := range events {
			if e.EventType == threads.EventTypeStatusChange {
				return e.ID
			}
		}
		return ""
	}
	id := statusEvent()
	require.NotEmpty(t, id)

	_, err = undoer.UndoStatusChange(ctx, threads.StatusUndo{ThreadID: "thr_undo", EventID: id, ActorID: "b", ActorEmail: "b@example.com", Now: time.Now()})
	require.ErrorIs(t, err, threads.ErrUndoNotAuthor)
	_, err = undoer.UndoStatusChange(ctx, threads.StatusUndo{ThreadID: "thr_undo", EventID: id, ActorID: "o", Now: time.Now().Add(11 * time.Minute)})
	require.ErrorIs(t, err, threads.ErrUndoExpired)

	restored, err := undoer.UndoStatusChange(ctx, threads.StatusUndo{
		ThreadID: "thr_undo", EventID: id, ActorID: "o", ActorEmail: "Owner@Example.com", Now: time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, threads.ThreadStatusOpen, restored)
	got, err := store.GetThread(ctx, "thr_undo")
	require.NoError(t, err)
	assert.Equal(t, threads.ThreadStatusOpen, got.Status)
	assert.Empty(t, statusEvent(), "the line the change wrote is gone")

	// Someone else adds to the thread after a change: it can no longer be undone.
	require.NoError(t, store.UpdateThread(ctx, "thr_undo", threads.ThreadUpdate{Status: &ack}, "o", "owner@example.com"))
	id = statusEvent()
	_, err = store.AppendEvent(ctx, threads.ThreadEvent{
		ID: "evt_reply", ThreadID: "thr_undo", EventType: threads.EventTypeComment,
		AuthorID: "b", AuthorEmail: "b@example.com", Body: "thanks",
	})
	require.NoError(t, err)
	_, err = undoer.UndoStatusChange(ctx, threads.StatusUndo{ThreadID: "thr_undo", EventID: id, ActorID: "o", Now: time.Now()})
	require.ErrorIs(t, err, threads.ErrUndoSuperseded)
	got, err = store.GetThread(ctx, "thr_undo")
	require.NoError(t, err)
	assert.Equal(t, threads.ThreadStatusAcknowledged, got.Status, "a refused undo changes nothing")
}
