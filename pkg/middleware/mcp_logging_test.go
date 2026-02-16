package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

func TestMCPClientLoggingMiddleware_Disabled(t *testing.T) {
	cfg := ClientLoggingConfig{Enabled: false}
	handlerCalled := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		handlerCalled = true
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPClientLoggingMiddleware(cfg)(base)
	_, err := handler(context.Background(), methodToolsCall, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

func TestMCPClientLoggingMiddleware_NonToolsCall(t *testing.T) {
	cfg := ClientLoggingConfig{Enabled: true}
	handlerCalled := false
	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		handlerCalled = true
		return &mcp.CallToolResult{}, nil
	}

	handler := MCPClientLoggingMiddleware(cfg)(base)
	_, err := handler(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handlerCalled {
		t.Error("expected handler to be called for non-tools/call method")
	}
}

func TestMCPClientLoggingMiddleware_Passthrough(t *testing.T) {
	cfg := ClientLoggingConfig{Enabled: true}
	want := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
	}

	base := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return want, nil
	}

	handler := MCPClientLoggingMiddleware(cfg)(base)
	got, err := handler(context.Background(), methodToolsCall, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Error("expected result to be passed through unmodified")
	}
}

func TestSendClientLog_NilPlatformContext(_ *testing.T) {
	// Should not panic when PlatformContext is nil.
	sendClientLog(context.Background(), &mcp.CallToolResult{}, nil)
}

func TestSendClientLog_NoEnrichment(_ *testing.T) {
	pc := &PlatformContext{EnrichmentApplied: false}
	ctx := WithPlatformContext(context.Background(), pc)
	// Should not panic or send log when enrichment was not applied.
	sendClientLog(ctx, &mcp.CallToolResult{}, nil)
}

func TestSendClientLog_HandlerError(_ *testing.T) {
	pc := &PlatformContext{EnrichmentApplied: true}
	ctx := WithPlatformContext(context.Background(), pc)
	// Should not attempt logging when handler returned an error.
	sendClientLog(ctx, nil, context.DeadlineExceeded)
}

func TestSendClientLog_NoSession(_ *testing.T) {
	pc := &PlatformContext{
		EnrichmentApplied: true,
		ToolName:          "trino_query",
		Duration:          50 * time.Millisecond,
	}
	ctx := WithPlatformContext(context.Background(), pc)
	// Should not panic when session is nil.
	sendClientLog(ctx, &mcp.CallToolResult{}, nil)
}

func TestSendClientLog_NilTypedSession(_ *testing.T) {
	pc := &PlatformContext{
		EnrichmentApplied: true,
		ToolName:          "trino_query",
		Duration:          50 * time.Millisecond,
	}
	ctx := WithPlatformContext(context.Background(), pc)
	// Store a typed nil — GetServerSession should return nil.
	ctx = mcpcontext.WithServerSession(ctx, (*mcp.ServerSession)(nil))
	sendClientLog(ctx, &mcp.CallToolResult{}, nil)
}

// mockSessionLogger implements sessionLogger for testing.
type mockSessionLogger struct {
	lastParams *mcp.LoggingMessageParams
	err        error
}

func (m *mockSessionLogger) Log(_ context.Context, params *mcp.LoggingMessageParams) error {
	m.lastParams = params
	return m.err
}

func TestEmitClientLog_Success(t *testing.T) {
	mock := &mockSessionLogger{}
	pc := &PlatformContext{
		ToolName: "trino_query",
		Duration: 123 * time.Millisecond,
	}

	emitClientLog(context.Background(), mock, pc)

	if mock.lastParams == nil {
		t.Fatal("expected Log to be called")
	}
	if mock.lastParams.Level != "info" {
		t.Errorf("level = %q, want %q", mock.lastParams.Level, "info")
	}
	if mock.lastParams.Logger != "mcp-data-platform" {
		t.Errorf("logger = %q, want %q", mock.lastParams.Logger, "mcp-data-platform")
	}
	msg, ok := mock.lastParams.Data.(string)
	if !ok {
		t.Fatalf("data type = %T, want string", mock.lastParams.Data)
	}
	if msg == "" {
		t.Error("expected non-empty log message")
	}
}

func TestEmitClientLog_Error(t *testing.T) {
	mock := &mockSessionLogger{err: context.DeadlineExceeded}
	pc := &PlatformContext{
		ToolName: "trino_query",
		Duration: 50 * time.Millisecond,
	}

	// Should not panic when Log returns an error.
	emitClientLog(context.Background(), mock, pc)

	if mock.lastParams == nil {
		t.Fatal("expected Log to be called even when it returns an error")
	}
}
