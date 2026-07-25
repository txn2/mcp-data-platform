package memory

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// schemaShape reports a JSON Schema's top-level property names and whether it is
// closed to unknown properties.
func schemaShape(t *testing.T, raw json.RawMessage) (props map[string]bool, closed bool) {
	t.Helper()
	var obj struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props = make(map[string]bool, len(obj.Properties))
	for name := range obj.Properties {
		props[name] = true
	}
	return props, obj.AdditionalProperties != nil && !*obj.AdditionalProperties
}

// jsonFieldNames returns the argument names a struct decodes.
func jsonFieldNames(v any) map[string]bool {
	names := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeOf(v)) {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		names[tag] = true
	}
	return names
}

// TestMemorySchemas_ClosedAndInSyncWithInputStructs holds both memory tools to
// the issue #1057 contract: unknown top-level arguments are refused, and the
// published properties are exactly the arguments the handler decodes. Before
// this, memory_manage published neither `source` nor `entity_urns` yet decoded
// both, so passing either was a no-op the caller never heard about.
func TestMemorySchemas_ClosedAndInSyncWithInputStructs(t *testing.T) {
	cases := []struct {
		tool   string
		schema json.RawMessage
		input  any
	}{
		{manageToolName, memoryManageSchema, manageInput{}},
		{memoryCaptureToolName, memoryCaptureSchema, memoryCaptureInput{}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			props, closed := schemaShape(t, tc.schema)
			if !closed {
				t.Errorf("%s: schema must declare \"additionalProperties\": false", tc.tool)
			}
			fields := jsonFieldNames(tc.input)
			for name := range props {
				if !fields[name] {
					t.Errorf("%s: schema publishes %q but the input struct does not decode it", tc.tool, name)
				}
			}
			for name := range fields {
				if !props[name] {
					t.Errorf("%s: input struct decodes %q but the closed schema does not publish it", tc.tool, name)
				}
			}
		})
	}
}

// TestMemoryManage_RejectsUnknownArgumentByName proves the refusal through a
// real mcp.Server: a misnamed argument comes back as a validation error naming
// the property, while the same call without it reaches the handler.
func TestMemoryManage_RejectsUnknownArgumentByName(t *testing.T) {
	ctx := context.Background()
	tk := newTestToolkit(&mockStore{}, nil)

	server := mcp.NewServer(&mcp.Implementation{Name: "memory-strict-schema", Version: "v0"}, nil)
	tk.RegisterTools(server)
	// The platform context every memory handler reads is established by
	// MCPToolCallMiddleware in production; stand in for it so the valid call
	// exercises the handler rather than a missing-context path.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			pc := middleware.NewPlatformContext("test-req")
			pc.UserEmail = "analyst@example.com"
			pc.PersonaName = "analyst"
			return next(middleware.WithPlatformContext(ctx, pc), method, req)
		}
	})

	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      manageToolName,
		Arguments: map[string]any{"command": "list", "entity_urns": []any{"urn:li:dataset:x"}},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("memory_manage accepted an argument it does not implement; want a validation error")
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if text := sb.String(); !strings.Contains(text, "entity_urns") {
		t.Errorf("validation error must name the offending property; got: %s", text)
	}

	res, err = sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      manageToolName,
		Arguments: map[string]any{"command": "list"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Errorf("valid memory_manage call rejected: %v", res.Content)
	}
}
