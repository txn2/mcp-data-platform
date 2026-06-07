package middleware

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPErrorContractMiddleware guarantees that every tools/call error result is
// self-describing and uniform (issue #539). It is the backstop that makes the
// error surface consistent by construction: source-categorized errors pass
// through untouched, while any bare IsError result (a toolkit that has not been
// upgraded, an SDK input-validation failure, an upstream tool error) is enriched
// into the {code, category, message, hint} contract so an agent never receives
// an opaque, undifferentiated string it cannot branch on.
//
// It also recovers a panicking tool call into an internal-category error result,
// so a handler bug surfaces as a categorized, attributable failure instead of a
// dropped connection.
//
// Placement: this middleware must run inner to MCPAuditMiddleware and
// MCPMetricsMiddleware so they observe the normalized category, and outer to the
// tool handlers whose results it normalizes.
func MCPErrorContractMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			defer func() {
				if r := recover(); r != nil {
					result, err = recoverToInternalError(ctx, r), nil
				}
			}()
			result, err = next(ctx, method, req)
			if err != nil {
				// A non-nil err is a protocol-level (JSON-RPC) failure, not a tool
				// result; leave it for the SDK to encode.
				return result, err
			}
			return normalizeErrorResult(result), nil
		}
	}
}

// recoverToInternalError logs a recovered panic and returns a categorized
// internal-error result so a handler bug surfaces as an attributable failure
// instead of a dropped connection.
func recoverToInternalError(ctx context.Context, r any) *mcp.CallToolResult {
	reqID := ""
	if pc := GetPlatformContext(ctx); pc != nil {
		reqID = pc.RequestID
	}
	slog.Error("tool call panicked; returning categorized internal error",
		"panic", r, "request_id", reqID)
	return BuildErrorResult(InternalError("the tool call failed unexpectedly"))
}

// normalizeErrorResult enriches a bare IsError result into the structured
// contract, leaving non-errors and already-structured results untouched.
func normalizeErrorResult(result mcp.Result) mcp.Result {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || !ctr.IsError || hasErrorEnvelope(ctr) {
		return result
	}
	return enrichBareErrorResult(ctr)
}

// enrichBareErrorResult promotes an IsError result that lacks the structured
// contract into one that carries it, preserving the original message. It adopts
// any category already stashed on the result (via SetError) and otherwise
// defaults to the generic tool_error category, so the result is self-describing
// even when the source could not classify it.
func enrichBareErrorResult(ctr *mcp.CallToolResult) *mcp.CallToolResult {
	msg := unwrapLegacyErrorJSON(extractMCPErrorMessage(ctr))
	if msg == "" {
		msg = "the tool call failed"
	}
	pe := &PlatformError{
		Code:     CodeToolError,
		Category: ErrCategoryToolError,
		Message:  msg,
	}
	if category := ErrorCategory(ctr.GetError()); category != "" {
		pe.Category = category
	}
	return BuildErrorResult(pe)
}

// unwrapLegacyErrorJSON returns the inner message from a toolkit error result
// whose text is the legacy `{"error":"..."}` envelope (the shape several
// toolkits emit via their local errorResult helper), so the normalized message
// is the human-readable text rather than a doubly-encoded JSON string. Any other
// text is returned unchanged.
func unwrapLegacyErrorJSON(text string) string {
	var legacy struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &legacy); err == nil && legacy.Error != "" {
		return legacy.Error
	}
	return text
}
