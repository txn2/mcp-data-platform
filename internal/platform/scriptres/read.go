package scriptres

import (
	"encoding/base64"
	"fmt"
	"path"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
)

// CapabilityRead is the member a script reads a managed resource's bytes with
// (#2616).
const CapabilityRead = "platform.read_resource"

// Invoke issues one host call through the run's call path: the write
// barrier, the call, and the recording of its answer.
type Invoke func(b *starlark.Builtin, tool string, payload map[string]any) (map[string]any, error)

// Read is platform.read_resource(resource): the managed resource an address
// (mcp://...) or a reference (mcp:resource:<id>) names, read under the run's
// identity, as a struct of its bytes, their sha256 in hex, its content type,
// its size, its filename and its address. A resource outside what the run's
// identity may read is refused, as the platform's bucket is to every S3 tool.
func Read(invoke Invoke) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ref string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "resource", &ref); err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		out, err := invoke(b, scriptsession.ResourceRead, map[string]any{"resource": ref})
		if err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		return readValue(out)
	}
}

// readValue is a read's answer as the script sees it.
func readValue(out map[string]any) (starlark.Value, error) {
	encoded, _ := out["content"].(string)
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("in %s: the answer's content is not readable: %w", CapabilityRead, err)
	}
	uri, _ := out["uri"].(string)
	contentType, _ := out["content_type"].(string)
	sum, _ := out["sha256"].(string)
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"bytes":        starlark.Bytes(body),
		"sha256":       starlark.String(sum),
		"content_type": starlark.String(contentType),
		"size":         starlark.MakeInt(len(body)),
		"filename":     starlark.String(path.Base(uri)),
		"uri":          starlark.String(uri),
	}), nil
}
