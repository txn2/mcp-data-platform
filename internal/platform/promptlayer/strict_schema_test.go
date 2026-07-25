package promptlayer

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// promptSchemaShape reports a schema's top-level property names and whether it
// is closed to unknown properties.
func promptSchemaShape(t *testing.T, schema any) (props map[string]bool, closed bool) {
	t.Helper()
	raw, err := json.Marshal(schema)
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
	props = make(map[string]bool, len(obj.Properties))
	for name := range obj.Properties {
		props[name] = true
	}
	return props, obj.AdditionalProperties != nil && !*obj.AdditionalProperties
}

// promptJSONFields returns the argument names a struct decodes.
func promptJSONFields(v any) map[string]bool {
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

// TestPromptSchemas_ClosedAndInSyncWithInputStructs holds both prompt tools to
// the issue #1057 contract. It also pins the fix for the drift it exposed:
// manage_prompt splices the shared textpatch grammar, which publishes
// `occurrence`, but its input struct did not decode it — so an agent
// disambiguating a selector match was silently ignored.
func TestPromptSchemas_ClosedAndInSyncWithInputStructs(t *testing.T) {
	cases := []struct {
		tool   string
		schema any
		input  any
	}{
		{ToolNameManagePrompt, managePromptSchema(), managePromptInput{}},
		{ToolNameShowPrompts, showPromptsSchema(), showPromptsInput{}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			props, closed := promptSchemaShape(t, tc.schema)
			if !closed {
				t.Errorf("%s: schema must declare \"additionalProperties\": false", tc.tool)
			}
			fields := promptJSONFields(tc.input)
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
