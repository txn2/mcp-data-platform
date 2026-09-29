//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// Issue #1971: after an upgrade to v1.138.0, platform_info failed for a client
// still holding v1.135.2's tool list whenever the caller owned a failing
// automation, because the result's notices carried failing_automations and
// v1.135.2 advertised notices as a closed object. These criteria read
// platform_info on the running platform as the owner of a failing automation
// and validate it against the output schema v1.135.2 advertised for
// platform_info, which testdata/issue_1971_platform_info_v1.135.2.json holds
// as that release's tools/list returned it.
//
// Wire forms: platform_info takes no parameters and is called with none, by
// the connect that opens each session. manage_script's command, name,
// description and source and run_script's name are typed strings, and
// run_script's wait_seconds an integer, so each admits one JSON form and is
// sent as a literal tools/call parameter of it.

// issue1971OldSchema resolves the platform_info output schema v1.135.2
// advertised.
func issue1971OldSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile("testdata/issue_1971_platform_info_v1.135.2.json")
	if err != nil {
		t.Fatal(err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the recorded schema does not parse: %v", err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolving the recorded schema: %v", err)
	}
	return resolved
}

// TestIssue1971_AFailingAutomationsOwnerPassesTheOldSchema covers the ticket's
// expectation: the platform_info an owner of a failing automation receives
// names the automation, at its top level, and validates against the schema a
// client holding v1.135.2's tool list checks it with.
func TestIssue1971_AFailingAutomationsOwnerPassesTheOldSchema(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := fmt.Sprintf("acc-1971-%d", time.Now().UnixNano())
	owner.saveScript(map[string]any{
		"command": "create", "name": name, "description": "Acceptance #1971: an automation that fails.",
		"source": "def main():\n    \"\"\"Fails.\"\"\"\n    fail(\"the input was not what this script expects\")\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	if run := owner.call("run_script", map[string]any{"name": name, "wait_seconds": 60}); run["status"] != "failed" {
		t.Fatalf("the run did not fail: %v", run)
	}

	info := connectAs(t, devOwnerAPIKey).info
	if issue1934Failing(info, name) == nil {
		t.Fatalf("platform_info does not carry the failing automation at its top level: %v", info)
	}
	if total, _ := info["failing_automations_total"].(float64); total < 1 {
		t.Errorf("failing_automations_total = %v", info["failing_automations_total"])
	}
	if notices, ok := info["notices"].(map[string]any); ok {
		for _, key := range []string{"failing_automations", "failing_automations_total"} {
			if _, found := notices[key]; found {
				t.Errorf("notices still carries %s", key)
			}
		}
	}

	var instance any
	raw, _ := json.Marshal(info)
	_ = json.Unmarshal(raw, &instance)
	if err := issue1971OldSchema(t).Validate(instance); err != nil {
		t.Errorf("platform_info is rejected by the schema v1.135.2 advertised: %v", err)
	}
}
