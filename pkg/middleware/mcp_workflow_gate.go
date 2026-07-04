package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrCategorySearchRequired is the error category for search-first gate violations.
const ErrCategorySearchRequired = "search_required"

// MCPWorkflowGateMiddleware creates MCP protocol-level middleware that enforces
// the search-first gate: a query tool call is refused until a discovery tool
// (search, by default) has been called at least once in the session. Once
// discovery has occurred, the gate stays open for the life of the session.
//
// It is modeled on MCPSessionGateMiddleware: on a violation it short-circuits
// with a SEARCH_REQUIRED error result and never invokes the underlying tool
// handler, so the query does not execute.
//
// This middleware must be positioned INNER to MCPToolCallMiddleware so that
// PlatformContext (with SessionID and ToolName) is available and the current
// tool call has already been recorded on the tracker. It should be positioned
// OUTER to audit and enrichment so that gated calls never reach those layers.
func MCPWorkflowGateMiddleware(tracker *SessionWorkflowTracker) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			pc := GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, method, req)
			}
			if errResult := checkWorkflowGate(tracker, pc); errResult != nil {
				return errResult, nil
			}
			return next(ctx, method, req)
		}
	}
}

// checkWorkflowGate evaluates whether a query tool call should proceed or be
// gated. It returns nil when the call is allowed and an error result when the
// session has not yet performed discovery.
func checkWorkflowGate(tracker *SessionWorkflowTracker, pc *PlatformContext) mcp.Result {
	// Only query tools are gated; everything else passes through.
	if !tracker.IsQueryTool(pc.ToolName) {
		return nil
	}
	// Once discovery has happened in the session, the gate stays open.
	if tracker.HasPerformedDiscovery(pc.SessionID) {
		return nil
	}

	slog.Warn("workflow gate: query tool called before search",
		"tool", pc.ToolName,
		"session_id", pc.SessionID,
		"user_id", pc.UserID,
	)
	return createWorkflowGateError(pc.ToolName)
}

// createWorkflowGateError builds a SEARCH_REQUIRED error result.
func createWorkflowGateError(blockedTool string) mcp.Result {
	msg := fmt.Sprintf(
		"SEARCH_REQUIRED: You must call search before using %s. search discovers the "+
			"table's business context (descriptions, owners, tags, glossary terms, prior "+
			"insights) and any access restrictions you need before running queries. "+
			"Call search first, then retry your query.",
		blockedTool,
	)

	// Build the full self-describing contract here: the workflow gate
	// short-circuits before the error-contract normalizer (it is registered
	// outer to it), so it must emit the {code, category, message, hint}
	// envelope itself rather than rely on enrichment.
	return BuildErrorResult(NewToolError(
		CodeSearchRequired, ErrCategorySearchRequired, msg,
		"Call search first, then retry. This is a workflow requirement, not a platform outage.",
	))
}
