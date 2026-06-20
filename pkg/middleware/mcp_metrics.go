package middleware

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// MCPMetricsMiddleware records Prometheus metrics for every tool call
// that reaches the middleware chain.
//
// Chain position: the middleware must be INNER to MCPToolCallMiddleware
// so the PlatformContext (carrying tool name, toolkit kind, persona) is
// available in the ctx the recorder sees, and OUTER to any handler so
// the measured duration covers semantic enrichment, rule enforcement,
// and the toolkit handler itself — i.e. what a Grafana dashboard
// labels "tool call latency" should actually mean.
//
// The middleware short-circuits on nil *observability.Metrics so it is
// safe to register unconditionally; when metrics are disabled the
// extra hop is a single nil-pointer compare per request.
func MCPMetricsMiddleware(metrics *observability.Metrics) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall || !metrics.Enabled() {
				return next(ctx, method, req)
			}
			return recordToolCall(ctx, method, req, next, metrics)
		}
	}
}

// recordToolCall wraps a tool call with in-flight gauge maintenance
// and a single histogram/counter observation. Splitting it out keeps
// MCPMetricsMiddleware under revive's cognitive-complexity ceiling.
func recordToolCall(
	ctx context.Context,
	method string,
	req mcp.Request,
	next mcp.MethodHandler,
	metrics *observability.Metrics,
) (mcp.Result, error) {
	metrics.IncInflightToolCalls(ctx)
	defer metrics.DecInflightToolCalls(ctx)

	start := time.Now()
	result, err := next(ctx, method, req)
	duration := time.Since(start)

	pc := GetPlatformContext(ctx)
	attrs := toolCallAttrs(pc, result, err)
	metrics.RecordToolCall(ctx, attrs, duration)
	return result, err
}

// toolCallAttrs derives the bounded metric labels from the
// PlatformContext (when set by MCPToolCallMiddleware) and the
// (result, err) pair. A missing PlatformContext is recorded with
// empty tool/toolkit_kind/persona so the call is still counted —
// dropping it would silently hide auth-rejected calls that never got
// far enough to populate the context, which is exactly the case
// operators want visible.
func toolCallAttrs(pc *PlatformContext, result mcp.Result, err error) observability.ToolCallAttrs {
	tool, toolkitKind, persona := "", "", ""
	if pc != nil {
		tool = pc.ToolName
		toolkitKind = pc.ToolkitKind
		persona = pc.PersonaName
	}

	isToolError, errCategory := toolResultErrorInfo(result)
	return observability.ToolCallAttrs{
		Tool:           tool,
		ToolkitKind:    toolkitKind,
		Persona:        persona,
		StatusCategory: observability.ClassifyToolCallResult(err, isToolError, errCategory),
	}
}

// toolResultErrorInfo reports whether an MCP result is a tool-level error
// and, if so, its category (from the error's CategorizedError, when
// present). Shared by the metrics and tracing middleware so both classify
// a tool failure identically. A non-CallToolResult or a success result
// yields (false, "").
func toolResultErrorInfo(result mcp.Result) (isToolError bool, category string) {
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil || !callResult.IsError {
		return false, ""
	}
	if getErr := callResult.GetError(); getErr != nil {
		return true, ErrorCategory(getErr)
	}
	return true, ""
}
