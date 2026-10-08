package opsobs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestEndTool_CountsTheOutcome(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := Metrics()
	SetMetrics(m)
	t.Cleanup(func() { SetMetrics(prev) })
	if Metrics() != m {
		t.Fatal("SetMetrics did not install the recorder")
	}

	ctx := context.Background()
	for _, tc := range []struct {
		res *mcp.CallToolResult
		err error
	}{
		{&mcp.CallToolResult{}, nil},
		{&mcp.CallToolResult{IsError: true}, nil},
		{nil, errors.New("handler failed")},
	} {
		_, op := Start(ctx, OpMemoryManage)
		EndTool(ctx, op, tc.res, tc.err)
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`domain_operations_total{operation="memory.manage",result="ok"} 1`,
		`domain_operations_total{operation="memory.manage",result="error"} 2`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}
