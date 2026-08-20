package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyRevision is a protocol revision predating 2026-07-28. An upstream that
// negotiates it makes the go-sdk client omit the per-message
// _meta.protocolVersion, which is what lets the request context's revision
// reach the outbound Mcp-Protocol-Version header (#1387).
const legacyRevision = "2025-06-18"

// upstreamHeaderLog records the Mcp-Protocol-Version header the upstream saw,
// keyed by JSON-RPC method.
type upstreamHeaderLog struct {
	mu   sync.Mutex
	seen map[string]string
}

func (l *upstreamHeaderLog) record(method, version string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]string{}
	}
	l.seen[method] = version
}

func (l *upstreamHeaderLog) get(method string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seen[method]
}

// legacyUpstreamServer stands up an MCP upstream that negotiates
// legacyRevision, exposes a single echo tool, records the
// Mcp-Protocol-Version header of every request it receives, and refuses any
// request carrying a revision it does not know -- the same HTTP 400 an SDK
// server on that revision returns (mcp/streamable.go). The go-sdk server
// always negotiates its own latest revision, so an upstream pinned to an older
// one has to be written by hand.
func legacyUpstreamServer(t *testing.T) (endpoint string, hdrs *upstreamHeaderLog) {
	t.Helper()
	hdrs = &upstreamHeaderLog{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		revision := r.Header.Get("Mcp-Protocol-Version")
		hdrs.record(req.Method, revision)
		if revision != "" && revision != legacyRevision {
			http.Error(w, "Bad Request: Unsupported protocol version", http.StatusBadRequest)
			return
		}

		if len(req.ID) == 0 {
			// A notification: no body, per the streamable HTTP transport.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result, rpcErr := legacyUpstreamResult(req.Method)
		if req.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "legacy-upstream-session")
		}
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			body["error"] = rpcErr
		} else {
			body["result"] = result
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	return ts.URL, hdrs
}

// legacyUpstreamResult returns the result for a method the legacy upstream
// implements, or a JSON-RPC method-not-found error for anything else. Notably
// server/discover is refused, which is what drives the go-sdk client onto the
// legacy initialize handshake and so onto legacyRevision.
func legacyUpstreamResult(method string) (result any, rpcErr map[string]any) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": legacyRevision,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "legacy-upstream", "version": "0.0.1"},
		}, nil
	case "tools/list":
		return map[string]any{"tools": []any{map[string]any{
			"name":        toolEcho,
			"description": "echo",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"message": map[string]any{"type": "string"}},
			},
		}}}, nil
	case "tools/call":
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo:ok"}}}, nil
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + method}
}

// platformOverHTTP serves the toolkit from a real stateless streamable HTTP
// endpoint and returns a client session connected to it. Both halves of #1387
// depend on this shape, which is what a database-backed deployment runs
// (Handle.StatelessForced makes pkg/platform set Server.Streamable.Stateless):
// the go-sdk serves protocol revision 2026-07-28 only from a stateless
// transport, and only a stateless transport builds each request's session on
// that request's context, so the caller's revision reaches the tool handler.
// The in-memory transports used elsewhere in this package cannot exercise it.
func platformOverHTTP(t *testing.T, tk *Toolkit) *mcp.ClientSession {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "0.0.1"}, nil)
	tk.RegisterTools(server)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err, "client Connect")
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestForwarder_SendsTheUpstreamsOwnProtocolRevision is the #1387 regression: a
// call arriving from a client on a newer protocol revision must reach the
// upstream stamped with the revision the platform negotiated WITH THAT
// UPSTREAM, not the caller's. The go-sdk client prefers the revision it finds
// in the request context over its own session's, so a forwarder that passes the
// inbound context through unchanged makes an older upstream refuse the call
// with HTTP 400.
func TestForwarder_SendsTheUpstreamsOwnProtocolRevision(t *testing.T) {
	upstreamURL, hdrs := legacyUpstreamServer(t)

	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	require.NoError(t, tk.AddConnection(connCRM, connectionConfig(upstreamURL, connCRM)))

	// Dialing already proves the upstream negotiated the legacy revision: the
	// dial-time tools/list carries it because that context never passed through
	// an inbound streamable server.
	require.Equal(t, legacyRevision, hdrs.get("tools/list"),
		"upstream should have negotiated %s at dial time", legacyRevision)

	cs := platformOverHTTP(t, tk)
	callerRevision := cs.InitializeResult().ProtocolVersion
	require.NotEqual(t, legacyRevision, callerRevision,
		"the caller and the upstream must differ for this test to mean anything")

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      localCRMEcho,
		Arguments: map[string]any{"message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "proxied call should succeed: %+v", res)
	assert.Equal(t, "echo:ok", firstText(t, res).Text)

	assert.Equal(t, legacyRevision, hdrs.get("tools/call"),
		"the upstream must see the revision it negotiated, not the caller's %s", callerRevision)
}

// TestUpstreamContext_KeepsCancellationAndDropsValues covers the two halves of
// the guard independently of the wire: the outbound context must still be
// canceled with the caller's, and must resolve no caller value.
func TestUpstreamContext_KeepsCancellationAndDropsValues(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "leak"))
	t.Cleanup(cancel)

	out := upstreamContext(parent)
	assert.Nil(t, out.Value(key{}), "caller values must not cross the proxy boundary")

	cancel()
	select {
	case <-out.Done():
	default:
		t.Fatal("outbound context should be canceled with the caller's")
	}
	assert.ErrorIs(t, out.Err(), context.Canceled)
}
