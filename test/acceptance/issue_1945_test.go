//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// Issue #1945: platform_info failed schema validation for a client holding
// the previous release's tool list once the caller had a failing automation,
// because only the top level of an advertised output schema was open and
// notices, a nested object, gained two keys. These criteria read tools/list
// and platform_info on the running platform as the owner of a failing
// automation.
//
// Wire forms: platform_info takes no parameters and is called with none, by
// the connect that opens each session. manage_script's command, name,
// description and source and run_script's name are typed strings, and
// run_script's wait_seconds an integer, so each admits one JSON form and is
// sent as a literal tools/call parameter of it. tools/list takes no
// parameters.

// closedObjects1945 names every place in a schema that refuses keys it does
// not declare.
func closedObjects1945(path string, v any) []string {
	var out []string
	switch n := v.(type) {
	case map[string]any:
		for _, key := range []string{"additionalProperties", "unevaluatedProperties"} {
			if b, ok := n[key].(bool); ok && !b {
				out = append(out, path)
			}
		}
		for k, child := range n {
			out = append(out, closedObjects1945(path+"/"+k, child)...)
		}
	case []any:
		for i, child := range n {
			out = append(out, closedObjects1945(path+"/"+strconv.Itoa(i), child)...)
		}
	}
	return out
}

// TestIssue1945_ANestedKeyALaterReleaseAddsValidates: the owner of a failing
// automation gets notices.failing_automations from platform_info; that result,
// with a further key added to notices as a later release would add one,
// validates against the output schema tools/list advertises; and no object in
// any advertised output schema is closed.
func TestIssue1945_ANestedKeyALaterReleaseAddsValidates(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := fmt.Sprintf("acc-1945-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #1945: an automation that fails.",
		"source": "def main():\n    \"\"\"Fails.\"\"\"\n    fail(\"the input was not what this script expects\")\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	if run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60}); run["status"] != "failed" {
		t.Fatalf("the run did not fail: %v", run)
	}

	next := connectAs(t, devOwnerAPIKey)
	if issue1934Failing(next.info, name) == nil {
		t.Fatalf("platform_info's notices do not carry the failing automation: %v", next.info["notices"])
	}
	var schema map[string]any
	for _, tool := range next.tools() {
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		if closed := closedObjects1945("", v); len(closed) > 0 {
			t.Errorf("%s advertises closed objects at %v", tool.Name, closed)
		}
		if tool.Name == "platform_info" {
			schema, _ = v.(map[string]any)
		}
	}
	if schema == nil {
		t.Fatalf("tools/list advertises no output schema for platform_info")
	}

	later := map[string]any{}
	raw, _ := json.Marshal(next.info)
	_ = json.Unmarshal(raw, &later)
	notices, _ := later["notices"].(map[string]any)
	notices["added_by_a_later_release"] = []any{map[string]any{"name": name}}

	var s jsonschema.Schema
	raw, _ = json.Marshal(schema)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the advertised schema does not parse: %v", err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolving the advertised schema: %v", err)
	}
	var instance any
	raw, _ = json.Marshal(later)
	_ = json.Unmarshal(raw, &instance)
	if err := resolved.Validate(instance); err != nil {
		t.Errorf("platform_info with a key a later release adds to notices is rejected by the schema it was advertised: %v", err)
	}
}
