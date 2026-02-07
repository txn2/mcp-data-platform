package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// estimatedCharsPerToken is the approximate number of characters per LLM token.
const estimatedCharsPerToken = 4

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
			if method != "tools/call" {
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
			event := buildMCPAuditEvent(pc, req, result, err, startTime, duration)

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

// buildMCPAuditEvent builds an audit event from the MCP request and response.
func buildMCPAuditEvent(
	pc *PlatformContext,
	req mcp.Request,
	result mcp.Result,
	err error,
	startTime time.Time,
	duration time.Duration,
) AuditEvent {
	// Determine success
	success := err == nil
	errorMsg := ""
	if err != nil {
		errorMsg = err.Error()
	} else if callResult, ok := result.(*mcp.CallToolResult); ok && callResult != nil && callResult.IsError {
		success = false
		errorMsg = extractMCPErrorMessage(callResult)
	}

	// Extract parameters from request
	params := extractMCPParameters(req)

	chars, tokens := calculateResponseSize(result, err)

	return AuditEvent{
		Timestamp:             startTime,
		RequestID:             pc.RequestID,
		UserID:                pc.UserID,
		UserEmail:             pc.UserEmail,
		Persona:               pc.PersonaName,
		ToolName:              pc.ToolName,
		ToolkitKind:           pc.ToolkitKind,
		ToolkitName:           pc.ToolkitName,
		Connection:            pc.Connection,
		Parameters:            params,
		Success:               success,
		ErrorMessage:          errorMsg,
		DurationMS:            duration.Milliseconds(),
		ResponseChars:         chars,
		ResponseTokenEstimate: tokens,
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

// calculateResponseSize computes the total character count and estimated token
// count from an MCP tool call result. Returns (0, 0) if err is non-nil or
// the result is not a CallToolResult.
func calculateResponseSize(result mcp.Result, err error) (chars, tokens int) {
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

	return total, total / estimatedCharsPerToken
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
