package dhobs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// A call is one observation under its operation, ok or upstream_err, and a
// datahub.<operation> span under the caller's span; Do returns what the call
// returned.
func TestDoAndCall(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	prev := outbound.Metrics()
	outbound.SetDefaultMetrics(m)
	t.Cleanup(func() { outbound.SetDefaultMetrics(prev); _ = m.Shutdown(context.Background()) })
	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})
	ctx, root := tr.Start(context.Background(), "tool_call")

	got, err := Do(ctx, "create_tag", func(context.Context) (string, error) { return "urn:li:tag:x", nil })
	require.NoError(t, err)
	assert.Equal(t, "urn:li:tag:x", got)
	boom := errors.New("refused")
	require.ErrorIs(t, Call(ctx, "delete_tag", func(context.Context) error { return boom }), boom)
	root.End()

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `datahub_requests_total{operation="create_tag",status="ok"} 1`)
	assert.Contains(t, body, `datahub_requests_total{operation="delete_tag",status="upstream_err"} 1`)

	names := map[string]bool{}
	for _, s := range sr.Ended() {
		if s.Name() != "tool_call" {
			names[s.Name()] = true
			assert.Equal(t, root.SpanContext().SpanID(), s.Parent().SpanID())
		}
	}
	assert.Equal(t, map[string]bool{"datahub.create_tag": true, "datahub.delete_tag": true}, names)
}
