package scriptlayer

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the assembled-system proof for #1664. The write barrier is
// engine machinery, but the thing that was broken was the manage_script tool's
// promise: a draft said it persisted nothing while it created resources,
// registrations and assets through platform.call. So these tests go through the
// tool, over a real client session, against a server carrying a real
// write-class tool, and assert what the caller is told.

// writeToolServer is assembledServer with a write-class platform tool and one
// proxied tool an upstream declares read-only, recording every call reaching a
// handler so a refusal can be shown to have stopped before it.
func writeToolServer(t *testing.T, calls *[]string) harness {
	t.Helper()
	h := assembledServer(t)
	record := func(name string) func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			*calls = append(*calls, name)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
				map[string]any{"ok": true}, nil
		}
	}
	mcp.AddTool(h.server, &mcp.Tool{Name: "manage_resource"}, record("manage_resource"))
	mcp.AddTool(h.server, &mcp.Tool{Name: "manage_table"}, record("manage_table"))
	mcp.AddTool(h.server, &mcp.Tool{
		Name: "vendor__list_contacts", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, record("vendor__list_contacts"))
	return h
}

// ingestSource is the shape #1664 reports: a script whose whole purpose is
// landing something.
const ingestSource = `platform.call("manage_resource", {"action": "create", "filename": "daily.csv"})
platform.call("manage_table", {"action": "register", "reference": "mcp:resource:daily"})
print("landed")
`

// TestIntegration_ADraftDoesNotLandThroughPlatformCall is the defect: the draft
// answered "Nothing was persisted" while the resource and the registration
// existed.
func TestIntegration_ADraftDoesNotLandThroughPlatformCall(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "ingest", "source": ingestSource,
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "ingest",
	})
	require.False(t, isErr, ran)

	assert.Equal(t, "failed", ran["status"], "a draft stops at the write rather than making it")
	assert.Empty(t, calls, "and no tool handler was reached")
	assert.Contains(t, ran["error"], "manage_resource action=create persists outside this run")

	refused, _ := ran["refused_write"].(map[string]any)
	require.NotNil(t, refused, "the response names the call that ended the run")
	assert.Equal(t, "manage_resource", refused["tool"])
	assert.Equal(t, "manage_resource action=create", refused["call"])

	message, _ := ran["message"].(string)
	assert.Contains(t, message, "allow_writes")
	assert.NotContains(t, message, "Nothing was persisted",
		"a failed draft is not described as a run that deliberately wrote nothing")

	log, _ := ran["log"].(string)
	assert.NotContains(t, log, "landed")
}

// TestIntegration_ADraftThatWroteNothingSaysWriteClassCallsAreRefused pins the
// sentence the surface owes a reader: "nothing was persisted" now carries the
// reason platform.call persisted nothing either.
func TestIntegration_ADraftThatWroteNothingSaysWriteClassCallsAreRefused(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "reader",
		"source": `platform.call("manage_table", {"action": "list"})
platform.call("vendor__list_contacts", {})
`,
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "reader",
	})
	require.False(t, isErr, ran)

	assert.Equal(t, "succeeded", ran["status"], ran["error"])
	assert.Equal(t, []string{"manage_table", "vendor__list_contacts"}, calls,
		"a read through platform.call is not refused, and neither is a proxied tool its upstream declares read-only")
	assert.Empty(t, ran["writes"])
	message, _ := ran["message"].(string)
	assert.Contains(t, message, "Nothing was persisted")
	assert.Contains(t, message, "write-class platform.call would have been refused")
}

// TestIntegration_ADraftWithAllowWritesLandsAndReportsEveryWrite is the opt-in
// the barrier is paired with: a pipeline whose next step reads what the last
// one created cannot be rehearsed without the create, and the person asking
// owns what it wrote.
func TestIntegration_ADraftWithAllowWritesLandsAndReportsEveryWrite(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "ingest", "source": ingestSource,
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "ingest", "allow_writes": true,
	})
	require.False(t, isErr, ran)

	assert.Equal(t, "succeeded", ran["status"], ran["error"])
	assert.Equal(t, []string{"manage_resource", "manage_table"}, calls, "both writes landed")

	writes, _ := ran["writes"].([]any)
	require.Len(t, writes, 2, "and both are reported")
	first, _ := writes[0].(map[string]any)
	assert.Equal(t, "manage_resource action=create", first["call"])
	second, _ := writes[1].(map[string]any)
	assert.Equal(t, "manage_table action=register", second["call"])

	message, _ := ran["message"].(string)
	assert.Contains(t, message, "allow_writes")
	assert.Contains(t, message, "the 2 calls listed under writes persisted for real")
	assert.NotContains(t, message, "Nothing was persisted")
	assert.Nil(t, ran["refused_write"])
}

// TestIntegration_AllowWritesLeavesTheOutputHelpersPreviewing pins the boundary:
// the opt-in is about platform.call, and an export is still measured rather
// than written.
func TestIntegration_AllowWritesLeavesTheOutputHelpersPreviewing(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "mixed",
		"source": `res = platform.query(connection="warehouse", sql="SELECT region, total FROM sales")
platform.call("manage_table", {"action": "register", "reference": "mcp:resource:x"})
platform.export(name="daily", rows=res["rows"], format="csv")
platform.save_state({"cursor": "2026-08-13"})
`,
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "mixed", "allow_writes": true,
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])

	exports, _ := ran["exports"].([]any)
	require.Len(t, exports, 1)
	preview, _ := exports[0].(map[string]any)
	assert.Equal(t, true, preview["preview"], "the export still previews")

	message, _ := ran["message"].(string)
	assert.Contains(t, message, "reported the shape of each output rather than writing it")
	assert.Contains(t, message, "still reported the state rather than saving it")
}

// TestIntegration_ADraftRefusesAToolNobodyClassified is the deny-by-default
// rule reaching the caller: the platform says it cannot tell, which is a
// different sentence from a declared write and asks a different question.
func TestIntegration_ADraftRefusesAToolNobodyClassified(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	mcp.AddTool(h.server, &mcp.Tool{Name: "vendor__create_invoice"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			calls = append(calls, "vendor__create_invoice")
			return &mcp.CallToolResult{}, map[string]any{"ok": true}, nil
		})
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "vendor",
		"source": `platform.call("vendor__create_invoice", {"amount": 10})`,
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "vendor",
	})
	require.False(t, isErr, ran)

	assert.Equal(t, "failed", ran["status"])
	assert.Contains(t, ran["error"], "cannot tell whether vendor__create_invoice persists")
	assert.Empty(t, calls)
}
