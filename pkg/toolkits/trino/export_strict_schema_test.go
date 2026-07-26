package trino

import (
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

// TestParseExportInput_RejectsUnknownArgumentByName proves the refusal is real
// rather than declarative. trino_export is registered through the untyped
// Server.AddTool path, which does NOT validate arguments against the tool's
// input schema, so the decoder enforces what the schema states.
func TestParseExportInput_RejectsUnknownArgumentByName(t *testing.T) {
	req := mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: exportToolName,
		Arguments: json.RawMessage(
			`{"sql":"SELECT 1","format":"csv","name":"rows","parameters":{"limit":1}}`),
	}}
	_, err := parseExportInput(req)
	if err == nil {
		t.Fatal("unknown `parameters` argument accepted; want a parse error")
	}
	if !strings.Contains(err.Error(), "parameters") {
		t.Errorf("error must name the offending property; got: %v", err)
	}
}

// TestParseExportInput_AcceptsEveryPublishedProperty walks the schema's own
// property set through the decoder, so closing the schema cannot narrow the
// accepted surface.
func TestParseExportInput_AcceptsEveryPublishedProperty(t *testing.T) {
	args := map[string]any{
		"sql": "SELECT 1", "connection": "primary", "format": "csv",
		"name": "rows", "description": "d", "tags": []string{"t"},
		"limit": 10, "idempotency_key": "k1", "timeout_seconds": 30,
		"create_public_link": false,
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
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

	in, err := parseExportInput(mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name: exportToolName, Arguments: raw,
	}})
	if err != nil {
		t.Fatalf("valid arguments rejected: %v", err)
	}
	if in.SQL != "SELECT 1" || in.Format != formatCSV || in.Name != "rows" {
		t.Errorf("decoded input lost fields: %+v", in)
	}
}
