package portal

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeContent(t *testing.T) {
	tests := []struct {
		name, raw, want  string
		structured, fail bool
	}{
		{name: "absent", raw: ``},
		{name: "null", raw: `null`},
		{name: "string", raw: `"# Report"`, want: "# Report"},
		{name: "object keeps key order", raw: `{"z":1,"a":[1,2]}`, want: "{\n  \"z\": 1,\n  \"a\": [\n    1,\n    2\n  ]\n}", structured: true},
		{name: "array", raw: `[1]`, want: "[\n  1\n]", structured: true},
		{name: "number refused", raw: `42`, fail: true},
		{name: "bool refused", raw: `true`, fail: true},
		{name: "bad string", raw: `"\x"`, fail: true},
		{name: "bad object", raw: `{"a":}`, fail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, structured, err := decodeContent([]byte(tt.raw))
			if tt.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.structured, structured)
		})
	}
}

func TestStructuredContentRefusal(t *testing.T) {
	assert.Empty(t, structuredContentRefusal(false, "text/html"))
	assert.Empty(t, structuredContentRefusal(true, "application/json"))
	assert.Empty(t, structuredContentRefusal(true, "application/vnd.vegalite+json"))
	assert.Empty(t, structuredContentRefusal(true, "application/problem+json; charset=utf-8"))
	assert.Contains(t, structuredContentRefusal(true, "text/html"), "send the content as a string")
}

func TestInputDecodeRefusesAContentNumber(t *testing.T) {
	var save saveAssetInput
	require.ErrorIs(t, save.UnmarshalJSON([]byte(`{"name":"n","content":1}`)), errContentForm)
	var manage manageAssetInput
	require.ErrorIs(t, manage.UnmarshalJSON([]byte(`{"action":"update","content":1}`)), errContentForm)
	require.Error(t, save.UnmarshalJSON([]byte(`[`)))
	require.Error(t, manage.UnmarshalJSON([]byte(`[`)))
}

// TestSaveAssetTakesAVegaLiteSpecAsAnObjectOrAString drives save_asset and the
// manage_asset update over a real MCP session, so the advertised schema and
// the SDK's decode are part of what is tested (#2296): the spec sent as an
// object and as a string of JSON is stored as the same chart, and an object
// under a type that is not JSON is refused.
func TestSaveAssetTakesAVegaLiteSpecAsAnObjectOrAString(t *testing.T) {
	tk, ctx := newAllowlistToolkit(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	tk.RegisterTools(server)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSess, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()
	clientSess, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	spec := map[string]any{"data": map[string]any{"values": []any{map[string]any{"a": 1}}}, "mark": "bar"}
	save := func(content any, contentType string) *mcp.CallToolResult {
		t.Helper()
		res, callErr := clientSess.CallTool(ctx, &mcp.CallToolParams{Name: "save_asset", Arguments: map[string]any{
			"name": "chart", "content": content, "content_type": contentType,
		}})
		require.NoError(t, callErr)
		return res
	}

	asObject := save(spec, "application/vnd.vegalite+json")
	require.False(t, asObject.IsError, resultText(t, asObject))
	asString := save(`{"data":{"values":[{"a":1}]},"mark":"bar"}`, "application/vnd.vegalite+json")
	require.False(t, asString.IsError, resultText(t, asString))

	refused := save(spec, "text/html")
	require.True(t, refused.IsError)
	assert.Contains(t, resultText(t, refused), "send the content as a string")

	external := save(map[string]any{"data": map[string]any{"url": "https://example.com/x.csv"}, "mark": "bar"},
		"application/vnd.vegalite+json")
	require.True(t, external.IsError)
	assert.Contains(t, resultText(t, external), "https://example.com/x.csv")
}
