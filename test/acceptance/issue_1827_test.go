//go:build integration

package acceptance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Issue #1827: the draft write barrier classifies each action tool by listing
// its verbs, and nothing noticed when a tool gained a verb the list did not
// name. Twice a read verb was refused as a write. The gate that closes it
// (test/structure/toolwrite_verbs_test.go) reads each action tool's verbs from
// its advertised input schema, which three tools, and the two whose schemas are
// inferred, did not carry: their verbs were prose in a description.
//
// What these hold, against the running platform: tools/list advertises the
// verb argument of each of those tools as an enum a client can read, and a verb
// outside it is refused by the schema, naming the ones it takes, before the
// handler runs.
//
// Wire forms: s3_object's `action` and `purpose` are typed string, `action`
// with an enum, and each is sent in that one form.

// issue1827Enums are the verbs each tool's schema now advertises.
var issue1827Enums = map[string][]string{
	"manage_asset": {
		"list", "get", "update", "delete", "list_versions", "revert", "search", "provenance", "patch",
		"locate", "get_content", "outline", "stats", "diff", "share", "list_shares", "revoke_share",
		"create_collection", "list_collections", "get_collection", "update_collection",
		"delete_collection", "set_sections",
	},
	"manage_feedback": {"list", "get", "reply", "resolve", "request_validation", "respond_validation"},
	"memory_manage":   {"update", "forget", "list", "review_stale", "review_duplicates", "consolidate"},
	"s3_object":       {"get", "metadata", "put", "copy", "delete", "presign"},
	"notify":          {"list", "send", "publish"},
}

// issue1827VerbArg is the argument each tool names its verb in.
var issue1827VerbArg = map[string]string{"memory_manage": "command"}

// TestIssue1827_ActionToolsAdvertiseTheirVerbs reads each tool's schema the
// way a client does.
func TestIssue1827_ActionToolsAdvertiseTheirVerbs(t *testing.T) {
	c := connect(t)
	listed := map[string]any{}
	for _, tool := range c.tools() {
		listed[tool.Name] = tool.InputSchema
	}
	for name, want := range issue1827Enums {
		t.Run(name, func(t *testing.T) {
			schema, ok := listed[name]
			if !ok {
				t.Fatalf("%s is not registered on this deployment", name)
			}
			arg := issue1827VerbArg[name]
			if arg == "" {
				arg = "action"
			}
			got := issue1827Enum(t, schema, arg)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s %s enum = %v; want %v", name, arg, got, want)
			}
		})
	}
}

// TestIssue1827_AVerbOutsideTheEnumIsRefusedByTheSchema holds what the enum
// does for a caller: an unknown verb is refused before the handler runs, and
// the refusal names the verbs the tool takes.
func TestIssue1827_AVerbOutsideTheEnumIsRefusedByTheSchema(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("s3_object", map[string]any{
		"action": "rename", "purpose": "Acceptance for #1827: an unknown verb is refused by the schema.",
	})
	if err != nil && !strings.Contains(err.Error(), "presign") {
		t.Fatalf("s3_object: %v", err)
	}
	if err == nil && !res.IsError {
		t.Fatalf("an unknown verb was accepted: %s", text)
	}
	msg := text
	if err != nil {
		msg = err.Error()
	}
	if !strings.Contains(msg, "presign") || !strings.Contains(msg, "rename") {
		t.Errorf("the refusal does not name the verb and the ones the tool takes: %s", msg)
	}
}

// issue1827Enum reads one property's enum out of an advertised input schema.
func issue1827Enum(t *testing.T, schema any, arg string) []string {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("the schema is not an object: %v", err)
	}
	return parsed.Properties[arg].Enum
}
