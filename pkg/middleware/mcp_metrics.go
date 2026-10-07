package middleware

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// MCPMetricsMiddleware records Prometheus metrics for every tool call
// that reaches the middleware chain, a call the platform refuses before
// the handler included.
//
// Chain position: the middleware is OUTER to MCPToolCallMiddleware and the
// gates, so a call refused by authentication, authorization, a gate or the
// rate limiter is counted under its status category (#1892). It attaches
// the PlatformContext the auth middleware then fills in
// (ensurePlatformContext) and reads the tool name, toolkit kind and
// persona off it after the call returns. It is OUTER to any handler so
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

	ctx, pc := ensurePlatformContext(ctx, req)
	start := time.Now()
	result, err := next(ctx, method, req)
	duration := time.Since(start)

	attrs := toolCallAttrs(pc, result, err)
	metrics.RecordToolCall(ctx, attrs, duration)

	// Enrichment runs inner to this middleware, so pc.EnrichmentBytes is set
	// on the shared PlatformContext by the time next() returns (issue #761).
	if pc.EnrichmentBytes > 0 {
		metrics.RecordEnrichmentBytes(ctx, attrs, pc.EnrichmentBytes)
	}
	return result, err
}

// toolCallAttrs derives the bounded metric labels from the PlatformContext
// the auth middleware filled in and the (result, err) pair. The tool label
// is the registered name, or observability.ToolLabelUnregistered when no
// toolkit registers the name the caller sent, so a caller cannot mint a
// series per invented name (#1892); a call that never carried a name (a
// malformed request) records MetricLabelUnknown.
func toolCallAttrs(pc *PlatformContext, result mcp.Result, err error) observability.ToolCallAttrs {
	tool := pc.ToolName
	switch {
	case pc.ToolUnregistered:
		tool = observability.ToolLabelUnregistered
	case tool == "":
		tool = observability.MetricLabelUnknown
	}

	isToolError, errCategory := toolResultErrorInfo(result)
	return observability.ToolCallAttrs{
		Tool:           tool,
		ToolkitKind:    pc.ToolkitKind,
		Persona:        pc.PersonaName,
		StatusCategory: observability.ClassifyToolCallResult(err, isToolError, errCategory),
		Source:         pc.Source,
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
