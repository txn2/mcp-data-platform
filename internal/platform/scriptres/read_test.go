package scriptres

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
)

// TestRead is platform.read_resource as a script sees it (#2616): the host
// call it issues, and the struct of bytes and hash it hands back.
func TestRead(t *testing.T) {
	var gotTool string
	var gotArgs map[string]any
	invoke := func(_ *starlark.Builtin, tool string, payload map[string]any) (map[string]any, error) {
		gotTool, gotArgs = tool, payload
		return map[string]any{
			"uri": "mcp://user/jane/data/pay.bin", "content_type": "application/octet-stream", "size": 3,
			"sha256": "abc", "content": base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		}, nil
	}
	b := starlark.NewBuiltin(CapabilityRead, Read(invoke))
	v, err := starlark.Call(&starlark.Thread{}, b, starlark.Tuple{starlark.String("mcp:resource:r1")}, nil)
	require.NoError(t, err)
	assert.Equal(t, scriptsession.ResourceRead, gotTool)
	assert.Equal(t, map[string]any{"resource": "mcp:resource:r1"}, gotArgs)
	s, ok := v.(*starlarkstruct.Struct)
	require.True(t, ok, "the read is a struct")
	field := func(name string) starlark.Value {
		f, err := s.Attr(name)
		require.NoError(t, err)
		return f
	}
	assert.Equal(t, starlark.Bytes([]byte{1, 2, 3}), field("bytes"))
	assert.Equal(t, starlark.String("abc"), field("sha256"))
	assert.Equal(t, starlark.String("pay.bin"), field("filename"))
	assert.Equal(t, starlark.MakeInt(3), field("size"))
	assert.Equal(t, starlark.String("application/octet-stream"), field("content_type"))

	_, err = starlark.Call(&starlark.Thread{}, b, nil, nil)
	require.ErrorContains(t, err, "in platform.read_resource")
	refuse := starlark.NewBuiltin(CapabilityRead, Read(func(*starlark.Builtin, string, map[string]any) (map[string]any, error) {
		return nil, errors.New("no resource the run can read is at mcp://user/bob/x")
	}))
	_, err = starlark.Call(&starlark.Thread{}, refuse, starlark.Tuple{starlark.String("mcp://user/bob/x")}, nil)
	require.ErrorContains(t, err, "in platform.read_resource: no resource the run can read")
	garbled := starlark.NewBuiltin(CapabilityRead, Read(func(*starlark.Builtin, string, map[string]any) (map[string]any, error) {
		return map[string]any{"content": "%%%"}, nil
	}))
	_, err = starlark.Call(&starlark.Thread{}, garbled, starlark.Tuple{starlark.String("mcp://x")}, nil)
	require.ErrorContains(t, err, "not readable")
}
