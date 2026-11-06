package feedbackapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// editingStore is the thread mock plus the message edit/delete half.
type editingStore struct {
	*mockThreadStore
	event         *threads.ThreadEvent
	getErr        error
	edited        *threads.EventEdit
	editErr       error
	deleted       string
	deletedBy     string
	deleteErr     error
	threadDeleted bool
}

func (s *editingStore) GetEvent(_ context.Context, _, _ string) (*threads.ThreadEvent, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	clone := *s.event
	return &clone, nil
}

func (s *editingStore) EditEvent(_ context.Context, e threads.EventEdit) (*threads.ThreadEvent, error) {
	s.edited = &e
	if s.editErr != nil {
		return nil, s.editErr
	}
	now := time.Now()
	out := *s.event
	out.Body, out.Metadata, out.EditedAt = e.Body, e.Metadata, &now
	return &out, nil
}

func (s *editingStore) DeleteEvent(_ context.Context, _, eventID, actor string) (bool, error) {
	s.deleted, s.deletedBy = eventID, actor
	return s.threadDeleted, s.deleteErr
}

func (*editingStore) Participants(context.Context, string) ([]string, error) { return []string{}, nil }

// messageNotifier records what the message half of the notifier was asked.
type messageNotifier struct {
	recordingNotifier
	messages  []threads.MessageNotice
	retracted []string
}

func (n *messageNotifier) NotifyThreadMessage(_ context.Context, msg threads.MessageNotice) {
	n.messages = append(n.messages, msg)
}

func (n *messageNotifier) RetractThreadMessage(_ context.Context, eventID string) {
	n.retracted = append(n.retracted, eventID)
}

type recordingAudit struct{ events []middleware.AuditEvent }

func (a *recordingAudit) Log(_ context.Context, ev middleware.AuditEvent) error {
	a.events = append(a.events, ev)
	return nil
}

const (
	editPath   = "/api/v1/portal/threads/thr_1/events/evt_1"
	authorID   = "author-sub"
	authorAddr = "author@example.com"
	ownerID    = "owner-sub"
)

type editFixture struct {
	store    *editingStore
	notifier *messageNotifier
	audit    *recordingAudit
	resolver *stubResolver
}

func newEditServer(t *testing.T, user *access.User, event *threads.ThreadEvent) (http.Handler, *editFixture) {
	t.Helper()
	f := &editFixture{
		store: &editingStore{
			mockThreadStore: &mockThreadStore{getResult: &threads.Thread{
				ID: "thr_1", Kind: threads.ThreadKindComment, TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1",
				AuthorID: authorID, AuthorEmail: authorAddr, Visibility: threads.VisibilityShared,
			}},
			event: event,
		},
		notifier: &messageNotifier{},
		audit:    &recordingAudit{},
		resolver: &stubResolver{},
	}
	assets := &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: ownerID, OwnerEmail: "owner@example.com"}}
	shares := &mockShareStore{listByAsset: []portaldomain.Share{
		{ID: "s1", AssetID: "asset_1", SharedWithEmail: authorAddr, Permission: portaldomain.PermissionViewer},
		{ID: "s2", AssetID: "asset_1", SharedWithEmail: "other@example.com", Permission: portaldomain.PermissionViewer},
	}}
	return newTestServer(Config{
		Assets: assets, Shares: shares, Threads: f.store,
		Mentions: f.resolver, Notifier: f.notifier, Audit: f.audit,
		PersonaName: func([]string) string { return "analyst" },
	}, user), f
}

func commentEvent() *threads.ThreadEvent {
	return &threads.ThreadEvent{
		ID: "evt_1", ThreadID: "thr_1", EventType: threads.EventTypeComment,
		AuthorID: authorID, AuthorEmail: authorAddr, Body: "old text",
		Metadata: json.RawMessage(`{"mentions":["kept@example.com"]}`),
	}
}

var authorUser = &access.User{UserID: authorID, Email: authorAddr}

func TestEditEvent_AuthorEditsAndOnlyNewMentionsAreNotified(t *testing.T) {
	h, f := newEditServer(t, authorUser, commentEvent())
	f.resolver.eligible = []string{"kept@example.com", "new@example.com"}

	rec := doThreadReq(t, h, http.MethodPatch, editPath, map[string]any{"body": " new text @new(example.com) "})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, f.store.edited)
	assert.Equal(t, "new text @new(example.com)", f.store.edited.Body)
	assert.Equal(t, authorAddr, f.store.edited.EditorEmail)
	assert.ElementsMatch(t, []string{"kept@example.com", "new@example.com"}, mention.FromMetadata(f.store.edited.Metadata))

	require.Len(t, f.notifier.messages, 1, "an edit that names someone tells that person")
	msg := f.notifier.messages[0]
	assert.True(t, msg.MentionsOnly, "an edit notifies nobody but the newly mentioned")
	assert.Equal(t, []string{"new@example.com"}, msg.Mentioned, "a person already mentioned is not notified again")
	assert.Equal(t, "evt_1", msg.EventID)

	var got threads.ThreadEvent
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.NotNil(t, got.EditedAt, "the answer carries when it was edited")

	require.Len(t, f.audit.events, 1)
	ev := f.audit.events[0]
	assert.Equal(t, "manage_feedback", ev.ToolName)
	assert.Equal(t, "portal", ev.Source)
	assert.Equal(t, "edit", ev.Parameters["action"])
	assert.Equal(t, "analyst", ev.Persona)
	assert.NotContains(t, ev.Parameters, "body", "the audit row names the message, never its text")
}

func TestEditEvent_UnchangedMentionsNotifyNobody(t *testing.T) {
	h, f := newEditServer(t, authorUser, commentEvent())
	f.resolver.eligible = []string{"kept@example.com"}
	rec := doThreadReq(t, h, http.MethodPatch, editPath, map[string]any{"body": "same @kept(example.com)"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, f.notifier.messages)
}

func TestEditEvent_RemovingEveryMentionClearsThem(t *testing.T) {
	h, f := newEditServer(t, authorUser, commentEvent())
	rec := doThreadReq(t, h, http.MethodPatch, editPath, map[string]any{"body": "nobody named"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, mention.FromMetadata(f.store.edited.Metadata))
}

func TestEditEvent_Refusals(t *testing.T) {
	other := &access.User{UserID: "other-sub", Email: "other@example.com"}
	owner := &access.User{UserID: ownerID, Email: "owner@example.com"}
	approval := commentEvent()
	approval.EventType = threads.EventTypeApproval
	deleted := commentEvent()
	now := time.Now()
	deleted.DeletedAt = &now

	cases := []struct {
		name  string
		user  *access.User
		event *threads.ThreadEvent
		body  any
		want  int
	}{
		{"another participant", other, commentEvent(), map[string]any{"body": "x"}, http.StatusForbidden},
		{"the target owner", owner, commentEvent(), map[string]any{"body": "x"}, http.StatusForbidden},
		{"an approval", authorUser, approval, map[string]any{"body": "x"}, http.StatusConflict},
		{"a deleted message", authorUser, deleted, map[string]any{"body": "x"}, http.StatusConflict},
		{"empty text", authorUser, commentEvent(), map[string]any{"body": "  "}, http.StatusBadRequest},
		{"bad body", authorUser, commentEvent(), "not an object", http.StatusBadRequest},
		{"a body past the limit", authorUser, commentEvent(), map[string]any{"body": strings.Repeat("a", maxRequestBody)}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, f := newEditServer(t, tc.user, tc.event)
			rec := doThreadReq(t, h, http.MethodPatch, editPath, tc.body)
			assert.Equal(t, tc.want, rec.Code, rec.Body.String())
			assert.Nil(t, f.store.edited, "a refused edit changes nothing")
		})
	}
}

func TestEditEvent_StoreErrors(t *testing.T) {
	for err, want := range map[error]int{
		threads.ErrEventNotFound:    http.StatusNotFound,
		threads.ErrEventNotEditable: http.StatusConflict,
		errors.New("boom"):          http.StatusInternalServerError,
	} {
		h, f := newEditServer(t, authorUser, commentEvent())
		f.store.editErr = err
		rec := doThreadReq(t, h, http.MethodPatch, editPath, map[string]any{"body": "x"})
		assert.Equal(t, want, rec.Code)
		require.Len(t, f.audit.events, 1)
		assert.False(t, f.audit.events[0].Success, "a failed edit is audited as failed")
	}
	h, f := newEditServer(t, authorUser, commentEvent())
	f.store.getErr = threads.ErrEventNotFound
	assert.Equal(t, http.StatusNotFound, doThreadReq(t, h, http.MethodPatch, editPath, map[string]any{"body": "x"}).Code)
}

func TestEditEvent_StoreWithoutEditing(t *testing.T) {
	h := newTestServer(Config{
		Assets: ownedAsset("u1"), Shares: &mockShareStore{},
		Threads: &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1"}},
	}, &access.User{UserID: "u1"})
	assert.Equal(t, http.StatusServiceUnavailable, doThreadReq(t, h, http.MethodPatch, editPath, map[string]any{"body": "x"}).Code)
	assert.Equal(t, http.StatusServiceUnavailable, doThreadReq(t, h, http.MethodDelete, editPath, nil).Code)
}

func TestDeleteEvent_WhoMayDelete(t *testing.T) {
	cases := []struct {
		name string
		user *access.User
		want int
	}{
		{"the author", authorUser, http.StatusOK},
		{"the target owner", &access.User{UserID: ownerID, Email: "owner@example.com"}, http.StatusOK},
		{"an administrator", &access.User{UserID: "root", Email: "root@example.com", Roles: []string{"admin"}}, http.StatusOK},
		{"another participant", &access.User{UserID: "other-sub", Email: "other@example.com"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, f := newEditServer(t, tc.user, commentEvent())
			rec := doThreadReq(t, h, http.MethodDelete, editPath, nil)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			if tc.want != http.StatusOK {
				assert.Empty(t, f.store.deleted)
				assert.Empty(t, f.notifier.retracted)
				return
			}
			assert.Equal(t, "evt_1", f.store.deleted)
			assert.Equal(t, tc.user.Email, f.store.deletedBy)
			assert.Equal(t, []string{"evt_1"}, f.notifier.retracted, "a delete takes the text out of the notifications")
			require.Len(t, f.audit.events, 1)
			assert.Equal(t, "delete", f.audit.events[0].Parameters["action"])
		})
	}
}

func TestDeleteEvent_ReportsTheThreadGoing(t *testing.T) {
	h, f := newEditServer(t, authorUser, commentEvent())
	f.store.threadDeleted = true
	rec := doThreadReq(t, h, http.MethodDelete, editPath, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got deleteEventResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.Deleted)
	assert.True(t, got.ThreadDeleted)
}

func TestDeleteEvent_StoreError(t *testing.T) {
	h, f := newEditServer(t, authorUser, commentEvent())
	f.store.deleteErr = threads.ErrEventDeleted
	assert.Equal(t, http.StatusConflict, doThreadReq(t, h, http.MethodDelete, editPath, nil).Code)
	assert.Empty(t, f.notifier.retracted)
}

func TestAudit_SkipsAChangeAnAgentMadeThroughTheTool(t *testing.T) {
	_, f := newEditServer(t, authorUser, commentEvent())
	h := &Handler{cfg: Config{Audit: f.audit}}
	r, err := http.NewRequestWithContext(
		selfcall.With(context.Background(), mcpcontext.Caller{UserID: authorID}), http.MethodDelete, "/", http.NoBody)
	require.NoError(t, err)
	h.audit(r, &eventChange{user: authorUser, thread: &threads.Thread{ID: "thr_1"}, event: &threads.ThreadEvent{ID: "evt_1"}}, "delete", nil)
	assert.Empty(t, f.audit.events, "the tool call is the audit row")
}

func TestNewlyMentioned(t *testing.T) {
	assert.Equal(t, []string{"b@x.io"}, newlyMentioned([]string{"A@x.io"}, []string{"a@x.io", "b@x.io"}))
	assert.Equal(t, []string{}, newlyMentioned(nil, nil))
}

func TestNotifyFallsBackForAPlainNotifier(t *testing.T) {
	n := &recordingNotifier{}
	h := &Handler{cfg: Config{Notifier: n}}
	h.notifyThreadEvent(context.Background(), threads.MessageNotice{Thread: &threads.Thread{ID: "t"}, ActorEmail: "a@x.io", Body: "hi"})
	assert.Equal(t, 1, n.threadCalls)
	h.notifyThreadEvent(context.Background(), threads.MessageNotice{Thread: &threads.Thread{ID: "t"}, MentionsOnly: true})
	assert.Equal(t, 1, n.threadCalls, "a mentions-only notice is never sent as a general one")
	h.retractThreadMessage(context.Background(), "evt") // a plain notifier cannot retract; no panic
}
