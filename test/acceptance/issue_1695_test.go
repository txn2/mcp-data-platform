//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1695: an asset write was recorded with no statement of why it
// happened. Before the platform runs a query or an API call, the agent states
// what the call is for and the platform keeps that sentence on the record; the
// asset writes sitting beside those calls in the activity log carried none.
//
// The purpose is what makes a record worth reading later, and an asset is the
// record most likely to be read: somebody opens it weeks afterward, usually
// somebody who was not there when it was made, and asks what it was for.
//
// The criteria below are that change as an agent and an operator meet it:
// save_asset and manage_asset advertise the argument on tools/list, a call
// that states none is refused by name, a call that states one is recorded with
// it in the audit log, and the tools deliberately left outside the gate are
// still outside it.
//
// Wire forms: `purpose` is added to each tool's input schema by the platform as
// {"type": "string"}, so it admits exactly ONE JSON form, a string, which is
// what every check below sends as literal tools/call params. The tools' own
// touched parameters are typed in their closed schemas and admit one form each:
// save_asset's `name`, `content` and `content_type` are strings, and
// manage_asset's `action` and `asset_id` are strings.

const issue1695Purpose = "Acceptance for #1695: recording why an asset was written, so the next reader knows what it was for."

// issue1695Name names this run's asset so a concurrent run cannot answer for
// it.
func issue1695Name(label string) string {
	return fmt.Sprintf("acc-1695-%s-%d", label, time.Now().UnixNano())
}

// issue1695Raw issues one tools/call with exactly the params given, threading
// the session handle and nothing else.
//
// The suite's own client states a purpose on an asset write the way a real
// agent does, which is what every OTHER criterion in the suite needs. A
// criterion about the gate itself has to be able to omit it, so it goes
// through the MCP session directly.
func issue1695Raw(t *testing.T, c *client, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	args["session_id"] = c.sessionID
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	return res, firstText(res)
}

// TestIssue1695_TheAssetToolsAdvertiseTheArgument is the first criterion: the
// two tools that write an asset carry `purpose` on the schema a client reads,
// so an agent knows to state one without being refused first.
func TestIssue1695_TheAssetToolsAdvertiseTheArgument(t *testing.T) {
	c := connect(t)
	byName := map[string]*mcp.Tool{}
	for _, tool := range c.tools() {
		byName[tool.Name] = tool
	}

	for _, name := range []string{"save_asset", "manage_asset"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("the running platform registers no %s", name)
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: the input schema is not an object: %T", name, tool.InputSchema)
		}
		props, _ := schema["properties"].(map[string]any)
		prop, present := props["purpose"]
		if !present {
			t.Fatalf("%s advertises no purpose argument, so an agent has no way to know to state one", name)
		}
		spec, _ := prop.(map[string]any)
		if spec["type"] != "string" {
			t.Errorf("%s: purpose is typed %v; the one form it admits is a string", name, spec["type"])
		}
		if desc, _ := spec["description"].(string); !strings.Contains(desc, "sentence") {
			t.Errorf("%s: the purpose description does not say what to write: %q", name, desc)
		}
	}
}

// TestIssue1695_AnAssetWriteWithNoPurposeIsRefused is the central criterion: an
// agent that writes an asset without saying why is told so by name, and told
// what to do about it.
func TestIssue1695_AnAssetWriteWithNoPurposeIsRefused(t *testing.T) {
	c := connect(t)

	res, text := issue1695Raw(t, c, "save_asset", map[string]any{
		"name":         issue1695Name("unexplained"),
		"content":      "# Unexplained\n\nWritten with no stated reason.",
		"content_type": "text/markdown",
	})
	if !res.IsError {
		t.Fatalf("save_asset with no purpose was accepted: %s", text)
	}
	if !strings.Contains(text, "PURPOSE_REQUIRED") {
		t.Errorf("the refusal does not name the contract it enforces: %s", text)
	}
	if !strings.Contains(text, "save_asset") {
		t.Errorf("the refusal does not name the tool: %s", text)
	}

	// The edit path is refused on the same terms. It is the half of
	// manage_asset that changes what a later reader finds.
	id := issue1695Save(t, c, "edited")
	res, text = issue1695Raw(t, c, "manage_asset", map[string]any{
		"action": "update", "asset_id": id, "description": "Edited with no stated reason.",
	})
	if !res.IsError {
		t.Fatalf("manage_asset update with no purpose was accepted: %s", text)
	}
	if !strings.Contains(text, "PURPOSE_REQUIRED") {
		t.Errorf("the refusal does not name the contract it enforces: %s", text)
	}
}

// TestIssue1695_TheStatedPurposeIsOnTheRecord is what the ticket is for: the
// sentence the agent wrote is readable afterwards, beside the call that wrote
// the asset, on the surface an operator actually opens.
func TestIssue1695_TheStatedPurposeIsOnTheRecord(t *testing.T) {
	c := connect(t)
	name := issue1695Name("recorded")
	why := "Acceptance for #1695: writing " + name + " so the activity log can say what it was for."

	out := c.call("save_asset", map[string]any{
		"name":         name,
		"content":      "# Recorded\n\nWritten with a stated reason.",
		"content_type": "text/markdown",
		"purpose":      why,
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})

	// The audit pipeline writes asynchronously, so the row is awaited rather
	// than demanded on the first read.
	if !issue1695AuditCarries(t, c, "save_asset", why) {
		t.Fatalf("no save_asset audit row carries the stated purpose %q", why)
	}
}

// TestIssue1695_TheDeliberateExceptionsStayOutside pins the boundary the ticket
// drew. Applying knowledge is a fair exception -- what it applies is itself the
// explanation -- and the orientation tools an agent uses to set itself up are
// not taxed for a sentence whose content is their own name.
func TestIssue1695_TheDeliberateExceptionsStayOutside(t *testing.T) {
	c := connect(t)
	byName := map[string]*mcp.Tool{}
	for _, tool := range c.tools() {
		byName[tool.Name] = tool
	}

	for _, name := range []string{"platform_info", "list_connections", "apply_knowledge", "manage_table"} {
		tool, ok := byName[name]
		if !ok {
			continue // not every deployment registers every toolkit
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: the input schema is not an object: %T", name, tool.InputSchema)
		}
		props, _ := schema["properties"].(map[string]any)
		if _, present := props["purpose"]; present {
			t.Errorf("%s advertises a purpose argument; it is outside the gate by design", name)
		}
	}

	// And the boundary is not only advertised: a call to one of them with no
	// purpose is answered, not refused.
	out := c.call("list_connections", nil)
	if out == nil {
		t.Error("list_connections answered nothing to a call that stated no purpose")
	}
}

// issue1695Save writes one asset with a purpose and removes it afterwards,
// returning its id.
func issue1695Save(t *testing.T, c *client, label string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         issue1695Name(label),
		"content":      "# Fixture\n\nWritten by the acceptance suite.",
		"content_type": "text/markdown",
		"purpose":      issue1695Purpose,
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
	return id
}

// issue1695AuditCarries polls the admin audit events for a row on tool that
// carries the stated purpose. The audit write is asynchronous, so this waits
// rather than reading once.
func issue1695AuditCarries(t *testing.T, c *client, tool, why string) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, body := c.rest(http.MethodGet,
			"/api/v1/admin/audit/events?tool_name="+tool+"&per_page=50", http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET the audit events: status %d", status)
		}
		raw, err := json.Marshal(body["data"])
		if err != nil {
			t.Fatalf("re-encoding the audit page: %v", err)
		}
		var rows []struct {
			ToolName string `json:"tool_name"`
			Purpose  string `json:"purpose"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatalf("decoding the audit page: %v", err)
		}
		for _, row := range rows {
			if row.Purpose == why {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}
