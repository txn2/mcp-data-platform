package middleware

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
// The toolkitLookup parameter is optional; if nil, toolkit metadata won't be populated.
func MCPToolCallMiddleware(authenticator Authenticator, authorizer Authorizer, toolkitLookup ToolkitLookup) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// Only intercept tools/call requests
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			// Extract tool name from request params
			toolName, err := extractToolName(req)
			if err != nil {
				return createErrorResult(fmt.Sprintf("invalid request: %v", err)), nil
			}

			// Create platform context
			pc := NewPlatformContext(generateRequestID())
			pc.ToolName = toolName
			ctx = WithPlatformContext(ctx, pc)

			// Populate toolkit metadata
			populateToolkitMetadata(pc, toolkitLookup, toolName)

			// Bridge auth token from Streamable HTTP per-request headers.
			ctx = bridgeAuthToken(ctx, req)

			// Authenticate and authorize
			return authenticateAndAuthorize(ctx, method, req, next, authenticator, authorizer, pc, toolName)
		}
	}
}

// populateToolkitMetadata fills PlatformContext toolkit fields from the lookup.
func populateToolkitMetadata(pc *PlatformContext, lookup ToolkitLookup, toolName string) {
	if lookup == nil {
		return
	}
	kind, name, connection, found := lookup.GetToolkitForTool(toolName)
	if found {
		pc.ToolkitKind = kind
		pc.ToolkitName = name
		pc.Connection = connection
	}
}

// bridgeAuthToken extracts auth tokens from Streamable HTTP RequestExtra headers
// into the context. SSE sets the token via HTTP middleware on the initial GET;
// Streamable HTTP provides headers in RequestExtra on every POST.
func bridgeAuthToken(ctx context.Context, req mcp.Request) context.Context {
	if GetToken(ctx) != "" {
		return ctx
	}
	extra := req.GetExtra()
	if extra == nil || extra.Header == nil {
		return ctx
	}
	if token := extractBearerOrAPIKey(extra.Header); token != "" {
		return WithToken(ctx, token)
	}
	return ctx
}

// authenticateAndAuthorize runs authentication and authorization, returning
// the next handler result or an error result.
func authenticateAndAuthorize(
	ctx context.Context, method string, req mcp.Request,
	next mcp.MethodHandler,
	authenticator Authenticator, authorizer Authorizer,
	pc *PlatformContext, toolName string,
) (mcp.Result, error) {
	userInfo, err := authenticator.Authenticate(ctx)
	if err != nil {
		return createErrorResult("authentication failed: " + err.Error()), nil
	}

	if userInfo != nil {
		pc.UserID = userInfo.UserID
		pc.UserEmail = userInfo.Email
		pc.UserClaims = userInfo.Claims
		pc.Roles = userInfo.Roles
	}

	authorized, personaName, reason := authorizer.IsAuthorized(ctx, pc.UserID, pc.Roles, toolName)
	pc.Authorized = authorized
	pc.PersonaName = personaName
	if !authorized {
		pc.AuthzError = reason
		return createErrorResult("not authorized: " + reason), nil
	}

	return next(ctx, method, req)
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

// createErrorResult creates an MCP result for an authorization error.
func createErrorResult(errMsg string) mcp.Result {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: errMsg},
		},
	}
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

// generateRequestID creates a cryptographically secure request ID.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("req-%x", b)
}
