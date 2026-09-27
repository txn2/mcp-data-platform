package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typedOut is the shape of a third-party toolkit's typed handler output: the
// SDK infers a schema for it and jsonschema-go closes that schema, exactly as it
// does for mcp-trino's QueryOutput.
type typedOut struct {
	Rows  int    `json:"rows"`
	Query string `json:"query"`
}

const (
	osToolTyped     = "os_typed"
	osToolExplicit  = "os_explicit"
	osToolArray     = "os_array"
	osToolSchemless = "os_none"
	osToolTypedErr  = "os_typed_error"
)

// newOutputSchemaServer assembles a server with the registration styles the
// platform hosts: a typed handler with an inferred (closed) schema, an explicit
// open schema like mcp-s3's, a non-object schema, no schema, and a typed
// handler that fails. A stand-in for the call-reference middleware appends a
// top-level key to every successful result, and the error contract replaces a
// failed result's body, as they do in the platform chain. withDecorator toggles
// the middleware under test.
func newOutputSchemaServer(withDecorator bool) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "os", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: osToolTyped},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, *typedOut, error) {
			return nil, &typedOut{Rows: 1, Query: "select 1"}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: osToolTypedErr},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, *typedOut, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "connection refused"}}}, nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{
		Name:         osToolExplicit,
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer"}}},
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"n": 1}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:         osToolArray,
		OutputSchema: map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, []string{"a"}, nil
	})
	server.AddTool(&mcp.Tool{Name: osToolSchemless, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})

	server.AddReceivingMiddleware(MCPErrorContractMiddleware())
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodToolsCall {
				return result, err
			}
			if ctr, ok := result.(*mcp.CallToolResult); ok && !ctr.IsError {
				appendCallReference(ctr, "evt-1")
			}
			return result, nil
		}
	})
	if withDecorator {
		server.AddReceivingMiddleware(MCPOutputSchemaMiddleware())
	}
	return server
}

// advertisedSchemas lists the server's tools and resolves every advertised
// output schema, keyed by tool name.
func advertisedSchemas(ctx context.Context, t *testing.T, cs *mcp.ClientSession) (resolved map[string]*jsonschema.Resolved, tools map[string]*mcp.Tool) {
	t.Helper()
	lt, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	resolved = map[string]*jsonschema.Resolved{}
	tools = map[string]*mcp.Tool{}
	for _, tool := range lt.Tools {
		tools[tool.Name] = tool
		if tool.OutputSchema == nil {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		require.NoError(t, err)
		var s jsonschema.Schema
		require.NoError(t, json.Unmarshal(raw, &s))
		r, err := s.Resolve(nil)
		require.NoError(t, err)
		resolved[tool.Name] = r
	}
	return resolved, tools
}

func validateAgainst(t *testing.T, resolved *jsonschema.Resolved, sc any) error {
	t.Helper()
	b, err := json.Marshal(sc)
	require.NoError(t, err)
	var v any
	require.NoError(t, json.Unmarshal(b, &v))
	if err := resolved.Validate(v); err != nil {
		return fmt.Errorf("validating structuredContent: %w", err)
	}
	return nil
}

// TestMCPOutputSchemaMiddleware_ResultsValidateAgainstWhatWasAdvertised is the
// #1381 contract: a tool result that crossed the platform chain validates
// against the output schema the same server advertised for it in tools/list,
// whether the handler succeeded (the platform appended call_reference) or
// failed (the error contract replaced the body). The negative control proves
// the harness sees the defect: without the decorator a typed handler's closed
// inferred schema rejects both.
func TestMCPOutputSchemaMiddleware_ResultsValidateAgainstWhatWasAdvertised(t *testing.T) {
	for _, withDecorator := range []bool{true, false} {
		ctx := context.Background()
		server := newOutputSchemaServer(withDecorator)
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		require.NoError(t, err)
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
		require.NoError(t, err)

		schemas, tools := advertisedSchemas(ctx, t, cs)
		require.Contains(t, schemas, osToolTyped, "a typed handler advertises an inferred schema")
		require.Contains(t, schemas, osToolExplicit)
		require.Contains(t, schemas, osToolArray)
		assert.Nil(t, tools[osToolSchemless].OutputSchema, "a tool with no schema still has none")

		for _, name := range []string{osToolTyped, osToolTypedErr, osToolExplicit} {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
			require.NoError(t, err)
			require.NotNil(t, res.StructuredContent, "%s emits structuredContent", name)
			verr := validateAgainst(t, schemas[name], res.StructuredContent)
			switch {
			case withDecorator:
				assert.NoError(t, verr, "%s: result validates against the advertised schema", name)
			case name == osToolExplicit:
				assert.NoError(t, verr, "%s: an explicit open schema admits the platform's keys either way", name)
			default:
				assert.Error(t, verr, "negative control: %s is rejected by the closed inferred schema", name)
			}
		}

		// The non-object schema is advertised unchanged, and its result (which
		// the platform cannot add keys to) validates as the toolkit declared.
		raw, err := json.Marshal(tools[osToolArray].OutputSchema)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"array","items":{"type":"string"}}`, string(raw))

		require.NoError(t, cs.Close())
		require.NoError(t, ss.Close())
	}
}

// TestMCPOutputSchemaMiddleware_LeavesTheServerRegistryUntouched proves the
// decorator rewrites a copy of the listed tool, not the registered one: the
// Tool the handler returned is unchanged and the list now points at a copy.
func TestMCPOutputSchemaMiddleware_LeavesTheServerRegistryUntouched(t *testing.T) {
	closed := map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	}
	registered := &mcp.Tool{Name: "x", OutputSchema: closed}
	none := &mcp.Tool{Name: "y"}
	handler := MCPOutputSchemaMiddleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{Tools: []*mcp.Tool{registered, none, nil}}, nil
	})
	res, err := handler(context.Background(), methodToolsList, &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}})
	require.NoError(t, err)
	listed, ok := res.(*mcp.ListToolsResult)
	require.True(t, ok)

	assert.NotSame(t, registered, listed.Tools[0], "the listed tool is a copy")
	assert.Same(t, none, listed.Tools[1], "a tool with no schema is listed as is")
	assert.Equal(t, false, closed["additionalProperties"], "the registered schema is untouched")
	assert.Contains(t, closed, "required")
	opened, ok := listed.Tools[0].OutputSchema.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, opened["additionalProperties"])
	assert.NotContains(t, opened, "required")
	assert.Contains(t, opened["properties"], errorEnvelopeKey)

	// Other methods and non-list results pass through untouched.
	other := &mcp.CallToolResult{}
	passthrough := MCPOutputSchemaMiddleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) { return other, nil })
	res, err = passthrough(context.Background(), methodToolsCall, &mcp.CallToolRequest{})
	require.NoError(t, err)
	assert.Same(t, other, res)
	assert.Same(t, other, openListedOutputSchemas(other))
}

// TestOpenOutputSchema covers the shapes the normalizer sees: a closed
// object schema is opened and documented, a tool's own "error" property is
// kept, a non-object schema and a non-schema value are refused.
func TestOpenOutputSchema(t *testing.T) {
	opened, ok := openOutputSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"a"},
		"properties":           map[string]any{"a": map[string]any{"type": "string"}},
	})
	require.True(t, ok)
	obj, props := openedObject(t, opened)
	assert.Equal(t, true, obj["additionalProperties"])
	assert.NotContains(t, obj, "required")
	assert.Contains(t, props, "a")
	assert.Contains(t, props, errorEnvelopeKey)

	own := map[string]any{"type": "string", "description": "mine"}
	opened, ok = openOutputSchema(map[string]any{"type": "object", "properties": map[string]any{errorEnvelopeKey: own}})
	require.True(t, ok)
	_, props = openedObject(t, opened)
	assert.Equal(t, own, props[errorEnvelopeKey], "a tool's own error property is kept")

	opened, ok = openOutputSchema(map[string]any{"properties": map[string]any{}})
	require.True(t, ok, "an object schema with no explicit type is an object schema")
	obj, _ = openedObject(t, opened)
	assert.Equal(t, true, obj["additionalProperties"])

	_, ok = openOutputSchema(map[string]any{"type": "array"})
	assert.False(t, ok)
	_, ok = openOutputSchema("not a schema")
	assert.False(t, ok)
	_, ok = openOutputSchema(make(chan int))
	assert.False(t, ok, "an unmarshalable value is refused, not panicked on")

	// The same contract OpenToolOutputSchema gives a platform-owned schema.
	fromTyped := MustOutputSchema[typedOut]()
	opened, ok = openOutputSchema(fromTyped)
	require.True(t, ok)
	raw, err := json.Marshal(opened)
	require.NoError(t, err)
	var s jsonschema.Schema
	require.NoError(t, json.Unmarshal(raw, &s))
	assert.NotNil(t, s.AdditionalProperties)
	assert.Nil(t, s.Required)
	assert.Contains(t, s.Properties, errorEnvelopeKey)
}

// openedObject reads an opened schema back as its object and property maps.
func openedObject(t *testing.T, opened any) (obj, props map[string]any) {
	t.Helper()
	obj, ok := opened.(map[string]any)
	require.True(t, ok, "an opened schema is a map")
	props, ok = obj["properties"].(map[string]any)
	require.True(t, ok, "an opened schema carries a properties map")
	return obj, props
}

// TestOpenOutputSchema_OpensNestedObjects is #1945: every object beneath the top
// level is advertised open, wherever the schema nests it (a property, an array's
// items, a map's value type, a $defs entry, a combinator branch), so a key a
// later release adds to a nested type validates against a schema a client
// cached before the upgrade. A property named "additionalProperties" is a
// property, not the keyword, and is left alone; nested required lists are kept.
func TestOpenOutputSchema_OpensNestedObjects(t *testing.T) {
	closed := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props}
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"notices": map[string]any{
				"type": "object", "additionalProperties": false, "required": []any{"count"},
				"properties": map[string]any{"count": map[string]any{"type": "integer"}},
			},
			"list":    map[string]any{"type": "array", "items": closed(map[string]any{"a": map[string]any{"type": "string"}})},
			"byName":  map[string]any{"type": "object", "additionalProperties": closed(map[string]any{})},
			"either":  map[string]any{"anyOf": []any{closed(map[string]any{}), true}},
			"literal": map[string]any{"type": "object", "properties": map[string]any{"additionalProperties": false}},
			"strict":  map[string]any{"type": "object", "unevaluatedProperties": false},
		},
		"$defs": map[string]any{"Inner": closed(map[string]any{})},
	}
	opened, ok := openOutputSchema(schema)
	require.True(t, ok)
	_, props := openedObject(t, opened)

	notices := schemaAt(t, props, "notices")
	assert.Equal(t, true, schemaAt(t, notices, "additionalProperties"))
	assert.Equal(t, []any{"count"}, schemaAt(t, notices, "required"), "a nested required list is kept")
	assert.Equal(t, true, schemaAt(t, props, "list", "items", "additionalProperties"))
	assert.Equal(t, true, schemaAt(t, props, "byName", "additionalProperties", "additionalProperties"),
		"a map's value type is walked, not replaced")
	assert.Equal(t, true, schemaAt(t, props, "either", "anyOf", 0, "additionalProperties"))
	assert.Equal(t, false, schemaAt(t, props, "literal", "properties", "additionalProperties"),
		"a property named additionalProperties is not the keyword")
	assert.Equal(t, true, schemaAt(t, props, "strict", "unevaluatedProperties"))
	assert.Equal(t, true, schemaAt(t, opened, "$defs", "Inner", "additionalProperties"))

	// The case the ticket reports: a nested type gains a key after a client
	// listed tools. Validated against what was advertised, it passes; against
	// the closed nested schema, it is rejected.
	type notice struct {
		Count int `json:"count"`
	}
	type body struct {
		Notices notice `json:"notices"`
	}
	strict := MustOutputSchema[body]()
	later := map[string]any{"notices": map[string]any{"count": 1, "added_next_release": 2}}
	for _, tc := range []struct {
		schema any
		wantOK bool
	}{{strict, false}, {mustOpen(t, strict), true}} {
		raw, err := json.Marshal(tc.schema)
		require.NoError(t, err)
		var s jsonschema.Schema
		require.NoError(t, json.Unmarshal(raw, &s))
		resolved, err := s.Resolve(nil)
		require.NoError(t, err)
		if tc.wantOK {
			assert.NoError(t, validateAgainst(t, resolved, later))
		} else {
			assert.Error(t, validateAgainst(t, resolved, later), "negative control: the closed nested object rejects the new key")
		}
	}
}

// schemaAt walks v by map keys and list indexes, failing the test when a step
// is not there.
func schemaAt(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch k := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			require.True(t, ok, "%v is not an object", v)
			v = m[k]
		case int:
			l, ok := v.([]any)
			require.True(t, ok, "%v is not a list", v)
			require.Greater(t, len(l), k)
			v = l[k]
		}
	}
	return v
}

func mustOpen(t *testing.T, schema any) any {
	t.Helper()
	opened, ok := openOutputSchema(schema)
	require.True(t, ok)
	return opened
}
