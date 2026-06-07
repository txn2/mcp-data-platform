package middleware

import (
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envelope extracts the structured error payload from a result, failing the
// test if it is absent. It is the machine-readable contract an agent branches on.
func envelope(t *testing.T, r *mcp.CallToolResult) errorPayload {
	t.Helper()
	require.True(t, r.IsError, "result must be an error")
	sc, ok := r.StructuredContent.(map[string]any)
	require.True(t, ok, "structuredContent must be a map")
	payload, ok := sc[errorEnvelopeKey].(errorPayload)
	require.True(t, ok, "structuredContent.error must be an errorPayload")
	return payload
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, r.Content)
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func TestBuildErrorResult_FullContract(t *testing.T) {
	r := BuildErrorResult(NewToolError(CodeMissingParameter, ErrCategoryClientInput,
		"the \"sql\" parameter is required", "Supply sql and retry."))

	// Structured envelope is surfaced for programmatic branching.
	p := envelope(t, r)
	assert.Equal(t, CodeMissingParameter, p.Code)
	assert.Equal(t, ErrCategoryClientInput, p.Category)
	assert.Equal(t, "the \"sql\" parameter is required", p.Message)
	assert.Equal(t, "Supply sql and retry.", p.Hint)

	// The text the model sees is self-describing: message + code + hint.
	text := resultText(t, r)
	assert.Contains(t, text, "the \"sql\" parameter is required")
	assert.Contains(t, text, "code: "+CodeMissingParameter)
	assert.Contains(t, text, "Supply sql and retry.")

	// The category still feeds audit/metrics via the stashed error.
	assert.Equal(t, ErrCategoryClientInput, ErrorCategory(r.GetError()))
}

func TestBuildErrorResult_DefaultsUncategorized(t *testing.T) {
	r := BuildErrorResult(&PlatformError{Message: "something went wrong"})
	p := envelope(t, r)
	assert.Equal(t, CodeToolError, p.Code, "empty code defaults to tool_error")
	assert.Equal(t, ErrCategoryToolError, p.Category, "empty category defaults to tool_error")
	assert.Equal(t, "something went wrong", p.Message)
}

func TestErrorConstructors(t *testing.T) {
	tests := []struct {
		name     string
		err      *PlatformError
		wantCat  string
		wantCode string
	}{
		{"client input", ClientInputError(CodeMissingParameter, "bad", "fix"), ErrCategoryClientInput, CodeMissingParameter},
		{"not found", NotFoundError(CodeNotFound, "no conn", "name one"), ErrCategoryNotFound, CodeNotFound},
		{"unavailable", UnavailableError("off", "enable it"), ErrCategoryUnavailable, CodeFeatureUnavailable},
		{"internal", InternalError("boom"), ErrCategoryInternal, CodeInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantCat, tt.err.Category)
			assert.Equal(t, tt.wantCode, tt.err.Code)
			// errors.As keeps working for the categorized-error interface.
			var ce CategorizedError
			require.True(t, errors.As(error(tt.err), &ce))
			assert.Equal(t, tt.wantCat, ce.ErrorCategory())
		})
	}
}

func TestResultBuilders(t *testing.T) {
	tests := []struct {
		name     string
		result   *mcp.CallToolResult
		wantCode string
		wantCat  string
	}{
		{"missing param", MissingParameterResult("sql"), CodeMissingParameter, ErrCategoryClientInput},
		{"not found", NotFoundResult("asset not found", "check the id"), CodeNotFound, ErrCategoryNotFound},
		{"unavailable", UnavailableResult("storage off", "configure s3"), CodeFeatureUnavailable, ErrCategoryUnavailable},
		{"unauthorized", UnauthorizedResult("not your asset", "ask the owner"), CodeUnauthorized, ErrCategoryAuthz},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := envelope(t, tt.result)
			assert.Equal(t, tt.wantCode, p.Code)
			assert.Equal(t, tt.wantCat, p.Category)
			assert.NotEmpty(t, p.Message)
		})
	}
	// MissingParameterResult names the offending parameter in the message.
	assert.Contains(t, resultText(t, MissingParameterResult("sql")), "sql")
}

func TestHasErrorEnvelope(t *testing.T) {
	assert.True(t, hasErrorEnvelope(BuildErrorResult(NewToolError(CodeNotFound, ErrCategoryNotFound, "x", ""))))
	bare := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "bare"}}}
	assert.False(t, hasErrorEnvelope(bare))
	assert.False(t, hasErrorEnvelope(nil))
}

func TestAuthzHint(t *testing.T) {
	withPersona := authzHint("analyst", "trino_query")
	assert.Contains(t, withPersona, "analyst")
	assert.Contains(t, withPersona, "trino_query")
	noPersona := authzHint("", "trino_query")
	assert.Contains(t, noPersona, "trino_query")
	assert.False(t, strings.Contains(noPersona, "Persona \"\""), "must not render an empty persona literal")
}

func TestAgentText(t *testing.T) {
	assert.Equal(t, "msg (code: c) Hint: h", (&PlatformError{Code: "c", Message: "msg", Hint: "h"}).agentText())
	assert.Equal(t, "msg (code: c)", (&PlatformError{Code: "c", Message: "msg"}).agentText())
	assert.Equal(t, "msg", (&PlatformError{Message: "msg"}).agentText())
}
