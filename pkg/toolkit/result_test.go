package toolkit

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resultText returns the text of a single-content tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "content should be text")
	return tc.Text
}

func TestErrorResult(t *testing.T) {
	res := ErrorResult("boom")
	require.True(t, res.IsError)
	// The message is JSON-escaped via a struct, not formatted into a literal.
	assert.JSONEq(t, `{"error":"boom"}`, resultText(t, res))
}

func TestErrorResult_EscapesMessage(t *testing.T) {
	// A quote in the message must not break the JSON envelope.
	res := ErrorResult(`he said "hi"`)
	var envelope struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &envelope))
	assert.Equal(t, `he said "hi"`, envelope.Error)
}

func TestJSONResult(t *testing.T) {
	res := JSONResult(map[string]any{"n": 1})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"n":1}`, resultText(t, res))
}

func TestJSONResult_MarshalFailureIsInBandError(t *testing.T) {
	// A channel cannot be marshaled; the failure must surface as an in-band
	// error result, never a panic or a dropped response.
	res := JSONResult(make(chan int))
	require.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "marshaling")
}

func TestJSONResultTyped(t *testing.T) {
	res, out, err := JSONResultTyped(map[string]any{"n": 2})
	require.NoError(t, err)
	assert.Nil(t, out, "the structured-output value is unused")
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"n":2}`, resultText(t, res))
}
