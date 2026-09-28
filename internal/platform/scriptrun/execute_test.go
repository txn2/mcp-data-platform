package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecute_BindsAndCallsTrinoExecute pins what platform.execute sends: the
// statement with its values bound by the query binder, a quote included, to
// trino_execute on the connection it names (#1950).
func TestExecute_BindsAndCallsTrinoExecute(t *testing.T) {
	caller := &recordingCaller{}
	result, err := Run(context.Background(), Options{
		Source: `
out = platform.execute("INSERT INTO t SELECT * FROM :src WHERE note = :note",
    connection = "acme", params = {"src": {"query_table": "scratch.s.src"}, "note": "it's"})
print(out["row_count"])
`,
		Name: "test", RunID: "run_1", FireTime: fireTime, Caller: caller, Writes: WritesReported,
	})

	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, ToolExecute, caller.calls[0].name)
	assert.Equal(t, "acme", caller.calls[0].args["connection"])
	assert.Equal(t, `INSERT INTO t SELECT * FROM "scratch"."s"."src" WHERE note = 'it''s'`, caller.calls[0].args["sql"])
	assert.Equal(t, "1\n", result.Log)
	require.Len(t, result.Writes, 1, "the statement is reported as the write it is")
	assert.Equal(t, ToolExecute, result.Writes[0].Tool)
}

// TestExecute_StopsAtADraftsWriteBarrier: a draft is a rehearsal, and a
// statement that changes state is refused there before it is sent.
func TestExecute_StopsAtADraftsWriteBarrier(t *testing.T) {
	caller := &recordingCaller{}
	result, err := barred(t, `platform.execute("DELETE FROM t WHERE id = :id", params = {"id": 1})`, caller)

	require.Error(t, err)
	assert.Empty(t, caller.calls)
	require.NotNil(t, result.RefusedWrite)
	assert.Equal(t, ToolExecute, result.RefusedWrite.Tool)
}

// TestExecute_RefusesWhatTheBinderRefuses: a placeholder with no value is the
// binder's refusal, named against platform.execute, and nothing is sent.
func TestExecute_RefusesWhatTheBinderRefuses(t *testing.T) {
	caller := &recordingCaller{}
	_, err := Run(context.Background(), Options{
		Source: `platform.execute("DELETE FROM t WHERE id = :id")`,
		Name:   "test", RunID: "run_1", FireTime: fireTime, Caller: caller,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), CapabilityExecute)
	assert.Empty(t, caller.calls)
}

// TestExecute_NeedsACaller: validation-only contexts have no session to send
// the statement over.
func TestExecute_NeedsACaller(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Source: `platform.execute("DELETE FROM t")`,
		Name:   "test", RunID: "run_1", FireTime: fireTime,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available in this context")
}

// TestValidate_ReportsExecuteAndItsConnection is the fourth criterion of
// #1950 at the unit level.
func TestValidate_ReportsExecuteAndItsConnection(t *testing.T) {
	report := Validate("def main():\n    \"\"\"Doc.\"\"\"\n    platform.execute(\"DELETE FROM t\", connection = \"acme\")\n")

	require.True(t, report.OK, "%+v", report.Findings)
	assert.Contains(t, report.Capabilities, CapabilityExecute)
	assert.Equal(t, []string{"acme"}, report.Connections)
	assert.False(t, report.DynamicConnections)

	computed := Validate("def main():\n    \"\"\"Doc.\"\"\"\n    platform.execute(\"DELETE FROM t\", connection = run.params[\"c\"])\n")
	assert.True(t, computed.DynamicConnections)
}
