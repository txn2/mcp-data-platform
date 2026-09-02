//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1589: trino_export returned its call reference as a second content
// block because it registered through the untyped Server.AddTool path, so the
// SDK wrote no structured result for the appended reference to merge into. It
// now registers through the generic mcp.AddTool with a typed output, as
// api_export does: one structured object carries the export's own fields and
// the call reference. The middleware rule is unchanged, so a tool that sets
// no structured result still keeps its appended blocks in content.

// issue1589ExportArgs is a trino_export call needing no table: the rows come
// from a VALUES clause on the dev stack's scratch connection. Every published
// parameter is typed with one JSON form; all of them are sent here.
func issue1589ExportArgs(name string) map[string]any {
	return map[string]any{
		"connection":         scratchConnection,
		"sql":                "SELECT * FROM (VALUES (1, 'north'), (2, 'south'), (3, 'east')) AS t(id, region)",
		"format":             "csv",
		"name":               name,
		"description":        "Acceptance for #1589: the export answers as one structured object.",
		"tags":               []string{"acceptance", "issue-1589"},
		"limit":              10,
		"timeout_seconds":    60,
		"create_public_link": false,
		"purpose":            "Acceptance: trino_export returns one structured object carrying its fields and the call reference.",
	}
}

// exportCall runs trino_export, fails the test on an error result, deletes the
// asset at the end, and returns the raw result with its structured object.
func exportCall(t *testing.T, c *client, args map[string]any) (res *mcp.CallToolResult, structured map[string]any) {
	t.Helper()
	res, text, err := c.callRaw("trino_export", args)
	if err != nil {
		t.Fatalf("trino_export: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("trino_export: tool error: %s", text)
	}
	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("trino_export structured result = %T (%v); want one JSON object", res.StructuredContent, res.StructuredContent)
	}
	if id, _ := structured["asset_id"].(string); id != "" {
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
		})
	}
	return res, structured
}

// TestIssue1589_ExportIsOneStructuredObject covers criteria 1 and 2: the
// structured result carries the export's own fields and the call reference in
// one object, and every text block in content is a JSON object of its own, so
// a client reading either surface never parses two documents run together.
func TestIssue1589_ExportIsOneStructuredObject(t *testing.T) {
	c := connect(t)
	res, structured := exportCall(t, c, issue1589ExportArgs(fmt.Sprintf("issue-1589-%d", time.Now().UnixNano())))

	for _, key := range []string{"asset_id", "portal_url", "row_count", "size_bytes", "format", "call_reference"} {
		if _, ok := structured[key]; !ok {
			t.Errorf("structured result lacks %q: %v", key, structured)
		}
	}
	if got := number(t, structured, "row_count"); got != 3 {
		t.Errorf("row_count = %v; want 3", got)
	}
	if got := number(t, structured, "size_bytes"); got <= 0 {
		t.Errorf("size_bytes = %v; want the size of the written file", got)
	}
	ref, _ := structured["call_reference"].(map[string]any)
	if id, _ := ref["call_id"].(string); id == "" {
		t.Errorf("call_reference.call_id = %v; want the recorded call's id", ref["call_id"])
	}
	if r, _ := ref["reference"].(string); !strings.HasPrefix(r, "mcp:call:") {
		t.Errorf("call_reference.reference = %v; want an mcp:call: reference", ref["reference"])
	}

	// The handler's own text block is the same object the structured result
	// carries, minus what the platform merged in beside it.
	var own map[string]any
	if err := json.Unmarshal([]byte(firstText(res)), &own); err != nil {
		t.Fatalf("first text block is not a JSON object: %v\n%s", err, firstText(res))
	}
	for _, key := range []string{"asset_id", "portal_url", "row_count", "size_bytes", "format"} {
		if fmt.Sprint(own[key]) != fmt.Sprint(structured[key]) {
			t.Errorf("%s: text block says %v, structured result says %v", key, own[key], structured[key])
		}
	}
	for i, block := range res.Content {
		tc, ok := block.(*mcp.TextContent)
		if !ok {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &obj); err != nil {
			t.Errorf("content[%d] is not a JSON object on its own: %v\n%s", i, err, tc.Text)
		}
	}
}

// TestIssue1589_IdempotentReplayIsStructuredToo covers the second success
// path: a repeated idempotency_key returns the existing asset, and that reply
// is the same one-object shape.
func TestIssue1589_IdempotentReplayIsStructuredToo(t *testing.T) {
	c := connect(t)
	key := fmt.Sprintf("issue-1589-%d", time.Now().UnixNano())
	args := issue1589ExportArgs(key)
	args["idempotency_key"] = key
	_, first := exportCall(t, c, args)
	_, replay := exportCall(t, c, args)
	if first["asset_id"] != replay["asset_id"] {
		t.Errorf("replay asset_id = %v; want the first call's %v", replay["asset_id"], first["asset_id"])
	}
	for _, key := range []string{"asset_id", "portal_url", "size_bytes", "call_reference"} {
		if _, ok := replay[key]; !ok {
			t.Errorf("replayed structured result lacks %q: %v", key, replay)
		}
	}
}

// TestIssue1589_ATextOnlyToolKeepsItsBlocksInContent is criterion 3: the
// middleware rule is unchanged. The dev-mock fixture's echo answers in plain
// text and sets no structured result, so the gateway-proxied call has no
// object to merge into: nothing is synthesized in its place, and the call
// reference stays in content as a block of its own beside the upstream's text.
func TestIssue1589_ATextOnlyToolKeepsItsBlocksInContent(t *testing.T) {
	c := connect(t)
	res, text, err := c.callRaw("dev-mock__echo", map[string]any{
		"message": "issue 1589",
		"purpose": "Acceptance: a text-only proxied tool keeps its appended blocks in content.",
	})
	if err != nil {
		t.Fatalf("dev-mock__echo: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("dev-mock__echo: tool error: %s", text)
	}
	if res.StructuredContent != nil {
		t.Fatalf("a text-only proxied tool was given a structured result: %v", res.StructuredContent)
	}
	if !strings.Contains(text, "issue 1589") {
		t.Errorf("first text block = %q; want the upstream's own echo", text)
	}
	var sawReference bool
	for _, block := range res.Content[1:] {
		tc, ok := block.(*mcp.TextContent)
		if !ok {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(tc.Text), &obj) == nil && obj["call_reference"] != nil {
			sawReference = true
		}
	}
	if !sawReference {
		t.Errorf("the call reference did not stay in content beside the upstream's text: %v", res.Content)
	}
}
