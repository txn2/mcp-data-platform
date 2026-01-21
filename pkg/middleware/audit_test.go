package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockAuditLogger implements AuditLogger for testing.
type mockAuditLogger struct {
	mu     sync.Mutex
	events []AuditEvent
	logErr error
}

func (m *mockAuditLogger) Log(_ context.Context, event AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return m.logErr
}

func (m *mockAuditLogger) getEvents() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent{}, m.events...)
}

func TestAuditMiddleware(t *testing.T) {
	t.Run("no platform context", func(t *testing.T) {
		logger := &mockAuditLogger{}
		middleware := AuditMiddleware(logger)
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultText("success"), nil
		})

		result, err := handler(context.Background(), mcp.CallToolRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected success result")
		}
	})

	t.Run("logs successful call", func(t *testing.T) {
		logger := &mockAuditLogger{}
		middleware := AuditMiddleware(logger)
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultText("success"), nil
		})

		pc := &PlatformContext{
			RequestID:   "req-123",
			UserID:      "user123",
			UserEmail:   "user@test.com",
			PersonaName: "analyst",
			ToolName:    "test_tool",
			ToolkitKind: "test",
			ToolkitName: "test-toolkit",
			Connection:  "default",
		}
		ctx := WithPlatformContext(context.Background(), pc)

		args, _ := json.Marshal(map[string]any{"query": "SELECT 1"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Arguments: args,
			},
		}

		result, err := handler(ctx, request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected success result")
		}

		// Wait for async logging
		time.Sleep(100 * time.Millisecond)

		events := logger.getEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}

		event := events[0]
		if event.RequestID != "req-123" {
			t.Errorf("expected RequestID 'req-123', got %q", event.RequestID)
		}
		if event.UserID != "user123" {
			t.Errorf("expected UserID 'user123', got %q", event.UserID)
		}
		if event.ToolName != "test_tool" {
			t.Errorf("expected ToolName 'test_tool', got %q", event.ToolName)
		}
		if !event.Success {
			t.Error("expected Success to be true")
		}
		if event.DurationMS < 0 {
			t.Error("expected positive duration")
		}
		if event.Parameters["query"] != "SELECT 1" {
			t.Errorf("expected parameter 'query'='SELECT 1', got %v", event.Parameters)
		}
	})

	t.Run("logs error from handler", func(t *testing.T) {
		logger := &mockAuditLogger{}
		middleware := AuditMiddleware(logger)
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New("handler error")
		})

		pc := &PlatformContext{ToolName: "test_tool"}
		ctx := WithPlatformContext(context.Background(), pc)

		_, _ = handler(ctx, mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})

		time.Sleep(100 * time.Millisecond)

		events := logger.getEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}

		event := events[0]
		if event.Success {
			t.Error("expected Success to be false")
		}
		if event.ErrorMessage != "handler error" {
			t.Errorf("expected ErrorMessage 'handler error', got %q", event.ErrorMessage)
		}
	})

	t.Run("logs error result", func(t *testing.T) {
		logger := &mockAuditLogger{}
		middleware := AuditMiddleware(logger)
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultError("tool error"), nil
		})

		pc := &PlatformContext{ToolName: "test_tool"}
		ctx := WithPlatformContext(context.Background(), pc)

		_, _ = handler(ctx, mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})

		time.Sleep(100 * time.Millisecond)

		events := logger.getEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}

		event := events[0]
		if event.Success {
			t.Error("expected Success to be false")
		}
		if event.ErrorMessage != "tool error" {
			t.Errorf("expected ErrorMessage 'tool error', got %q", event.ErrorMessage)
		}
	})
}

func TestExtractParameters(t *testing.T) {
	t.Run("empty arguments", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}
		params := extractParameters(request)
		if params != nil {
			t.Errorf("expected nil, got %v", params)
		}
	})

	t.Run("valid JSON arguments", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"query": "SELECT 1",
			"limit": 10,
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Arguments: args,
			},
		}
		params := extractParameters(request)
		if params == nil {
			t.Fatal("expected non-nil params")
		}
		if params["query"] != "SELECT 1" {
			t.Errorf("expected query 'SELECT 1', got %v", params["query"])
		}
	})

	t.Run("invalid JSON arguments", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Arguments: []byte("invalid json"),
			},
		}
		params := extractParameters(request)
		if params != nil {
			t.Errorf("expected nil for invalid JSON, got %v", params)
		}
	})
}

func TestExtractErrorMessage(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		msg := extractErrorMessage(nil)
		if msg != "" {
			t.Errorf("expected empty string, got %q", msg)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		result := &mcp.CallToolResult{}
		msg := extractErrorMessage(result)
		if msg != "" {
			t.Errorf("expected empty string, got %q", msg)
		}
	})

	t.Run("text content", func(t *testing.T) {
		result := NewToolResultError("test error message")
		msg := extractErrorMessage(result)
		if msg != "test error message" {
			t.Errorf("expected 'test error message', got %q", msg)
		}
	})

	t.Run("non-text content", func(t *testing.T) {
		// Create a result with non-TextContent (e.g., ImageContent)
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{Data: []byte("base64data")},
			},
		}
		msg := extractErrorMessage(result)
		if msg != "" {
			t.Errorf("expected empty string for non-text content, got %q", msg)
		}
	})
}

func TestNoopAuditLogger(t *testing.T) {
	logger := &NoopAuditLogger{}
	err := logger.Log(context.Background(), AuditEvent{
		ToolName: "test_tool",
		UserID:   "user123",
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAuditEvent(t *testing.T) {
	now := time.Now()
	event := AuditEvent{
		Timestamp:    now,
		RequestID:    "req-123",
		UserID:       "user123",
		UserEmail:    "user@test.com",
		Persona:      "analyst",
		ToolName:     "trino_query",
		ToolkitKind:  "trino",
		ToolkitName:  "main-trino",
		Connection:   "prod",
		Parameters:   map[string]any{"query": "SELECT 1"},
		Success:      true,
		ErrorMessage: "",
		DurationMS:   100,
	}

	// Verify JSON marshaling
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var unmarshaled AuditEvent
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if unmarshaled.ToolName != "trino_query" {
		t.Errorf("expected ToolName 'trino_query', got %q", unmarshaled.ToolName)
	}
	if unmarshaled.DurationMS != 100 {
		t.Errorf("expected DurationMS 100, got %d", unmarshaled.DurationMS)
	}
}

// Verify interface compliance.
var _ AuditLogger = (*NoopAuditLogger)(nil)
var _ AuditLogger = (*mockAuditLogger)(nil)
