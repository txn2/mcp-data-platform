package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// AddTool registers a typed tool on s, as mcp.AddTool does, with its output
// encoded by the platform's response encoder (internal/wirejson), so an empty
// list in it reaches the client as [] rather than null (#1832). mcp.AddTool
// encodes a typed output with encoding/json, which writes a nil slice as null.
//
// The advertised output schema is the one mcp.AddTool derives from Out when
// the tool declares none, and a nil pointer output is its type's zero value,
// as there. Every platform tool is registered through here; test/structure
// refuses a call to mcp.AddTool anywhere else.
func AddTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	tool := *t
	out := reflect.TypeFor[Out]()
	if tool.OutputSchema == nil && out != reflect.TypeFor[any]() {
		schema, err := jsonschema.ForType(elem(out), &jsonschema.ForOptions{})
		if err != nil {
			// mcp.AddTool panics on the same failure: a tool whose output type
			// has no schema is a programming error, found at registration.
			panic(fmt.Sprintf("tool %q: output schema: %v", t.Name, err))
		}
		tool.OutputSchema = schema
	}
	mcp.AddTool(s, &tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		res, value, err := h(ctx, req, in)
		if err != nil {
			return res, nil, err
		}
		encoded, err := encodeOutput(value)
		if err != nil {
			return nil, nil, fmt.Errorf("marshaling output: %w", err)
		}
		if encoded == nil {
			return res, nil, nil
		}
		return res, encoded, nil
	})
}

// encodeOutput is a typed tool's output as the JSON mcp.AddTool puts in the
// result, or nil for no output: nil when the handler returned none (a nil
// interface), and the zero value of the pointed-to type for a nil pointer.
func encodeOutput(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		value = reflect.Zero(v.Type().Elem()).Interface()
	}
	b, err := wirejson.Marshal(value)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names what failed to marshal
	}
	return b, nil
}

// elem is t, or what t points to: mcp.AddTool derives a pointer output's
// schema from the type it points to.
func elem(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}
