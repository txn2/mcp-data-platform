package scriptsession

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readServer serves one managed resource at mcp://user/jane/pay.bin by
// resources/read, and manage_resource get resolving its reference.
func readServer(t *testing.T, body []byte) *SessionCaller {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	server.AddResource(&mcp.Resource{URI: "mcp://user/jane/pay.bin", Name: "pay.bin", MIMEType: "application/octet-stream"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/octet-stream", Blob: body}}}, nil
		})
	server.AddResource(&mcp.Resource{URI: "mcp://user/jane/note.txt", Name: "note.txt", MIMEType: "text/plain"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/plain", Text: "hello"}}}, nil
		})
	server.AddTool(&mcp.Tool{Name: "manage_resource", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in map[string]any
			_ = json.Unmarshal(req.Params.Arguments, &in)
			out := map[string]any{"found": false}
			if in["reference"] == "mcp:resource:r1" {
				out = map[string]any{"found": true, "uri": "mcp://user/jane/pay.bin"}
			}
			return &mcp.CallToolResult{StructuredContent: out, Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
		})
	caller, closeFn, err := Connect(context.Background(), server, "test")
	require.NoError(t, err)
	t.Cleanup(closeFn)
	return caller
}

// TestSessionCaller_ReadsAResource is #2616's script read: the bytes of a
// resource by its address or its reference, with their sha256.
func TestSessionCaller_ReadsAResource(t *testing.T) {
	body := []byte{0xff, 0x00, 0x10, 'p', 'a', 'y'}
	caller := readServer(t, body)
	sum := sha256.Sum256(body)
	for _, ref := range []string{"mcp://user/jane/pay.bin", " mcp:resource:r1 "} {
		out, err := caller.CallTool(context.Background(), ResourceRead, map[string]any{"resource": ref})
		require.NoError(t, err, ref)
		assert.Equal(t, base64.StdEncoding.EncodeToString(body), out["content"], ref)
		assert.Equal(t, hex.EncodeToString(sum[:]), out["sha256"])
		assert.Equal(t, "mcp://user/jane/pay.bin", out["uri"])
		assert.Equal(t, "application/octet-stream", out["content_type"])
		assert.EqualValues(t, len(body), out["size"])
	}
	out, err := caller.CallTool(context.Background(), ResourceRead, map[string]any{"resource": "mcp://user/jane/note.txt"})
	require.NoError(t, err)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("hello")), out["content"], "text arrives as its bytes")
}

// TestSessionCaller_RefusesAReadItCannotMake: an address the run cannot
// read, a reference that names nothing, and a name that is neither.
func TestSessionCaller_RefusesAReadItCannotMake(t *testing.T) {
	caller := readServer(t, []byte("x"))
	tests := []struct{ ref, want string }{
		{"mcp://user/bob/secret.bin", "no resource the run can read is at mcp://user/bob/secret.bin"},
		{"mcp:resource:r9", "nothing the run can read is filed under mcp:resource:r9"},
		{"pay.bin", "neither a resource address nor a reference"},
		{"", "name the resource"},
	}
	for _, tt := range tests {
		_, err := caller.CallTool(context.Background(), ResourceRead, map[string]any{"resource": tt.ref})
		require.ErrorContains(t, err, tt.want, tt.ref)
	}
}

// TestSessionCaller_RefusesAReadPastTheCap: a resource larger than one read
// hands a script is refused, naming its size.
func TestSessionCaller_RefusesAReadPastTheCap(t *testing.T) {
	caller := readServer(t, make([]byte, 11))
	assert.Equal(t, MaxResourceReadBytes, caller.readLimit())
	caller.maxRead = 10
	_, err := caller.CallTool(context.Background(), ResourceRead, map[string]any{"resource": "mcp://user/jane/pay.bin"})
	require.ErrorContains(t, err, "is 11 bytes, over the 10 bytes one read hands a script")

	far := readServer(t, make([]byte, 16<<20))
	_, err = far.CallTool(context.Background(), ResourceRead, map[string]any{"resource": "mcp://user/jane/pay.bin"})
	require.ErrorContains(t, err, "is larger than the 8388608 bytes one read hands a script", "past the transport's frame, it is told the cap")
}
