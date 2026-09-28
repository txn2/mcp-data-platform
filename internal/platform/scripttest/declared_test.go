package scripttest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/toolanswer"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// created is the body a resource create answers with, for the contract the
// declared-answer tests hold answers to.
type created struct {
	ResourceID string `json:"resource_id"`
	URI        string `json:"uri"`
	Size       int64  `json:"size_bytes"`
	Owner      struct {
		Email string `json:"email"`
	} `json:"owner"`
	Note string `json:"note,omitempty"`
}

var contracts = toolanswer.New(toolkit.ContractFor[created]("manage_resource", "action", "create"))

// ingest writes a file and reports the resource it became: a script no draft
// records without performing the write.
const ingest = `def main():
    """File the day's extract."""
    made = platform.call("manage_resource", {"action": "create", "filename": "day.csv", "content": "a,b"})
    platform.result({"resource": made["resource_id"]})
`

func runDeclared(t *testing.T, source string) *Report {
	t.Helper()
	report, err := Run(context.Background(), Request{Source: source, Name: "ingest", Contracts: contracts})
	require.NoError(t, err)
	return report
}

func TestADeclaredAnswerAnswersAWriteNoRecordingHolds(t *testing.T) {
	report := runDeclared(t, ingest+`
def test_ingest():
    testing.answer("manage_resource", {"action": "create"},
        {"resource_id": "r1", "uri": "mcp://r1", "size_bytes": 3, "owner": {"email": "a@example.com"}})
    main()
    out = testing.outputs()
    assert.eq(out.result, {"resource": "r1"})
    assert.eq(out.calls[0].tool, "manage_resource")
    assert.eq(out.calls[0].args["filename"], "day.csv")
    assert.true(out.calls[0].declared)
`)
	require.Len(t, report.Tests, 1)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
	assert.Empty(t, report.Tests[0].Notes)
	assert.InDelta(t, 100, report.Coverage.Percent, 0.001)
}

func TestADeclaredAnswerLackingARequiredFieldFailsNamingIt(t *testing.T) {
	report := runDeclared(t, ingest+`
def test_ingest():
    testing.answer("manage_resource", {"action": "create"}, {"resource_id": "r1", "owner": {"email": "a@example.com", "name": "A"}})
    main()
    assert.eq(testing.outputs().result, {"resource": "r1"})
`)
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Contains(t, got.Failure, `the answer declared for manage_resource (action=create) lacks "uri", "size_bytes"`)
	assert.Contains(t, got.Failure, `has "owner.name", which the tool never returns`)
	assert.Equal(t, 7, got.Line, "the line of the testing.answer call")
}

func TestADeclaredAnswerOfTheWrongTypeFailsNamingTheField(t *testing.T) {
	report := runDeclared(t, ingest+`
def test_ingest():
    testing.answer("manage_resource", {"action": "create"},
        {"resource_id": 7, "uri": "u", "size_bytes": 1.5, "owner": {"email": "e"}})
    main()
    assert.eq(testing.outputs().result, {"resource": 7})
`)
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Contains(t, got.Failure, `has "resource_id" as integer, where the tool answers string`)
	assert.Contains(t, got.Failure, `has "size_bytes" as number, where the tool answers integer`)
}

func TestAnAnswerForAToolWithNoContractIsNotedUnchecked(t *testing.T) {
	report := runDeclared(t, `def main():
    """Post the count."""
    got = platform.call("api_invoke_endpoint", {"operation": "post_count", "body": {"n": 3}})
    platform.result(got)
`+`
def test_post():
    testing.answer("api_invoke_endpoint", {"operation": "post_count"}, {"status": 201})
    main()
    assert.eq(testing.outputs().result, {"status": 201})
`)
	got := report.Tests[0]
	assert.True(t, got.Passed, got.Failure)
	assert.Equal(t, []string{"the answer declared for api_invoke_endpoint was not checked against the tool: " +
		"api_invoke_endpoint declares no answer contract"}, got.Notes)
}

func TestDeclaredAnswersAreUsedOnceEachInTheOrderDeclared(t *testing.T) {
	report := runDeclared(t, `def main():
    """Two writes, then a third nothing answers."""
    a = platform.call("notify_hook", {"n": 1, "to": "x"})
    b = platform.call("notify_hook", {"n": 1, "to": "x"})
    platform.result([a["id"], b["id"]])
    platform.call("notify_hook", {"n": 1, "to": "x"})
`+`
def test_order():
    testing.answer("notify_hook", {"n": 1}, {"id": "first"})
    testing.answer("notify_hook", {"to": "x"}, {"id": "second"})
    msg = assert.fails(main)
    assert.contains(msg, "the recording holds no answer for platform.call(\"notify_hook\"")
    assert.contains(msg, "testing.answer")
    assert.eq(testing.outputs().result, ["first", "second"])
`)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
}

func TestADeclaredAnswerDoesNotMatchADifferentArgument(t *testing.T) {
	report := runDeclared(t, ingest+`
def test_ingest():
    testing.answer("manage_resource", {"action": "create", "filename": "other.csv"},
        {"resource_id": "r1", "uri": "u", "size_bytes": 3, "owner": {"email": "e"}})
    main()
    assert.eq(testing.outputs().result, {"resource": "r1"})
`)
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Contains(t, got.Failure, `the recording holds no answer for platform.call("manage_resource"`)
}

func TestADeclaredErrorFailsTheCall(t *testing.T) {
	report := runDeclared(t, ingest+`
def test_refused():
    testing.answer("manage_resource", {"action": "create"}, error = "quota exceeded")
    msg = assert.fails(main)
    assert.contains(msg, "quota exceeded")
    assert.eq(testing.outputs().calls[0].error, "quota exceeded")
`)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
}

func TestTestingAnswerRefusesWhatItCannotDeclare(t *testing.T) {
	for name, call := range map[string]string{
		"neither":  `testing.answer("t", {})`,
		"both":     `testing.answer("t", {}, {"a": 1}, error = "x")`,
		"not dict": `testing.answer("t", {}, [1])`,
		"keys":     `testing.answer("t", {1: 2}, {"a": 1})`,
	} {
		t.Run(name, func(t *testing.T) {
			report := runDeclared(t, ingest+`
def test_bad():
    `+call+`
    main()
    assert.true(True)
`)
			got := report.Tests[0]
			assert.False(t, got.Passed)
			assert.Contains(t, got.Failure, "testing.answer")
		})
	}
}

// stateful reaches its second branch only when a run saved state before.
const stateful = `def main():
    """Report the change since the last run."""
    rows = platform.query("select n from counts")["rows"]
    total = sum([r["n"] for r in rows])
    if "last" in run.state:
        platform.call("notify_hook", {"change": total - run.state["last"]})
    platform.save_state({"last": total})
`

func TestSetRunReachesABranchThatNeedsSavedState(t *testing.T) {
	rows := `{"columns": ["n"], "rows": [{"n": 2}, {"n": 3}], "stats": {"row_count": 2}}`
	report := runDeclared(t, stateful+`
def test_first():
    testing.answer("trino_query", {"sql": "select n from counts"}, `+rows+`)
    main()
    out = testing.outputs()
    assert.eq(out.state, {"last": 5})
    assert.eq(len(out.calls), 1)

def test_since():
    testing.set_run(state = {"last": 1}, params = {"region": "east"})
    testing.answer("trino_query", {"sql": "select n from counts"}, `+rows+`)
    testing.answer("notify_hook", {"change": 4}, {"ok": True})
    main()
    out = testing.outputs()
    assert.eq(out.calls[1].args, {"change": 4})
    assert.eq(out.state, {"last": 5})
    assert.eq(run.params, {"region": "east"})
`)
	for _, r := range report.Tests {
		assert.True(t, r.Passed, r.Name+": "+r.Failure)
	}
	assert.InDelta(t, 100, report.Coverage.Percent, 0.001, "the branch set_run reaches is covered")
}

func TestATestBlindToItsDeclaredRowsIsRefused(t *testing.T) {
	report := runDeclared(t, stateful+`
def test_blind():
    testing.answer("trino_query", {"sql": "select n from counts"}, {"rows": [{"n": 2}, {"n": 3}]})
    main()
    assert.eq(len(testing.outputs().calls), 1)
`)
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Contains(t, got.Failure, "test_blind still passes when the query results it replays or declares change")
}
