//go:build integration

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Issue #2815: every tool that chooses its operation by a command or an
// action answers help with its commands, each command's parameters and an
// example, from its own schema; an unknown command lists the valid ones.
//
// Wire forms: each tool's command or action is a string, sent as "help" and as
// an unknown word. No parameter touched admits a second form.

// issue2815CommandField is the enum field a tool chooses its operation by,
// "" for none.
func issue2815CommandField(schema any) string {
	raw, _ := json.Marshal(schema)
	var s struct {
		Properties map[string]struct {
			Enum []any `json:"enum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(raw, &s)
	for _, f := range []string{"command", "action"} {
		if len(s.Properties[f].Enum) > 0 {
			return f
		}
	}
	return ""
}

// TestIssue2815_EveryMultiCommandToolAnswersHelp, and lists the valid
// commands when given one it does not have.
func TestIssue2815_EveryMultiCommandToolAnswersHelp(t *testing.T) {
	c := connect(t)
	checked := []string{}
	for _, tool := range c.tools() {
		field := issue2815CommandField(tool.InputSchema)
		if field == "" {
			continue
		}
		checked = append(checked, tool.Name)
		res, text, err := c.callRaw(tool.Name, map[string]any{field: "help", "purpose": "Acceptance #2815: what this tool takes."})
		if err != nil || res.IsError {
			t.Errorf("%s help: %v %.300s", tool.Name, err, text)
			continue
		}
		if !strings.Contains(text, "command") && !strings.Contains(text, "action") {
			t.Errorf("%s help names no commands: %.300s", tool.Name, text)
		}
		res, text, err = c.callRaw(tool.Name, map[string]any{field: "zz_not_a_command", "purpose": "Acceptance #2815: an unknown command."})
		if err != nil || !res.IsError || !strings.Contains(text, "it takes one of:") {
			t.Errorf("%s with an unknown %s does not list the valid ones: %v %.300s", tool.Name, field, err, text)
		}
	}
	for _, want := range []string{"manage_site", "manage_asset", "manage_script", "manage_prompt", "memory_manage", "s3_object"} {
		if !slices.Contains(checked, want) {
			t.Errorf("%s was not checked; checked %v", want, checked)
		}
	}
}

// TestIssue2815_HelpCarriesParametersAndAnExample: manage_site's help lists
// publish with its parameters and an example call.
func TestIssue2815_HelpCarriesParametersAndAnExample(t *testing.T) {
	out := connect(t).call("manage_site", map[string]any{"command": "help"})
	commands, _ := out["commands"].([]any)
	for _, c := range commands {
		cmd, _ := c.(map[string]any)
		if cmd["command"] != "publish" {
			continue
		}
		if params, _ := cmd["parameters"].([]any); len(params) == 0 {
			t.Error("publish lists no parameters")
		}
		if ex, _ := cmd["example"].(map[string]any); ex["command"] != "publish" {
			t.Errorf("publish's example = %v", cmd["example"])
		}
		return
	}
	t.Fatalf("help does not list publish: %v", out)
}
