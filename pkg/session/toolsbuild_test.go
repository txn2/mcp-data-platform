package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingBroadcaster is a MemoryBroadcaster that counts the tools/list_changed
// events published to each session and signals every subscription.
type countingBroadcaster struct {
	*MemoryBroadcaster
	mu         sync.Mutex
	announced  map[string]int
	subscribed chan string
}

func newCountingBroadcaster(t *testing.T) *countingBroadcaster {
	t.Helper()
	b := &countingBroadcaster{MemoryBroadcaster: NewMemoryBroadcaster(nil), announced: map[string]int{}, subscribed: make(chan string, 16)}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func (b *countingBroadcaster) Subscribe(ctx context.Context, sessionID string) Subscription {
	sub := b.MemoryBroadcaster.Subscribe(ctx, sessionID)
	b.subscribed <- sessionID
	return sub
}

func (b *countingBroadcaster) Publish(ctx context.Context, ev Event) error {
	if ev.Method == toolsListChanged {
		b.mu.Lock()
		b.announced[ev.SessionID]++
		b.mu.Unlock()
	}
	return b.MemoryBroadcaster.Publish(ctx, ev)
}

func (b *countingBroadcaster) count(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.announced[sessionID]
}

// A session that listed tools under one build and makes its next request on
// another is sent notifications/tools/list_changed once, through a real MCP
// client; a session whose build did not change is sent none (#1946).
func TestHandler_ToolsChanged_ARealClientIsToldOnce(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "tools-build", Version: "0.0.1"}, nil)
	server.AddTool(&mcp.Tool{Name: "noop", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	stateless := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})
	store := NewMemoryStore(handlerTestTTL)
	t.Cleanup(func() { _ = store.Close() })
	broker := newCountingBroadcaster(t)
	srv := httptest.NewServer(NewAwareHandler(stateless, HandlerConfig{Store: store, TTL: handlerTestTTL, Broadcaster: broker, Build: "v2"}))
	t.Cleanup(srv.Close)

	changed := make(chan struct{}, 4)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
	})
	// A client on the protocol revision with sessions: it initializes, keeps
	// its Mcp-Session-Id and opens the stream a session is told on. A client
	// on 2026-07-28 has no session to resume (SEP-2575).
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	id := cs.ID()
	require.NotEmpty(t, id)
	select {
	case got := <-broker.subscribed:
		require.Equal(t, id, got)
	case <-time.After(10 * time.Second):
		t.Fatal("the client never opened its stream")
	}

	_, err = cs.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Zero(t, broker.count(id), "a session on the build it listed under is told nothing")

	// The session listed its tools under the build before this one.
	require.NoError(t, store.UpdateState(ctx, id, map[string]any{toolsBuildKey: "v1"}))
	_, err = cs.ListTools(ctx, nil)
	require.NoError(t, err)
	select {
	case <-changed:
	case <-time.After(10 * time.Second):
		t.Fatal("the client was not told its tool list changed")
	}
	_, err = cs.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, broker.count(id), "told once")

	sess, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "v2", sess.State[toolsBuildKey])
}

// An event addressed to one session reaches that session's stream and no
// other's, and an unaddressed one reaches both.
func TestHandler_SSE_DeliversAnAddressedEventToItsSessionAlone(t *testing.T) {
	handler, store, broker := newSSEHandler(t)
	ctx := context.Background()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	open := func(id string) io.Reader {
		sess := newTestSession(id, handlerTestTTL)
		sess.UserID = ""
		require.NoError(t, store.Create(ctx, sess))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set(sessionIDHeader, id)
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp.Body
	}
	a, b := open("sess-a"), open("sess-b")
	for broker.SubscriberCount() < 2 {
		time.Sleep(5 * time.Millisecond)
	}

	require.NoError(t, broker.Publish(ctx, Event{Method: toolsListChanged, SessionID: "sess-a"}))
	require.NoError(t, broker.Publish(ctx, Event{Method: "notifications/resources/list_changed"}))

	gotA := readSSEUntil(t, a, "resources/list_changed", 5*time.Second)
	gotB := readSSEUntil(t, b, "resources/list_changed", 5*time.Second)
	assert.Contains(t, gotA, toolsListChanged)
	assert.NotContains(t, gotB, toolsListChanged, "another session's event")
	assert.NotContains(t, gotA, "session_id", "the address is not part of the notification")
}

// The rules announceToolsChanged applies: what a session holds, whether its
// stream is open, whether it is told, and what it holds after.
func TestAnnounceToolsChanged_Rules(t *testing.T) {
	for _, tc := range []struct {
		name      string
		build     string
		state     map[string]any
		streaming bool
		told      bool
		after     map[string]any
	}{
		{"same build", "v2", map[string]any{toolsBuildKey: "v2"}, false, false, map[string]any{toolsBuildKey: "v2"}},
		{"same build, stream opens", "v2", map[string]any{toolsBuildKey: "v2"}, true, false, map[string]any{toolsBuildKey: "v2"}},
		{
			"another build, from a request", "v2",
			map[string]any{toolsBuildKey: "v1"},
			false, true,
			map[string]any{toolsBuildKey: "v2", toolsPendingKey: true},
		},
		{
			"another build, stream opens", "v2",
			map[string]any{toolsBuildKey: "v1"},
			true, true,
			map[string]any{toolsBuildKey: "v2", toolsPendingKey: false},
		},
		{"no build recorded", "v2", map[string]any{}, false, true, map[string]any{toolsBuildKey: "v2", toolsPendingKey: true}},
		{
			"pending, a request", "v2",
			map[string]any{toolsBuildKey: "v2", toolsPendingKey: true},
			false, false,
			map[string]any{toolsBuildKey: "v2", toolsPendingKey: true},
		},
		{
			"pending, stream opens", "v2",
			map[string]any{toolsBuildKey: "v2", toolsPendingKey: true},
			true, true,
			map[string]any{toolsBuildKey: "v2", toolsPendingKey: false},
		},
		{"no build running", "", map[string]any{toolsBuildKey: "v1"}, true, false, map[string]any{toolsBuildKey: "v1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewMemoryStore(handlerTestTTL)
			t.Cleanup(func() { _ = store.Close() })
			broker := newCountingBroadcaster(t)
			h := NewAwareHandler(http.NotFoundHandler(), HandlerConfig{Store: store, TTL: handlerTestTTL, Broadcaster: broker, Build: tc.build})
			sess := newTestSession("s", handlerTestTTL)
			sess.State = tc.state
			require.NoError(t, store.Create(ctx, sess))

			if tc.streaming {
				h.announceOnStream(ctx, "s")
			} else {
				h.announceToolsChanged(ctx, sess, false)
			}
			assert.Equal(t, map[bool]int{true: 1, false: 0}[tc.told], broker.count("s"))
			got, err := store.Get(ctx, "s")
			require.NoError(t, err)
			assert.Equal(t, tc.after, got.State)
		})
	}
}

// A new session is stamped with the running build, and with nothing when no
// build is named; a session revived after its row expired is told.
func TestHandler_ToolsChanged_NewAndRevivedSessions(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore(handlerTestTTL)
	t.Cleanup(func() { _ = store.Close() })
	broker := newCountingBroadcaster(t)
	h := NewAwareHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		HandlerConfig{Store: store, TTL: handlerTestTTL, Broadcaster: broker, Build: "v2"})
	assert.Equal(t, map[string]any{toolsBuildKey: "v2"}, h.newState())
	assert.Empty(t, (&AwareHandler{}).newState())

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, handlerTestPath, strings.NewReader("{}"))
	req.Header.Set(sessionIDHeader, "gone")
	req.Header.Set("Authorization", "Bearer token")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, 1, broker.count("gone"), "a revived session may hold a list from any build")

	// Neither a missing session nor a store that cannot record stops a request.
	h.announceToolsChanged(ctx, nil, false)
	h.announceOnStream(ctx, "missing")
	assert.Zero(t, broker.count("missing"))
}
