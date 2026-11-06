package portal

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// TestHandleEditMessage_EditsThroughThePortalAsTheCaller holds #2514: edit is
// PATCH /api/v1/portal/threads/{id}/events/{eventID}, served as the caller,
// so the portal decides who may edit.
func TestHandleEditMessage_EditsThroughThePortalAsTheCaller(t *testing.T) {
	stub := &portalStub{status: http.StatusOK, answer: `{"id":"evt_1","body":"fixed","edited_at":"2026-11-05T00:00:00Z"}`}
	tk := createToolkit(t, stub)
	res, _, err := tk.handleManageFeedback(callerCtx(), nil, manageFeedbackInput{
		Action: fbActionEdit, ThreadID: "thr_1", EventID: "evt_1", Body: "fixed",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", decodeResult(t, res))
	assert.Equal(t, http.MethodPatch, stub.method)
	assert.Equal(t, threadsRoute+"/thr_1/events/evt_1", stub.path)
	assert.True(t, stub.served)
	assert.Equal(t, otherID, stub.caller.UserID)
	assert.Equal(t, map[string]any{"body": "fixed"}, stub.body)
	assert.Equal(t, "fixed", decodeResult(t, res)["body"])
}

func TestHandleDeleteMessage_DeletesThroughThePortal(t *testing.T) {
	stub := &portalStub{status: http.StatusOK, answer: `{"event_id":"evt_1","deleted":true,"thread_deleted":false}`}
	tk := createToolkit(t, stub)
	res, _, err := tk.handleManageFeedback(callerCtx(), nil, manageFeedbackInput{
		Action: fbActionDelete, ThreadID: "thr_1", EventID: "evt_1",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", decodeResult(t, res))
	assert.Equal(t, http.MethodDelete, stub.method)
	assert.Equal(t, threadsRoute+"/thr_1/events/evt_1", stub.path)
	assert.Equal(t, true, decodeResult(t, res)["deleted"])
}

func TestHandleChangeMessage_Refusals(t *testing.T) {
	refused := &portalStub{status: http.StatusForbidden, answer: `{"title":"Forbidden","status":403,"detail":"only the author of a message can edit it"}`}
	cases := []struct {
		name  string
		stub  *portalStub
		input manageFeedbackInput
		want  string
	}{
		{"no body", refused, manageFeedbackInput{Action: fbActionEdit, ThreadID: "t", EventID: "e"}, "body is required"},
		{"no event id", refused, manageFeedbackInput{Action: fbActionDelete, ThreadID: "t"}, "event_id are required"},
		{
			"the portal refuses", refused,
			manageFeedbackInput{Action: fbActionEdit, ThreadID: "t", EventID: "e", Body: "x"},
			"the portal refused to edit the message: only the author of a message can edit it (HTTP 403)",
		},
		{
			"an unreadable answer", &portalStub{status: http.StatusOK, answer: "not json"},
			manageFeedbackInput{Action: fbActionDelete, ThreadID: "t", EventID: "e"},
			"reading the portal's answer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := createToolkit(t, tc.stub)
			res, _, err := tk.handleManageFeedback(callerCtx(), nil, tc.input)
			require.NoError(t, err)
			require.True(t, res.IsError)
			assert.Contains(t, resultText(t, res), tc.want)
		})
	}

	tk := createToolkit(t, refused)
	res, _, err := tk.handleManageFeedback(strangerCtx(), nil, manageFeedbackInput{Action: fbActionDelete, ThreadID: "t", EventID: "e"})
	require.NoError(t, err)
	assert.Contains(t, resultText(t, res), "signed-in caller")

	noStore := threadToolkit(t, nil, nil)
	res, _, err = noStore.handleManageFeedback(callerCtx(), nil, manageFeedbackInput{Action: fbActionDelete, ThreadID: "t", EventID: "e"})
	require.NoError(t, err)
	assert.Contains(t, resultText(t, res), threadsUnavail)
}

// messageThreadNotifier is the platform notifier's message half.
type messageThreadNotifier struct {
	recordingThreadNotifier
	messages []threads.MessageNotice
}

func (n *messageThreadNotifier) NotifyThreadMessage(_ context.Context, msg threads.MessageNotice) {
	n.messages = append(n.messages, msg)
}

// TestHandleReplyThread_NamesTheMessage holds #2514: an agent's reply is
// notified with the message it is, so deleting it later takes its text out of
// the notifications.
func TestHandleReplyThread_NamesTheMessage(t *testing.T) {
	fts := &fakeThreadStore{getResult: &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"}}
	tk := threadToolkit(t, fts, nil)
	notifier := &messageThreadNotifier{}
	tk.SetFeedbackNotifications(notifier, nil)
	res, _, err := tk.handleReplyThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1", Body: "done"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, notifier.messages, 1)
	require.NotNil(t, fts.lastAppended)
	assert.Equal(t, fts.lastAppended.ID, notifier.messages[0].EventID)
	assert.Equal(t, ownerEmail, notifier.messages[0].ActorEmail)
	assert.Zero(t, notifier.calls, "the message half is used when the notifier has it")
}
