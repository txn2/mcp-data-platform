package datahub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func newMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

// A datahub_* tool call, through the real mcp-datahub toolkit on a real MCP
// server, records one datahub_requests_total observation under what the tool
// does and a datahub.<operation> span under the tool_call span. The stub
// DataHub refuses every request, so the call is an upstream error.
func TestTelemetry_ToolCallIsRecorded(t *testing.T) {
	dh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A refused credential: the client does not retry it, so the test
		// waits on no backoff.
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(dh.Close)
	tk, err := New("primary", Config{URL: dh.URL, Token: "test-token"})
	require.NoError(t, err)
	m := newMetrics(t)
	tk.SetMetrics(m)

	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})
	server := mcp.NewServer(&mcp.Implementation{Name: "dh-test", Version: "v0"}, nil)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx, span := tr.Start(ctx, "tool_call")
			defer span.End()
			return next(ctx, method, req)
		}
	})
	tk.RegisterTools(server)
	st, ct := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      string(dhtools.ToolGetLineage),
		Arguments: map[string]any{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)

	assert.Contains(t, scrape(t, m), `datahub_requests_total{operation="get_lineage",status="upstream_err"} 1`)
	var found bool
	for _, s := range sr.Ended() {
		if s.Name() != "datahub.get_lineage" {
			continue
		}
		found = true
		for _, p := range sr.Ended() {
			if p.SpanContext().SpanID() == s.Parent().SpanID() {
				assert.Equal(t, "tool_call", p.Name())
			}
		}
	}
	assert.True(t, found, "no datahub.get_lineage span")

	// The reads made outside a tool record the same way.
	_, err = tk.GetEntity(context.Background(), "urn:li:dataset:x")
	require.Error(t, err)
	_, err = tk.GetGlossaryTerm(context.Background(), "urn:li:glossaryTerm:x")
	require.Error(t, err)
	body := scrape(t, m)
	assert.Contains(t, body, `datahub_requests_total{operation="get_entity",status="upstream_err"} 1`)
	assert.Contains(t, body, `datahub_requests_total{operation="get_glossary_term",status="upstream_err"} 1`)
}

// A tool call the toolkit answered is ok; the operation is the tool's name
// without its prefix.
func TestToolObserver_OKCall(t *testing.T) {
	m := newMetrics(t)
	tm := &telemetry{}
	tm.metrics.Store(m)
	o := toolObserver{tm: tm}
	tc := &dhtools.ToolContext{ToolName: dhtools.ToolBrowse}
	ctx, err := o.Before(context.Background(), tc)
	require.NoError(t, err)
	_, err = o.After(ctx, tc, &mcp.CallToolResult{}, nil)
	require.NoError(t, err)
	// After on a context Before never saw records nothing and passes through.
	res := &mcp.CallToolResult{}
	got, err := o.After(context.Background(), tc, res, nil)
	require.NoError(t, err)
	assert.Same(t, res, got)

	body := scrape(t, m)
	assert.Contains(t, body, `datahub_requests_total{operation="browse",status="ok"} 1`)
	assert.Equal(t, 1, strings.Count(body, `datahub_requests_total{`))
}
