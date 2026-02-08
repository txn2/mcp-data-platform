package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPAuditMiddleware creates MCP protocol-level middleware that logs tool calls
// for auditing purposes.
//
// This middleware intercepts tools/call requests and:
//  1. Records the start time
//  2. Executes the tool handler
//  3. Gets the PlatformContext (set by MCPToolCallMiddleware)
//  4. Builds an audit event with all captured information
//  5. Logs asynchronously (non-blocking) to avoid impacting response time
func MCPAuditMiddleware(logger AuditLogger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// Only audit tools/call requests
			if method != methodToolsCall {
				return next(ctx, method, req)
			}

			startTime := time.Now()

			// Execute handler
			result, err := next(ctx, method, req)

			duration := time.Since(startTime)

			// Get platform context (set by MCPToolCallMiddleware)
			pc := GetPlatformContext(ctx)
			if pc == nil {
				// No platform context means auth middleware didn't run
				// or this is an edge case - don't log
				slog.Warn("audit: no platform context available, skipping audit log")
				return result, err
			}

			// Build audit event
			event := buildMCPAuditEvent(pc, auditCallInfo{
				Request:   req,
				Result:    result,
				Err:       err,
				StartTime: startTime,
				Duration:  duration,
			})

			// Log asynchronously to not block the response
			go func() {
				if err := logger.Log(context.Background(), event); err != nil {
					slog.Error("failed to log audit event",
						"error", err,
						"tool", event.ToolName,
						"user_id", event.UserID,
						"request_id", event.RequestID,
					)
				}
			}()

			return result, err
		}
	}
}

// auditCallInfo groups the call-related parameters for building an audit event.
type auditCallInfo struct {
	Request   mcp.Request
	Result    mcp.Result
	Err       error
	StartTime time.Time
	Duration  time.Duration
}

// buildMCPAuditEvent builds an audit event from the MCP request and response.
func buildMCPAuditEvent(pc *PlatformContext, info auditCallInfo) AuditEvent {
	// Determine success
	success := info.Err == nil
	errorMsg := ""
	if info.Err != nil {
		errorMsg = info.Err.Error()
	} else if callResult, ok := info.Result.(*mcp.CallToolResult); ok && callResult != nil && callResult.IsError {
		success = false
		errorMsg = extractMCPErrorMessage(callResult)
	}

	// Extract parameters from request
	params := extractMCPParameters(info.Request)

	chars, blocks := calculateResponseSize(info.Result, info.Err)
	reqChars := calculateRequestSize(info.Request)

	return AuditEvent{
		Timestamp:         info.StartTime,
		RequestID:         pc.RequestID,
		SessionID:         pc.SessionID,
		UserID:            pc.UserID,
		UserEmail:         pc.UserEmail,
		Persona:           pc.PersonaName,
		ToolName:          pc.ToolName,
		ToolkitKind:       pc.ToolkitKind,
		ToolkitName:       pc.ToolkitName,
		Connection:        pc.Connection,
		Parameters:        params,
		Success:           success,
		ErrorMessage:      errorMsg,
		DurationMS:        info.Duration.Milliseconds(),
		ResponseChars:     chars,
		RequestChars:      reqChars,
		ContentBlocks:     blocks,
		Transport:         pc.Transport,
		Source:            pc.Source,
		EnrichmentApplied: pc.EnrichmentApplied,
		Authorized:        pc.Authorized,
	}
}

// extractMCPParameters extracts parameters from an MCP request.
func extractMCPParameters(req mcp.Request) map[string]any {
	if req == nil {
		return nil
	}
	params := req.GetParams()
	if params == nil {
		return nil
	}

	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok || callParams == nil {
		return nil
	}

	return extractArgumentsMap(callParams)
}

// calculateResponseSize computes the total character count and content block
// count from an MCP tool call result. Returns (0, 0) if err is non-nil or
// the result is not a CallToolResult.
func calculateResponseSize(result mcp.Result, err error) (chars, contentBlocks int) {
	if err != nil {
		return 0, 0
	}
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil {
		return 0, 0
	}

	total := 0
	for _, content := range callResult.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			total += len(c.Text)
		case *mcp.ImageContent:
			total += len(c.Data)
		case *mcp.AudioContent:
			total += len(c.Data)
		}
	}

	return total, len(callResult.Content)
}

// calculateRequestSize computes the character count of the request arguments.
func calculateRequestSize(req mcp.Request) int {
	if req == nil {
		return 0
	}
	params := req.GetParams()
	if params == nil {
		return 0
	}
	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok || callParams == nil {
		return 0
	}
	return len(callParams.Arguments)
}

// extractMCPErrorMessage extracts the error message from an MCP CallToolResult.
func extractMCPErrorMessage(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
		return textContent.Text
	}
	return ""
}
