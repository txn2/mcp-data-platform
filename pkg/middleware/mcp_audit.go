package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// redactedPlaceholder is the sentinel stored in place of a redacted argument
// value. It matches audit.SanitizeParameters' baseline sentinel so a redacted
// value looks the same whether it was caught by the configured redact_keys here
// or by the store adapter's built-in sensitive-key list.
const redactedPlaceholder = "[REDACTED]"

// auditParamPolicy governs how tool-call parameters are captured on audit
// events. It is applied in the middleware, before the event leaves the request
// path, so a redacted or dropped value is never enqueued or stored (issue #898).
type auditParamPolicy struct {
	// logParameters, when false, drops the whole Parameters map (stored null).
	logParameters bool
	// redactKeys holds lowercased top-level argument keys whose values are
	// replaced with redactedPlaceholder. Empty means no configured redaction.
	redactKeys map[string]struct{}
}

// defaultAuditParamPolicy is the policy applied when no options are supplied:
// parameters are captured and nothing is redacted.
func defaultAuditParamPolicy() auditParamPolicy {
	return auditParamPolicy{logParameters: true, redactKeys: map[string]struct{}{}}
}

// AuditOption configures MCPAuditMiddleware's parameter-capture policy.
type AuditOption func(*auditParamPolicy)

// WithParameterLogging controls whether tool-call arguments are captured on the
// audit event. When false, the event's Parameters field is stored as null
// (issue #898).
func WithParameterLogging(enabled bool) AuditOption {
	return func(p *auditParamPolicy) { p.logParameters = enabled }
}

// WithRedactKeys registers top-level argument keys whose values are replaced
// with "[REDACTED]" before the audit event is built. Matching is
// case-insensitive; nested keys are not matched, by design (issue #898).
// Keys are trimmed and lowercased so a stray space in the config (e.g.
// "password ") still redacts rather than silently storing the value verbatim.
func WithRedactKeys(keys []string) AuditOption {
	return func(p *auditParamPolicy) {
		for _, k := range keys {
			norm := strings.ToLower(strings.TrimSpace(k))
			if norm == "" {
				continue
			}
			p.redactKeys[norm] = struct{}{}
		}
	}
}

// applyParamPolicy enforces the parameter policy on freshly extracted
// arguments: it drops the whole map when parameter logging is disabled, and
// otherwise replaces the values of configured top-level keys (case-insensitive)
// with the redaction sentinel. The map is safe to mutate in place because
// extractMCPParameters returns a fresh map per call.
func applyParamPolicy(params map[string]any, policy auditParamPolicy) map[string]any {
	if !policy.logParameters {
		return nil
	}
	for k, v := range params {
		if _, ok := policy.redactKeys[strings.ToLower(k)]; ok {
			params[k] = redactedPlaceholder
			continue
		}
		params[k] = boundValue(v)
	}
	return params
}

// maxAuditValueBytes bounds one captured argument value. It is generous on
// purpose: a query, a prompt, or a path is worth recording in full, and none of
// them is anywhere near this size.
const maxAuditValueBytes = 16 << 10

// boundValue replaces an argument value too large to belong in an audit row
// with a note of its size.
//
// An audit row records what was called, not what was carried. Some tools take a
// payload as an argument — an object body written to storage, a file uploaded —
// and a managed script delivering a report writes one on every scheduled fire.
// Storing those verbatim puts a copy of the data in the audit table (and in the
// async writer's queue on the way there), which is both a size problem and a
// second, unintended copy of content whose access is governed elsewhere.
func boundValue(v any) any {
	s, ok := v.(string)
	if !ok || len(s) <= maxAuditValueBytes {
		return v
	}
	return fmt.Sprintf("[TRUNCATED: %d bytes]", len(s))
}

// MCPAuditMiddleware creates MCP protocol-level middleware that logs tool calls
// for auditing purposes.
//
// This middleware intercepts tools/call requests and:
//  1. Records the start time
//  2. Executes the tool handler
//  3. Gets the PlatformContext (set by MCPToolCallMiddleware)
//  4. Builds an audit event with all captured information
//  5. Hands the event to the logger's Log call
//
// The middleware itself spawns no goroutine (issue #884, which replaced a
// per-call detached goroutine that grew without bound under a stalled store);
// how Log behaves depends on the configured delivery mode. With the default
// async writer (pkg/audit.AsyncWriter) Log enqueues and returns immediately.
// With the sync writer (audit.delivery: sync) Log performs the store write
// inline, so it blocks the tool call for up to the per-write timeout — this is
// the intended backpressure of the durable mode. Either writer bounds the write
// with its own timeout and never fails the tool call.
//
// Parameter capture is governed by the options: WithParameterLogging drops the
// Parameters field entirely, and WithRedactKeys masks named top-level argument
// values. Both are applied here, where the event is built, so a sensitive value
// never leaves the request path (issue #898).
func MCPAuditMiddleware(logger AuditLogger, opts ...AuditOption) mcp.Middleware {
	policy := defaultAuditParamPolicy()
	for _, opt := range opts {
		opt(&policy)
	}
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
			}, policy)

			// Hand the event to the logger. context.Background is intentional:
			// the async writer ignores this context, and the sync writer (which
			// performs the store write inline) must not have its write canceled
			// when the request ends — shutdown cancellation flows through the
			// writer's Close, not the request context. Depending on delivery
			// mode Log either enqueues (async) or writes inline within its
			// per-write timeout (sync); neither fails the tool call.
			if err := logger.Log(context.Background(), event); err != nil {
				slog.Error("failed to log audit event",
					"error", err,
					"tool", event.ToolName,
					"user_id", event.UserID,
					"request_id", event.RequestID,
				)
			}

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
// The parameter policy governs whether and how the request arguments are
// captured on the event.
func buildMCPAuditEvent(pc *PlatformContext, info auditCallInfo, policy auditParamPolicy) AuditEvent {
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

	// Extract parameters from request, then apply the capture policy
	// (drop-all or per-key redaction) before the values reach the event.
	params := applyParamPolicy(extractMCPParameters(info.Request), policy)

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
