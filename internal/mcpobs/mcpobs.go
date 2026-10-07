// Package mcpobs is what every MCP request carries for observation (#1889,
// #1893): the caller's W3C trace context read from params._meta or the HTTP
// headers, the protocol revision the session negotiated, the MCP semantic
// convention attribute keys, and the observer of every method other than
// tools/call. A tools/call has its own observers in pkg/middleware
// (MCPTracingMiddleware, MCPMetricsMiddleware), which read the same trace
// context through this package so the two never disagree about it.
package mcpobs

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

// MethodToolsCall is the one MCP method this package's observer leaves to the
// tool-call observers.
const MethodToolsCall = "tools/call"

// The MCP semantic convention keys (GenAI conventions, status Development).
// error.type is the bounded error category of a failed request; a succeeded
// request does not carry it, as the convention requires.
const (
	AttrMethodName      = "mcp.method.name"
	AttrGenAIToolName   = "gen_ai.tool.name"
	AttrGenAIOperation  = "gen_ai.operation.name"
	AttrSessionID       = "mcp.session.id"
	AttrProtocolVersion = "mcp.protocol.version"
	AttrErrorType       = "error.type"

	// GenAIOperationExecuteTool is gen_ai.operation.name on a tools/call span.
	GenAIOperationExecuteTool = "execute_tool"
)

// The _meta keys a caller carries trace context in, where the MCP semantic
// conventions put it; the same names as the W3C headers.
const (
	metaKeyTraceparent = "traceparent"
	metaKeyTracestate  = "tracestate"
)

// TraceContext continues the caller's trace (#1893): the W3C traceparent and
// tracestate are read from params._meta, where the MCP semantic conventions
// carry them, and else from the HTTP request's headers (req.GetExtra().Header,
// nil on stdio and on an in-process session). With neither, or an invalid
// header, ctx is returned as it was and the span the caller opens is a root;
// with a sampled parent, ParentBased keeps the whole trace. _meta wins over
// the header: it is the one the caller wrote for this request, where a header
// may be the client library's own.
func TraceContext(ctx context.Context, req mcp.Request) context.Context {
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
// Guarded against a typed-nil params value, which GetMeta dereferences.
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

// ProtocolVersion is the MCP protocol revision the session negotiated, or
// empty before initialization or off a session the platform did not open.
func ProtocolVersion(req mcp.Request) string {
	ss := serverSession(req)
	if ss == nil {
		return ""
	}
	if params := ss.InitializeParams(); params != nil {
		return params.ProtocolVersion
	}
	return ""
}

// serverSession is the request's session as the server's, or nil for a
// request with none or with a session of another type (a test's fake).
func serverSession(req mcp.Request) (ss *mcp.ServerSession) {
	if req == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			ss = nil
		}
	}()
	sess := req.GetSession()
	if sess == nil {
		return nil
	}
	ss, _ = sess.(*mcp.ServerSession)
	return ss
}

// sessionID is the id of the session the request arrived on: the SDK's
// (the Mcp-Session-Id of a streamable session), else the one the
// session-aware handler stashed on the context (an SSE or stateless session,
// which the SDK does not surface), else empty. The same order the tool-call
// middleware resolves a session in.
func sessionID(ctx context.Context, req mcp.Request) string {
	if ss := serverSession(req); ss != nil {
		if id := ss.ID(); id != "" {
			return id
		}
	}
	return session.AwareSessionID(ctx)
}

// Middleware observes every MCP method other than tools/call: a server span
// named by the method, as the MCP semantic conventions say ("tools/list",
// "resources/read"), carrying mcp.method.name, mcp.session.id and
// mcp.protocol.version, with error.type on a failure; and
// mcp_requests_total / mcp_request_duration_seconds by method and status.
// It is the OUTERMOST receiving middleware, so the span and the duration
// cover the list decorators and the typing of the result as well as the
// SDK's own handler. A tools/call passes straight through to its own
// observers. Nil-safe on both: with neither enabled the hop is a method
// compare per request.
func Middleware(tracer *observability.Tracer, metrics *observability.Metrics) mcp.Middleware {
	o := observer{tracer: tracer, metrics: metrics}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == MethodToolsCall || (!tracer.Enabled() && !metrics.Enabled()) {
				return next(ctx, method, req)
			}
			return o.observe(ctx, method, req, next)
		}
	}
}

// observer is the tracer and recorder the middleware observes with.
type observer struct {
	tracer  *observability.Tracer
	metrics *observability.Metrics
}

func (o observer) observe(ctx context.Context, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	ctx = TraceContext(ctx, req)
	ctx, span := o.tracer.Start(ctx, method, trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	start := time.Now()
	result, err := next(ctx, method, req)
	d := time.Since(start)

	status := Status(err)
	span.SetAttributes(
		attribute.String(AttrMethodName, method),
		attribute.String(AttrSessionID, sessionID(ctx, req)),
		attribute.String(AttrProtocolVersion, ProtocolVersion(req)),
	)
	if status != observability.StatusOK {
		span.SetAttributes(attribute.String(AttrErrorType, status))
	}
	observability.SetSpanStatus(span, status, err)
	o.metrics.RecordMCPRequest(ctx, method, status, d)
	return result, err
}

// Status is the bounded status of a method's outcome: ok with no error;
// client_err for a JSON-RPC error the caller caused (an invalid request or
// params, a method that does not exist, a resource that does not exist,
// which the SDK reports as invalid params); internal_err otherwise.
// The resource-not-found code is the MCP one (-32002).
func Status(err error) string {
	if err == nil {
		return observability.StatusOK
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case jsonrpc.CodeInvalidRequest, jsonrpc.CodeMethodNotFound, jsonrpc.CodeInvalidParams:
			return observability.StatusClientErr
		}
	}
	return observability.StatusInternalErr
}
