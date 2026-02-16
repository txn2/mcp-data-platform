package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// sessionLogger abstracts the ServerSession.Log method for testability.
type sessionLogger interface {
	Log(ctx context.Context, params *mcp.LoggingMessageParams) error
}

// ClientLoggingConfig configures server-to-client logging middleware.
type ClientLoggingConfig struct {
	Enabled bool `yaml:"enabled"`
}

// MCPClientLoggingMiddleware creates MCP protocol-level middleware that sends
// log notifications to the client via ServerSession.Log().
//
// After the handler and enrichment run, this middleware checks whether
// enrichment was applied and sends an info-level log to the client. The client
// only receives the log if it has previously called logging/setLevel; otherwise
// ServerSession.Log() is a silent no-op (zero overhead).
func MCPClientLoggingMiddleware(cfg ClientLoggingConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		if !cfg.Enabled {
			return next
		}

		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}

			result, err := next(ctx, method, req)

			sendClientLog(ctx, result, err)

			return result, err
		}
	}
}

// sendClientLog sends a log notification to the client if enrichment was
// applied and a server session is available. All errors are silently ignored
// to keep logging best-effort.
func sendClientLog(ctx context.Context, _ mcp.Result, handlerErr error) {
	if handlerErr != nil {
		return
	}

	pc := GetPlatformContext(ctx)
	if pc == nil || !pc.EnrichmentApplied {
		return
	}

	session := mcpcontext.GetServerSession(ctx)
	if session == nil {
		return
	}

	emitClientLog(ctx, session, pc)
}

// emitClientLog builds and sends a log notification to the client.
func emitClientLog(ctx context.Context, logger sessionLogger, pc *PlatformContext) {
	msg := fmt.Sprintf("enriched %s response with semantic context (%.0fms)",
		pc.ToolName, float64(pc.Duration.Milliseconds()))

	if err := logger.Log(ctx, &mcp.LoggingMessageParams{
		Level:  "info",
		Logger: "mcp-data-platform",
		Data:   msg,
	}); err != nil {
		slog.Debug("client logging: failed to send log notification", "error", err)
	}
}
