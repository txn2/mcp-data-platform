package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/observability"
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

// auditMWWithParams runs the audit middleware over a single tool call carrying
// the given arguments and returns the captured event. Options configure the
// parameter-capture policy under test.
func auditMWWithParams(t *testing.T, args map[string]any, opts ...AuditOption) AuditEvent {
	t.Helper()
	mockLogger := newCapturingAuditLogger()
	mw := MCPAuditMiddleware(mockLogger, opts...)

	handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	}

	pc := NewPlatformContext("req-redact")
	pc.UserID = testAuditEmail
	pc.ToolName = testAuditToolName
	ctx := WithPlatformContext(context.Background(), pc)

	req := createAuditTestRequest(t, testAuditToolName, args)
	_, err := mw(handler)(ctx, testAuditMethodCall, req)
	require.NoError(t, err)

	events := mockLogger.Events()
	require.Len(t, events, 1)
	return events[0]
}

func TestMCPAuditMiddleware_RedactKeys(t *testing.T) {
	event := auditMWWithParams(t, map[string]any{
		"password": "hunter2",
		"sql":      "SELECT secret FROM users",
		"catalog":  "hive",
	}, WithRedactKeys([]string{"password", "sql"}))

	assert.Equal(t, "[REDACTED]", event.Parameters["password"])
	assert.Equal(t, "[REDACTED]", event.Parameters["sql"])
	// Unlisted keys pass through intact.
	assert.Equal(t, "hive", event.Parameters["catalog"])
}

func TestMCPAuditMiddleware_RedactKeys_SkipsEmpty(t *testing.T) {
	// An empty configured key is ignored (never matches the empty-string arg),
	// and real keys still redact.
	event := auditMWWithParams(t, map[string]any{
		"password": "hunter2",
		"catalog":  "hive",
	}, WithRedactKeys([]string{"", "password"}))

	assert.Equal(t, "[REDACTED]", event.Parameters["password"])
	assert.Equal(t, "hive", event.Parameters["catalog"])
}

func TestMCPAuditMiddleware_RedactKeys_TrimsWhitespace(t *testing.T) {
	// A config key with a stray trailing space (an easy YAML slip) must still
	// redact, not silently store the value verbatim.
	event := auditMWWithParams(t, map[string]any{
		"password": "hunter2",
		"catalog":  "hive",
	}, WithRedactKeys([]string{"  password  "}))

	assert.Equal(t, "[REDACTED]", event.Parameters["password"])
	assert.Equal(t, "hive", event.Parameters["catalog"])
}

func TestMCPAuditMiddleware_RedactKeys_CaseInsensitive(t *testing.T) {
	// Config key "PASSWORD" must match the request arg "password", and vice
	// versa: matching is case-insensitive on the top-level key.
	event := auditMWWithParams(t, map[string]any{
		"Password": "hunter2",
		"SQL":      "SELECT 1",
	}, WithRedactKeys([]string{"password", "sql"}))

	assert.Equal(t, "[REDACTED]", event.Parameters["Password"])
	assert.Equal(t, "[REDACTED]", event.Parameters["SQL"])
}

func TestMCPAuditMiddleware_RedactKeys_TopLevelOnly(t *testing.T) {
	// A nested key that happens to share a redacted name is NOT matched:
	// redaction is top-level only, by design.
	event := auditMWWithParams(t, map[string]any{
		"filter": map[string]any{"password": "hunter2"},
	}, WithRedactKeys([]string{"password"}))

	nested, ok := event.Parameters["filter"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "hunter2", nested["password"])
}

func TestMCPAuditMiddleware_LogParametersDisabled(t *testing.T) {
	event := auditMWWithParams(t, map[string]any{
		"sql": "SELECT 1",
	}, WithParameterLogging(false))

	// Parameters are dropped entirely; the row stores null.
	assert.Nil(t, event.Parameters)
	// Other fields are unaffected.
	assert.Equal(t, testAuditToolName, event.ToolName)
	assert.True(t, event.Success)
}

func TestMCPAuditMiddleware_LogParametersDisabled_BeatsRedaction(t *testing.T) {
	// When parameter logging is off, redaction is moot: the whole map is gone.
	event := auditMWWithParams(t, map[string]any{
		"password": "hunter2",
	}, WithParameterLogging(false), WithRedactKeys([]string{"password"}))

	assert.Nil(t, event.Parameters)
}

func TestMCPAuditMiddleware_DefaultsCaptureParameters(t *testing.T) {
	// With no options, parameters are captured verbatim (redaction opt-in).
	event := auditMWWithParams(t, map[string]any{
		"password": "hunter2",
		"sql":      "SELECT 1",
	})

	assert.Equal(t, "hunter2", event.Parameters["password"])
	assert.Equal(t, "SELECT 1", event.Parameters["sql"])
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
	}, defaultAuditParamPolicy())

	assert.Equal(t, testAuditCharsHello, event.ResponseChars)
	assert.Equal(t, 1, event.ContentBlocks)
	assert.Greater(t, event.RequestChars, 0)
	assert.Equal(t, "test-session", event.SessionID)
	assert.Equal(t, "stdio", event.Transport)
	assert.Equal(t, testAuditSourceMCP, event.Source)
	assert.True(t, event.Authorized)
}

func TestBuildMCPAuditEvent_ThreadsEnrichmentTokens(t *testing.T) {
	pc := NewPlatformContext("req-tokens")
	pc.ToolName = testAuditToolName
	pc.EnrichmentTokensFull = 500
	pc.EnrichmentTokensDedup = 50

	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
	}
	req := createAuditTestRequest(t, testAuditToolName, nil)

	event := buildMCPAuditEvent(pc, auditCallInfo{
		Request:   req,
		Result:    result,
		StartTime: time.Now(),
		Duration:  time.Millisecond,
	}, defaultAuditParamPolicy())

	assert.Equal(t, 500, event.EnrichmentTokensFull)
	assert.Equal(t, 50, event.EnrichmentTokensDedup)
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

// recordingAuditStore captures the final audit.Event values that reach
// the storage sink. It implements the auditStore interface the real
// auditStoreAdapter depends on, so a test can wire the real adapter
// over it and assert the end-to-end conversion result.
type recordingAuditStore struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAuditStore) Log(_ context.Context, event audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *recordingAuditStore) Events() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Event, len(r.events))
	copy(out, r.events)
	return out
}

// TestMCPAuditMiddleware_EventKindEndToEnd proves the full audit path:
// MCPAuditMiddleware derives the event kind from the toolkit kind in
// PlatformContext, the real auditStoreAdapter maps it through its
// builder chain, and the resulting audit.Event carries the correct
// EventKind at the storage sink. Issue #465.
func TestMCPAuditMiddleware_EventKindEndToEnd(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		toolkitKind string
		want        audit.EventType
	}{
		{
			name:        "apigateway invoke",
			toolName:    "api_invoke_endpoint",
			toolkitKind: "api",
			want:        audit.EventTypeAPIGatewayInvoke,
		},
		{
			name:        "mcp tool call",
			toolName:    testAuditToolName,
			toolkitKind: testAuditToolkit,
			want:        audit.EventTypeMCPToolCall,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingAuditStore{}
			// Real adapter over the recording sink — exercises the
			// production builder chain, not a hand-built event.
			logger := &auditStoreAdapter{store: rec}
			mw := MCPAuditMiddleware(logger)

			handler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
				}, nil
			}
			wrapped := mw(handler)

			pc := NewPlatformContext("req-ek")
			pc.UserID = testAuditEmail
			pc.ToolName = tc.toolName
			pc.ToolkitKind = tc.toolkitKind
			ctx := WithPlatformContext(context.Background(), pc)

			req := createAuditTestRequest(t, tc.toolName, nil)
			_, err := wrapped(ctx, testAuditMethodCall, req)
			require.NoError(t, err)

			require.Eventually(t, func() bool {
				return len(rec.Events()) == 1
			}, time.Second, 10*time.Millisecond, "audit event should be logged")

			got := rec.Events()[0]
			assert.Equal(t, tc.want, got.EventKind)
			assert.Equal(t, tc.toolName, got.ToolName)
			assert.Equal(t, tc.toolkitKind, got.ToolkitKind)
		})
	}
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

		result := BuildErrorResult(&PlatformError{Category: ErrCategoryAuth, Message: "auth failed"})
		event := buildMCPAuditEvent(pc, auditCallInfo{
			Request:   createAuditTestRequest(t, testAuditToolName, nil),
			Result:    result,
			StartTime: time.Now(),
			Duration:  time.Millisecond,
		}, defaultAuditParamPolicy())

		assert.False(t, event.Success)
		assert.Equal(t, "auth failed", event.ErrorMessage)
		assert.Equal(t, ErrCategoryAuth, event.ErrorCategory)
	})

	t.Run("plain error in result has empty category", func(t *testing.T) {
		pc := NewPlatformContext("req-plain")
		pc.ToolName = testAuditToolName

		result := &mcp.CallToolResult{}
		result.SetError(errors.New("some error"))
		event := buildMCPAuditEvent(pc, auditCallInfo{
			Request:   createAuditTestRequest(t, testAuditToolName, nil),
			Result:    result,
			StartTime: time.Now(),
			Duration:  time.Millisecond,
		}, defaultAuditParamPolicy())

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
		}, defaultAuditParamPolicy())

		assert.True(t, event.Success)
		assert.Empty(t, event.ErrorMessage)
		assert.Empty(t, event.ErrorCategory)
	})
}

func TestBuildMCPAuditEvent_WithProtocolError(t *testing.T) {
	pc := NewPlatformContext("req-proto")
	pc.ToolName = testAuditToolName
	pc.Transport = "http"
	pc.Source = testAuditSourceMCP

	protoErr := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid request: missing tool name"}

	event := buildMCPAuditEvent(pc, auditCallInfo{
		Request:   createAuditTestRequest(t, testAuditToolName, nil),
		Result:    nil,
		Err:       protoErr,
		StartTime: time.Now(),
		Duration:  time.Millisecond,
	}, defaultAuditParamPolicy())

	assert.False(t, event.Success)
	assert.Equal(t, "invalid request: missing tool name", event.ErrorMessage)
	// jsonrpc.Error doesn't implement CategorizedError, so category should be empty.
	assert.Empty(t, event.ErrorCategory)
	assert.Equal(t, 0, event.ResponseChars, "no response for protocol error")
	assert.Equal(t, 0, event.ContentBlocks, "no content blocks for protocol error")
}

// TestBuildMCPAuditEvent_AuditOutcomeMetaOverride verifies the audit
// middleware honors the _meta.audit_outcome / audit_outcome_message
// hint that upstream-proxying toolkits (apigateway) populate on
// every result. See issue #432: success cannot be derived from
// IsError for the apigateway because upstream 4xx/5xx are NOT
// gateway failures (gateway succeeded at proxying). They are
// successful proxies of unsuccessful upstream calls, and the audit
// row should reflect the latter.
func TestBuildMCPAuditEvent_AuditOutcomeMetaOverride(t *testing.T) {
	tests := []struct {
		name             string
		isError          bool
		outcome          string
		outcomeMessage   string
		existingErrText  string
		wantSuccess      bool
		wantCategory     string
		wantErrorMessage string
	}{
		{
			name:             "outcome=ok leaves success true, no category",
			isError:          false,
			outcome:          observability.OutcomeOK,
			wantSuccess:      true,
			wantCategory:     "",
			wantErrorMessage: "",
		},
		{
			name:             "upstream_4xx with IsError false → success false, category set, message lifted",
			isError:          false,
			outcome:          observability.OutcomeUpstream4xx,
			outcomeMessage:   "Not Found",
			wantSuccess:      false,
			wantCategory:     observability.OutcomeUpstream4xx,
			wantErrorMessage: "Not Found",
		},
		{
			name:             "upstream_5xx with IsError false → success false, category set",
			isError:          false,
			outcome:          observability.OutcomeUpstream5xx,
			outcomeMessage:   "Service Unavailable",
			wantSuccess:      false,
			wantCategory:     observability.OutcomeUpstream5xx,
			wantErrorMessage: "Service Unavailable",
		},
		{
			name:             "transport_err with IsError true → success false, category overrides empty",
			isError:          true,
			existingErrText:  "Get \"https://x/\": dial tcp: connection refused",
			outcome:          observability.OutcomeTransportErr,
			outcomeMessage:   "Get \"https://x/\": dial tcp: connection refused",
			wantSuccess:      false,
			wantCategory:     observability.OutcomeTransportErr,
			wantErrorMessage: "Get \"https://x/\": dial tcp: connection refused",
		},
		{
			name:             "upstream_timeout with IsError true → category overrides",
			isError:          true,
			existingErrText:  "Get \"https://x/\": context deadline exceeded",
			outcome:          observability.OutcomeUpstreamTimeout,
			outcomeMessage:   "Get \"https://x/\": context deadline exceeded",
			wantSuccess:      false,
			wantCategory:     observability.OutcomeUpstreamTimeout,
			wantErrorMessage: "Get \"https://x/\": context deadline exceeded",
		},
		{
			name:             "no _meta hint → behavior unchanged (success result)",
			isError:          false,
			outcome:          "",
			wantSuccess:      true,
			wantCategory:     "",
			wantErrorMessage: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pc := NewPlatformContext("req-outcome")
			pc.ToolName = testAuditToolName

			// Build Content to mirror what the apigateway's
			// buildInvokeResult actually emits: a JSON-marshaled
			// envelope when IsError is set. Without this, the
			// IsError branch in buildMCPAuditEvent would receive
			// the plain scrubbed error string and the test would
			// give a false positive for the meta-message override
			// logic (the override is supposed to REPLACE the
			// JSON-blob extraction, not just fill an empty slot).
			contentText := "{}"
			if tc.isError && tc.existingErrText != "" {
				body, err := json.Marshal(map[string]any{
					"status":      0,
					"duration_ms": 0,
					"error":       tc.existingErrText,
				})
				require.NoError(t, err)
				contentText = string(body)
			}
			result := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: contentText}},
				IsError: tc.isError,
			}
			if tc.outcome != "" {
				result.Meta = mcp.Meta{
					observability.MetaAuditOutcome: tc.outcome,
				}
				if tc.outcomeMessage != "" {
					result.Meta[observability.MetaAuditOutcomeMessage] = tc.outcomeMessage
				}
			}

			event := buildMCPAuditEvent(pc, auditCallInfo{
				Request:   createAuditTestRequest(t, testAuditToolName, nil),
				Result:    result,
				StartTime: time.Now(),
				Duration:  time.Millisecond,
			}, defaultAuditParamPolicy())

			assert.Equal(t, tc.wantSuccess, event.Success, "Success")
			assert.Equal(t, tc.wantCategory, event.ErrorCategory, "ErrorCategory")
			assert.Equal(t, tc.wantErrorMessage, event.ErrorMessage, "ErrorMessage")
		})
	}
}

// TestReadAuditOutcomeMeta covers the nil-safety and type-assertion
// corners of the meta-reader so the audit middleware doesn't panic
// on a malformed result.
func TestReadAuditOutcomeMeta(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		o, m := readAuditOutcomeMeta(nil)
		assert.Empty(t, o)
		assert.Empty(t, m)
	})
	t.Run("nil meta", func(t *testing.T) {
		o, m := readAuditOutcomeMeta(&mcp.CallToolResult{})
		assert.Empty(t, o)
		assert.Empty(t, m)
	})
	t.Run("non-string outcome ignored", func(t *testing.T) {
		r := &mcp.CallToolResult{Meta: mcp.Meta{observability.MetaAuditOutcome: 42}}
		o, _ := readAuditOutcomeMeta(r)
		assert.Empty(t, o)
	})
	t.Run("string values returned", func(t *testing.T) {
		r := &mcp.CallToolResult{Meta: mcp.Meta{
			observability.MetaAuditOutcome:        observability.OutcomeUpstream5xx,
			observability.MetaAuditOutcomeMessage: "Service Unavailable",
		}}
		o, m := readAuditOutcomeMeta(r)
		assert.Equal(t, observability.OutcomeUpstream5xx, o)
		assert.Equal(t, "Service Unavailable", m)
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

// TestMCPAuditMiddleware_BoundsAPayloadArgument covers the arguments that carry
// content rather than describe a call: an object body written to storage, a
// file uploaded, a report a managed script delivers on every scheduled fire.
// Storing those verbatim puts a copy of the data in the audit table and in the
// writer's queue on the way there.
func TestMCPAuditMiddleware_BoundsAPayloadArgument(t *testing.T) {
	payload := strings.Repeat("A", maxAuditValueBytes+1)
	event := auditMWWithParams(t, map[string]any{
		"bucket":  "acme-exports",
		"key":     "weekly/sales.csv",
		"content": payload,
		"count":   float64(3),
	})

	assert.Equal(t, "acme-exports", event.Parameters["bucket"], "what was called is still recorded")
	assert.Equal(t, "weekly/sales.csv", event.Parameters["key"])
	assert.Equal(t, float64(3), event.Parameters["count"], "a non-string value is untouched")
	assert.Equal(t, fmt.Sprintf("[TRUNCATED: %d bytes]", len(payload)), event.Parameters["content"])
}

// TestMCPAuditMiddleware_KeepsAValueWithinTheBound pins the other half: the
// bound is generous, and a query worth auditing is recorded in full.
func TestMCPAuditMiddleware_KeepsAValueWithinTheBound(t *testing.T) {
	sql := "SELECT " + strings.Repeat("x", maxAuditValueBytes-16)
	event := auditMWWithParams(t, map[string]any{"sql": sql})
	assert.Equal(t, sql, event.Parameters["sql"])
}

// TestReadAuditResultMeta covers the nil-safety of the result-facts
// reader: a missing or mistyped value yields nil rather than a panic.
func TestReadAuditResultMeta(t *testing.T) {
	assert.Nil(t, readAuditResultMeta(nil))
	assert.Nil(t, readAuditResultMeta(&mcp.CallToolResult{}))
	assert.Nil(t, readAuditResultMeta(&mcp.CallToolResult{Meta: mcp.Meta{observability.MetaAuditResult: "not a map"}}))
	facts := map[string]any{"pages_fetched": 3}
	assert.Equal(t, facts, readAuditResultMeta(&mcp.CallToolResult{Meta: mcp.Meta{observability.MetaAuditResult: facts}}))
}

// TestBuildMCPAuditEvent_ResultFactsRecordedUnderParameters: facts a tool
// stamps on its result land under parameters.result, beside the
// arguments, and are recorded even when argument logging is off because
// they are not argument values (issue #1535).
func TestBuildMCPAuditEvent_ResultFactsRecordedUnderParameters(t *testing.T) {
	pc := &PlatformContext{ToolName: "api_export"}
	req := createAuditTestRequest(t, "api_export", map[string]any{"connection": "vendor", "paginate": map[string]any{"items": "data"}})
	facts := map[string]any{"pages_fetched": 160, "items_merged": 16000, "stopped_by": "end"}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "{}"}},
		Meta:    mcp.Meta{observability.MetaAuditResult: facts},
	}

	ev := buildMCPAuditEvent(pc, auditCallInfo{Request: req, Result: result}, defaultAuditParamPolicy())
	assert.Equal(t, "vendor", ev.Parameters["connection"], "arguments are still recorded beside the facts")
	assert.Equal(t, facts, ev.Parameters["result"])
	assert.True(t, ev.Success)

	off := auditParamPolicy{logParameters: false, redactKeys: map[string]struct{}{}}
	ev = buildMCPAuditEvent(pc, auditCallInfo{Request: req, Result: result}, off)
	assert.Nil(t, ev.Parameters["connection"], "arguments are dropped by the policy")
	assert.Equal(t, facts, ev.Parameters["result"], "the facts are not arguments and survive the policy")

	plain := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}
	ev = buildMCPAuditEvent(pc, auditCallInfo{Request: req, Result: plain}, defaultAuditParamPolicy())
	_, has := ev.Parameters["result"]
	assert.False(t, has, "a result with no facts adds no key")
}
