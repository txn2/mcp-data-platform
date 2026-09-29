package listenbridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// Listens reports how many listen streams the Bridge holds.
func (b *Bridge) Listens() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.listens)
}

// sent is one notification a fake send handler received.
type sent struct {
	method string
	id     any
}

// recorder is a sending handler standing in for the SDK's transport write.
type recorder struct {
	mu   sync.Mutex
	got  []sent
	fail error
}

func (r *recorder) send(_ context.Context, method string, req mcp.Request) (mcp.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return nil, r.fail
	}
	if method == methodAcknowledged {
		return nil, nil //nolint:nilnil // a notification has no result, as with the SDK's own sending handler
	}
	id := req.GetParams().GetMeta()[mcp.MetaKeySubscriptionID]
	r.got = append(r.got, sent{method: method, id: id})
	return nil, nil //nolint:nilnil // a notification has no result, as with the SDK's own sending handler
}

func (r *recorder) methods() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.got))
	for _, s := range r.got {
		out = append(out, s.method)
	}
	return out
}

// announcements records the sessions an Announcer was asked about.
type announcements struct {
	mu  sync.Mutex
	ids []string
}

func (a *announcements) AnnounceOnListen(_ context.Context, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ids = append(a.ids, sessionID)
}

// ack is the acknowledgment the SDK sends for a listen with request id id.
func ack(ss *mcp.ServerSession, id any, n mcp.NotificationSubscriptions) mcp.Request {
	return &mcp.ServerRequest[*mcp.SubscriptionsAcknowledgedParams]{
		Session: ss,
		Params:  &mcp.SubscriptionsAcknowledgedParams{Meta: mcp.Meta{mcp.MetaKeySubscriptionID: id}, Notifications: n},
	}
}

var all = mcp.NotificationSubscriptions{ToolsListChanged: true, PromptsListChanged: true, ResourcesListChanged: true}

// acknowledge runs an acknowledgment through the Bridge's middleware under a
// listen context carrying sessionID, and returns the stream's recorder and the
// context's cancel.
func acknowledge(t *testing.T, b *Bridge, sessionID string, id any, n mcp.NotificationSubscriptions) (*recorder, context.CancelFunc) {
	t.Helper()
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if sessionID != "" {
		ctx = session.WithAwareSessionID(ctx, sessionID)
	}
	_, err := b.Middleware(rec.send)(ctx, methodAcknowledged, ack(&mcp.ServerSession{}, id, n))
	require.NoError(t, err)
	return rec, cancel
}

func TestMiddleware_RecordsOnlyAnAcknowledgedListChangeSubscription(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	rec := &recorder{}
	mw := b.Middleware(rec.send)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		method string
		req    mcp.Request
	}{
		"another notification": {methodPromptsChanged, &mcp.ServerRequest[*mcp.PromptListChangedParams]{Session: &mcp.ServerSession{}, Params: &mcp.PromptListChangedParams{}}},
		"no list types":        {methodAcknowledged, ack(&mcp.ServerSession{}, 1, mcp.NotificationSubscriptions{ResourceSubscriptions: []string{"x"}})},
		"no subscription id":   {methodAcknowledged, ack(&mcp.ServerSession{}, nil, all)},
		"nil params":           {methodAcknowledged, &mcp.ServerRequest[*mcp.SubscriptionsAcknowledgedParams]{Session: &mcp.ServerSession{}}},
		"a client session":     {methodAcknowledged, &mcp.ClientRequest[*mcp.SubscriptionsAcknowledgedParams]{Session: &mcp.ClientSession{}, Params: &mcp.SubscriptionsAcknowledgedParams{Notifications: all}}},
	} {
		_, err := mw(ctx, tc.method, tc.req)
		require.NoError(t, err, name)
		assert.Zero(t, b.Listens(), name)
	}

	// A failed acknowledgment opened no stream.
	rec.fail = errors.New("closed")
	_, err := mw(ctx, methodAcknowledged, ack(&mcp.ServerSession{}, 1, all))
	require.Error(t, err)
	assert.Zero(t, b.Listens())
}

func TestDeliver_WritesEachTypeToTheListensThatAgreedToIt(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	prompts, _ := acknowledge(t, b, "a", int64(7), mcp.NotificationSubscriptions{PromptsListChanged: true})
	resources, _ := acknowledge(t, b, "b", "listen-b", mcp.NotificationSubscriptions{ResourcesListChanged: true})
	require.Equal(t, 2, b.Listens())

	b.Deliver(session.Event{Method: methodPromptsChanged})
	b.Deliver(session.Event{Method: methodResourcesChanged})
	b.Deliver(session.Event{Method: "notifications/message"})

	assert.Equal(t, []sent{{methodPromptsChanged, int64(7)}}, prompts.got)
	assert.Equal(t, []sent{{methodResourcesChanged, "listen-b"}}, resources.got)
}

func TestDeliver_ToolsListChanged(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	a, _ := acknowledge(t, b, "a", 1, all)
	other, _ := acknowledge(t, b, "b", 2, all)

	// The SDK already told each replica's listens of a gateway change.
	b.Deliver(session.Event{Method: methodToolsChanged})
	assert.Empty(t, a.methods())
	assert.Empty(t, other.methods())

	// A notice addressed to a session reaches its listen once.
	b.Deliver(session.Event{Method: methodToolsChanged, SessionID: "a"})
	b.Deliver(session.Event{Method: methodToolsChanged, SessionID: "a"})
	assert.Equal(t, []string{methodToolsChanged}, a.methods())
	assert.Empty(t, other.methods())

	// An addressed prompt event reaches that session alone.
	b.Deliver(session.Event{Method: methodPromptsChanged, SessionID: "b"})
	assert.Equal(t, []string{methodPromptsChanged}, other.methods())
	assert.Equal(t, []string{methodToolsChanged}, a.methods())
}

func TestListen_DroppedWhenItEndsOrCannotBeWritten(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	ended, cancel := acknowledge(t, b, "a", 1, all)
	broken, _ := acknowledge(t, b, "b", 2, all)
	require.Equal(t, 2, b.Listens())

	cancel()
	require.Eventually(t, func() bool { return b.Listens() == 1 }, 5*time.Second, time.Millisecond)

	broken.fail = errors.New("write to closed stream")
	b.Deliver(session.Event{Method: methodPromptsChanged})
	assert.Zero(t, b.Listens())
	assert.Empty(t, ended.methods())
}

func TestRegister_AsksTheAnnouncerAboutTheListensSession(t *testing.T) {
	a := &announcements{}
	b := New(session.NewMemoryBroadcaster(nil), a, nil)
	acknowledge(t, b, "resumed", 1, all)
	acknowledge(t, b, "", 2, all)
	acknowledge(t, b, "prompts-only", 3, mcp.NotificationSubscriptions{PromptsListChanged: true})
	assert.Equal(t, []string{"resumed"}, a.ids)
}

func TestStart_DeliversBroadcastEventsUntilTheContextEnds(t *testing.T) {
	broker := session.NewMemoryBroadcaster(nil)
	t.Cleanup(func() { _ = broker.Close() })
	b := New(broker, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := b.Start(ctx)

	rec, _ := acknowledge(t, b, "a", 1, all)
	require.NoError(t, broker.Publish(ctx, session.Event{Method: methodResourcesChanged}))
	require.Eventually(t, func() bool { return len(rec.methods()) == 1 }, 5*time.Second, time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not stop when its context ended")
	}
}

func TestWire_NeedsAServerAndABroadcaster(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	Wire(t.Context(), nil, session.NewMemoryBroadcaster(nil), nil)
	Wire(t.Context(), server, nil, nil)

	broker := session.NewMemoryBroadcaster(nil)
	t.Cleanup(func() { _ = broker.Close() })
	Wire(t.Context(), server, broker, nil)
	require.Eventually(t, func() bool { return broker.SubscriberCount() == 1 }, 5*time.Second, time.Millisecond)
}

// The SDK's own prompts or resources list_changed to a listen stream is
// dropped, since the broadcaster's copy reaches it on every replica, the
// writer's included; to a session holding no listen it passes as before.
func TestMiddleware_DropsTheSDKsOwnPromptAndResourceNoticeToAListen(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	rec := &recorder{}
	mw := b.Middleware(rec.send)
	listening, other := &mcp.ServerSession{}, &mcp.ServerSession{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_, err := mw(ctx, methodAcknowledged, ack(listening, 1, all))
	require.NoError(t, err)

	resources := func(ss *mcp.ServerSession) mcp.Request {
		return &mcp.ServerRequest[*mcp.ResourceListChangedParams]{Session: ss, Params: &mcp.ResourceListChangedParams{}}
	}
	prompts := &mcp.ServerRequest[*mcp.PromptListChangedParams]{Session: listening, Params: &mcp.PromptListChangedParams{}}
	tools := &mcp.ServerRequest[*mcp.ToolListChangedParams]{Session: listening, Params: &mcp.ToolListChangedParams{}}
	for _, call := range []struct {
		method string
		req    mcp.Request
	}{
		{methodResourcesChanged, resources(listening)},
		{methodPromptsChanged, prompts},
		{methodResourcesChanged, resources(other)},
		{methodToolsChanged, tools},
	} {
		_, err := mw(ctx, call.method, call.req)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{methodResourcesChanged, methodToolsChanged}, rec.methods(),
		"only the session with no listen, and the tools notice the SDK owns, pass")

	// Once the listen ends the session is an ordinary one again.
	cancel()
	require.Eventually(t, func() bool { return b.Listens() == 0 }, 5*time.Second, time.Millisecond)
	_, err = mw(context.Background(), methodPromptsChanged, prompts)
	require.NoError(t, err)
	assert.Equal(t, []string{methodResourcesChanged, methodToolsChanged, methodPromptsChanged}, rec.methods())
}

// Two notices addressed to one session inside the notice window are the one
// notice the listen request and its acknowledgment both published; one after
// it is a new build announced, as in a rolling deploy, and is written.
func TestDeliver_ANoticeAfterTheWindowIsWritten(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	clock := time.Unix(1_000, 0)
	b.now = func() time.Time { return clock }
	a, _ := acknowledge(t, b, "a", 1, all)
	notice := session.Event{Method: methodToolsChanged, SessionID: "a"}

	b.Deliver(notice)
	clock = clock.Add(noticeWindow - time.Millisecond)
	b.Deliver(notice)
	assert.Equal(t, []string{methodToolsChanged}, a.methods())

	clock = clock.Add(noticeWindow)
	b.Deliver(notice)
	assert.Equal(t, []string{methodToolsChanged, methodToolsChanged}, a.methods())
}

// A listen whose write stalls holds up no other listen: the streams are
// written at once.
func TestDeliver_AStalledListenHoldsUpNoOther(t *testing.T) {
	b := New(session.NewMemoryBroadcaster(nil), nil, nil)
	stalled, release := make(chan struct{}), make(chan struct{})
	slow := func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		if method == methodAcknowledged {
			return nil, nil //nolint:nilnil // a notification has no result
		}
		close(stalled)
		<-release
		return nil, nil //nolint:nilnil // a notification has no result
	}
	_, err := b.Middleware(slow)(context.Background(), methodAcknowledged, ack(&mcp.ServerSession{}, 1, all))
	require.NoError(t, err)
	fast, _ := acknowledge(t, b, "", 2, all)

	done := make(chan struct{})
	go func() {
		b.Deliver(session.Event{Method: methodPromptsChanged})
		close(done)
	}()
	<-stalled
	require.Eventually(t, func() bool { return len(fast.methods()) == 1 }, 5*time.Second, time.Millisecond,
		"the other listen is written while the first is stalled")
	close(release)
	<-done
}
