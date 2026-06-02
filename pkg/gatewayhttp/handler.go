// Package gatewayhttp exposes the apigateway toolkit's invoke
// operation over plain REST so non-MCP HTTP clients (e.g. Apache
// NiFi's InvokeHTTP processor) can drive the same gateway connections
// that MCP callers use through api_invoke_endpoint.
//
// The handler is a thin shim: every request is routed through an
// in-memory MCP session against the platform's assembled server, so
// the existing authenticator, persona authorization, route policy,
// and audit middleware all apply to REST callers identically to MCP
// callers. No auth, audit, or policy logic is reimplemented here.
package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// RequestBodyLimit caps the inbound JSON body the REST handler will
// read. The api_invoke_endpoint contract already trims response
// bodies to a per-connection maximum; this limit bounds the
// request-side memory footprint when non-MCP callers POST through
// the gateway.
const RequestBodyLimit = 1 << 20 // 1 MiB

// Deps wires the REST gateway handler to the platform's assembled
// MCP server. The handler does not fork the auth or audit pipelines:
// every REST request goes through an in-memory MCP session so the
// existing authenticator, persona authorization, route policy, and
// audit middleware all apply.
type Deps struct {
	// MCPServer is the assembled MCP server (with middleware chain
	// already attached). Required.
	MCPServer *mcp.Server

	// Metrics records inbound request observations. Optional: nil
	// disables inbound instrumentation (the handler is returned
	// unwrapped, zero overhead).
	Metrics *observability.Metrics

	// Resolver maps (connection, method, path) to an OpenAPI
	// operationId for the metric label. Optional: nil yields
	// operation_id="unknown".
	Resolver OperationResolver

	// Identity maps the request auth context to a display identity for
	// the metric label. Optional: nil yields identity="unknown".
	Identity IdentityResolver

	// RawMaxBytes caps a single raw passthrough response on the
	// /invoke-raw route (issue #535). A response whose upstream
	// Content-Length exceeds this is rejected with 413 before any bytes
	// are streamed. 0 = no all-or-nothing cap (memory stays bounded
	// regardless because the raw path streams, never buffers).
	RawMaxBytes int64
}

// NewHandler returns an http.Handler that exposes
// api_invoke_endpoint as a connection-scoped REST resource:
//
//	POST /api/v1/gateway/{connection}/invoke
//
// Non-MCP callers POST a JSON body shaped like apigateway.InvokeInput
// (minus the connection field, which is taken from the URL) and
// receive an apigateway.InvokeOutput JSON body back. The upstream's
// HTTP status code is returned inside the body (InvokeOutput.Status);
// the HTTP status of the platform's own response only signals
// platform-level outcomes:
//
//	200 - the platform performed the upstream call (outcome in body)
//	400 - the request failed validation (method, path, body)
//	401 - no credential, or the credential was rejected
//	403 - persona or route policy denied the call
//	404 - the named connection is not registered
//	502 - the gateway could not reach the upstream (DNS, TCP, TLS, reset)
//	504 - the upstream call exceeded its deadline before responding
//
// 502 and 504 represent gateway-level failures (issue #432): the
// gateway tried to proxy and did not succeed. Upstream-level failures
// (the upstream responded with 4xx or 5xx) still flow as wire HTTP
// 200 with the upstream code embedded in InvokeOutput.Status, so HTTP
// clients can distinguish "the gateway is broken" from "the upstream
// is unhappy" using their built-in status-code routing.
func NewHandler(deps Deps) (http.Handler, error) {
	if deps.MCPServer == nil {
		return nil, errors.New("gatewayhttp: MCPServer is required")
	}
	mux := http.NewServeMux()
	h := &handler{mcpServer: deps.MCPServer, rawMaxBytes: deps.RawMaxBytes}
	// Register the metrics-wrapped handler on the route so the wrapper
	// sees the {connection} path value. withMetrics returns the handler
	// unwrapped when deps.Metrics is nil.
	mux.Handle("POST /api/v1/gateway/{connection}/invoke", withMetrics(http.HandlerFunc(h.invoke), deps))
	// invoke-raw streams the upstream body straight to the client with
	// bounded memory (issue #535), for retrieving large/binary bodies
	// through the gateway without buffering them into a JSON envelope.
	mux.Handle("POST /api/v1/gateway/{connection}/invoke-raw", withMetrics(http.HandlerFunc(h.invokeRaw), deps))
	return mux, nil
}

type handler struct {
	mcpServer   *mcp.Server
	rawMaxBytes int64
}

// invokeRequest is the JSON shape REST callers POST. It mirrors
// apigateway.InvokeInput without the Connection field, which is
// supplied via the URL path and overrides any value placed in the
// body.
type invokeRequest struct {
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	QueryParams    map[string]any    `json:"query_params,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           any               `json:"body,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// errorEnvelope matches the JSON body the apigateway toolkit emits
// for tool errors: {"error": "..."}. Keeping the same shape means an
// MCP tool error flowing through the in-memory transport reaches the
// REST caller unmodified.
type errorEnvelope struct {
	Error string `json:"error"`
}

func (h *handler) invoke(w http.ResponseWriter, r *http.Request) {
	connection := r.PathValue("connection")
	req, err := decodeInvokeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Surface method/path to the metrics middleware (if present) so it
	// can resolve the operationId label without re-reading the body.
	if m := getInvokeMeta(r.Context()); m != nil {
		m.method, m.path = req.Method, req.Path
	}

	session, cleanup, sessErr := h.connectInternalSession(r)
	if sessErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to MCP server")
		return
	}
	defer cleanup()

	result, callErr := session.CallTool(r.Context(), &mcp.CallToolParams{
		Name:      apigatewaykit.ToolInvokeEndpoint,
		Arguments: buildInvokeArgs(connection, req),
	})
	if callErr != nil {
		writeError(w, http.StatusInternalServerError, callErr.Error())
		return
	}

	writeToolResult(w, result)
}

// invokeRaw streams the upstream response body straight to the client
// with bounded memory (issue #535). It routes through the SAME
// in-memory MCP session as invoke — so the authenticator, persona
// authorization, route policy, and audit middleware all apply — but
// installs a RawSink on the session context so api_invoke_endpoint's
// handler io.Copy's the upstream body to this ResponseWriter instead of
// buffering it into the JSON envelope. The upstream credential is still
// held and injected by the gateway; the caller never sees it.
func (h *handler) invokeRaw(w http.ResponseWriter, r *http.Request) {
	connection := r.PathValue("connection")
	req, err := decodeInvokeRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if m := getInvokeMeta(r.Context()); m != nil {
		m.method, m.path = req.Method, req.Path
	}

	sink := &responseWriterSink{w: w}
	rp := &apigatewaykit.RawPassthrough{Sink: sink, MaxBytes: h.rawMaxBytes}
	session, cleanup, sessErr := h.connectSession(r, func(ctx context.Context) context.Context {
		return apigatewaykit.WithRawPassthrough(ctx, rp)
	})
	if sessErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to MCP server")
		return
	}
	defer cleanup()

	result, callErr := session.CallTool(r.Context(), &mcp.CallToolParams{
		Name:      apigatewaykit.ToolInvokeEndpoint,
		Arguments: buildInvokeArgs(connection, req),
	})
	// Once any byte (or the status line) has been streamed, the HTTP
	// response is committed and cannot be rewritten. Whatever the tool
	// result says, the client already has its answer.
	if sink.wroteHeader() {
		return
	}
	if callErr != nil {
		writeError(w, http.StatusInternalServerError, callErr.Error())
		return
	}
	writeRawToolError(w, result)
}

// writeRawToolError maps a non-streamed api_invoke_endpoint result to an
// HTTP error. Reached only when nothing was streamed, i.e. the call
// failed before the body began (request validation, auth/authz, route
// policy, transport, or the 413 size rejection). A non-error result
// here would mean the handler returned a sentinel without streaming,
// which is a contract violation surfaced as 500.
func writeRawToolError(w http.ResponseWriter, result *mcp.CallToolResult) {
	payload, ok := firstTextContent(result.Content)
	if !ok {
		writeError(w, http.StatusInternalServerError, "unexpected response shape from api_invoke_endpoint")
		return
	}
	if !result.IsError {
		writeError(w, http.StatusInternalServerError, "raw passthrough produced no streamed response")
		return
	}
	status, msg := classifyToolError(payload)
	writeClassifiedError(w, status, payload, msg)
}

// responseWriterSink adapts an http.ResponseWriter to
// apigatewaykit.RawSink. wroteHeader reports whether the status line
// has been flushed so invokeRaw knows the response is committed.
type responseWriterSink struct {
	w     http.ResponseWriter
	wrote bool
}

// AddHeader appends a response header value (RawSink).
func (s *responseWriterSink) AddHeader(key, value string) { s.w.Header().Add(key, value) }

// SetStatus flushes the response status line once (RawSink).
func (s *responseWriterSink) SetStatus(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.w.WriteHeader(code)
}

// Write streams body bytes, flushing a default 200 status first if the
// handler streamed a body without an explicit status (RawSink).
func (s *responseWriterSink) Write(p []byte) (int, error) {
	if !s.wrote {
		s.SetStatus(http.StatusOK)
	}
	n, err := s.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("gatewayhttp: writing raw response: %w", err)
	}
	return n, nil
}

func (s *responseWriterSink) wroteHeader() bool { return s.wrote }

var _ apigatewaykit.RawSink = (*responseWriterSink)(nil)

// writeToolResult maps an api_invoke_endpoint CallToolResult to an
// HTTP response. Split from invoke so it can be unit-tested with
// hand-crafted results — the branches it guards (non-text content,
// malformed InvokeOutput JSON) are reachable only via a hypothetical
// upstream SDK change and cannot be triggered through the
// in-memory MCP session.
func writeToolResult(w http.ResponseWriter, result *mcp.CallToolResult) {
	payload, ok := firstTextContent(result.Content)
	if !ok {
		writeError(w, http.StatusInternalServerError, "unexpected response shape from api_invoke_endpoint")
		return
	}
	if result.IsError {
		status, msg := classifyToolError(payload)
		writeClassifiedError(w, status, payload, msg)
		return
	}
	var out apigatewaykit.InvokeOutput
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse upstream invoke result")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// buildInvokeArgs constructs the api_invoke_endpoint argument map.
// The connection is taken from the URL and authoritatively overrides
// any "connection" key the caller might have placed in the body.
func buildInvokeArgs(connection string, req *invokeRequest) map[string]any {
	args := map[string]any{
		"connection": connection,
		"method":     req.Method,
		"path":       req.Path,
	}
	if len(req.QueryParams) > 0 {
		args["query_params"] = req.QueryParams
	}
	if len(req.Headers) > 0 {
		args["headers"] = req.Headers
	}
	if req.Body != nil {
		args["body"] = req.Body
	}
	if req.TimeoutSeconds > 0 {
		args["timeout_seconds"] = req.TimeoutSeconds
	}
	return args
}

func decodeInvokeRequest(r *http.Request) (*invokeRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, RequestBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("request body is required")
	}
	var req invokeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if strings.TrimSpace(req.Method) == "" {
		return nil, errors.New("method is required")
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, errors.New("path is required")
	}
	return &req, nil
}

func (h *handler) connectInternalSession(r *http.Request) (*mcp.ClientSession, func(), error) {
	return h.connectSession(r, nil)
}

// connectSession opens an in-memory MCP session against the platform's
// assembled server. The optional decorate hook augments the SERVER
// connection context (the one the tool handler observes), used by the
// raw passthrough to install its RawSink so api_invoke_endpoint streams
// to the client instead of buffering. Decorating the server context is
// the same mechanism that propagates Source=rest to the handler.
func (h *handler) connectSession(r *http.Request, decorate func(context.Context) context.Context) (*mcp.ClientSession, func(), error) {
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := r.Context()
	// Tag this in-memory MCP session as originating from the REST shim so
	// the audit middleware records source="rest", letting operators
	// distinguish external automation traffic (NiFi, cronjobs, etc.) from
	// real MCP agents that share the same api_invoke_endpoint tool.
	ctx = middleware.WithSource(ctx, middleware.SourceREST)
	if token := readRequestToken(r); token != "" {
		ctx = middleware.WithToken(ctx, token)
	}
	if decorate != nil {
		ctx = decorate(ctx)
	}
	serverSession, err := h.mcpServer.Connect(ctx, t1, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("server connect: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-rest", Version: "v1"}, nil)
	clientSession, err := client.Connect(r.Context(), t2, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, nil, fmt.Errorf("client connect: %w", err)
	}
	cleanup := func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}
	return clientSession, cleanup, nil
}

// readRequestToken pulls the credential from either X-API-Key or a
// Bearer Authorization header. Header precedence matches the admin
// REST shim so behavior is identical for clients that send both.
func readRequestToken(r *http.Request) string {
	if k := r.Header.Get("X-API-Key"); k != "" {
		return k
	}
	if a := r.Header.Get("Authorization"); a != "" {
		if t, ok := strings.CutPrefix(a, "Bearer "); ok {
			return t
		}
	}
	return ""
}

func firstTextContent(content []mcp.Content) (string, bool) {
	for _, c := range content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text, true
		}
	}
	return "", false
}

// classifyToolError inspects an MCP tool error and picks an HTTP
// status that best represents the failure. The matched patterns are
// stable strings emitted by the apigateway toolkit and the MCP
// auth/authz middleware. Categories, in order of evaluation:
//
//   - "authentication failed" → 401 (caller's token bad)
//   - "not authorized"        → 403 (caller's persona denies)
//   - timeout signatures      → 504 (gateway gave up waiting on upstream)
//   - transport signatures    → 502 (gateway could not reach upstream)
//   - "not found"             → 404 (connection name unknown to the toolkit)
//   - anything else           → 400 (request the platform refused, NOT a
//     platform fault; clients see 4xx so they don't trigger retry loops
//     on what is essentially a malformed input).
//
// Order matters: timeout/transport patterns are evaluated AFTER auth
// because auth errors are independent of the upstream and should
// surface as auth failures, but BEFORE the "not found" pattern
// because a transport error reading "no such host" must not be
// mistaken for a connection-name lookup miss.
//
// Two producers feed this classifier with DIFFERENT payload shapes
// (issue #533): the apigateway toolkit wraps its message in a
// {"error":"..."} JSON envelope, while the auth/authz middleware
// (PlatformError, see pkg/middleware) emits a bare string such as
// "not authorized: ...". The envelope is only a serialization detail
// and must not change the HTTP status, so the message is normalized
// first (unwrap the envelope when present, otherwise use the raw
// payload) and the SAME switch classifies both. Previously a
// non-envelope payload short-circuited to 500, turning a permanent
// 403/401 denial into a retryable 5xx that made upstream HTTP clients
// retry-loop on a request that could never succeed.
//
// An unrecognized message defaults to 400, not 500: every genuine
// transient or server-side condition (timeout → 504, transport → 502)
// is matched explicitly above, so an unmatched message is by
// elimination not known-retryable, and defaulting unknowns to a
// retryable 5xx is the very retry-storm failure mode this classifier
// exists to prevent.
func classifyToolError(payload string) (status int, message string) {
	msg := payload
	var env errorEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err == nil && env.Error != "" {
		msg = env.Error
	}
	lower := strings.ToLower(msg)
	if status, ok := classifyMemoryRejection(lower); ok {
		return status, msg
	}
	switch {
	case strings.Contains(lower, "authentication failed"):
		return http.StatusUnauthorized, msg
	case strings.Contains(lower, "not authorized"):
		return http.StatusForbidden, msg
	case isTimeoutSignature(lower):
		return http.StatusGatewayTimeout, msg
	case isTransportSignature(lower):
		return http.StatusBadGateway, msg
	case strings.Contains(lower, "not found"):
		return http.StatusNotFound, msg
	default:
		return http.StatusBadRequest, msg
	}
}

// classifyMemoryRejection maps the gateway's memory-protection error
// codes (issue #535) to HTTP statuses with deliberate retry semantics,
// returning ok=false when the message is not a memory rejection so the
// main classifier continues. 413 (body too large) is permanent and
// non-retryable; 429 (budget exhausted) is transient and
// retryable-with-backoff, consistent with the #533 retry policy.
func classifyMemoryRejection(lower string) (status int, ok bool) {
	switch {
	case strings.Contains(lower, apigatewaykit.ErrCodeBodyTooLarge):
		return http.StatusRequestEntityTooLarge, true
	case strings.Contains(lower, apigatewaykit.ErrCodeBudgetExhausted):
		return http.StatusTooManyRequests, true
	default:
		return 0, false
	}
}

// isTimeoutSignature reports whether the lowercased error message
// carries one of Go's stable timeout phrases. Mirrors the same set
// used by pkg/toolkits/apigateway.isTimeoutErrorMessage so the
// REST shim and the toolkit agree on what counts as a timeout.
func isTimeoutSignature(lower string) bool {
	return strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "client.timeout exceeded") ||
		strings.Contains(lower, "i/o timeout")
}

// isTransportSignature reports whether the lowercased error message
// carries one of Go's stable non-timeout transport-error phrases:
// DNS resolution, TCP refused, TLS handshake, broken stream, peer
// reset. Each of these means the gateway could not successfully
// proxy the call to the upstream, so the REST shim returns 502
// Bad Gateway to its caller (NiFi / curl / etc.), which lets
// those clients use their built-in retry semantics for 5xx instead
// of having to inspect the JSON body.
func isTransportSignature(lower string) bool {
	return strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "tls:") ||
		strings.Contains(lower, "tls handshake") ||
		strings.Contains(lower, "eof") ||
		strings.Contains(lower, "connection reset")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorEnvelope{Error: msg})
}

// retryAfterSeconds is the Retry-After hint sent with a 429 budget
// rejection. Small and fixed: budget exhaustion is transient and
// drains as concurrent buffered reads complete, so a short backoff is
// appropriate (issue #535).
const retryAfterSeconds = 1

// writeClassifiedError writes a classified tool error to the client. It
// preserves the toolkit's structured error envelope verbatim when the
// payload is already a JSON object — so the diagnostic fields on the
// 413/429 envelopes (limit_bytes, actual_bytes, in_use_bytes, ...)
// reach the caller — and otherwise wraps the unwrapped message in the
// standard {"error": ...} shape (e.g. the auth/authz middleware's bare
// string payloads). A 429 also carries a Retry-After header so HTTP
// clients back off rather than hot-retry.
func writeClassifiedError(w http.ResponseWriter, status int, payload, msg string) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	// Re-decode and re-encode the toolkit's structured envelope rather
	// than passing the payload through verbatim: this preserves the
	// diagnostic fields (limit_bytes, actual_bytes, ...) while emitting
	// canonical JSON, so no untrusted bytes are reflected into the
	// response. A non-object payload (e.g. the auth middleware's bare
	// string) falls back to the standard {"error": ...} wrapper.
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err == nil && obj["error"] != nil {
		writeJSON(w, status, obj)
		return
	}
	writeError(w, status, msg)
}
