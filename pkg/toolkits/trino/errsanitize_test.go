package trino

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

const observedUpstreamError = `Query failed: elasticsearch request failed: ` +
	`method [GET], host [https://es-internal.data.svc.cluster.local:9200], URI [/orders/_search], ` +
	`status line [HTTP/1.1 400 Bad Request]
{"error":{"root_cause":[{"type":"parsing_exception","reason":"unknown field [aggz]","line":1,"col":10}]},"status":400}`

func TestSanitizeUpstreamError(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantGone    []string
		wantPresent []string
	}{
		{
			name:  "strips transport envelope, keeps engine payload",
			input: observedUpstreamError,
			wantGone: []string{
				"es-internal.data.svc.cluster.local",
				"9200",
				"method [GET]",
				"URI [/orders/_search]",
			},
			wantPresent: []string{
				"parsing_exception",
				"unknown field [aggz]",
				`"line":1`,
				"status line [HTTP/1.1 400 Bad Request]",
			},
		},
		{
			name:        "plain trino error untouched",
			input:       "Query failed: line 3:14: Column 'foo' cannot be resolved",
			wantPresent: []string{"line 3:14", "Column 'foo' cannot be resolved"},
		},
		{
			name: "brackets inside host and URI values are fully stripped",
			input: `failed: method [GET], host [https://[::1]:9200], URI [/logs-[2024]/_search], ` +
				`status line [HTTP/1.1 400 Bad Request]`,
			wantGone: []string{
				"[::1]", "9200", "/logs-[2024]/_search", "method [GET]",
			},
			wantPresent: []string{"status line [HTTP/1.1 400 Bad Request]"},
		},
		{
			name:     "trailing envelope segment without status line is stripped",
			input:    "failed: host [https://internal-service:9200]",
			wantGone: []string{"internal-service", "9200"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUpstreamError(tt.input)
			for _, s := range tt.wantGone {
				if strings.Contains(got, s) {
					t.Errorf("expected %q to be stripped, output: %s", s, got)
				}
			}
			for _, s := range tt.wantPresent {
				if !strings.Contains(got, s) {
					t.Errorf("expected %q to be kept, output: %s", s, got)
				}
			}
		})
	}
}

func TestErrorSanitizerMiddleware_After(t *testing.T) {
	m := &ErrorSanitizerMiddleware{}
	tc := trinotools.NewToolContext(trinotools.ToolQuery, nil)

	t.Run("sanitizes handler error", func(t *testing.T) {
		_, err := m.After(context.Background(), tc, nil, errors.New(observedUpstreamError))
		if err == nil {
			t.Fatal("expected error to be preserved")
		}
		if strings.Contains(err.Error(), "es-internal.data.svc.cluster.local") {
			t.Errorf("internal host leaked: %s", err.Error())
		}
		if !strings.Contains(err.Error(), "parsing_exception") {
			t.Errorf("engine payload lost: %s", err.Error())
		}
	})

	t.Run("clean handler error passes through unchanged", func(t *testing.T) {
		orig := errors.New("Query failed: table not found")
		_, err := m.After(context.Background(), tc, nil, orig)
		if !errors.Is(err, orig) {
			t.Errorf("expected original error instance, got %v", err)
		}
	})

	t.Run("sanitizes error result text", func(t *testing.T) {
		result := &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: observedUpstreamError}},
		}
		got, err := m.After(context.Background(), tc, result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		textContent, ok := got.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("content[0] is not TextContent: %T", got.Content[0])
		}
		if strings.Contains(textContent.Text, "es-internal.data.svc.cluster.local") {
			t.Errorf("internal host leaked: %s", textContent.Text)
		}
		if !strings.Contains(textContent.Text, "parsing_exception") {
			t.Errorf("engine payload lost: %s", textContent.Text)
		}
	})

	t.Run("non-error result untouched", func(t *testing.T) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "host [not-an-error-result]"}},
		}
		got, err := m.After(context.Background(), tc, result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		textContent, ok := got.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("content[0] is not TextContent: %T", got.Content[0])
		}
		if textContent.Text != "host [not-an-error-result]" {
			t.Error("successful result content must not be rewritten")
		}
	})

	t.Run("before passes through", func(t *testing.T) {
		ctx := context.Background()
		got, err := m.Before(ctx, tc)
		if err != nil || got != ctx {
			t.Errorf("expected pass-through, got ctx=%v err=%v", got, err)
		}
	})
}
