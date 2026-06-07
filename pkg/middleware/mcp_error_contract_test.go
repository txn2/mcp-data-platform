package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wrapErrorContract wraps a leaf handler with the error-contract middleware and
// returns a callable for tools/call.
func wrapErrorContract(t *testing.T, leaf mcp.MethodHandler) mcp.MethodHandler {
	t.Helper()
	return MCPErrorContractMiddleware()(leaf)
}

// mustCTR asserts that a result is a *mcp.CallToolResult.
func mustCTR(t *testing.T, res mcp.Result) *mcp.CallToolResult {
	t.Helper()
	ctr, ok := res.(*mcp.CallToolResult)
	require.True(t, ok, "result must be a *mcp.CallToolResult")
	return ctr
}

func TestErrorContract_EnrichesBareError(t *testing.T) {
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "asset not found"}}}, nil
	}
	req := createAuditTestRequest(t, "manage_artifact", nil)
	res, err := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	require.NoError(t, err)

	ctr := mustCTR(t, res)
	p := envelope(t, ctr)
	assert.Equal(t, ErrCategoryToolError, p.Category, "uncategorized bare error defaults to tool_error")
	assert.Equal(t, CodeToolError, p.Code)
	assert.Equal(t, "asset not found", p.Message, "original message is preserved")
}

func TestErrorContract_EmptyMessageGetsFallback(t *testing.T) {
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{IsError: true}, nil // no content
	}
	req := createAuditTestRequest(t, "trino_query", nil)
	res, _ := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	assert.NotEmpty(t, envelope(t, mustCTR(t, res)).Message, "an empty bare error still gets a message")
}

func TestErrorContract_AdoptsStashedCategory(t *testing.T) {
	// A result that carries a category via SetError but no structured envelope
	// (e.g. a handler that did not use BuildErrorResult) keeps its category.
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		r := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "denied"}}}
		r.SetError(&PlatformError{Category: ErrCategoryAuthz, Message: "denied"})
		return r, nil
	}
	req := createAuditTestRequest(t, "trino_query", nil)
	res, _ := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	assert.Equal(t, ErrCategoryAuthz, envelope(t, mustCTR(t, res)).Category)
}

func TestErrorContract_PassesThroughStructured(t *testing.T) {
	// An already-structured result (source-categorized) is left untouched.
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return BuildErrorResult(NewToolError(CodeMissingParameter, ErrCategoryClientInput, "sql required", "supply sql")), nil
	}
	req := createAuditTestRequest(t, "trino_query", nil)
	res, _ := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	p := envelope(t, mustCTR(t, res))
	assert.Equal(t, CodeMissingParameter, p.Code, "source code preserved")
	assert.Equal(t, ErrCategoryClientInput, p.Category)
}

func TestErrorContract_RecoversPanic(t *testing.T) {
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		panic("boom in a tool handler")
	}
	req := createAuditTestRequest(t, "trino_query", nil)
	res, err := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	require.NoError(t, err, "a panic must be recovered into a result, not propagated")
	p := envelope(t, mustCTR(t, res))
	assert.Equal(t, CodeInternalError, p.Code)
	assert.Equal(t, ErrCategoryInternal, p.Category)
}

func TestErrorContract_PassThroughSuccess(t *testing.T) {
	success := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) { return success, nil }
	req := createAuditTestRequest(t, "trino_query", nil)
	res, err := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	require.NoError(t, err)
	assert.Same(t, success, res, "a success result passes through unchanged")
}

func TestErrorContract_PassThroughProtocolError(t *testing.T) {
	jerr := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "bad request"}
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) { return nil, jerr }
	req := createAuditTestRequest(t, "trino_query", nil)
	res, err := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	assert.Nil(t, res)
	assert.ErrorIs(t, err, jerr, "protocol-level JSON-RPC errors pass through untouched")
}

func TestUnwrapLegacyErrorJSON(t *testing.T) {
	// A legacy {"error":"..."} toolkit envelope is unwrapped to its inner message.
	assert.Equal(t, "asset not found", unwrapLegacyErrorJSON(`{"error":"asset not found"}`))
	// Plain text is returned unchanged.
	assert.Equal(t, "plain message", unwrapLegacyErrorJSON("plain message"))
	// A JSON object without a non-empty error field is left as-is.
	assert.Equal(t, `{"other":"x"}`, unwrapLegacyErrorJSON(`{"other":"x"}`))
}

// TestErrorContract_EnrichUnwrapsLegacyJSON proves the normalizer presents a
// clean message (not doubly-encoded JSON) for a toolkit that emits the legacy
// {"error":...} text shape.
func TestErrorContract_EnrichUnwrapsLegacyJSON(t *testing.T) {
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: `{"error":"collection not found"}`}}}, nil
	}
	req := createAuditTestRequest(t, "manage_artifact", nil)
	res, _ := wrapErrorContract(t, leaf)(context.Background(), methodToolsCall, req)
	assert.Equal(t, "collection not found", envelope(t, mustCTR(t, res)).Message)
}

func TestErrorContract_IgnoresNonToolsCall(t *testing.T) {
	called := false
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		called = true
		return &mcp.ListToolsResult{}, nil
	}
	_, err := wrapErrorContract(t, leaf)(context.Background(), "tools/list", createAuditTestRequest(t, "x", nil))
	require.NoError(t, err)
	assert.True(t, called)
}
