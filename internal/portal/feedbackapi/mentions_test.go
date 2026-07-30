package feedbackapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/mention"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// stubResolver stands in for the audience-backed resolver, recording what the
// write path asked it about.
type stubResolver struct {
	eligible      []string
	gotTargetType string
	gotTargetID   string
	gotBody       string
	gotAuthor     string
	callCount     int
}

func (s *stubResolver) ResolveMentions(_ context.Context, targetType, targetID, body, author string) []string {
	s.callCount++
	s.gotTargetType, s.gotTargetID, s.gotBody, s.gotAuthor = targetType, targetID, body, author
	return s.eligible
}

// mentionTestHandler wires the real thread handlers over the thread mocks plus
// a resolver and a notifier, so a request exercises the assembled path rather
// than the helpers in isolation.
func mentionTestHandler(t *testing.T, resolver MentionResolver) (http.Handler, *mockThreadStore, *recordingNotifier) {
	t.Helper()
	user := &access.User{UserID: "u1", Email: "author@example.com"}
	store := &mockThreadStore{
		getResult: &threads.Thread{
			ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1",
			AuthorID: "u1", AuthorEmail: "author@example.com", Status: threads.ThreadStatusOpen,
		},
	}
	assets := &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "u1", OwnerEmail: "author@example.com"}}
	notifier := &recordingNotifier{}
	h := newTestServer(Config{
		Assets:   assets,
		Shares:   &mockShareStore{},
		Threads:  store,
		Mentions: resolver,
		Notifier: notifier,
	}, user)
	return h, store, notifier
}

// storedMentions reads back what the write path recorded on an event.
func storedMentions(t *testing.T, e *threads.ThreadEvent) []string {
	t.Helper()
	require.NotNil(t, e, "no event reached the store")
	return mention.FromMetadata(e.Metadata)
}

// The full path: a comment naming a teammate is stored with that mention on the
// event and hands the same list to the notifier.
func TestCreateThread_StampsAndNotifiesMentions(t *testing.T) {
	resolver := &stubResolver{eligible: []string{"teammate@example.com"}}
	h, store, notifier := mentionTestHandler(t, resolver)

	body := "@teammate(example.com) can you check the Q4 numbers?"
	rec := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": threads.ThreadKindComment, "target_type": portaldomain.TargetTypeAsset, "asset_id": "asset_1", "body": body,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	assert.Equal(t, portaldomain.TargetTypeAsset, resolver.gotTargetType)
	assert.Equal(t, "asset_1", resolver.gotTargetID, "the audience is resolved against the thread's target")
	assert.Equal(t, body, resolver.gotBody)
	assert.Equal(t, "author@example.com", resolver.gotAuthor,
		"the author is passed so a self-mention can be dropped")
	assert.Equal(t, []string{"teammate@example.com"}, storedMentions(t, store.lastFirstEvent))
	assert.Equal(t, []string{"teammate@example.com"}, notifier.lastMention)
	assert.Equal(t, body, notifier.lastBody)
}

func TestAppendThreadEvent_StampsAndNotifiesMentions(t *testing.T) {
	resolver := &stubResolver{eligible: []string{"teammate@example.com"}}
	h, store, notifier := mentionTestHandler(t, resolver)

	rec := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads/thr_1/events", map[string]any{
		"body": "agreed, @teammate(example.com)",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	assert.Equal(t, []string{"teammate@example.com"}, storedMentions(t, store.lastAppended))
	assert.Equal(t, []string{"teammate@example.com"}, notifier.lastMention)
}

// Someone who cannot open the target is not a mention: the resolver drops them,
// so nothing is recorded and nothing is delivered. The comment still posts, with
// the name left as ordinary text in the body.
func TestCreateThread_MentionOutsideTheAudienceIsPlainText(t *testing.T) {
	resolver := &stubResolver{eligible: nil}
	h, store, notifier := mentionTestHandler(t, resolver)

	body := "@stranger(example.com) take a look"
	rec := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": threads.ThreadKindComment, "target_type": portaldomain.TargetTypeAsset, "asset_id": "asset_1", "body": body,
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	assert.Empty(t, storedMentions(t, store.lastFirstEvent))
	assert.Empty(t, notifier.lastMention)
	assert.Equal(t, body, store.lastFirstEvent.Body, "the token stays in the body it was written in")
	assert.Equal(t, 1, notifier.threadCalls, "the thread notification still fires")
}

// Without a resolver (no database) a comment posts unchanged.
func TestCreateThread_WithoutResolver(t *testing.T) {
	h, store, _ := mentionTestHandler(t, nil)

	rec := doThreadReq(t, h, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": threads.ThreadKindComment, "target_type": portaldomain.TargetTypeAsset, "asset_id": "asset_1",
		"body": "@teammate(example.com) hi",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Empty(t, storedMentions(t, store.lastFirstEvent))
}

func TestResolveMentions_EmptyBodySkipsTheResolver(t *testing.T) {
	resolver := &stubResolver{eligible: []string{"teammate@example.com"}}
	h := New(Config{Mentions: resolver})

	assert.Nil(t, h.resolveMentions(context.Background(), &threads.Thread{TargetType: portaldomain.TargetTypeAsset}, "", "me@example.com"))
	assert.Zero(t, resolver.callCount)
}

func TestStampMentions(t *testing.T) {
	got := stampMentions(threads.ThreadEvent{Metadata: json.RawMessage(`{"anchor":"p3"}`)}, []string{"bob@example.com"})
	assert.JSONEq(t, `{"anchor":"p3","mentions":["bob@example.com"]}`, string(got.Metadata))
}

// Metadata that cannot be re-encoded must not lose the comment: the event is
// stored as it was, without mentions.
func TestStampMentions_MalformedMetadataLeavesTheEventIntact(t *testing.T) {
	original := threads.ThreadEvent{Body: "hi", Metadata: json.RawMessage("not json")}
	got := stampMentions(original, []string{"bob@example.com"})
	assert.Equal(t, original, got)
}

func TestThreadTargetID(t *testing.T) {
	tests := []struct {
		name   string
		thread threads.Thread
		want   string
	}{
		{name: "asset", thread: threads.Thread{AssetID: "a1"}, want: "a1"},
		{name: "collection", thread: threads.Thread{CollectionID: "c1"}, want: "c1"},
		{name: "prompt", thread: threads.Thread{PromptID: "p1"}, want: "p1"},
		{name: "knowledge page", thread: threads.Thread{KnowledgePageID: "kp1"}, want: "kp1"},
		{name: "standalone", thread: threads.Thread{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.thread.TargetID())
		})
	}
}

// recordingNotifier captures the thread notifications the write path fires.
type recordingNotifier struct {
	threadCalls int
	lastThread  *threads.Thread
	lastActor   string
	lastBody    string
	lastMention []string
}

func (n *recordingNotifier) NotifyThreadEvent(_ context.Context, t *threads.Thread, actorEmail, body string, mentioned []string) {
	n.threadCalls++
	n.lastThread, n.lastActor, n.lastBody, n.lastMention = t, actorEmail, body, mentioned
}
