package httpserver

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// countingTripper counts the HTTP requests a client sends.
type countingTripper struct {
	base http.RoundTripper
	n    atomic.Int32
}

func (c *countingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.base.RoundTrip(req) //nolint:wrapcheck // test transport
}

// TestStreamableHTTP_AnAuthAttemptIsCountedOncePerRequest holds #1898's
// counting rule end to end: the HTTP gate validates every request and the
// tool-call middleware validates every tool call again, and
// auth_attempts_total counts each request once. Before the gate marked what
// it counted, a tool call read as two successful attempts against one refused
// one, so the ratio on a dashboard was wrong.
func TestStreamableHTTP_AnAuthAttemptIsCountedOncePerRequest(t *testing.T) {
	ctx := context.Background()
	var (
		mu       sync.Mutex
		outcomes []string
	)
	chain := auth.NewChainedAuthenticator(auth.ChainedAuthConfig{
		Observe: func(_ context.Context, method, result, _ string) {
			mu.Lock()
			defer mu.Unlock()
			outcomes = append(outcomes, method+"/"+result)
		},
	}, auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{Keys: []auth.APIKey{
		{Key: "key-1898", Name: "analyst", Roles: []string{"dp_admin"}},
	}}))
	httpServer, _ := outageServer(t, chain)

	requests := &countingTripper{base: &authRoundTripper{token: "key-1898", base: http.DefaultTransport}}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL, HTTPClient: &http.Client{Transport: requests}}, nil)
	if err != nil {
		t.Fatalf(fmtConnectFailed, err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"Message": "hi"}}); err != nil {
		t.Fatalf(fmtCallToolFailed, err)
	}
	_ = session.Close()
	sent := int(requests.n.Load())

	mu.Lock()
	defer mu.Unlock()
	if len(outcomes) != sent {
		t.Errorf("%d attempts counted for %d requests: the tool call's re-validation was counted again (%v)", len(outcomes), sent, outcomes)
	}
	if len(outcomes) == 0 {
		t.Fatal("no attempt was counted at all")
	}
	for _, o := range outcomes {
		if o != "api_key/success" {
			t.Errorf("an attempt was counted as %q, want api_key/success", o)
		}
	}
}
