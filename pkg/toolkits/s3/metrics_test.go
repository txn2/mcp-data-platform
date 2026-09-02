package s3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	ctx := context.Background()
	tk.observe(ctx, "s3_object.get", time.Now(), nil)
	tk.observe(ctx, "s3_list.objects", time.Now(), &mcp.CallToolResult{IsError: true})

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
	tk.observe(context.Background(), "s3_object.put", time.Now(), nil)
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
