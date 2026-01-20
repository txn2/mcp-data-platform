package middleware

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AuditLogger logs tool calls for auditing.
type AuditLogger interface {
	// Log records an audit event.
	Log(ctx context.Context, event AuditEvent) error
}

// AuditEvent represents an auditable event.
type AuditEvent struct {
	Timestamp    time.Time      `json:"timestamp"`
	RequestID    string         `json:"request_id"`
	UserID       string         `json:"user_id"`
	UserEmail    string         `json:"user_email"`
	Persona      string         `json:"persona"`
	ToolName     string         `json:"tool_name"`
	ToolkitKind  string         `json:"toolkit_kind"`
	ToolkitName  string         `json:"toolkit_name"`
	Connection   string         `json:"connection"`
	Parameters   map[string]any `json:"parameters"`
	Success      bool           `json:"success"`
	ErrorMessage string         `json:"error_message,omitempty"`
	DurationMS   int64          `json:"duration_ms"`
}

// AuditMiddleware creates middleware that logs tool calls.
func AuditMiddleware(logger AuditLogger) Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			startTime := time.Now()

			// Run the handler
			result, err := next(ctx, request)

			// Calculate duration
			duration := time.Since(startTime)

			// Get platform context
			pc := GetPlatformContext(ctx)
			if pc == nil {
				return result, err
			}

			// Determine success
			success := err == nil && (result == nil || !result.IsError)
			errorMsg := ""
			if err != nil {
				errorMsg = err.Error()
			} else if result != nil && result.IsError {
				errorMsg = extractErrorMessage(result)
			}

			// Extract parameters
			params := extractParameters(request)

			// Create audit event
			event := AuditEvent{
				Timestamp:    startTime,
				RequestID:    pc.RequestID,
				UserID:       pc.UserID,
				UserEmail:    pc.UserEmail,
				Persona:      pc.PersonaName,
				ToolName:     pc.ToolName,
				ToolkitKind:  pc.ToolkitKind,
				ToolkitName:  pc.ToolkitName,
				Connection:   pc.Connection,
				Parameters:   params,
				Success:      success,
				ErrorMessage: errorMsg,
				DurationMS:   duration.Milliseconds(),
			}

			// Log asynchronously to not block the response
			go func() {
				_ = logger.Log(context.Background(), event)
			}()

			return result, err
		}
	}
}

// extractParameters extracts parameters from a request.
func extractParameters(request mcp.CallToolRequest) map[string]any {
	if len(request.Params.Arguments) == 0 {
		return nil
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &params); err != nil {
		return nil
	}
	return params
}

// extractErrorMessage extracts the error message from a result.
func extractErrorMessage(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
		return textContent.Text
	}
	return ""
}

// NoopAuditLogger discards all audit events.
type NoopAuditLogger struct{}

// Log does nothing.
func (n *NoopAuditLogger) Log(_ context.Context, _ AuditEvent) error {
	return nil
}
