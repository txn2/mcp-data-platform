package trino

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestExportInputSchema_ClosedAndInSyncWithInputStruct holds trino_export to the
// issue #1057 contract: the published schema refuses unknown top-level
// arguments, and its properties are exactly the arguments exportInput decodes.
func TestExportInputSchema_ClosedAndInSyncWithInputStruct(t *testing.T) {
	raw, err := json.Marshal(exportInputSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var obj struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if obj.AdditionalProperties == nil || *obj.AdditionalProperties {
		t.Error("trino_export schema must declare \"additionalProperties\": false")
	}

	fields := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeFor[exportInput]()) {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			fields[tag] = true
		}
	}
	for name := range obj.Properties {
		if !fields[name] {
			t.Errorf("schema publishes %q but exportInput does not decode it", name)
		}
	}
	for name := range fields {
		if _, published := obj.Properties[name]; !published {
			t.Errorf("exportInput decodes %q but the closed schema does not publish it", name)
		}
	}
}

// TestExportRegistration_RejectsUnknownArgumentByName proves the refusal is
// real rather than declarative. trino_export registers through the generic
// mcp.AddTool, so the SDK validates every call against exportInputSchema before
// the handler runs, and a misnamed argument is refused by name (#1057).
func TestExportRegistration_RejectsUnknownArgumentByName(t *testing.T) {
	sess := connectExportServer(t, newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{}))
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: exportToolName,
		Arguments: map[string]any{
			"sql": "SELECT 1", "format": "csv", "name": "rows", "parameters": map[string]any{"limit": 1},
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("unknown `parameters` argument accepted; want a refusal")
	}
	if text := firstTextBlock(res); !strings.Contains(text, "parameters") {
		t.Errorf("refusal must name the offending property; got: %s", text)
	}
	if res.StructuredContent != nil {
		t.Errorf("a refusal carries no structured result; got %v", res.StructuredContent)
	}
}

// TestExportRegistration_AcceptsEveryPublishedProperty walks the schema's own
// property set through the registered tool, so closing the schema cannot
// narrow the accepted surface: every published argument passes the SDK's
// validation and reaches the handler decoded.
func TestExportRegistration_AcceptsEveryPublishedProperty(t *testing.T) {
	args := map[string]any{
		"sql": "SELECT 1", "connection": "primary", "format": "csv",
		"name": "rows", "description": "d", "tags": []string{"t"},
		"limit": 10, "idempotency_key": "k1", "timeout_seconds": 30,
		"create_public_link": false,
	}
	// Every published property must appear in the sample, or the walk proves
	// nothing about the ones it skipped.
	schemaRaw, err := json.Marshal(exportInputSchema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var obj struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(schemaRaw, &obj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	for name := range obj.Properties {
		if _, ok := args[name]; !ok {
			t.Fatalf("published property %q missing from the sample call", name)
		}
	}

	// The toolkit has no Trino client, so a call that passes validation and
	// reaches the query is refused there, naming the missing client; a call
	// refused by the SDK's validation never gets that far.
	sess := connectExportServer(t, newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{}))
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: exportToolName, Arguments: args})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if text := firstTextBlock(res); !strings.Contains(text, "query execution failed") {
		t.Fatalf("valid arguments did not reach the handler's query step; got: %s", text)
	}
}
