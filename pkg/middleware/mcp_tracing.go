package middleware

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Span attribute keys for the tool-call span. The bounded keys mirror
// the metric label keys (tool, toolkit_kind, persona, status_category);
// the rest are HIGH-cardinality fields (user, session, request id,
// connection) that are deliberately kept OFF Prometheus labels and live
// here instead, where per-request detail is the whole point.
//
// The mcp.tool and mcp.session_id keys predate the MCP semantic conventions
// and are kept beside the convention keys below for one release (#1893);
// a query on them should move to gen_ai.tool.name and mcp.session.id.
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

// The MCP semantic convention keys (GenAI conventions, status Development),
// emitted on every tool-call span beside the platform's own (#1893).
// error.type is the bounded error category of a failed call; a succeeded
// call does not carry it, as the convention requires.
const (
	spanAttrMethodName      = "mcp.method.name"
	spanAttrGenAIToolName   = "gen_ai.tool.name"
	spanAttrGenAIOperation  = "gen_ai.operation.name"
	spanAttrConvSessionID   = "mcp.session.id"
	spanAttrProtocolVersion = "mcp.protocol.version"
	spanAttrErrorType       = "error.type"

	genAIOperationExecuteTool = "execute_tool"
)

// The _meta keys a caller carries trace context in, where the MCP semantic
// conventions put it; the same names as the W3C headers.
const (
	metaKeyTraceparent = "traceparent"
	metaKeyTracestate  = "tracestate"
)

// MCPTracingMiddleware records an OpenTelemetry span for every tool call,
// a call the platform refuses before the handler included.
//
// Chain position: like MCPMetricsMiddleware it is OUTER to
// MCPToolCallMiddleware and the gates, so a call refused by authentication,
// authorization, a gate or the rate limiter still yields its span (#1892).
// It attaches the PlatformContext the auth middleware then fills in
// (ensurePlatformContext), and reads it after the call returns, so the
// span carries the tool, toolkit, persona, user and session the call
// resolved to. It is OUTER to the audit/rule/enrichment steps and the
// handler so the span's duration covers all of them, and the span becomes
// the parent of every downstream span the toolkit adapters create
// (Trino/DataHub/S3/OAuth/enrichment) via context propagation, so one tool
// call yields a single flame graph.
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

// The root span is named as the MCP semantic conventions say,
// "{mcp.method.name} {target}": "tools/call trino_query" (#1893). The target
// is the bounded tool name (boundedToolName), so a caller cannot mint a span
// name per invented tool, and it is set once the call has resolved it; the
// span opens under the method alone. Before #1893 the name was the fixed
// "tool_call"; a query on that name should move to the method prefix.
func toolCallSpanName(pc *PlatformContext) string {
	return methodToolsCall + " " + boundedToolName(pc)
}

// traceToolCall wraps a tool call in a span and records the outcome. Split
// out so MCPTracingMiddleware stays under the complexity ceiling.
func traceToolCall(
	ctx context.Context,
	method string,
	req mcp.Request,
	next mcp.MethodHandler,
	tracer *observability.Tracer,
) (mcp.Result, error) {
	ctx, pc := ensurePlatformContext(ctx, req)
	ctx = inboundTraceContext(ctx, req)
	ctx, span := tracer.Start(ctx, methodToolsCall, trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	result, err := next(ctx, method, req)

	// Read the PlatformContext AFTER the call: the auth middleware and the
	// enrichment middleware inner to this one have written into it by now.
	span.SetName(toolCallSpanName(pc))
	setToolSpanAttributes(span, pc, tracer.IncludeUserEmail(), protocolVersion(req))
	isToolError, errCategory := toolResultErrorInfo(result)
	status := observability.ClassifyToolCallResult(err, isToolError, errCategory)
	if status != observability.StatusOK {
		span.SetAttributes(attribute.String(spanAttrErrorType, errorType(status, errCategory)))
	}
	observability.SetSpanStatus(span, status, spanError(result, err))
	return result, err
}

// errorType is the bounded value error.type carries on a failed call: the
// error contract's category when the tool reported one, else the status
// category the call was classified under.
func errorType(status, errCategory string) string {
	if errCategory != "" {
		return errCategory
	}
	return status
}

// inboundTraceContext continues the caller's trace (#1893): the W3C
// traceparent and tracestate are read from params._meta, where the MCP
// semantic conventions carry them, and else from the HTTP request's headers
// (req.GetExtra().Header, nil on stdio and on an in-process session). With
// neither, or an invalid header, ctx is returned as it was and the span the
// caller opens is a root; with a sampled parent, ParentBased keeps the whole
// trace. _meta wins over the header: it is the one the caller wrote for this
// request, where a header may be the client library's own.
func inboundTraceContext(ctx context.Context, req mcp.Request) context.Context {
	prop := otel.GetTextMapPropagator()
	if extra := req.GetExtra(); extra != nil && extra.Header != nil {
		ctx = prop.Extract(ctx, propagation.HeaderCarrier(extra.Header))
	}
	if carrier := metaTraceCarrier(req); carrier != nil {
		ctx = prop.Extract(ctx, carrier)
	}
	return ctx
}

// metaTraceCarrier is the request's _meta read as a propagation carrier: the
// traceparent and tracestate entries when they are strings, nil otherwise.
// Guarded like extractProgressToken against a typed-nil params value.
func metaTraceCarrier(req mcp.Request) (carrier propagation.MapCarrier) {
	defer func() {
		if r := recover(); r != nil {
			carrier = nil
		}
	}()
	params := req.GetParams()
	if params == nil {
		return nil
	}
	meta := params.GetMeta()
	for _, key := range []string{metaKeyTraceparent, metaKeyTracestate} {
		if v, ok := meta[key].(string); ok && v != "" {
			if carrier == nil {
				carrier = propagation.MapCarrier{}
			}
			carrier[key] = v
		}
	}
	return carrier
}

// protocolVersion is the MCP protocol revision the session negotiated, or
// empty before initialization or off a session the platform did not open.
func protocolVersion(req mcp.Request) string {
	ss := extractServerSession(req)
	if ss == nil {
		return ""
	}
	if params := ss.InitializeParams(); params != nil {
		return params.ProtocolVersion
	}
	return ""
}

// spanError is the error a tool call's span records: the protocol-level
// error when there is one, else the error a tool-level failure carries in
// its result (a refusal's PlatformError, an upstream's message). SetSpanStatus
// redacts it before it reaches the span.
func spanError(result mcp.Result, err error) error {
	if err != nil {
		return err
	}
	if callResult, ok := result.(*mcp.CallToolResult); ok && callResult != nil && callResult.IsError {
		return callResult.GetError() //nolint:wrapcheck // the tool's own error, recorded as it is after redaction
	}
	return nil
}

// maxSpanToolNameBytes bounds the tool name a span carries: the name is the
// caller's to choose, and a span attribute is stored as sent.
const maxSpanToolNameBytes = 128

// setToolSpanAttributes copies the request's identifying fields from the
// PlatformContext onto the span, under the platform's keys and the MCP
// semantic convention keys (#1893). The caller's email address is personal
// data and goes on the span only when the deployment opted in
// (OTEL_TRACES_INCLUDE_USER_EMAIL, #1892); the user id is always there.
func setToolSpanAttributes(span trace.Span, pc *PlatformContext, includeEmail bool, protocol string) {
	tool := logsan.Excerpt(pc.ToolName, maxSpanToolNameBytes)
	attrs := []attribute.KeyValue{
		attribute.String(spanAttrMethodName, methodToolsCall),
		attribute.String(spanAttrGenAIOperation, genAIOperationExecuteTool),
		attribute.String(spanAttrGenAIToolName, tool),
		attribute.String(spanAttrConvSessionID, pc.SessionID),
		attribute.String(spanAttrProtocolVersion, protocol),
		attribute.String(spanAttrTool, tool),
		attribute.String(spanAttrToolkitKind, pc.ToolkitKind),
		attribute.String(spanAttrToolkitName, pc.ToolkitName),
		attribute.String(spanAttrConnection, pc.Connection),
		attribute.String(spanAttrPersona, pc.PersonaName),
		attribute.String(spanAttrUserID, pc.UserID),
		attribute.String(spanAttrSessionID, pc.SessionID),
		attribute.String(spanAttrRequestID, pc.RequestID),
		attribute.String(spanAttrTransport, pc.Transport),
		attribute.String(spanAttrSource, pc.Source),
		attribute.Bool(spanAttrEnrichApplied, pc.EnrichmentApplied),
		attribute.String(spanAttrEnrichMode, pc.EnrichmentMode),
	}
	if includeEmail {
		attrs = append(attrs, attribute.String(spanAttrUserEmail, pc.UserEmail))
	}
	span.SetAttributes(attrs...)
}
