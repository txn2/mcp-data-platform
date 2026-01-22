package middleware

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPToolCallMiddleware creates MCP protocol-level middleware that intercepts
// tools/call requests and enforces authentication and authorization.
//
// This middleware runs at the MCP protocol level, intercepting all incoming
// requests before they reach tool handlers. For tools/call requests, it:
// 1. Extracts the tool name from the request
// 2. Creates a PlatformContext with the tool information
// 3. Runs authentication to identify the user
// 4. Runs authorization to check if the user can access the tool
// 5. Either proceeds with the call or returns an access denied error
func MCPToolCallMiddleware(authenticator Authenticator, authorizer Authorizer) mcp.Middleware {
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
			requestID := generateRequestID()
			pc := NewPlatformContext(requestID)
			pc.ToolName = toolName
			ctx = WithPlatformContext(ctx, pc)

			// Authenticate
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

			// Authorize
			authorized, reason := authorizer.IsAuthorized(ctx, pc.UserID, pc.Roles, toolName)
			pc.Authorized = authorized
			if !authorized {
				pc.AuthzError = reason
				return createErrorResult("not authorized: " + reason), nil
			}

			// Proceed with the actual tool call
			return next(ctx, method, req)
		}
	}
}

// extractToolName extracts the tool name from a tools/call request.
func extractToolName(req mcp.Request) (string, error) {
	// Type assert to get the CallToolRequest which has typed params
	params := req.GetParams()
	if params == nil {
		return "", fmt.Errorf("missing params")
	}

	// Try to get the Name field from CallToolParamsRaw
	// The params implement the Params interface, but we need to access the Name field
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
// This returns an error in the format expected by the MCP protocol.
func createErrorResult(errMsg string) mcp.Result {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: errMsg},
		},
	}
}
