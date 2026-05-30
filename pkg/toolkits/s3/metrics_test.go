package s3

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func scrapeForTest(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// TestMetricsMiddleware_RecordsOperation drives the real mcp-s3 metrics
// middleware's After hook for a success and a failure and asserts both
// s3_operations series increment with the right operation and status labels.
func TestMetricsMiddleware_RecordsOperation(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	mw := newMetricsMiddleware(m)
	ctx := context.Background()

	_, _ = mw.After(ctx, &s3tools.ToolContext{ToolName: "get_object", StartTime: time.Now()}, &mcp.CallToolResult{}, nil)
	_, _ = mw.After(ctx, &s3tools.ToolContext{ToolName: "list_objects", StartTime: time.Now()}, nil, errors.New("access denied"))
	_, _ = mw.After(ctx, &s3tools.ToolContext{ToolName: "get_object_metadata", StartTime: time.Now()}, &mcp.CallToolResult{IsError: true}, nil)

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		"s3_operations_total",
		`operation="get_object"`,
		`status="ok"`,
		`operation="list_objects"`,
		`status="upstream_err"`,
		"s3_operation_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// newTestToolkit builds a real S3 toolkit with static credentials (no network
// at construction). createToolkit derefs the client, so a real one is required.
func newTestToolkit(t *testing.T) *Toolkit {
	t.Helper()
	tk, err := New("test", Config{
		Region:          "us-east-1",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "test",
		SecretAccessKey: "test",
		ConnectionName:  "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tk
}

// TestSetMetrics_InstallsMiddleware confirms SetMetrics stores the recorder and
// rebuilds the underlying toolkit (so the middleware is present at registration).
func TestSetMetrics_InstallsMiddleware(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	tk := newTestToolkit(t)
	before := tk.s3Toolkit
	tk.SetMetrics(m)
	if tk.metrics != m {
		t.Error("SetMetrics did not store the recorder")
	}
	if tk.s3Toolkit == before {
		t.Error("SetMetrics did not rebuild the underlying toolkit with the middleware")
	}
}

// TestSetMetrics_DisabledS3 confirms a disabled recorder is a no-op.
func TestSetMetrics_DisabledS3(t *testing.T) {
	tk := newTestToolkit(t)
	tk.SetMetrics(nil)
	if tk.metrics != nil {
		t.Error("SetMetrics(nil) must not store a recorder")
	}
}
