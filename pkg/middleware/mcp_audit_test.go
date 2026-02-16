package middleware

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for MCP audit tests.
const (
	testAuditEmail       = "user@example.com"
	testAuditMethodCall  = "tools/call"
	testAuditDurationMin = 50
	testAuditCharsHello  = 11
	testAuditCharsImage  = 19
	testAuditCharsResult = 16
	testAuditSourceMCP   = "mcp"
)

func TestMCPAuditMiddleware_NonToolsCallPassthrough(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	handlerCalled := false
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		handlerCalled = true
		return &mcp.ListResourcesResult{}, nil
	}

	wrapped := mw(mockHandler)

	result, err := wrapped(context.Background(), "resources/list", nil)

	require.NoError(t, err)
	assert.True(t, handlerCalled)
	assert.IsType(t, &mcp.ListResourcesResult{}, result)

	// No audit log for non-tools/call.
	time.Sleep(10 * time.Millisecond)
	assert.Empty(t, mockLogger.Events())
}

func TestMCPAuditMiddleware_LogsToolCall(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "success"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	// Create context with PlatformContext (as MCPToolCallMiddleware would set).
	pc := NewPlatformContext("req-123")
	pc.UserID = testAuditEmail
	pc.UserEmail = testAuditEmail
	pc.ToolName = testAuditToolName
	pc.ToolkitKind = testAuditToolkit
	pc.PersonaName = testAuditPersona
	ctx := WithPlatformContext(context.Background(), pc)

	req := createAuditTestRequest(t, testAuditToolName, map[string]any{
		"sql": "SELECT 1",
	})

	result, err := wrapped(ctx, testAuditMethodCall, req)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Wait for async logging.
	time.Sleep(50 * time.Millisecond)

	events := mockLogger.Events()
	require.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "req-123", event.RequestID)
	assert.Equal(t, testAuditEmail, event.UserID)
	assert.Equal(t, testAuditToolName, event.ToolName)
	assert.Equal(t, testAuditToolkit, event.ToolkitKind)
	assert.Equal(t, testAuditPersona, event.Persona)
	assert.True(t, event.Success)
	assert.Empty(t, event.ErrorMessage)
	assert.NotNil(t, event.Parameters)
	assert.Equal(t, "SELECT 1", event.Parameters["sql"])
	assert.Greater(t, event.DurationMS, int64(-1))
}

func TestMCPAuditMiddleware_LogsToolCallError(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, assert.AnError
	}

	wrapped := mw(mockHandler)

	pc := NewPlatformContext("req-456")
	pc.UserID = testAuditEmail
	pc.ToolName = testAuditToolName
	ctx := WithPlatformContext(context.Background(), pc)

	req := createAuditTestRequest(t, testAuditToolName, nil)

	result, err := wrapped(ctx, testAuditMethodCall, req)

	assert.Error(t, err)
	assert.Nil(t, result)

	// Wait for async logging.
	time.Sleep(50 * time.Millisecond)

	events := mockLogger.Events()
	require.Len(t, events, 1)

	event := events[0]
	assert.False(t, event.Success)
	assert.NotEmpty(t, event.ErrorMessage)
}

func TestMCPAuditMiddleware_LogsToolResultError(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "permission denied"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	pc := NewPlatformContext("req-789")
	pc.UserID = testAuditEmail
	pc.ToolName = testAuditToolName
	ctx := WithPlatformContext(context.Background(), pc)

	req := createAuditTestRequest(t, testAuditToolName, nil)

	_, err := wrapped(ctx, testAuditMethodCall, req)

	require.NoError(t, err) // No Go error, but result is an error.

	// Wait for async logging.
	time.Sleep(50 * time.Millisecond)

	events := mockLogger.Events()
	require.Len(t, events, 1)

	event := events[0]
	assert.False(t, event.Success)
	assert.Equal(t, "permission denied", event.ErrorMessage)
}

func TestMCPAuditMiddleware_NoPlatformContext(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "success"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	// No PlatformContext in context.
	req := createAuditTestRequest(t, testAuditToolName, nil)
	result, err := wrapped(context.Background(), testAuditMethodCall, req)

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Wait for async logging - should NOT log without platform context.
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, mockLogger.Events())
}

func TestMCPAuditMiddleware_DurationTracking(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		time.Sleep(50 * time.Millisecond)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "success"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	pc := NewPlatformContext("req-dur")
	pc.ToolName = "slow_tool"
	ctx := WithPlatformContext(context.Background(), pc)

	req := createAuditTestRequest(t, "slow_tool", nil)
	_, _ = wrapped(ctx, testAuditMethodCall, req)

	// Wait for async logging.
	time.Sleep(100 * time.Millisecond)

	events := mockLogger.Events()
	require.Len(t, events, 1)

	// Duration should be at least 50ms.
	assert.GreaterOrEqual(t, events[0].DurationMS, int64(testAuditDurationMin))
}

func TestExtractMCPParameters(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		result := extractMCPParameters(nil)
		assert.Nil(t, result)
	})

	t.Run("with arguments", func(t *testing.T) {
		req := createAuditTestRequest(t, "test", map[string]any{"key": "value", "num": float64(42)})
		result := extractMCPParameters(req)
		assert.Equal(t, map[string]any{"key": "value", "num": float64(42)}, result)
	})
}

func TestExtractMCPErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		result   *mcp.CallToolResult
		expected string
	}{
		{
			name:     "nil result",
			result:   nil,
			expected: "",
		},
		{
			name:     "empty content",
			result:   &mcp.CallToolResult{},
			expected: "",
		},
		{
			name: "with text content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "error message"}},
			},
			expected: "error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMCPErrorMessage(tt.result)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateResponseSize_SingleText(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello world"}},
	}
	chars, blocks := calculateResponseSize(result, nil)
	assert.Equal(t, testAuditCharsHello, chars)
	assert.Equal(t, 1, blocks)
}

func TestCalculateResponseSize_MultipleItems(t *testing.T) {
	// Build 1000 chars across multiple content items.
	text1 := make([]byte, 600)
	for i := range text1 {
		text1[i] = 'a'
	}
	text2 := make([]byte, 400)
	for i := range text2 {
		text2[i] = 'b'
	}

	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(text1)},
			&mcp.TextContent{Text: string(text2)},
		},
	}
	chars, blocks := calculateResponseSize(result, nil)
	assert.Equal(t, 1000, chars)
	assert.Equal(t, 2, blocks)
}

func TestCalculateResponseSize_ErrorResult(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
	}
	chars, blocks := calculateResponseSize(result, assert.AnError)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, blocks)
}

func TestCalculateResponseSize_NilResult(t *testing.T) {
	chars, blocks := calculateResponseSize(nil, nil)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, blocks)
}

func TestCalculateResponseSize_NonCallToolResult(t *testing.T) {
	chars, blocks := calculateResponseSize(&mcp.ListResourcesResult{}, nil)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, blocks)
}

func TestCalculateResponseSize_ImageContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "text"},
			&mcp.ImageContent{Data: []byte("base64imagedata")},
		},
	}
	chars, blocks := calculateResponseSize(result, nil)
	// "text" = 4, "base64imagedata" = 15, total = 19.
	assert.Equal(t, testAuditCharsImage, chars)
	assert.Equal(t, 2, blocks)
}

func TestCalculateResponseSize_EmptyContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{},
	}
	chars, blocks := calculateResponseSize(result, nil)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, blocks)
}

func TestCalculateRequestSize(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		assert.Equal(t, 0, calculateRequestSize(nil))
	})

	t.Run("with arguments", func(t *testing.T) {
		req := createAuditTestRequest(t, "test", map[string]any{"key": "value"})
		size := calculateRequestSize(req)
		assert.Greater(t, size, 0)
	})

	t.Run("nil arguments", func(t *testing.T) {
		req := createAuditTestRequest(t, "test", nil)
		size := calculateRequestSize(req)
		assert.Equal(t, 0, size)
	})

	t.Run("nil params", func(t *testing.T) {
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: nil}
		assert.Equal(t, 0, calculateRequestSize(req))
	})

	t.Run("wrong params type", func(t *testing.T) {
		req := &mcp.ServerRequest[*mcp.ListToolsParams]{Params: &mcp.ListToolsParams{}}
		assert.Equal(t, 0, calculateRequestSize(req))
	})
}

func TestBuildMCPAuditEvent_IncludesResponseSize(t *testing.T) {
	pc := NewPlatformContext("req-test")
	pc.ToolName = testAuditToolName
	pc.SessionID = "test-session"
	pc.Transport = "stdio"
	pc.Source = testAuditSourceMCP
	pc.Authorized = true

	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello world"}},
	}
	req := createAuditTestRequest(t, testAuditToolName, map[string]any{"sql": "SELECT 1"})

	event := buildMCPAuditEvent(pc, auditCallInfo{
		Request:   req,
		Result:    result,
		Err:       nil,
		StartTime: time.Now(),
		Duration:  time.Millisecond,
	})

	assert.Equal(t, testAuditCharsHello, event.ResponseChars)
	assert.Equal(t, 1, event.ContentBlocks)
	assert.Greater(t, event.RequestChars, 0)
	assert.Equal(t, "test-session", event.SessionID)
	assert.Equal(t, "stdio", event.Transport)
	assert.Equal(t, testAuditSourceMCP, event.Source)
	assert.True(t, event.Authorized)
}

func TestMCPAuditMiddleware_ResponseSizeLogged(t *testing.T) {
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result data here"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	pc := NewPlatformContext("req-size")
	pc.ToolName = "test_tool"
	ctx := WithPlatformContext(context.Background(), pc)

	req := createAuditTestRequest(t, "test_tool", nil)
	_, _ = wrapped(ctx, testAuditMethodCall, req)

	time.Sleep(50 * time.Millisecond)

	events := mockLogger.Events()
	require.Len(t, events, 1)
	assert.Equal(t, testAuditCharsResult, events[0].ResponseChars) // "result data here" = 16 chars.
	assert.Equal(t, 1, events[0].ContentBlocks)
}

// capturingAuditLogger captures audit events for testing.
type capturingAuditLogger struct {
	mu     sync.Mutex
	events []AuditEvent
}

func newCapturingAuditLogger() *capturingAuditLogger {
	return &capturingAuditLogger{
		events: make([]AuditEvent, 0),
	}
}

func (c *capturingAuditLogger) Log(_ context.Context, event AuditEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *capturingAuditLogger) Events() []AuditEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]AuditEvent, len(c.events))
	copy(result, c.events)
	return result
}

func TestBuildMCPAuditEvent_ErrorCategory(t *testing.T) {
	t.Run("categorized error in result", func(t *testing.T) {
		pc := NewPlatformContext("req-cat")
		pc.ToolName = testAuditToolName
		pc.Transport = "http"
		pc.Source = testAuditSourceMCP

		result := createCategorizedErrorResult(ErrCategoryAuth, "auth failed")
		event := buildMCPAuditEvent(pc, auditCallInfo{
			Request:   createAuditTestRequest(t, testAuditToolName, nil),
			Result:    result,
			StartTime: time.Now(),
			Duration:  time.Millisecond,
		})

		assert.False(t, event.Success)
		assert.Equal(t, "auth failed", event.ErrorMessage)
		assert.Equal(t, ErrCategoryAuth, event.ErrorCategory)
	})

	t.Run("plain error in result has empty category", func(t *testing.T) {
		pc := NewPlatformContext("req-plain")
		pc.ToolName = testAuditToolName

		result := createErrorResult("some error")
		event := buildMCPAuditEvent(pc, auditCallInfo{
			Request:   createAuditTestRequest(t, testAuditToolName, nil),
			Result:    result,
			StartTime: time.Now(),
			Duration:  time.Millisecond,
		})

		assert.False(t, event.Success)
		assert.Equal(t, "some error", event.ErrorMessage)
		assert.Empty(t, event.ErrorCategory)
	})

	t.Run("successful result has no category", func(t *testing.T) {
		pc := NewPlatformContext("req-ok")
		pc.ToolName = testAuditToolName

		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}
		event := buildMCPAuditEvent(pc, auditCallInfo{
			Request:   createAuditTestRequest(t, testAuditToolName, nil),
			Result:    result,
			StartTime: time.Now(),
			Duration:  time.Millisecond,
		})

		assert.True(t, event.Success)
		assert.Empty(t, event.ErrorMessage)
		assert.Empty(t, event.ErrorCategory)
	})
}

// Helper to create ServerRequest for audit testing.
func createAuditTestRequest(t *testing.T, toolName string, args map[string]any) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	t.Helper()
	var argsJSON json.RawMessage
	if args != nil {
		var err error
		argsJSON, err = json.Marshal(args)
		require.NoError(t, err)
	}

	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{
			Name:      toolName,
			Arguments: argsJSON,
		},
	}
}
