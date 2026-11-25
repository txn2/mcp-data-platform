//go:build integration

package acceptance

import (
	"strings"
	"testing"
)

// Issue #2814: tool descriptions steer away from the misuse the audit found:
// s3_object names the tools that read the platform's own stored files,
// trino_describe_table says table is the bare name, memory_capture says what
// is worth capturing, memory_manage says when to correct a memory, and the
// baseline names memory_manage only for a persona that can call it.
//
// Wire forms: tools/list and platform_info take an empty object. No
// parameter touched admits a second form.

// TestIssue2814_DescriptionsSteer: tools/list shows each changed description.
func TestIssue2814_DescriptionsSteer(t *testing.T) {
	descs := map[string]string{}
	for _, tool := range connect(t).tools() {
		descs[tool.Name] = tool.Description
	}
	for tool, want := range map[string]string{
		"s3_object":            "a managed resource with fetch or manage_resource",
		"trino_describe_table": "table is the bare table name, never a dotted catalog.schema.table",
		"memory_capture":       "Worth capturing: a definition",
		"memory_manage":        "correct it with update or retire it with forget",
	} {
		if !strings.Contains(descs[tool], want) {
			t.Errorf("%s's description lacks %q:\n%s", tool, want, descs[tool])
		}
	}
}

// TestIssue2814_TheBaselineNamesMemoryManageForWhoCanCallIt: the
// administrator's baseline names memory_manage; the analyst's, whose persona
// cannot call it, does not.
func TestIssue2814_TheBaselineNamesMemoryManageForWhoCanCallIt(t *testing.T) {
	admin, _ := connect(t).call("platform_info", map[string]any{})["agent_instructions"].(string)
	if !strings.Contains(admin, "`memory_manage` command `update`") {
		t.Error("the administrator's baseline does not name memory_manage")
	}
	analyst, _ := connectAs(t, "acme-analyst-key").call("platform_info", map[string]any{})["agent_instructions"].(string)
	if strings.Contains(analyst, "memory_manage") {
		t.Error("the analyst's baseline names memory_manage, which the persona cannot call")
	}
}
