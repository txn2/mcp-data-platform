package scripttest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/notifylayer"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/toolanswer"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// upstream is the tool server a recorded run reaches: it answers a query with
// three rows and every other call with an acknowledgement, and counts what it
// was asked, so a test can prove a replay never reached it.
type upstream struct{ calls int }

func (u *upstream) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	u.calls++
	if name == scriptrun.ToolQuery {
		return map[string]any{
			"columns": []any{"region", "n"},
			"rows": []any{
				map[string]any{"region": "east", "n": 1.0},
				map[string]any{"region": "west", "n": 2.0},
				map[string]any{"region": "north", "n": 3.0},
			},
			"stats": map[string]any{"row_count": 3.0, "truncated": false},
		}, nil
	}
	return map[string]any{"ok": true, "tool": name, "args": args}, nil
}

// weekly is the script under test: a query, an export, a notify and a state
// write, with one branch its recorded run does not take.
const weekly = `def main():
    """Export the weekly regions."""
    rows = platform.query("select region, n from weekly")["rows"]
    if not rows:
        fail("no rows")
    platform.export(name = "weekly", rows = rows, format = "csv")
    platform.notify(channel = "ops", title = "weekly", body = str(len(rows)))
    platform.save_state({"count": len(rows)})
`

// record runs weekly once against an upstream and returns its recording.
func record(t *testing.T) (*scriptrec.Recording, *upstream) {
	t.Helper()
	up := &upstream{}
	rec := scriptrec.NewRecorder(scriptrec.Header{
		RunID: "run_1", FireTime: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
		MaxRows: scriptrun.DraftMaxRows, Preview: true,
	})
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: weekly, Name: "weekly", RunID: "run_1",
		FireTime: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
		Caller:   up, OnCall: rec.OnCall,
	})
	require.NoError(t, err)
	data, reason := rec.Finish()
	require.Empty(t, reason)
	decoded, err := scriptrec.Decode(data)
	require.NoError(t, err)
	return decoded, up
}

// runTests runs the tests in source, answering "run_1" from rec.
func runTests(t *testing.T, source string, rec *scriptrec.Recording) *Report {
	t.Helper()
	report, err := Run(context.Background(), Request{
		Source: source, Name: "weekly",
		Load: func(_ context.Context, id string) (*scriptrec.Recording, error) {
			if id != "run_1" {
				return nil, scriptrec.ErrNotFound
			}
			return rec, nil
		},
	})
	require.NoError(t, err)
	return report
}

func TestATestReplaysARecordingWithoutReachingTheUpstream(t *testing.T) {
	rec, up := record(t)
	before := up.calls
	report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    out = testing.outputs()
    assert.eq(len(out.exports), 1)
    assert.eq(out.exports[0].row_count, 3)
    assert.eq(out.exports[0].rows[1]["region"], "west")
    assert.eq(out.notifies[0]["body"], "3")
    assert.eq(out.state, {"count": 3})
`, rec)
	require.Len(t, report.Tests, 1)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
	assert.True(t, report.OK())
	assert.Equal(t, before, up.calls, "a test never reaches the upstream")
}

func TestAWrongAssertionFailsNamingItsLine(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    assert.eq(testing.outputs().exports[0].row_count, 4, "rows exported")
`, rec)
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Equal(t, "rows exported: assert.eq: got 3, want 4", got.Failure)
	assert.Equal(t, 13, got.Line)
	assert.False(t, report.OK())
}

func TestACallTheRecordingLacksFailsNamingTheQuery(t *testing.T) {
	rec, _ := record(t)
	changed := strings.Replace(weekly, `    platform.export(`, `    platform.query("select 1")
    platform.export(`, 1)
	report := runTests(t, changed+`
def test_weekly():
    testing.replay("run_1")
    main()
`, rec)
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Contains(t, got.Failure, `the recording holds no answer for platform.query("select 1")`)
	assert.Equal(t, 6, got.Line)
}

func TestCoverageReportsTheBranchNoTestTook(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    assert.eq(testing.outputs().exports[0].row_count, 3)
`, rec)
	require.True(t, report.OK(), report.Tests[0].Failure)
	// def main, rows =, if, fail, export, notify, save_state: the fail on
	// line 5 never ran.
	assert.Equal(t, 7, report.Coverage.Statements)
	assert.Equal(t, 6, report.Coverage.Covered)
	assert.Equal(t, []int{5}, report.Coverage.MissedLines)
}

func TestAFailureInsideAnInstrumentedFunctionNamesTheAuthorsLine(t *testing.T) {
	report := runTests(t, `def half(n):
    """Half of n, refusing an odd one."""
    if n % 2:
        fail("odd: %d" % n)
    return n // 2

def main():
    """Nothing to do."""
    half(2)

def test_half():
    assert.eq(half(4), 2)
    half(3)
`, &scriptrec.Recording{})
	got := report.Tests[0]
	assert.False(t, got.Passed)
	assert.Contains(t, got.Failure, "odd: 3")
	assert.Equal(t, 4, got.Line)
	assert.Equal(t, 100.0, report.Coverage.Percent-report.Coverage.Percent+100.0)
}

func TestInstrumentationDoesNotSpendATestsStepBudget(t *testing.T) {
	// One iteration costs 10 steps uninstrumented and 14 instrumented, so
	// 1.9M iterations fit the cap only when the instrumentation's own steps
	// are not charged, and 2.1M fit it in no case.
	loop := func(n string) string {
		return `def main():
    """Count."""
    total = 0
    for i in range(` + n + `):
        total += i
    platform.result(total)

def test_count():
    main()
    assert.true(testing.outputs().result > 0)
`
	}
	fits := runTests(t, loop("1900000"), &scriptrec.Recording{})
	assert.True(t, fits.Tests[0].Passed, fits.Tests[0].Failure)

	over := runTests(t, loop("2100000"), &scriptrec.Recording{})
	assert.False(t, over.Tests[0].Passed)
	assert.Contains(t, over.Tests[0].Failure, "execution-step limit")
}

func TestAssertFailsReturnsTheMessage(t *testing.T) {
	report := runTests(t, `def main():
    """Refuse."""
    fail("nope")

def test_refuses():
    msg = assert.fails(main)
    assert.contains(msg, "nope")
    assert.ne(msg, "")
    assert.true(len(msg) > 0)

def test_does_not_fail():
    assert.fails(len, [])
`, &scriptrec.Recording{})
	require.Len(t, report.Tests, 2)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
	assert.False(t, report.Tests[1].Passed)
	assert.Contains(t, report.Tests[1].Failure, "did not fail")
}

func TestARecordingNamedByAnythingButALiteralIsRefused(t *testing.T) {
	_, err := Run(context.Background(), Request{Source: `def main():
    """x"""
    pass

def test_x():
    id = "run_1"
    testing.replay(id)
`, Name: "weekly"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "as a string literal")

	_, err = Run(context.Background(), Request{Source: `def test_x():
    testing.replay("a")
    testing.replay("b")
`, Name: "weekly"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "second recording")
}

func TestAMissingRecordingFailsTheTest(t *testing.T) {
	report, err := Run(context.Background(), Request{Source: `def test_x():
    testing.replay("gone")
`, Name: "weekly", Load: func(context.Context, string) (*scriptrec.Recording, error) {
		return nil, errors.New("no such recording")
	}})
	require.NoError(t, err)
	assert.False(t, report.OK())
	assert.Contains(t, report.Tests[0].Failure, "reading recording gone")
}

func TestRecordingsNamedListsEachOnce(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, RecordingsNamed(`def test_x():
    testing.replay("b")

def test_y():
    testing.replay("a")

def test_z():
    testing.replay("b")
`))
	assert.Nil(t, RecordingsNamed("def ("))
	assert.Empty(t, RecordingsNamed("def main():\n    pass\n"))
}

func TestARunCannotUseTheTestModules(t *testing.T) {
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{Source: `def main():
    """x"""
    assert.eq(1, 1)
`})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assert.eq is available only inside a test_* function")
}

func TestATestThatAssertsNothingFails(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
`, rec)
	assert.False(t, report.OK())
	assert.Contains(t, report.Tests[0].Failure, "test_weekly made no assertion")
}

// A test whose assertions hold whatever the recorded rows were proves nothing
// about what the script made of them.
func TestATestBlindToTheRecordedRowsFails(t *testing.T) {
	rec, _ := record(t)
	blind := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    assert.eq(len(testing.outputs().exports), 1)
`, rec)
	assert.False(t, blind.OK())
	assert.Contains(t, blind.Tests[0].Failure, "still passes when the query results it replays or declares change")

	// Reading the rows is enough: dropping one changes the count, and
	// changing the first changes its region.
	reads := runTests(t, weekly+`
def test_weekly():
    testing.replay("run_1")
    main()
    rows = testing.outputs().exports[0].rows
    assert.eq([r["region"] for r in rows], ["east", "west", "north"])
`, rec)
	assert.True(t, reads.OK(), reads.Tests[0].Failure)
}

// Replay reports what a source produced from a recording, and names a call
// the recording does not hold.
func TestReplayReportsWhatASourceProduced(t *testing.T) {
	rec, _ := record(t)
	req := Request{Source: weekly, Name: "weekly"}
	got := Replay(context.Background(), req, rec)
	assert.Empty(t, got.Failure)
	require.Len(t, got.Exports, 1)
	assert.Equal(t, 3, got.Exports[0].RowCount())
	assert.Equal(t, map[string]any{"count": int64(3)}, got.State)
	assert.Len(t, got.Calls, 2, "the query and the notify")

	req.Source = strings.Replace(weekly, `platform.export(`, `platform.query("select 2")
    platform.export(`, 1)
	missing := Replay(context.Background(), req, rec)
	assert.Equal(t, `platform.query("select 2")`, missing.Missing)
	assert.Contains(t, missing.Failure, "holds no answer")
}

// What a test reads back: documents, refreshed data, every call with its
// arguments, the value returned and the log.
func TestOutputsCarryEveryKindOfOutput(t *testing.T) {
	report := runTests(t, `def main():
    """Writes a document, refreshes data and posts."""
    print("hello")
    platform.export(name = "doc", rows = "# Title", format = "markdown")
    platform.publish_data("dash", {"n": 1})
    platform.result({"ok": True})

def test_main():
    """Reads each back."""
    main()
    out = testing.outputs()
    assert.eq(out.exports[0].body, "# Title")
    assert.eq(out.exports[0].rows, [])
    assert.eq(out.publishes[0].data, {"n": 1})
    assert.eq(out.result, {"ok": True})
    assert.eq(out.state, None)
    assert.contains(out.log, "hello")
    assert.eq(out.calls, [])
`, &scriptrec.Recording{})
	assert.True(t, report.OK(), report.Tests[0].Failure)
}

// Each assertion fails at its line in its own words, and a malformed one is
// an error rather than a pass.
func TestEachAssertionFailsInItsOwnWords(t *testing.T) {
	cases := map[string]string{
		`assert.ne(testing.outputs().log, "x\n")`:       "assert.ne: both are",
		`assert.true(len(testing.outputs().log) > 99)`:  "assert.true: False is not true",
		`assert.contains(testing.outputs().log, "zzz")`: "does not contain",
		`assert.fails(1)`:                  "int is not callable",
		`assert.fails()`:                   "missing the function to call",
		`assert.eq(testing.outputs().log)`: "missing argument",
		`assert.contains(1, 2)`:            "in assert.contains",
		`assert.ne(testing.outputs().log)`: "missing argument",
		`assert.true()`:                    "missing argument",
		`elsewhere()`:                      "names its recording once",
		`testing.outputs(1)`:               "got 1 arguments",
	}
	for body, want := range cases {
		report := runTests(t, "def main():\n    \"\"\"Prints.\"\"\"\n    print(\"x\")\n\ndef elsewhere():\n    \"\"\"Names a recording outside a test.\"\"\"\n    testing.replay(\"other\")\n\ndef test_main():\n    \"\"\"Asserts.\"\"\"\n    main()\n    "+body+"\n", &scriptrec.Recording{})
		require.Len(t, report.Tests, 1, body)
		assert.False(t, report.Tests[0].Passed, body)
		assert.Contains(t, report.Tests[0].Failure, want, body)
	}
}

// A source that does not parse is the runner's error, not a failed test.
func TestASourceThatDoesNotParseIsAnError(t *testing.T) {
	_, err := Run(context.Background(), Request{Source: "def (", Name: "x"})
	assert.ErrorContains(t, err, "parsing the script")
}

// The alteration leaves a row that is not a record as it is, and flips a
// boolean.
func TestChangeFirstRowChangesEveryKind(t *testing.T) {
	rows := changeFirstRow([]any{map[string]any{"n": 1.0, "s": "a", "b": true, "x": nil}})
	assert.Equal(t, map[string]any{"n": 2.0, "s": "a~", "b": false, "x": nil}, rows[0])
	assert.Equal(t, []any{"row"}, changeFirstRow([]any{"row"}))
}

// A value testing.outputs() cannot hand a test is an error, not a pass.
func TestOutputsRefuseAValueTheyCannotConvert(t *testing.T) {
	rec, _ := record(t)
	p := &produced{replay: scriptrec.NewReplay(rec), live: scriptlive.New(0, 0)}
	p.observe(scriptrun.PublishRequest{Name: "d", Data: make(chan int)})
	_, err := p.value()
	assert.ErrorContains(t, err, "converting published data")

	p = &produced{replay: scriptrec.NewReplay(rec), live: scriptlive.New(0, 0)}
	p.observe(scriptrun.ExportRequest{Name: "w", Rows: []any{make(chan int)}})
	_, err = p.value()
	assert.ErrorContains(t, err, "converting the rows")

	p = &produced{replay: scriptrec.NewReplay(rec), live: scriptlive.New(0, 0)}
	p.observe(&script.StateWrite{Value: map[string]any{"c": make(chan int)}})
	_, err = p.value()
	assert.ErrorContains(t, err, "converting the staged state")
}

// A test that the run fails, assert.fails(main), is not held to the altered
// rows: a failure that holds whatever the rows were is what it checks. Making
// any other function fail does not exempt a test.
func TestATestOfAFailureIsNotAltered(t *testing.T) {
	rec, _ := record(t)
	refusing := strings.Replace(weekly, `    if not rows:
        fail("no rows")`, `    fail("refused %d" % len(rows))`, 1)
	report := runTests(t, refusing+`
def test_refuses():
    """The run refuses."""
    testing.replay("run_1")
    assert.contains(assert.fails(main), "refused")
`, rec)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)

	blind := runTests(t, weekly+`
def test_blind():
    """Asserts only a failure it made itself."""
    testing.replay("run_1")
    main()
    assert.fails(lambda: fail("x"))
    assert.eq(len(testing.outputs().exports), 1)
`, rec)
	assert.False(t, blind.Tests[0].Passed)
	assert.Contains(t, blind.Tests[0].Failure, "still passes when the query results it replays or declares change")
}

// An appended output is one output to a test, its pages in order, while the
// run is still writing it.
func TestAnAppendedOutputIsReadAsItIsWritten(t *testing.T) {
	report := runTests(t, `def main():
    """Pages two pages into one file."""
    for page in range(2):
        platform.export(name = "all", rows = [{"page": page}], format = "csv", append = True)

def test_main():
    """Both pages are in the one output."""
    main()
    out = testing.outputs()
    assert.eq(len(out.exports), 1)
    assert.eq([r["page"] for r in out.exports[0].rows], [0, 1])
`, &scriptrec.Recording{})
	assert.True(t, report.OK(), report.Tests[0].Failure)
}

// A run that made nothing of its rows leaves an assertion nothing to read
// from them, so the rows are not altered under it.
func TestARunThatProducedNothingIsNotAltered(t *testing.T) {
	rec, _ := record(t)
	report := runTests(t, weekly+`
def count():
    """Reads the rows and keeps nothing."""
    print(len(platform.query("select region, n from weekly")["rows"]))

def test_count():
    """The rows are read."""
    testing.replay("run_1")
    count()
    assert.contains(testing.outputs().log, "3")
`, rec)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
}

// weekRows answers the reference script's query with the given rows.
type weekRows []any

func (w weekRows) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{"columns": []any{"region", "revenue", "orders"}, "rows": []any(w)}, nil
}

// The reference script (#1939) passes every one of its own tests, reaches
// the statements a save requires, is not blind to its data, reads every
// output it produces (#1952) and answers the write its draft stopped at with
// a declared answer the notify tool's contract accepts (#1953): it is what the
// platform-reference-script page shows an author.
func TestTheReferenceScriptPassesItsOwnTests(t *testing.T) {
	ex, ok := scriptexamples.Lookup(scriptexamples.ReferenceName)
	require.True(t, ok)
	fire := time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)
	recordWeek := func(id string, rows weekRows) *scriptrec.Recording {
		params := map[string]any{"connection": "warehouse"}
		rec := scriptrec.NewRecorder(scriptrec.Header{RunID: id, FireTime: fire, Params: params, MaxRows: scriptrun.DraftMaxRows, Preview: true})
		// Recorded as a draft is: behind the write barrier, so the draft of a
		// week with orders stops at the notification it would post.
		_, _ = scriptrun.Run(context.Background(), scriptrun.Options{
			Source: ex.Source, Name: "weekly", RunID: id, FireTime: fire, Params: params, Caller: rows, OnCall: rec.OnCall,
			Writes: scriptrun.WritesRefused,
		})
		data, reason := rec.Finish()
		require.Empty(t, reason)
		decoded, err := scriptrec.Decode(data)
		require.NoError(t, err)
		return decoded
	}
	recordings := map[string]*scriptrec.Recording{
		"dpx_recording_of_a_draft": recordWeek("dpx_recording_of_a_draft", weekRows{
			map[string]any{"region": "east", "revenue": "10.50", "orders": 2.0},
			map[string]any{"region": "west", "revenue": "4.25", "orders": 1.0},
		}),
		"dpx_recording_of_an_empty_week": recordWeek("dpx_recording_of_an_empty_week", weekRows{}),
	}
	for _, c := range recordings["dpx_recording_of_a_draft"].Calls {
		assert.NotEqual(t, "notify", c.Tool, "the draft stops at the notification and records no answer for it")
	}
	report, err := Run(context.Background(), Request{
		Source: ex.Source, Name: "weekly", Contracts: toolanswer.New(notifylayer.AnswerContracts()...),
		Load: func(_ context.Context, id string) (*scriptrec.Recording, error) { return recordings[id], nil },
	})
	require.NoError(t, err)
	for _, test := range report.Tests {
		assert.True(t, test.Passed, "%s: %s (line %d)", test.Name, test.Failure, test.Line)
		assert.Empty(t, test.Notes, "the declared answer was checked against the notify tool's contract")
	}
	assert.Len(t, report.Tests, 4)
	assert.GreaterOrEqual(t, report.Coverage.Percent, 80.0, "missed %v", report.Coverage.MissedLines)
	assert.Empty(t, report.Unread)
}
