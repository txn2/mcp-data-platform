package listenbridge

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// These tests assemble what internal/httpserver serves: a stateless streamable
// handler behind session.AwareHandler, the Bridge's sending middleware on the
// server and its delivery subscribed to the broadcaster, and real go-sdk
// clients. Events are published on the memory broadcaster the way a peer
// replica's arrive through the postgres one.

const (
	clientTestTTL = time.Hour
	await         = 10 * time.Second
	build         = "v2"
)

// watchingBroadcaster signals every subscription a session's GET stream makes.
type watchingBroadcaster struct {
	*session.MemoryBroadcaster
	streams chan string
}

func (w *watchingBroadcaster) Subscribe(ctx context.Context, sessionID string) session.Subscription {
	sub := w.MemoryBroadcaster.Subscribe(ctx, sessionID)
	if sessionID != subscriberName {
		w.streams <- sessionID
	}
	return sub
}

// stack is one replica: its server, session store, broadcaster and Bridge.
type stack struct {
	server *mcp.Server
	store  *session.MemoryStore
	broker *watchingBroadcaster
	url    string
	// acked receives once for every listen the Bridge has recorded.
	acked chan struct{}
}

func newStack(t *testing.T) *stack {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "listenbridge", Version: "0.0.1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{ListChanged: true},
			Prompts:   &mcp.PromptCapabilities{ListChanged: true},
			Resources: &mcp.ResourceCapabilities{ListChanged: true},
		},
	})
	server.AddTool(&mcp.Tool{Name: "noop", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})

	store := session.NewMemoryStore(clientTestTTL)
	t.Cleanup(func() { _ = store.Close() })
	broker := &watchingBroadcaster{MemoryBroadcaster: session.NewMemoryBroadcaster(nil), streams: make(chan string, 16)}
	t.Cleanup(func() { _ = broker.Close() })

	stateless := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})
	aware := session.NewAwareHandler(stateless, session.HandlerConfig{Store: store, TTL: clientTestTTL, Broadcaster: broker, Build: build})

	s := &stack{server: server, store: store, broker: broker, acked: make(chan struct{}, 16)}
	bridge := New(broker, aware, nil)
	server.AddSendingMiddleware(bridge.Middleware)
	// Outermost, so it sees an acknowledgment after the Bridge recorded it.
	server.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method == methodAcknowledged {
				s.acked <- struct{}{}
			}
			return res, err
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bridge.Start(ctx)

	srv := httptest.NewServer(aware)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// listener is a client counting the list-changed notifications it receives.
type listener struct {
	cs                        *mcp.ClientSession
	tools, prompts, resources chan any
}

// connect opens a client on version. header, when set, is sent on every
// request, which is how a client resumes a session it already holds.
func (s *stack) connect(t *testing.T, version string, header http.Header) *listener {
	t.Helper()
	l := &listener{tools: make(chan any, 16), prompts: make(chan any, 16), resources: make(chan any, 16)}
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, r *mcp.ToolListChangedRequest) {
			l.tools <- r.Params.GetMeta()[mcp.MetaKeySubscriptionID]
		},
		PromptListChangedHandler: func(_ context.Context, r *mcp.PromptListChangedRequest) {
			l.prompts <- r.Params.GetMeta()[mcp.MetaKeySubscriptionID]
		},
		ResourceListChangedHandler: func(_ context.Context, r *mcp.ResourceListChangedRequest) {
			l.resources <- r.Params.GetMeta()[mcp.MetaKeySubscriptionID]
		},
	})
	transport := &mcp.StreamableClientTransport{Endpoint: s.url}
	if header != nil {
		transport.HTTPClient = &http.Client{Transport: headerTransport{header: header}}
	}
	cs, err := client.Connect(context.Background(), transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	l.cs = cs
	return l
}

type headerTransport struct{ header http.Header }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	maps.Copy(r.Header, h.header)
	return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // test transport
}

func (s *stack) awaitAck(t *testing.T) {
	t.Helper()
	select {
	case <-s.acked:
	case <-time.After(await):
		t.Fatal("the listen was never acknowledged")
	}
}

func (s *stack) awaitStream(t *testing.T, id string) {
	t.Helper()
	select {
	case got := <-s.broker.streams:
		require.Equal(t, id, got)
	case <-time.After(await):
		t.Fatal("the client never opened its stream")
	}
}

func (s *stack) publish(t *testing.T, ev session.Event) {
	t.Helper()
	require.NoError(t, s.broker.Publish(context.Background(), ev))
}

// receive waits for one notification on ch and returns its subscription id.
func receive(t *testing.T, ch chan any, what string) any {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(await):
		t.Fatalf("the client was not told %s", what)
		return nil
	}
}

// A client on 2026-07-28 is told of a prompt and a resource change another
// replica published, and of a tool change on its own replica's server, once
// each, on its listen stream and stamped with its subscription id.
func TestBridge_AListeningClientIsToldOfEachChangeOnce(t *testing.T) {
	s := newStack(t)
	l := s.connect(t, "2026-07-28", nil)
	require.Equal(t, "2026-07-28", l.cs.InitializeResult().ProtocolVersion)
	s.awaitAck(t)

	s.publish(t, session.Event{Method: methodPromptsChanged})
	assert.NotNil(t, receive(t, l.prompts, "a prompt was saved"), "stamped with the subscription id")
	s.publish(t, session.Event{Method: methodResourcesChanged})
	assert.NotNil(t, receive(t, l.resources, "a resource was created"))

	// A gateway change: this replica's server changes its tools, which the
	// SDK tells the listen of, and each replica that applied the change
	// publishes it on the broadcaster too.
	s.server.AddTool(&mcp.Tool{Name: "gateway__added", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	receive(t, l.tools, "its tools changed")
	s.publish(t, session.Event{Method: methodToolsChanged})
	s.publish(t, session.Event{Method: methodToolsChanged})

	// Anything written before this sentinel has arrived once it does.
	s.publish(t, session.Event{Method: methodPromptsChanged})
	receive(t, l.prompts, "the sentinel")
	assert.Empty(t, l.tools, "the broadcaster's copies of the tool change are not written")
	assert.Empty(t, l.resources)

	// The client kept the Mcp-Session-Id it was given on server/discover, and
	// its listen is known by it: a notice addressed to the session reaches it,
	// and one addressed to another session does not.
	require.NotEmpty(t, l.cs.ID())
	s.publish(t, session.Event{Method: methodToolsChanged, SessionID: "another"})
	s.publish(t, session.Event{Method: methodToolsChanged, SessionID: l.cs.ID()})
	receive(t, l.tools, "the notice addressed to its session")
	s.publish(t, session.Event{Method: methodPromptsChanged})
	receive(t, l.prompts, "the sentinel")
	assert.Empty(t, l.tools)
}

// A client on 2025-11-25 is told on its GET stream, once, and the Bridge
// records no listen for it.
func TestBridge_AClientWithASessionIsToldAsBefore(t *testing.T) {
	s := newStack(t)
	l := s.connect(t, "2025-11-25", nil)
	s.awaitStream(t, l.cs.ID())

	s.publish(t, session.Event{Method: methodPromptsChanged})
	receive(t, l.prompts, "a prompt was saved")
	s.publish(t, session.Event{Method: methodToolsChanged})
	receive(t, l.tools, "its tools changed")

	s.publish(t, session.Event{Method: methodResourcesChanged})
	receive(t, l.resources, "the sentinel")
	assert.Empty(t, l.prompts, "told once")
	assert.Empty(t, l.tools, "told once")
	assert.Empty(t, s.acked, "no listen")
}

// A 2026-07-28 client that resumes a session which listed its tools under
// another build is told once on its listen (#1946), and the session records
// that it was.
func TestBridge_AResumedSessionOnAnotherBuildIsToldOnItsListen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	require.NoError(t, s.store.Create(ctx, &session.Session{
		ID: "resumed", CreatedAt: time.Now(), LastActiveAt: time.Now(), ExpiresAt: time.Now().Add(clientTestTTL),
		State: map[string]any{"tools_build": "v1"},
	}))
	l := s.connect(t, "2026-07-28", http.Header{"Mcp-Session-Id": {"resumed"}})
	s.awaitAck(t)
	receive(t, l.tools, "its tools changed")

	s.publish(t, session.Event{Method: methodPromptsChanged})
	receive(t, l.prompts, "the sentinel")
	assert.Empty(t, l.tools, "told once")

	sess, err := s.store.Get(ctx, "resumed")
	require.NoError(t, err)
	assert.Equal(t, build, sess.State["tools_build"])
	assert.Equal(t, false, sess.State["tools_pending"])
}
