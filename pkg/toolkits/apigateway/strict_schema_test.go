package apigateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// strictSchemaCase pairs a tool's published input schema with the Go struct its
// handler decodes into. The two must agree exactly: a property the struct does
// not carry is a silently-dropped argument, and a struct field the schema does
// not publish is an argument a closed schema now REFUSES. Both directions are
// asserted so the closure cannot rot into a rejected-valid-call bug.
type strictSchemaCase struct {
	tool   string
	schema json.RawMessage
	input  any
}

func strictSchemaCases() []strictSchemaCase {
	return []strictSchemaCase{
		{ToolInvokeEndpoint, invokeEndpointSchema, InvokeInput{}},
		{ToolListEndpoints, listEndpointsSchema, ListEndpointsInput{}},
		{ToolListSpecs, listSpecsSchema, ListSpecsInput{}},
		{ToolGetEndpointSchema, getEndpointSchemaInputSchema, GetEndpointSchemaInput{}},
		{exportToolName, apiExportInputSchema, exportInput{}},
	}
}

// schemaProperties returns the top-level property names of a JSON Schema and
// whether it is closed to unknown properties.
func schemaProperties(t *testing.T, raw json.RawMessage) (names map[string]bool, closed bool) {
	t.Helper()
	var obj struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	names = make(map[string]bool, len(obj.Properties))
	for name := range obj.Properties {
		names[name] = true
	}
	return names, obj.AdditionalProperties != nil && !*obj.AdditionalProperties
}

// structJSONNames returns the JSON argument names a struct decodes.
func structJSONNames(v any) map[string]bool {
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

// TestAPISchemas_ClosedAndInSyncWithInputStructs is the static half of issue
// #1057: every api_* schema refuses unknown top-level arguments, and its
// property set is exactly the set of arguments the handler struct decodes.
func TestAPISchemas_ClosedAndInSyncWithInputStructs(t *testing.T) {
	for _, tc := range strictSchemaCases() {
		t.Run(tc.tool, func(t *testing.T) {
			props, closed := schemaProperties(t, tc.schema)
			if !closed {
				t.Errorf("%s: schema must declare \"additionalProperties\": false so a misnamed "+
					"argument is refused instead of silently dropped", tc.tool)
			}
			fields := structJSONNames(tc.input)
			for name := range props {
				if !fields[name] {
					t.Errorf("%s: schema publishes %q but the input struct does not decode it", tc.tool, name)
				}
			}
			for name := range fields {
				if !props[name] {
					t.Errorf("%s: input struct decodes %q but the closed schema does not publish it, "+
						"so a caller passing it is now refused", tc.tool, name)
				}
			}
		})
	}
}

// TestAPISchemas_NestedMapsStayOpen locks in the deliberate asymmetry: the
// tool's OWN argument names are strict, but query_params / headers / body carry
// the upstream API's namespace and must keep accepting arbitrary keys.
func TestAPISchemas_NestedMapsStayOpen(t *testing.T) {
	for _, raw := range []json.RawMessage{invokeEndpointSchema, apiExportInputSchema} {
		var obj struct {
			Properties map[string]struct {
				AdditionalProperties json.RawMessage `json:"additionalProperties"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("unmarshal schema: %v", err)
		}
		for _, name := range []string{"query_params", "headers", "path_params"} {
			if got := string(obj.Properties[name].AdditionalProperties); got == "" || got == "false" {
				t.Errorf("%s must stay open to arbitrary upstream keys; additionalProperties = %q", name, got)
			}
		}
		if _, hasBody := obj.Properties["body"]; !hasBody {
			t.Error("body property missing")
		}
	}
}

// strictSchemaServer registers the real api_* tools (including api_export) on a
// real mcp.Server and returns a connected client session. ExportDeps are wired
// empty on purpose: the assertions here are about the schema boundary, which the
// SDK enforces BEFORE the handler runs.
func strictSchemaServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": upstream.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.SetExportDeps(ExportDeps{})

	server := mcp.NewServer(&mcp.Implementation{Name: "strict-schema-test", Version: "v0"}, nil)
	tk.RegisterTools(server)

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "strict-schema-client", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// callText runs a tool and reports whether the result was an error along with
// its joined text content.
func callText(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (isError bool, text string) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return res.IsError, sb.String()
}

// TestAPITools_RejectUnknownArgumentByName is the behavioral half of issue
// #1057, through a real assembled mcp.Server: a misnamed top-level argument is
// refused at the tool boundary with an error naming it, before the handler (and
// therefore before any upstream HTTP call) runs.
func TestAPITools_RejectUnknownArgumentByName(t *testing.T) {
	sess := strictSchemaServer(t)

	cases := []struct {
		tool string
		args map[string]any
	}{
		// The reported shape: `parameters` instead of `query_params`.
		{ToolInvokeEndpoint, map[string]any{
			"connection": "crm", "method": "GET", "path": "/v1/things",
			"parameters": map[string]any{"limit": 1},
		}},
		{ToolListEndpoints, map[string]any{"connection": "crm", "parameters": "x"}},
		{ToolListSpecs, map[string]any{"connection": "crm", "parameters": "x"}},
		{ToolGetEndpointSchema, map[string]any{
			"connection": "crm", "operation_id": "getThings", "parameters": "x",
		}},
		{exportToolName, map[string]any{
			"connection": "crm", "name": "things", "method": "GET", "path": "/v1/things",
			"parameters": map[string]any{"limit": 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			isErr, text := callText(t, sess, tc.tool, tc.args)
			if !isErr {
				t.Fatalf("%s accepted an unknown `parameters` argument; want a validation error. got: %s",
					tc.tool, text)
			}
			if !strings.Contains(text, "parameters") {
				t.Errorf("%s: validation error must name the offending property; got: %s", tc.tool, text)
			}
		})
	}
}

// TestAPIInvoke_UnknownArgumentRefusedBeforeUpstreamCall proves the refusal is a
// boundary refusal, not a late one: the upstream connection never sees a
// request. This is what turns a multi-call diagnosis into a one-step correction.
func TestAPIInvoke_UnknownArgumentRefusedBeforeUpstreamCall(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": upstream.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "strict-schema-test", Version: "v0"}, nil)
	tk.RegisterTools(server)

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "strict-schema-client", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if isErr, text := callText(t, sess, ToolInvokeEndpoint, map[string]any{
		"connection": "crm", "method": "GET", "path": "/v1/things",
		"parameters": map[string]any{"limit": 1},
	}); !isErr {
		t.Fatalf("unknown argument accepted: %s", text)
	}
	if hits != 0 {
		t.Errorf("upstream was called %d time(s); a schema refusal must precede the HTTP call", hits)
	}

	// The corrected call — same intent, right argument name — goes through.
	if isErr, text := callText(t, sess, ToolInvokeEndpoint, map[string]any{
		"connection": "crm", "method": "GET", "path": "/v1/things",
		"query_params": map[string]any{"limit": 1},
	}); isErr {
		t.Fatalf("valid call rejected: %s", text)
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d; want 1 after the corrected call", hits)
	}
}

// TestAPITools_ValidArgumentsUnaffected walks every published property of every
// api_* schema through the boundary. None may be refused as an unknown
// property — closing the schemas must not narrow the accepted surface.
func TestAPITools_ValidArgumentsUnaffected(t *testing.T) {
	sess := strictSchemaServer(t)

	// One representative value per property type, so the call exercises the
	// property rather than a type error.
	sample := map[string]any{
		"connection": "crm", "operation_id": "getThings", "spec": "default",
		"method": "GET", "path": "/v1/things", "query": "things", "limit": 5,
		"ranking": "lexical", "path_params": map[string]any{"id": "1"},
		"query_params": map[string]any{"limit": 1}, "headers": map[string]any{"X-Trace": "t"},
		"body": map[string]any{"k": "v"}, "timeout_seconds": 5,
		"name": "things", "description": "d", "tags": []any{"t"},
		"idempotency_key": "k1", "create_public_link": false,
	}

	for _, tc := range strictSchemaCases() {
		t.Run(tc.tool, func(t *testing.T) {
			props, _ := schemaProperties(t, tc.schema)
			args := make(map[string]any, len(props))
			for name := range props {
				value, ok := sample[name]
				if !ok {
					t.Fatalf("no sample value for published property %q", name)
				}
				args[name] = value
			}
			_, text := callText(t, sess, tc.tool, args)
			if strings.Contains(text, "additional properties") {
				t.Errorf("%s refused its own published arguments: %s", tc.tool, text)
			}
		})
	}
}

// TestAPIInvoke_SessionHandleArgumentIsNotPublished documents why closing these
// schemas is safe alongside the platform-injected session_id: the schemas
// themselves never publish it. The middleware adds it to the tools/list view and
// strips it from the arguments before the SDK validates them; the end-to-end
// proof of that ordering lives in
// pkg/middleware/strict_schema_session_integration_test.go.
func TestAPIInvoke_SessionHandleArgumentIsNotPublished(t *testing.T) {
	for _, tc := range strictSchemaCases() {
		props, _ := schemaProperties(t, tc.schema)
		if props["session_id"] {
			t.Errorf("%s publishes session_id; it is a platform-injected argument, not a tool argument", tc.tool)
		}
	}
}
