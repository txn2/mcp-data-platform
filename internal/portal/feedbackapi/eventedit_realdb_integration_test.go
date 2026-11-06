//go:build integration

package feedbackapi

// Real-Postgres tests for editing and deleting a message (#2514) and the
// people a thread involves (#2512): the revision row, the redaction every read
// applies to a deleted message, the thread deleted with a lone opening
// message, and the participants query's jsonb walk.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

func TestRealDB_EditAndDeleteMessage(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedAsset(t, db, "asset_edit", realdbThreadOwner)
	store := threads.NewPostgresThreadStore(db)
	editor := store.(threads.EventEditor)

	_, err := store.CreateThread(ctx, threads.Thread{
		ID: "thr_edit", Kind: threads.ThreadKindCorrection, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_edit",
		AuthorID: "b", AuthorEmail: "b@example.com",
	}, threads.ThreadEvent{
		ID: "evt_open", ThreadID: "thr_edit", EventType: threads.EventTypeComment,
		AuthorID: "b", AuthorEmail: "b@example.com", Body: "the total is wrong",
	})
	require.NoError(t, err)
	_, err = store.AppendEvent(ctx, threads.ThreadEvent{
		ID: "evt_reply", ThreadID: "thr_edit", EventType: threads.EventTypeComment,
		AuthorID: "o", AuthorEmail: "Owner@Example.com", Body: "the secret figure is 9137",
		Metadata: json.RawMessage(`{"mentions":["c@example.com"]}`),
	})
	require.NoError(t, err)
	_, err = store.AppendEvent(ctx, threads.ThreadEvent{
		ID: "evt_ok", ThreadID: "thr_edit", EventType: threads.EventTypeApproval,
		AuthorID: "o", AuthorEmail: "owner@example.com",
	})
	require.NoError(t, err)

	people, err := editor.Participants(ctx, "thr_edit")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"b@example.com", "owner@example.com", "c@example.com"}, people)

	edited, err := editor.EditEvent(ctx, threads.EventEdit{
		ThreadID: "thr_edit", EventID: "evt_open",
		Body: "the Q4 total is wrong", Metadata: json.RawMessage(`{}`), EditorEmail: "b@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, "the Q4 total is wrong", edited.Body)
	require.NotNil(t, edited.EditedAt)
	var kept string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT body FROM portal_thread_event_revisions WHERE event_id = 'evt_open'`).Scan(&kept))
	assert.Equal(t, "the total is wrong", kept, "the previous text is kept")

	_, err = editor.EditEvent(ctx, threads.EventEdit{ThreadID: "thr_edit", EventID: "evt_ok", Body: "x"})
	assert.ErrorIs(t, err, threads.ErrEventNotEditable, "an approval is never edited")
	_, err = editor.DeleteEvent(ctx, "thr_edit", "evt_ok", "owner@example.com")
	assert.ErrorIs(t, err, threads.ErrEventNotEditable, "an approval is never deleted")

	gone, err := editor.DeleteEvent(ctx, "thr_edit", "evt_reply", "owner@example.com")
	require.NoError(t, err)
	assert.False(t, gone)
	_, err = editor.DeleteEvent(ctx, "thr_edit", "evt_reply", "owner@example.com")
	assert.ErrorIs(t, err, threads.ErrEventDeleted)

	events, err := store.ListEvents(ctx, "thr_edit")
	require.NoError(t, err)
	require.Len(t, events, 3, "a deleted message keeps its place")
	raw, err := json.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "9137", "no read returns a deleted message's text")
	assert.NotContains(t, string(raw), "c@example.com", "nor the people it mentioned")
	got, err := editor.GetEvent(ctx, "thr_edit", "evt_reply")
	require.NoError(t, err)
	assert.Empty(t, got.Body)
	require.NotNil(t, got.DeletedAt)

	people, err = editor.Participants(ctx, "thr_edit")
	require.NoError(t, err)
	assert.NotContains(t, people, "c@example.com", "a deleted message no longer makes anyone a participant")

	found, err := store.(interface {
		SearchThreads(ctx context.Context, ownerEmail, intent string, limit int) ([]threads.Thread, error)
	}).SearchThreads(ctx, "b@example.com", "9137", 5)
	require.NoError(t, err)
	assert.Empty(t, found, "search does not match a deleted message's text")

	mentioned, _, err := store.ListThreads(ctx, threads.ThreadFilter{MentionedEmail: "c@example.com"})
	require.NoError(t, err)
	assert.Empty(t, mentioned, "a deleted mention leaves the mentions inbox")

	// The opening message is deleted, but an approval followed it: the
	// thread stays.
	gone, err = editor.DeleteEvent(ctx, "thr_edit", "evt_open", "b@example.com")
	require.NoError(t, err)
	assert.False(t, gone)
	_, err = store.GetThread(ctx, "thr_edit")
	require.NoError(t, err)
}

func TestRealDB_DeletingALoneOpeningMessageDeletesTheThread(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedAsset(t, db, "asset_lone", realdbThreadOwner)
	store := threads.NewPostgresThreadStore(db)
	editor := store.(threads.EventEditor)
	_, err := store.CreateThread(ctx, threads.Thread{
		ID: "thr_lone", Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_lone",
		AuthorID: "b", AuthorEmail: "b@example.com",
	}, threads.ThreadEvent{
		ID: "evt_lone", ThreadID: "thr_lone", EventType: threads.EventTypeComment,
		AuthorID: "b", AuthorEmail: "b@example.com", Body: "wrong thread",
	})
	require.NoError(t, err)
	// A status change is the thread's bookkeeping, not a reply.
	status := threads.ThreadStatusAcknowledged
	require.NoError(t, store.UpdateThread(ctx, "thr_lone", threads.ThreadUpdate{Status: &status}, "o", "owner@example.com"))

	gone, err := editor.DeleteEvent(ctx, "thr_lone", "evt_lone", "b@example.com")
	require.NoError(t, err)
	assert.True(t, gone)
	_, err = store.GetThread(ctx, "thr_lone")
	assert.Error(t, err, "the thread went with its only message")
}
