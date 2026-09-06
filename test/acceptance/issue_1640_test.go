//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1640: an agent that followed the platform's own instruction about the
// `purpose` argument lost a round trip on every tool the purpose gate does not
// cover. `manage_table action=list` with a purpose was refused with
// `invalid_arguments` ("unexpected additional properties [\"purpose\"]"), and
// the corrective hint told the agent to drop a property the instructions had
// told it to send. After this change the argument is taken off the request and
// recorded whether or not the tool is gated, while what the gate decides --
// where the argument is advertised, and where a missing one is refused -- is
// unchanged.
//
// Wire forms: `purpose` is the one parameter these criteria touch. On a gated
// tool its schema is `{"type":"string"}`, so the wire form a compliant caller
// sends is a JSON string, and that is what every check below sends as literal
// tools/call params. On an UNGATED tool the argument is on no schema at all, so
// the platform is the only thing that reads it and there is no declared type
// to constrain the caller: this file therefore sends the ungated form twice,
// once as a string and once as a JSON number, since a schema that does not
// mention a property cannot refuse a shape of it and both must leave the call
// running. Every other parameter these criteria touch is typed and admits one
// form each: `manage_table.action` and `manage_table.reference` as strings,
// `search.intent` as a string and `search.limit` as a number, and
// `manage_resource`'s `action`, `filename`, `path`, `content`, `content_type`,
// `display_name` and `description` as strings.

// gatedPurposeTools1640 are tools the default gated set covers that the dev
// stack registers, and ungatedPurposeTools1640 are ones deliberately outside
// it (middleware.defaultPurposeTools). Criterion 4 is that tools/list advertises
// the argument on exactly the first group.
var (
	gatedPurposeTools1640   = []string{"search", "fetch", "trino_query", "trino_describe_table", "s3_object", "s3_list"}
	ungatedPurposeTools1640 = []string{"manage_table", "platform_info", "list_connections", "manage_resource", "save_asset"}
)

// registeredTableReference1640 files one CSV into the caller's own resource
// library and returns the `mcp:resource:` reference `manage_table` addresses it
// by. `manage_table action=list` is keyed on a reference -- it lists the tables
// registered over ONE stored file -- so a criterion about that call needs a real
// file to name, not a bare action.
func registeredTableReference1640(t *testing.T, c *client) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     fmt.Sprintf("issue-1640-%d.csv", time.Now().UnixNano()),
		"path":         "acceptance/issue-1640",
		"content":      "store_id,units,region\n1,10,north\n2,20,south\n",
		"content_type": "text/csv",
		"display_name": "Acceptance #1640 registered-table listing",
		"description":  "The stored file the manage_table listing criteria address.",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, nil) })
	return "mcp:resource:" + id
}

// listedTools1640 reads tools/list through the client session, which is the
// same response an agent decides from.
func listedTools1640(t *testing.T, c *client) map[string]*mcp.Tool {
	t.Helper()
	res, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	byName := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	return byName
}

// advertisesPurpose1640 reports whether a listed tool's input schema carries the
// purpose property. The schema arrives in whatever shape the tool registered it
// in, so it is normalized through JSON exactly as the platform's own decorator
// normalizes it.
func advertisesPurpose1640(t *testing.T, tool *mcp.Tool) bool {
	t.Helper()
	if tool.InputSchema == nil {
		return false
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("%s: input schema does not marshal: %v", tool.Name, err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("%s: input schema is not a JSON object: %v", tool.Name, err)
	}
	_, ok := schema.Properties["purpose"]
	return ok
}

// auditPurpose1640 waits for the audit row of one tool call in this session and
// returns the purpose recorded on it. The row is written on the audit writer's
// drain goroutine after the call answers, so a read taken the moment the tool
// returns can outrun it.
func auditPurpose1640(t *testing.T, admin *client, sessionID, tool string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		events := admin.list(fmt.Sprintf(
			"/api/v1/admin/audit/events?session_id=%s&tool_name=%s&per_page=20", sessionID, tool))
		for _, entry := range events {
			event, _ := entry.(map[string]any)
			if purpose, _ := event["purpose"].(string); purpose != "" {
				return purpose
			}
		}
		if time.Now().After(deadline) {
			if len(events) == 0 {
				t.Fatalf("no audit event for %s in session %s within 30s", tool, sessionID)
			}
			return ""
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestIssue1640_AnUngatedToolAcceptsAStatedPurpose is criterion 1: the call the
// ticket reports as refused now returns the listing.
func TestIssue1640_AnUngatedToolAcceptsAStatedPurpose(t *testing.T) {
	c := connect(t)
	stated := fmt.Sprintf(
		"Acceptance #1640: checking which stored files are registered as tables before a refresh (%d).",
		time.Now().UnixNano())

	res, text, err := c.callRaw("manage_table", map[string]any{
		"action": "list", "reference": registeredTableReference1640(t, c), "purpose": stated,
	})
	if err != nil {
		t.Fatalf("manage_table: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("manage_table action=list with a purpose was refused: %s", text)
	}
	if strings.Contains(text, "purpose") && strings.Contains(text, "additional properties") {
		t.Fatalf("the argument reached the tool's input-schema validation: %s", text)
	}
}

// TestIssue1640_AnUngatedToolAcceptsANonStringPurpose sends the second wire
// form. Nothing declares `purpose` on an ungated tool's schema, so a caller has
// no declared type to conform to and the platform must not refuse a shape it
// simply cannot record.
func TestIssue1640_AnUngatedToolAcceptsANonStringPurpose(t *testing.T) {
	c := connect(t)

	res, text, err := c.callRaw("manage_table", map[string]any{
		"action": "list", "reference": registeredTableReference1640(t, c), "purpose": 1640,
	})
	if err != nil {
		t.Fatalf("manage_table: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("manage_table action=list with a non-string purpose was refused: %s", text)
	}
}

// TestIssue1640_AnUngatedToolWithoutOneIsUnchanged is the other half of
// criterion 1: not gating still means a missing purpose refuses nothing.
func TestIssue1640_AnUngatedToolWithoutOneIsUnchanged(t *testing.T) {
	c := connect(t)

	res, text, err := c.callRaw("manage_table", map[string]any{
		"action": "list", "reference": registeredTableReference1640(t, c),
	})
	if err != nil {
		t.Fatalf("manage_table: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("manage_table action=list with no purpose was refused: %s", text)
	}
}

// TestIssue1640_TheStatedPurposeIsOnTheAuditRow is criterion 2: a sentence
// volunteered on a tool outside the gate reaches the operator reading the log.
func TestIssue1640_TheStatedPurposeIsOnTheAuditRow(t *testing.T) {
	c := connect(t)
	admin := connect(t)
	stated := fmt.Sprintf(
		"Acceptance #1640: auditing the registered-table list before the weekly refresh (%d).",
		time.Now().UnixNano())

	c.call("manage_table", map[string]any{
		"action": "list", "reference": registeredTableReference1640(t, c), "purpose": stated,
	})

	if got := auditPurpose1640(t, admin, c.sessionID, "manage_table"); got != stated {
		t.Errorf("audit purpose = %q, want %q; a volunteered purpose is worth the same to the operator as a required one",
			got, stated)
	}
}

// TestIssue1640_AGatedToolStillRefusesAMissingPurpose is criterion 3: the gate
// is untouched. The dev stack runs the default `purpose.require: true`, and this
// session threads a handle, which is the condition the refusal keys on.
func TestIssue1640_AGatedToolStillRefusesAMissingPurpose(t *testing.T) {
	c := connect(t)

	res, text, err := c.callRaw("search", map[string]any{
		"intent": "acceptance 1640 gated refusal", "limit": 1,
	})
	if err != nil {
		t.Fatalf("search: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("search with no purpose was admitted; the gate must still refuse it: %s", text)
	}
	if !strings.Contains(text, "PURPOSE_REQUIRED") {
		t.Errorf("search with no purpose = %q, want a PURPOSE_REQUIRED refusal", text)
	}
}

// TestIssue1640_AGatedProxiedCallsUpstreamNeverSeesThePurpose is the rest of
// criterion 3, through the upstream the dev stack actually runs: the mcp-test
// fixture, registered as an MCP gateway connection by dev/start.sh. Its echo
// tool returns the arguments it received, so this reads what the upstream server
// was sent rather than asserting about it.
func TestIssue1640_AGatedProxiedCallsUpstreamNeverSeesThePurpose(t *testing.T) {
	c := connect(t)
	echo := proxiedEchoTool1640(t, c)

	// Read the raw result: an upstream server answers in whatever shape it
	// chooses, and these fixtures answer in plain text.
	res, text, err := c.callRaw(echo, map[string]any{
		"message": "acceptance-1640",
		"purpose": "Acceptance #1640: proving the upstream server never receives the platform argument.",
	})
	if err != nil {
		t.Fatalf("%s: transport error: %v", echo, err)
	}
	if res.IsError {
		t.Fatalf("%s with a purpose was refused: %s", echo, text)
	}
	if !strings.Contains(text, "acceptance-1640") {
		t.Fatalf("%s did not echo its own argument, so this proves nothing: %s", echo, text)
	}
	if strings.Contains(text, "purpose") {
		t.Errorf("the upstream server saw the platform argument; %s echoed %s", echo, text)
	}
}

// proxiedEchoTool1640 names the gateway-proxied echo tool this criterion calls.
// The choice is deterministic (the first such name in sorted order) because more
// than one MCP connection registers one on this stack, and a criterion that
// silently addresses a different upstream from run to run is a criterion that
// passes for a different reason each time. It fails rather than skips: the
// fixtures are part of the stack this suite runs against, and a criterion that
// quietly does not run has proved nothing.
func proxiedEchoTool1640(t *testing.T, c *client) string {
	t.Helper()
	var echoes []string
	for name := range listedTools1640(t, c) {
		if strings.HasSuffix(name, "__echo") {
			echoes = append(echoes, name)
		}
	}
	if len(echoes) == 0 {
		t.Fatal("no gateway-proxied echo tool is registered; is an MCP fixture connection present (dev/start.sh)?")
	}
	sort.Strings(echoes)
	return echoes[0]
}

// TestIssue1640_PurposeIsAdvertisedOnTheGatedToolsOnly is criterion 4:
// tolerating a stated purpose off the gate is not an extension of the gate, so
// tools/list must advertise the argument exactly where it did before.
func TestIssue1640_PurposeIsAdvertisedOnTheGatedToolsOnly(t *testing.T) {
	c := connect(t)
	tools := listedTools1640(t, c)

	for _, name := range gatedPurposeTools1640 {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("%s is not registered on this deployment; the gated set cannot be checked against it", name)
		}
		if !advertisesPurpose1640(t, tool) {
			t.Errorf("%s does not advertise purpose; a gated tool must ask for the argument it refuses a call for omitting", name)
		}
	}
	for _, name := range ungatedPurposeTools1640 {
		tool, ok := tools[name]
		if !ok {
			continue
		}
		if advertisesPurpose1640(t, tool) {
			t.Errorf("%s advertises purpose; tolerating a stated one must not extend the gate", name)
		}
	}
}

// TestIssue1640_TheInstructionsNameTheGatedTools is the ticket's fourth
// requirement under "What should be true": the note names the boundary so a
// model can tell which calls take the argument without probing for it.
func TestIssue1640_TheInstructionsNameTheGatedTools(t *testing.T) {
	c := connect(t)
	info := c.call("platform_info", nil)
	instructions, _ := info["agent_instructions"].(string)
	if instructions == "" {
		t.Fatal("platform_info returned no agent_instructions")
	}

	start := strings.Index(instructions, "Stating why you are calling:")
	if start < 0 {
		t.Fatal("the instructions carry no purpose note")
	}
	section := instructions[start:]
	end := strings.Index(section, "\n\n")
	if end > 0 {
		section = section[:end]
	}

	for _, name := range gatedPurposeTools1640 {
		if !strings.Contains(section, name) {
			t.Errorf("the purpose note does not name %s; a model cannot tell which calls take the argument: %s", name, section)
		}
	}
	if strings.Contains(section, "manage_table") {
		t.Errorf("the purpose note names manage_table, which the gate does not cover: %s", section)
	}

	// The gateway-proxied tools are gated by kind:mcp, and this stack registers
	// fifteen of them across two connections. Naming the kind is what keeps the
	// boundary readable; enumerating them would bury it.
	if !strings.Contains(section, "every tool served by a connection of kind `mcp`") {
		t.Errorf("the purpose note does not name the wholesale-gated connection kind: %s", section)
	}
	if strings.Contains(section, "mcp-test-fixture__") {
		t.Errorf("the purpose note enumerates an upstream's proxied tools instead of naming the kind: %s", section)
	}
}
