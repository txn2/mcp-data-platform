package middleware

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Span attribute keys for the tool-call span. The bounded keys mirror
// the metric label keys (tool, toolkit_kind, persona, status_category);
// the rest are HIGH-cardinality fields (user, session, request id,
// connection) that are deliberately kept OFF Prometheus labels and live
// here instead, where per-request detail is the whole point.
const (
	spanAttrTool          = "mcp.tool"
	spanAttrToolkitKind   = "mcp.toolkit_kind"
	spanAttrToolkitName   = "mcp.toolkit_name"
	spanAttrConnection    = "mcp.connection"
	spanAttrPersona       = "mcp.persona"
	spanAttrUserID        = "mcp.user_id"
	spanAttrUserEmail     = "mcp.user_email"
	spanAttrSessionID     = "mcp.session_id"
	spanAttrRequestID     = "mcp.request_id"
	spanAttrTransport     = "mcp.transport"
	spanAttrSource        = "mcp.source"
	spanAttrEnrichApplied = "mcp.enrichment_applied"
	spanAttrEnrichMode    = "mcp.enrichment_mode"
)

// MCPTracingMiddleware records an OpenTelemetry span for every tool call.
//
// Chain position: like MCPMetricsMiddleware it must be INNER to
// MCPToolCallMiddleware so the PlatformContext (tool, toolkit, persona,
// user, session) is available, and OUTER to the audit/rule/enrichment
// steps and the handler so the span's duration covers all of them. The
// span becomes the parent of every downstream span the toolkit adapters
// create (Trino/DataHub/S3/OAuth/enrichment) via context propagation, so
// one tool call yields a single flame graph.
//
// The middleware short-circuits on a nil/disabled *observability.Tracer
// so it is safe to register unconditionally; when tracing is off the
// extra hop is a single nil-pointer compare per request.
func MCPTracingMiddleware(tracer *observability.Tracer) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall || !tracer.Enabled() {
				return next(ctx, method, req)
			}
			return traceToolCall(ctx, method, req, next, tracer)
		}
	}
}

// spanNameToolCall is the fixed, low-cardinality root span name for every
// tool call. The specific tool is carried on the mcp.tool attribute (set
// in setToolSpanAttributes), not the span name, so operators can query all
// tool calls by a stable name and span-name cardinality stays at one. This
// follows the OTel convention of low-cardinality span names + the operation
// on an attribute.
const spanNameToolCall = "tool_call"

// traceToolCall wraps a tool call in a span and records the outcome. Split
// out so MCPTracingMiddleware stays under the complexity ceiling.
func traceToolCall(
	ctx context.Context,
	method string,
	req mcp.Request,
	next mcp.MethodHandler,
	tracer *observability.Tracer,
) (mcp.Result, error) {
	ctx, span := tracer.Start(ctx, spanNameToolCall, trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	result, err := next(ctx, method, req)

	// Read the PlatformContext AFTER the call so enrichment fields the
	// inner enrichment middleware populated are captured on the span.
	setToolSpanAttributes(span, GetPlatformContext(ctx))
	isToolError, errCategory := toolResultErrorInfo(result)
	status := observability.ClassifyToolCallResult(err, isToolError, errCategory)
	observability.SetSpanStatus(span, status, err)
	return result, err
}

// setToolSpanAttributes copies the request's identifying fields from the
// PlatformContext onto the span. A nil PlatformContext leaves the span
// with no attributes rather than panicking — the call is still traced,
// which is exactly the case (auth rejected before context population)
// operators want visible.
func setToolSpanAttributes(span trace.Span, pc *PlatformContext) {
	if pc == nil {
		return
	}
	span.SetAttributes(
		attribute.String(spanAttrTool, pc.ToolName),
		attribute.String(spanAttrToolkitKind, pc.ToolkitKind),
		attribute.String(spanAttrToolkitName, pc.ToolkitName),
		attribute.String(spanAttrConnection, pc.Connection),
		attribute.String(spanAttrPersona, pc.PersonaName),
		attribute.String(spanAttrUserID, pc.UserID),
		attribute.String(spanAttrUserEmail, pc.UserEmail),
		attribute.String(spanAttrSessionID, pc.SessionID),
		attribute.String(spanAttrRequestID, pc.RequestID),
		attribute.String(spanAttrTransport, pc.Transport),
		attribute.String(spanAttrSource, pc.Source),
		attribute.Bool(spanAttrEnrichApplied, pc.EnrichmentApplied),
		attribute.String(spanAttrEnrichMode, pc.EnrichmentMode),
	)
}
