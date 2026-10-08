package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/connstate"
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

// TestObserve_RecordsOperation drives the recorder for a success and a failure
// and asserts both s3_operations series increment with the operation the call
// performed and its status.
func TestObserve_RecordsOperation(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	tk := &Toolkit{metrics: m}
	ctx, call := begin(context.Background(), "s3_object.get", "")
	tk.observe(ctx, call, nil)
	ctx, call = begin(ctx, "s3_list.objects", "")
	tk.observe(ctx, call, &mcp.CallToolResult{IsError: true})

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		"s3_operations_total",
		`operation="s3_object.get"`,
		`status="ok"`,
		`operation="s3_list.objects"`,
		`status="upstream_err"`,
		"s3_operation_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestObserve_NilRecorder covers the tracing-only contract: the platform calls
// SetMetrics when metrics OR tracing is enabled, so a nil (disabled-metrics)
// recorder must be recorded to without panicking, leaving the span emission
// as the observation.
func TestObserve_NilRecorder(t *testing.T) {
	tk := &Toolkit{}
	tk.SetMetrics(nil)
	if tk.metrics != nil {
		t.Error("SetMetrics(nil) must not store a (non-nil) recorder")
	}
	ctx, call := begin(context.Background(), "s3_object.put", "")
	tk.observe(ctx, call, nil)
}

// TestSetMetrics_StoresRecorder confirms the recorder the handlers report to
// is the one the platform wired.
func TestSetMetrics_StoresRecorder(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	tk := &Toolkit{}
	tk.SetMetrics(m)
	if tk.metrics != m {
		t.Error("SetMetrics did not store the recorder")
	}
}

// Each call leaves its connection in the state its result reads as (#1898): a
// refused key is auth_failed, a lost network unreachable, any answer healthy.
// A call that names no connection is the default one's.
func TestObserve_RecordsTheConnectionsState(t *testing.T) {
	tk := &Toolkit{name: "lake"}
	failed := func(text string) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	}
	for _, tc := range []struct {
		conn   string
		result *mcp.CallToolResult
		want   string
	}{
		{"", nil, connstate.Healthy},
		{"archive", failed("failed to list buckets: api error InvalidAccessKeyId: The AWS Access Key Id does not exist"), connstate.AuthFailed},
		{"archive", failed("failed to list objects: dial tcp 10.1.1.1:9000: connect: connection refused"), connstate.Unreachable},
		{"archive", failed("failed to get object: NoSuchKey"), connstate.Healthy},
	} {
		ctx, call := begin(context.Background(), "s3_list.buckets", tc.conn)
		tk.observe(ctx, call, tc.result)
		name := tc.conn
		if name == "" {
			name = "lake"
		}
		if got, _ := connstate.State(kindS3, name); got != tc.want {
			t.Errorf("%s after %v: %q, want %q", name, tc.result, got, tc.want)
		}
	}
}
