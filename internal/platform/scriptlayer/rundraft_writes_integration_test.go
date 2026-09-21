package scriptlayer

import (
	"context"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
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
	assert.Contains(t, message, "The 2 calls listed under writes persisted for real")
	assert.NotContains(t, message, "Nothing was persisted")
	assert.Nil(t, ran["refused_write"])
}

// mixedSource makes a platform.call write, an export and a state save: the
// three kinds of write a draft reports on.
const mixedSource = `res = platform.query(connection="warehouse", sql="SELECT region, total FROM sales")
platform.call("manage_table", {"action": "register", "reference": "mcp:resource:x"})
out = platform.export(name="daily", rows=res["rows"], format="csv", destination="resources", key="d/daily.csv")
print("exported", out["preview"], out.get("reference", "none"))
platform.save_state({"cursor": "2026-08-13"})
`

// draftExporter is the writer a composition root hands a draft allowed to
// write, recording what it was asked to write and for whom.
type draftExporter struct {
	targets  []scriptdraft.Target
	requests []scriptrun.ExportRequest
}

func (d *draftExporter) exports(t scriptdraft.Target) scriptrun.Exporter {
	d.targets = append(d.targets, t)
	return d
}

func (d *draftExporter) Export(_ context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	d.requests = append(d.requests, req)
	return &scriptrun.ExportResult{ResourceID: "res_1", ResourceRef: "mcp:resource:res_1", ResourceVersion: 1, Bytes: 40}, nil
}

func (*draftExporter) PublishData(context.Context, scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	return &scriptrun.ExportResult{}, nil
}

// TestIntegration_AllowWritesWritesTheExport is #1822: allow_writes promises
// the run writes for real, and a staging pipeline's next step reads what
// platform.export wrote, so the export is written through the deployment's
// writer and its reference is handed to the script.
func TestIntegration_AllowWritesWritesTheExport(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	writer := &draftExporter{}
	h.handle.draftExports = writer.exports
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{"command": "create", "name": "mixed", "source": mixedSource})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "mixed", "allow_writes": true,
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])

	require.Len(t, writer.requests, 1, "the export was written")
	assert.Equal(t, "d/daily.csv", writer.requests[0].Key)
	require.Len(t, writer.targets, 1)
	assert.Equal(t, "mixed", writer.targets[0].Script.Name)
	assert.Equal(t, "jane@example.com", writer.targets[0].Identity.Email, "it writes as the person drafting")

	exports, _ := ran["exports"].([]any)
	require.Len(t, exports, 1)
	record, _ := exports[0].(map[string]any)
	assert.Equal(t, false, record["preview"])
	assert.Equal(t, "mcp:resource:res_1", record["reference"])
	assert.Contains(t, ran["log"], "exported False mcp:resource:res_1")

	message, _ := ran["message"].(string)
	assert.Contains(t, message, "The 1 call listed under writes, and The 1 output listed under exports, persisted for real.")
	assert.Contains(t, message, "did not save it", "save_state still reports rather than saves")
}

// TestIntegration_ABarredDraftStillPreviewsTheExport: without allow_writes the
// writer is never built and the export is measured, as before.
func TestIntegration_ABarredDraftStillPreviewsTheExport(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	writer := &draftExporter{}
	h.handle.draftExports = writer.exports
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "exportonly",
		"source": `platform.export(name="daily", rows=[{"a": 1}], format="csv")`,
	})
	require.False(t, isErr, created)
	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "exportonly"})
	require.False(t, isErr, ran)

	assert.Empty(t, writer.targets, "no writer is built for a draft that did not ask to write")
	exports, _ := ran["exports"].([]any)
	require.Len(t, exports, 1)
	record, _ := exports[0].(map[string]any)
	assert.Equal(t, true, record["preview"])
	assert.Contains(t, ran["message"], "Nothing was persisted")
}

// TestIntegration_AllowWritesWithNowhereToWritePreviewsAndSaysSo: a deployment
// that supplies no draft writer cannot write the export, and the message says
// which outputs previewed rather than claiming they were written.
func TestIntegration_AllowWritesWithNowhereToWritePreviewsAndSaysSo(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{"command": "create", "name": "mixed", "source": mixedSource})
	require.False(t, isErr, created)
	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "mixed", "allow_writes": true,
	})
	require.False(t, isErr, ran)

	exports, _ := ran["exports"].([]any)
	require.Len(t, exports, 1)
	record, _ := exports[0].(map[string]any)
	assert.Equal(t, true, record["preview"])
	assert.Contains(t, ran["message"], "marked preview reported its shape instead")
}

// TestIntegration_ADraftOfAnUnsavedScriptRuns is #1822's second half: source
// sent under a name the caller has no script by runs as that prospective
// script, with its params bound against the params sent with it, and nothing
// is saved by running it.
func TestIntegration_ADraftOfAnUnsavedScriptRuns(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	writer := &draftExporter{}
	h.handle.draftExports = writer.exports
	session := connectAgent(ctx, t, h.server)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "brand_new", "allow_writes": true,
		"source": `print("region is", run.params["region"], "state", run.state)
platform.export(name="d", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/d.jsonl")`,
		"params": []any{map[string]any{"name": "region", "type": "string", "required": true}},
		"args":   map[string]any{"region": "west"},
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])
	assert.Equal(t, false, ran["saved"])
	assert.Contains(t, ran["log"], "region is west state {}")
	require.Len(t, writer.targets, 1)
	assert.Empty(t, writer.targets[0].Script.ID, "the prospective script has no id")
	assert.Equal(t, "brand_new", writer.targets[0].Script.Name)

	got, err := h.store.GetByName(ctx, "jane@example.com", "brand_new")
	require.NoError(t, err)
	assert.Nil(t, got, "running a draft saves nothing")
}

// TestIntegration_AnUnsavedDraftIsRefusedWhatASavedOneWouldBe: the name and
// params of a prospective script are held to the rules a save applies, and a
// draft with no source still needs a saved script.
func TestIntegration_AnUnsavedDraftIsRefusedWhatASavedOneWouldBe(t *testing.T) {
	ctx := context.Background()
	var calls []string
	h := writeToolServer(t, &calls)
	session := connectAgent(ctx, t, h.server)

	for _, tt := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"no source", map[string]any{"command": "run_draft", "name": "nope"}, `script "nope" not found`},
		{"a bad name", map[string]any{"command": "run_draft", "name": "Bad Name!", "source": `print(1)`}, "name"},
		{"a bad param", map[string]any{
			"command": "run_draft", "name": "ok", "source": `print(1)`,
			"params": []any{map[string]any{"name": "x", "type": "nonsense"}},
		}, "nonsense"},
		{"another owner's name", map[string]any{
			"command": "run_draft", "name": "ok", "source": `print(1)`,
			"owner_email": "someone@example.com",
		}, "your own scripts"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, isErr := callTool(ctx, t, session, tt.args)
			require.True(t, isErr, out)
			assert.Contains(t, fmt.Sprint(out), tt.want)
		})
	}
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
