package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/observability"
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

			// Log asynchronously to not block the response; context.Background is
			// intentional — the audit write must not be canceled when the request ends.
			go func() { // #nosec G118 -- detached context is required for fire-and-forget audit logging
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
	// Determine success and error category
	success := info.Err == nil
	errorMsg := ""
	errorCategory := ""
	callResult, _ := info.Result.(*mcp.CallToolResult)
	if info.Err != nil {
		errorMsg = info.Err.Error()
		errorCategory = ErrorCategory(info.Err)
	} else if callResult != nil && callResult.IsError {
		success = false
		// Prefer the structured error's bare message (no code/hint suffix) so
		// the audit row stays terse; the self-describing text and structured
		// envelope are for the agent, not the audit log. Fall back to the
		// rendered text content for results that carry no stashed error.
		if getErr := callResult.GetError(); getErr != nil {
			errorMsg = getErr.Error()
			errorCategory = ErrorCategory(getErr)
		} else {
			errorMsg = extractMCPErrorMessage(callResult)
		}
	}

	// Audit-outcome _meta override. Toolkits that proxy external
	// services (apigateway) set this on every result so the audit row
	// reflects the real upstream outcome rather than just "the MCP
	// tool returned." When present and not 'ok', it overrides the
	// IsError-derived success/category. Upstream 4xx/5xx come through
	// here even though IsError stays false (correct gateway wire
	// semantics: the gateway succeeded at proxying; the upstream
	// returned what it returned). See issue #432.
	//
	// The meta message takes precedence over the IsError-branch's
	// errorMsg because the IsError branch reads the full JSON-encoded
	// tool result text, which for the apigateway is a multi-field
	// JSON envelope ({"status":0,"duration_ms":...,"error":"..."}).
	// The meta message is the concise scrubbed error string the
	// toolkit explicitly stamped for audit consumption, preferable
	// for grep/dashboards.
	if outcome, message := readAuditOutcomeMeta(callResult); outcome != "" && outcome != observability.OutcomeOK {
		success = false
		errorCategory = outcome
		if message != "" {
			errorMsg = message
		}
	}

	// Extract parameters from request
	params := extractMCPParameters(info.Request)

	chars, blocks := calculateResponseSize(info.Result, info.Err)
	reqChars := calculateRequestSize(info.Request)

	return AuditEvent{
		Timestamp:             info.StartTime,
		RequestID:             pc.RequestID,
		SessionID:             pc.SessionID,
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
		ErrorCategory:         errorCategory,
		DurationMS:            info.Duration.Milliseconds(),
		ResponseChars:         chars,
		RequestChars:          reqChars,
		ContentBlocks:         blocks,
		Transport:             pc.Transport,
		Source:                pc.Source,
		EnrichmentApplied:     pc.EnrichmentApplied,
		EnrichmentTokensFull:  pc.EnrichmentTokensFull,
		EnrichmentTokensDedup: pc.EnrichmentTokensDedup,
		EnrichmentMode:        pc.EnrichmentMode,
		EnrichmentMatchKind:   pc.EnrichmentMatchKind,
		Authorized:            pc.Authorized,
		EventKind:             string(audit.EventKindForToolkit(pc.ToolkitKind)),
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

// readAuditOutcomeMeta extracts the audit outcome and (optional)
// human-readable message from a CallToolResult's _meta map. Returns
// empty strings when the result is nil, the Meta is nil, the keys
// are absent, or the values are not strings, so the caller's check
// for outcome == "" cleanly skips the override path.
func readAuditOutcomeMeta(result *mcp.CallToolResult) (outcome, message string) {
	if result == nil || result.Meta == nil {
		return "", ""
	}
	outcome, _ = result.Meta[observability.MetaAuditOutcome].(string)
	message, _ = result.Meta[observability.MetaAuditOutcomeMessage].(string)
	return outcome, message
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
