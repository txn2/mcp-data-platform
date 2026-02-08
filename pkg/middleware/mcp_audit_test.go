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
	time.Sleep(100 * time.Millisecond) //nolint:revive // test timing

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
		req := createAuditTestRequest(t, "test", map[string]any{"key": "value", "num": float64(42)}) //nolint:revive // test fixture
		result := extractMCPParameters(req)
		assert.Equal(t, map[string]any{"key": "value", "num": float64(42)}, result) //nolint:revive // test fixture
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
	chars, tokens := calculateResponseSize(result, nil)
	assert.Equal(t, testAuditCharsHello, chars)
	assert.Equal(t, 2, tokens) // 11/4 = 2 (truncated).
}

func TestCalculateResponseSize_MultipleItems(t *testing.T) {
	// Build 1000 chars across multiple content items.
	text1 := make([]byte, 600) //nolint:revive // test size
	for i := range text1 {
		text1[i] = 'a'
	}
	text2 := make([]byte, 400) //nolint:revive // test size
	for i := range text2 {
		text2[i] = 'b'
	}

	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(text1)},
			&mcp.TextContent{Text: string(text2)},
		},
	}
	chars, tokens := calculateResponseSize(result, nil)
	assert.Equal(t, 1000, chars) //nolint:revive // expected test value
	assert.Equal(t, 250, tokens) //nolint:revive // 1000/4 = 250
}

func TestCalculateResponseSize_ErrorResult(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
	}
	chars, tokens := calculateResponseSize(result, assert.AnError)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, tokens)
}

func TestCalculateResponseSize_NilResult(t *testing.T) {
	chars, tokens := calculateResponseSize(nil, nil)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, tokens)
}

func TestCalculateResponseSize_NonCallToolResult(t *testing.T) {
	chars, tokens := calculateResponseSize(&mcp.ListResourcesResult{}, nil)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, tokens)
}

func TestCalculateResponseSize_ImageContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "text"},
			&mcp.ImageContent{Data: []byte("base64imagedata")},
		},
	}
	chars, tokens := calculateResponseSize(result, nil)
	// "text" = 4, "base64imagedata" = 15, total = 19.
	assert.Equal(t, testAuditCharsImage, chars)
	assert.Equal(t, 4, tokens) //nolint:revive // 19/4 = 4
}

func TestCalculateResponseSize_EmptyContent(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{},
	}
	chars, tokens := calculateResponseSize(result, nil)
	assert.Equal(t, 0, chars)
	assert.Equal(t, 0, tokens)
}

func TestBuildMCPAuditEvent_IncludesResponseSize(t *testing.T) {
	pc := NewPlatformContext("req-test")
	pc.ToolName = testAuditToolName

	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello world"}},
	}
	req := createAuditTestRequest(t, testAuditToolName, nil)

	event := buildMCPAuditEvent(pc, auditCallInfo{
		Request:   req,
		Result:    result,
		Err:       nil,
		StartTime: time.Now(),
		Duration:  time.Millisecond,
	})

	assert.Equal(t, testAuditCharsHello, event.ResponseChars)
	assert.Equal(t, 2, event.ResponseTokenEstimate)
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
	assert.Equal(t, 4, events[0].ResponseTokenEstimate)            //nolint:revive // 16/4 = 4
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
