package scriptlayer

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
)

// salesSource queries the harness's sales rows and exports them.
const salesSource = `def main():
    """Exports the sales by region."""
    rows = platform.query(connection = "warehouse", sql = "SELECT region, total FROM sales")["rows"]
    platform.export(name = "sales", rows = rows, format = "csv")
`

// salesAssertion is what a test of salesSource checks.
const salesAssertion = `assert.eq([(r["region"], r["total"]) for r in testing.outputs().exports[0].rows], [("west", 120), ("east", 80)])`

// asRun files the draft recording id as a run of scriptID, which is what a
// run of the saved script would have recorded: the save's replay reads a
// script's runs.
func asRun(t *testing.T, h harness, draftID, runID, scriptID string) {
	t.Helper()
	recs, ok := h.handle.recordings.(*memRecordings)
	require.True(t, ok)
	got, err := recs.Get(context.Background(), draftID)
	require.NoError(t, err)
	run := *got
	run.RunID, run.ScriptID, run.Kind = runID, scriptID, scriptrec.KindRun
	require.NoError(t, recs.Save(context.Background(), run))
}

// recordingOf is the recording a saved script's test replays.
func recordingOf(t *testing.T, source string) string {
	t.Helper()
	_, rest, found := strings.Cut(source, `testing.replay("`)
	require.True(t, found, "the source names no recording")
	id, _, _ := strings.Cut(rest, `"`)
	return id
}

// #1939 through the tool: command=test replays the recording without
// reaching the query engine, names a call the recording lacks, and names the
// line of a wrong assertion.
func TestIntegration_TestCommandReplaysTheRecording(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)
	saveTested(ctx, t, session, saved{name: "sales", source: salesSource, assertion: salesAssertion})
	queried := len(h.queries.calls())

	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "test", "name": "sales"})
	require.False(t, isErr, ran)
	assert.Equal(t, true, ran["ok"], ran)
	coverage, _ := ran["coverage"].(map[string]any)
	assert.EqualValues(t, 100, coverage["percent"])
	assert.Len(t, h.queries.calls(), queried, "a test reaches no upstream")

	saved := h.store.scripts
	var source string
	for _, sc := range saved {
		source = sc.Source
	}
	extra := strings.Replace(source, `    platform.export(`, `    platform.query(connection = "warehouse", sql = "SELECT 1")
    platform.export(`, 1)
	ran, isErr = callTool(ctx, t, session, map[string]any{"command": "test", "name": "sales", "source": extra})
	require.False(t, isErr, ran)
	assert.Equal(t, false, ran["ok"])
	assert.Contains(t, firstFailure(ran), `the recording holds no answer for platform.query("SELECT 1")`)

	wrong := strings.Replace(source, `("east", 80)]`, `("east", 81)]`, 1)
	ran, isErr = callTool(ctx, t, session, map[string]any{"command": "test", "name": "sales", "source": wrong})
	require.False(t, isErr, ran)
	tests, _ := ran["tests"].([]any)
	first, _ := tests[0].(map[string]any)
	assert.Contains(t, first["failure"], `assert.eq: got [("west", 120), ("east", 80)], want [("west", 120), ("east", 81)]`)
	assert.EqualValues(t, 10, first["line"], "the assertion's own line")
}

func firstFailure(ran map[string]any) string {
	tests, _ := ran["tests"].([]any)
	if len(tests) == 0 {
		return ""
	}
	first, _ := tests[0].(map[string]any)
	s, _ := first["failure"].(string)
	return s
}

// #1939: a recording is read by its owner and an administrator, and refused
// to anyone else as one that does not exist.
func TestIntegration_ARecordingIsReadByItsOwnerAndAnAdministrator(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)
	created := saveTested(ctx, t, session, saved{name: "sales", source: salesSource, assertion: salesAssertion})
	_ = created
	var id string
	for _, sc := range h.store.scripts {
		id = recordingOf(t, sc.Source)
	}

	own := resultFields(t, call(t, h.handle, authorCtx(), manageScriptInput{Command: cmdRecording, RunID: id}))
	calls, _ := own["calls"].([]any)
	require.Len(t, calls, 1, "the query the draft made")
	assert.NotNil(t, own["started_from"])

	admin := call(t, h.handle, adminCtx(), manageScriptInput{Command: cmdRecording, RunID: id})
	assert.False(t, admin.IsError, resultText(admin))

	other := call(t, h.handle, callerCtx("bob@example.com", "analyst"), manageScriptInput{Command: cmdRecording, RunID: id})
	assert.True(t, other.IsError)
	assert.Contains(t, resultText(other), "no recording")

	missing := call(t, h.handle, authorCtx(), manageScriptInput{Command: cmdRecording})
	assert.Contains(t, resultText(missing), "run_id is required")
}

// #1942 through the tool: a refactor saves without a summary; a version that
// changes an exported column is refused naming it, and saves with a summary
// and the agreement, which the version history carries; validate reports the
// same difference without saving.
func TestIntegration_ABehaviorChangeNeedsAnAgreedSummary(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)
	saveTested(ctx, t, session, saved{name: "sales", source: salesSource, assertion: salesAssertion})
	var scriptID, source string
	for _, sc := range h.store.scripts {
		scriptID, source = sc.ID, sc.Source
	}
	asRun(t, h, recordingOf(t, source), "srun_1", scriptID)

	refactor := strings.Replace(source, `rows = platform.query(`, `found = platform.query(`, 1)
	refactor = strings.Replace(refactor, `["rows"]
    platform.export(name = "sales", rows = rows,`, `["rows"]
    platform.export(name = "sales", rows = found,`, 1)
	saved, isErr := callTool(ctx, t, session, map[string]any{"command": "update", "name": "sales", "source": refactor})
	require.False(t, isErr, saved)
	assert.Equal(t, "updated", saved["status"], saved)

	changed := strings.Replace(refactor, `rows = found,`, `rows = [{"region": r["region"]} for r in found],`, 1)
	changed = strings.Replace(changed, salesAssertion, `assert.eq([r["region"] for r in testing.outputs().exports[0].rows], ["west", "east"])`, 1)

	validated, isErr := callTool(ctx, t, session, map[string]any{"command": "validate", "name": "sales", "source": changed})
	require.False(t, isErr, validated)
	assert.Equal(t, false, validated["ok"])
	assert.Equal(t, true, validated["change_needed"])
	assert.Contains(t, validated["save_refusal"], `no longer has column "total"`)

	refused, isErr := callTool(ctx, t, session, map[string]any{"command": "update", "name": "sales", "source": changed})
	require.False(t, isErr, refused)
	assert.Equal(t, "invalid", refused["status"])
	assert.Contains(t, refused["message"], `output "sales" no longer has column "total"`)

	agreed, isErr := callTool(ctx, t, session, map[string]any{
		"command": "update", "name": "sales", "source": changed,
		"change_summary": "The sales file lists the regions without their totals.", "user_agreed": true,
	})
	require.False(t, isErr, agreed)
	assert.Equal(t, "updated", agreed["status"], agreed)

	versions, isErr := callTool(ctx, t, session, map[string]any{"command": "versions", "name": "sales"})
	require.False(t, isErr, versions)
	list, _ := versions["versions"].([]any)
	latest, _ := list[0].(map[string]any)
	assert.Equal(t, "The sales file lists the regions without their totals.", latest["change_summary"])
	assert.Equal(t, "jane@example.com", latest["change_agreed_by"])
	assert.NotEmpty(t, latest["change_agreed_at"])
}

// #1942: a new platform.call to a tool the saved version never reached is a
// change naming the tool.
func TestIntegration_ANewToolNeedsAnAgreedSummary(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)
	saveTested(ctx, t, session, saved{name: "sales", source: salesSource, assertion: salesAssertion})
	var source string
	for _, sc := range h.store.scripts {
		source = sc.Source
	}
	reaches := strings.Replace(source, `    platform.export(`, `    platform.call("s3_object", {"action": "list"})
    platform.export(`, 1)
	refused, isErr := callTool(ctx, t, session, map[string]any{"command": "update", "name": "sales", "source": reaches})
	require.False(t, isErr, refused)
	assert.Equal(t, "invalid", refused["status"])
	// The test's recording lacks the new call, which the tests report first.
	assert.Contains(t, refused["message"], `platform.call("s3_object"`)
}
