package s3

import (
	"encoding/json"

	s3tools "github.com/txn2/mcp-s3/pkg/tools"
)

// schemaKeyType and schemaKeyProperties are the JSON Schema keys the override
// below reads.
const (
	schemaKeyType       = "type"
	schemaKeyProperties = "properties"
)

// nullTolerantOutputSchemas returns mcp-s3's default output schemas with every
// top-level array property also admitting null, for the tools where that
// changes anything (list_buckets and list_objects in mcp-s3 v1.3.0).
//
// mcp-s3 registers typed handlers and answers a failure with an error result
// and a nil typed output. The MCP SDK then substitutes the zero value of the
// output struct and validates it against the tool's output schema before the
// result leaves the handler; a nil slice marshals as null, mcp-s3's schema says
// array, and the validation failure is returned as a JSON-RPC error in place of
// the tool's own error result. A failed listing therefore reached the client as
// "validating tool output: ... has type null, want array" instead of the
// reason the listing failed. Admitting null where the zero value produces it
// lets the tool's error through; a successful listing still validates exactly
// as before, since it never carries null.
func nullTolerantOutputSchemas() map[s3tools.ToolName]any {
	out := map[s3tools.ToolName]any{}
	for _, name := range s3tools.AllTools() {
		schema, changed := nullTolerantSchema(s3tools.DefaultOutputSchema(name))
		if changed {
			out[name] = schema
		}
	}
	return out
}

// nullTolerantSchema returns a copy of an object schema in which each top-level
// property typed "array" is typed ["array","null"], and whether any property
// changed. The copy is taken through a JSON round-trip so mcp-s3's own default
// is never mutated.
func nullTolerantSchema(schema any) (any, bool) {
	if schema == nil {
		return nil, false
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	props, _ := obj[schemaKeyProperties].(map[string]any)
	changed := false
	for _, p := range props {
		prop, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := prop[schemaKeyType].(string); t == "array" {
			prop[schemaKeyType] = []any{"array", "null"}
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	return obj, true
}
