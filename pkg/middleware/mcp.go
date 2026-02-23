package middleware

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultSessionID is used for stdio/SSE transports that don't provide a session ID.
	defaultSessionID = "stdio"

	// methodToolsCall is the MCP method name for tool invocations.
	methodToolsCall = "tools/call"
)

// Error categories for structured error handling and audit queries.
const (
	ErrCategoryAuth     = "authentication_failed"
	ErrCategoryAuthz    = "authorization_denied"
	ErrCategoryDeclined = "user_declined"
)

// PlatformError is a categorized error for structured audit and client handling.
type PlatformError struct {
	Category string
	Message  string
}

// Error implements the error interface.
func (e *PlatformError) Error() string { return e.Message }

// ErrorCategory implements CategorizedError.
func (e *PlatformError) ErrorCategory() string { return e.Category }

// MCPToolCallMiddleware creates MCP protocol-level middleware that intercepts
// tools/call requests and enforces authentication and authorization.
//
// This middleware runs at the MCP protocol level, intercepting all incoming
// requests before they reach tool handlers. For tools/call requests, it:
// 1. Extracts the tool name from the request
// 2. Creates a PlatformContext with the tool information
// 3. Looks up toolkit metadata (kind, name, connection)
// 4. Runs authentication to identify the user
// 5. Runs authorization to check if the user can access the tool
// 6. Either proceeds with the call or returns an access denied error
//
// The transport parameter identifies the server transport ("stdio" or "http").
// The toolkitLookup parameter is optional; if nil, toolkit metadata won't be populated.
func MCPToolCallMiddleware(authenticator Authenticator, authorizer Authorizer, toolkitLookup ToolkitLookup, transport string, workflowTracker ...*SessionWorkflowTracker) mcp.Middleware {
	var tracker *SessionWorkflowTracker
	if len(workflowTracker) > 0 {
		tracker = workflowTracker[0]
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// Only intercept tools/call requests
			if method != methodToolsCall {
				return next(ctx, method, req)
			}

			// Extract tool name from request params
			toolName, err := extractToolName(req)
			if err != nil {
				return nil, newInvalidParamsError(fmt.Sprintf("invalid request: %v", err))
			}

			// Build platform context and enrich the Go context.
			pc := NewPlatformContext(generateRequestID())
			pc.ToolName = toolName
			pc.SessionID = extractSessionID(req)
			pc.Transport = transport
			pc.Source = "mcp"
			ctx = buildToolCallContext(ctx, req, pc, toolkitLookup, toolName)

			// Authenticate and authorize
			return authenticateAndAuthorize(ctx, method, req, next, authParams{
				authenticator:   authenticator,
				authorizer:      authorizer,
				pc:              pc,
				toolName:        toolName,
				workflowTracker: tracker,
			})
		}
	}
}

// buildToolCallContext enriches the context with session, progress, toolkit
// metadata, connection override, and auth token bridging for a tool call.
func buildToolCallContext(ctx context.Context, req mcp.Request, pc *PlatformContext, toolkitLookup ToolkitLookup, toolName string) context.Context {
	ctx = WithPlatformContext(ctx, pc)

	// Store ServerSession and progress token in context for
	// progress notifications and client logging.
	if ss := extractServerSession(req); ss != nil {
		ctx = WithServerSession(ctx, ss)
	}
	if pt := extractProgressToken(req); pt != nil {
		ctx = WithProgressToken(ctx, pt)
	}

	// Populate toolkit metadata (kind, name, default connection).
	populateToolkitMetadata(pc, toolkitLookup, toolName)

	// Override connection from request arguments for accurate audit logging.
	// With multi-connection toolkits, the toolkit's Connection() returns the
	// default, but the actual connection is determined by the request's
	// "connection" argument.
	if connFromArgs := extractConnectionArg(req); connFromArgs != "" {
		pc.Connection = connFromArgs
	}

	// Bridge auth token from Streamable HTTP per-request headers.
	return bridgeAuthToken(ctx, req)
}

// populateToolkitMetadata fills PlatformContext toolkit fields from the lookup.
func populateToolkitMetadata(pc *PlatformContext, lookup ToolkitLookup, toolName string) {
	if lookup == nil {
		return
	}
	match := lookup.GetToolkitForTool(toolName)
	if match.Found {
		pc.ToolkitKind = match.Kind
		pc.ToolkitName = match.Name
		pc.Connection = match.Connection
	}
}

// bridgeAuthToken extracts auth tokens from Streamable HTTP RequestExtra headers
// into the context. SSE sets the token via HTTP middleware on the initial GET;
// Streamable HTTP provides headers in RequestExtra on every POST.
// MCPAuthGateway also bridges tokens at the HTTP level so they propagate via
// the connection context; this function acts as a fallback.
func bridgeAuthToken(ctx context.Context, req mcp.Request) context.Context {
	if GetToken(ctx) != "" {
		slog.Debug("bridgeAuthToken: token already in context")
		return ctx
	}
	extra := req.GetExtra()
	if extra == nil || extra.Header == nil {
		slog.Debug("bridgeAuthToken: no extra headers in request")
		return ctx
	}
	if token := extractBearerOrAPIKey(extra.Header); token != "" {
		slog.Debug("bridgeAuthToken: extracted token from request headers")
		return WithToken(ctx, token)
	}
	slog.Debug("bridgeAuthToken: no token in request headers")
	return ctx
}

// authParams groups authentication and authorization parameters.
type authParams struct {
	authenticator   Authenticator
	authorizer      Authorizer
	pc              *PlatformContext
	toolName        string
	workflowTracker *SessionWorkflowTracker
}

// authenticateAndAuthorize runs authentication and authorization, returning
// the next handler result or an error result.
func authenticateAndAuthorize(
	ctx context.Context, method string, req mcp.Request,
	next mcp.MethodHandler,
	params authParams,
) (mcp.Result, error) {
	userInfo, err := params.authenticator.Authenticate(ctx)
	if err != nil {
		slog.Warn("tool call authentication failed",
			"tool", params.toolName,
			"request_id", params.pc.RequestID,
			"error", err.Error(),
		)
		return createCategorizedErrorResult(ErrCategoryAuth, "authentication failed: "+err.Error()), nil
	}

	if userInfo != nil {
		params.pc.UserID = userInfo.UserID
		params.pc.UserEmail = userInfo.Email
		params.pc.UserClaims = userInfo.Claims
		params.pc.Roles = userInfo.Roles
	}

	authorized, personaName, reason := params.authorizer.IsAuthorized(ctx, params.pc.UserID, params.pc.Roles, params.toolName)
	params.pc.Authorized = authorized
	params.pc.PersonaName = personaName
	if !authorized {
		params.pc.AuthzError = reason
		slog.Warn("tool call authorization denied",
			"tool", params.toolName,
			"user_id", params.pc.UserID,
			"email", params.pc.UserEmail,
			"roles", params.pc.Roles,
			"persona", personaName,
			"reason", reason,
			"request_id", params.pc.RequestID,
		)
		return createCategorizedErrorResult(ErrCategoryAuthz, "not authorized: "+reason), nil
	}

	authType := ""
	if userInfo != nil {
		authType = userInfo.AuthType
	}
	slog.Debug("tool call authorized",
		"tool", params.toolName,
		"user_id", params.pc.UserID,
		"email", params.pc.UserEmail,
		"roles", params.pc.Roles,
		"persona", personaName,
		"auth_type", authType,
		"request_id", params.pc.RequestID,
	)

	// Record tool call for workflow tracking (after successful auth)
	if params.workflowTracker != nil {
		params.workflowTracker.RecordToolCall(params.pc.SessionID, params.toolName)
	}

	return next(ctx, method, req)
}

// extractSessionID extracts the session ID from a request.
// For Streamable HTTP transport, this is the Mcp-Session-Id header value.
// For stdio/SSE transport, it falls back to a constant defaultSessionID so that
// all calls within the same process share a single implicit session.
func extractSessionID(req mcp.Request) (id string) {
	id = defaultSessionID
	if req == nil {
		return id
	}
	// GetSession() may return a typed nil *ServerSession wrapped in the
	// Session interface, which passes the != nil check but panics on
	// method calls. Guard with recover for safety.
	defer func() {
		if r := recover(); r != nil {
			id = defaultSessionID
		}
	}()
	if session := req.GetSession(); session != nil {
		if sid := session.ID(); sid != "" {
			return sid
		}
	}
	return id
}

// extractToolName extracts the tool name from a tools/call request.
func extractToolName(req mcp.Request) (string, error) {
	params := req.GetParams()
	if params == nil {
		return "", fmt.Errorf("missing params")
	}

	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok {
		return "", fmt.Errorf("unexpected params type: %T", params)
	}

	// Check if the pointer itself is nil (type assertion can succeed with nil pointer)
	if callParams == nil {
		return "", fmt.Errorf("missing params")
	}

	if callParams.Name == "" {
		return "", fmt.Errorf("missing tool name")
	}

	return callParams.Name, nil
}

// newInvalidParamsError creates a JSON-RPC error with CodeInvalidParams.
// Used for malformed requests (e.g., missing tool name or wrong params type)
// which are genuine protocol-level errors rather than tool-level failures.
func newInvalidParamsError(msg string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: msg}
}

// createCategorizedErrorResult creates an MCP error result with a category
// for structured audit queries. The category is embedded in the error and
// extractable via ErrorCategory().
func createCategorizedErrorResult(category, errMsg string) mcp.Result {
	result := &mcp.CallToolResult{}
	result.SetError(&PlatformError{Category: category, Message: errMsg})
	return result
}

// CategorizedError is implemented by errors that carry a category for audit.
type CategorizedError interface {
	error
	ErrorCategory() string
}

// ErrorCategory extracts the error category from a categorized error.
// Returns an empty string if the error is not categorized.
func ErrorCategory(err error) string {
	var ce CategorizedError
	if errors.As(err, &ce) {
		return ce.ErrorCategory()
	}
	return ""
}

// extractBearerOrAPIKey extracts an auth token from HTTP headers.
// It checks the Authorization header for a Bearer token first,
// then falls back to the X-API-Key header.
func extractBearerOrAPIKey(h http.Header) string {
	if authHeader := h.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	return h.Get("X-API-Key")
}

// extractServerSession extracts the ServerSession from an MCP Request.
// Uses the same defensive pattern as extractSessionID to guard against
// typed-nil panics from GetSession().
func extractServerSession(req mcp.Request) (ss *mcp.ServerSession) {
	if req == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			ss = nil
		}
	}()
	session := req.GetSession()
	if session == nil {
		return nil
	}
	ss, _ = session.(*mcp.ServerSession)
	return ss
}

// extractProgressToken extracts the progress token from an MCP Request.
// Returns nil if the request has no progress token.
// Uses defer/recover to guard against typed-nil panics from GetProgressToken.
func extractProgressToken(req mcp.Request) (token any) {
	if req == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			token = nil
		}
	}()
	params := req.GetParams()
	if params == nil {
		return nil
	}
	// GetProgressToken is on RequestParams, not Params.
	rp, ok := params.(mcp.RequestParams)
	if !ok {
		return nil
	}
	return rp.GetProgressToken()
}

// generateRequestID creates a cryptographically secure request ID.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("req-%x", b)
}

// extractConnectionArg extracts the "connection" field from tool call arguments.
// Returns an empty string if the request has no connection argument.
func extractConnectionArg(req mcp.Request) string {
	if req == nil {
		return ""
	}
	params := req.GetParams()
	if params == nil {
		return ""
	}
	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok || callParams == nil || len(callParams.Arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(callParams.Arguments, &args); err != nil {
		return ""
	}
	conn, _ := args["connection"].(string)
	return conn
}
