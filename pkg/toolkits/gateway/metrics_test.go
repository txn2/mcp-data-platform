package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func scrapeMetrics(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

// TestForwardedCallsAreCounted drives the gateway's forwarder against an
// in-process upstream: a successful call, a tool that answers with an error
// result, and the upstream's HTTP requests themselves, each under the
// connection name (#1895).
func TestForwardedCallsAreCounted(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	url := upstreamServer(t)
	tk := New("primary")
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetMetrics(m)
	require.NoError(t, tk.AddConnection(connCRM, connectionConfig(url, connCRM)))

	srv := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "0"}, nil)
	tk.RegisterTools(srv)
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := context.Background()
	_, err = srv.Connect(ctx, t1, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: localCRMEcho, Arguments: map[string]any{"message": "hi"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: localCRMBoom, Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, res.IsError)

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`gateway_upstream_calls_total{connection="crm",outcome="ok"} 1`,
		`gateway_upstream_calls_total{connection="crm",outcome="tool_error"} 1`,
		`gateway_upstream_call_duration_seconds_count{connection="crm"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

func TestCallOutcome(t *testing.T) {
	require.Equal(t, observability.GatewayOutcomeOK, callOutcome(&mcp.CallToolResult{}, nil))
	require.Equal(t, observability.GatewayOutcomeToolError, callOutcome(&mcp.CallToolResult{IsError: true}, nil))
	require.Equal(t, observability.GatewayOutcomeTransportError, callOutcome(nil, errors.New("connection reset")))
	require.Equal(t, observability.GatewayOutcomeTimeout, callOutcome(nil, context.DeadlineExceeded))
	require.Equal(t, observability.GatewayOutcomeTimeout, callOutcome(nil, errors.Join(errors.New("call"), context.DeadlineExceeded)))
}
