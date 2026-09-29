package httpserver

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

// The root handler Serve mounts carries a list-changed event published on the
// platform's broadcaster to the listen stream of a real client on 2026-07-28
// (#1967).
func TestBuildRootHandler_AListeningClientIsToldThroughTheBroadcaster(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test", Streamable: platform.StreamableConfig{Stateless: true, SessionTimeout: testSessionTimeout}},
		Sessions: platform.SessionsConfig{TTL: testSessionTimeout},
	})
	defer func() { _ = p.Close() }()
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Prompts: &mcp.PromptCapabilities{ListChanged: true}},
	})
	acked := make(chan struct{}, 1)
	ctx := t.Context()
	srv := httptest.NewServer(buildRootHandler(ctx, mcpServer, p, extractHTTPConfig(p)))
	defer srv.Close()
	// Added after the bridge, so it runs outside it and sees the listen recorded.
	mcpServer.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method == "notifications/subscriptions/acknowledged" {
				acked <- struct{}{}
			}
			return res, err
		}
	})

	told := make(chan struct{}, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, &mcp.ClientOptions{
		PromptListChangedHandler: func(context.Context, *mcp.PromptListChangedRequest) { told <- struct{}{} },
	})
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = cs.Close() }()
	select {
	case <-acked:
	case <-time.After(10 * time.Second):
		t.Fatal("the listen was never acknowledged")
	}

	if err := p.Broadcaster().Publish(ctx, session.Event{Method: "notifications/prompts/list_changed"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-told:
	case <-time.After(10 * time.Second):
		t.Fatal("the client was not told the prompt list changed")
	}
}
