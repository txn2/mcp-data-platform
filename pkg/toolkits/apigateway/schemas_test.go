package apigateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestInvokeInputJSONTag_QueryParams locks in the rename from
// `query` → `query_params` for api_invoke_endpoint. The schema name
// must match the struct tag or the model's argument silently
// disappears (the field stays zero) — exactly the failure mode
// issue #388 reported.
func TestInvokeInputJSONTag_QueryParams(t *testing.T) {
	raw := []byte(`{"connection":"c","method":"GET","path":"/x","query_params":{"bytes":128}}`)
	var in InvokeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := in.Query["bytes"], float64(128); got != want {
		t.Errorf("query_params not decoded into Query: got %v (%T), want %v", got, got, want)
	}

	// The old `query` name must no longer populate the field — a
	// caller that still passes `query` should get nothing through,
	// not a silent fallback that masks the change.
	raw = []byte(`{"connection":"c","method":"GET","path":"/x","query":{"bytes":128}}`)
	in = InvokeInput{}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.Query != nil {
		t.Errorf("legacy `query` field should no longer populate Query; got %v", in.Query)
	}
}

// TestExportInputJSONTag_QueryParams mirrors the invoke test for
// api_export — same rename, same risk of a silent miss.
func TestExportInputJSONTag_QueryParams(t *testing.T) {
	raw := []byte(`{"connection":"c","method":"GET","path":"/x","name":"n","query_params":{"k":"v"}}`)
	var in exportInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := in.Query["k"], "v"; got != want {
		t.Errorf("query_params not decoded into Query: got %v, want %v", got, want)
	}

	raw = []byte(`{"connection":"c","method":"GET","path":"/x","name":"n","query":{"k":"v"}}`)
	in = exportInput{}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.Query != nil {
		t.Errorf("legacy `query` field should no longer populate Query; got %v", in.Query)
	}
}

// TestSchemas_QueryParamsRename verifies the JSON Schema published
// to the model matches the struct tag. A drift between the two is
// what makes the rename load-bearing: the schema is what the
// model's tool-call validator consults.
func TestSchemas_QueryParamsRename(t *testing.T) {
	invoke := string(invokeEndpointSchema)
	if !strings.Contains(invoke, `"query_params"`) {
		t.Error("invokeEndpointSchema missing query_params property")
	}
	if strings.Contains(invoke, `"query":`) {
		t.Error("invokeEndpointSchema still defines a query property; should be query_params")
	}

	export := string(apiExportInputSchema)
	if !strings.Contains(export, `"query_params"`) {
		t.Error("apiExportInputSchema missing query_params property")
	}
	if strings.Contains(export, `"query":`) {
		t.Error("apiExportInputSchema still defines a query property; should be query_params")
	}

	// list_endpoints intentionally keeps `query` (search text, not
	// HTTP query params). Guard against an over-eager rename.
	list := string(listEndpointsSchema)
	if !strings.Contains(list, `"query":`) {
		t.Error("listEndpointsSchema must keep the `query` search-text property")
	}
}
