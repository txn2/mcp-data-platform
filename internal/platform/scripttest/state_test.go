package scripttest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// mirror merges pages until one will not answer or the run is short of time:
// it checkpoints after each page, saves its final mark only when every page
// landed, and fails when a page would not answer.
const mirror = `def main():
    """Mirror the pages, checkpointing as each one lands."""
    mark = run.state.get("through", 0)
    for page in [1, 2, 3]:
        if platform.remaining_ms() < 1000:
            platform.checkpoint({"through": mark})
            return
        rows = platform.query("select * from p where page = :p", params = {"p": page})["rows"]
        if not rows:
            platform.save_state({"through": mark})
            fail("page %d would not answer" % page, retryable = True)
        mark = page
        platform.checkpoint({"through": mark})
    platform.save_state({"through": mark})
`

// pageUpstream answers pages 1 and 2 and leaves page 3 empty.
type pageUpstream struct{}

func (pageUpstream) CallTool(_ context.Context, _ string, args map[string]any) (map[string]any, error) {
	if sql, _ := args["sql"].(string); strings.Contains(sql, "page = 3") {
		return map[string]any{"columns": []any{"x"}, "rows": []any{}, "stats": map[string]any{"row_count": 0.0}}, nil
	}
	return map[string]any{"columns": []any{"x"}, "rows": []any{map[string]any{"x": 1.0}}, "stats": map[string]any{"row_count": 1.0}}, nil
}

// recordMirror runs mirror once, live, with an hour to spare, and returns the
// recording: the reads of the time left among its calls.
func recordMirror(t *testing.T) *scriptrec.Recording {
	t.Helper()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rec := scriptrec.NewRecorder(scriptrec.Header{RunID: "run_1", FireTime: at, MaxRows: scriptrun.DraftMaxRows, Preview: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	res, err := scriptrun.Run(ctx, scriptrun.Options{
		Source: mirror, Name: "mirror", RunID: "run_1", FireTime: at, Caller: pageUpstream{}, OnCall: rec.OnCall,
	})
	require.Error(t, err, "page 3 does not answer")
	require.NotNil(t, res.Checkpoint, "the live run keeps its last checkpoint")
	assert.Equal(t, map[string]any{"through": int64(2)}, res.Checkpoint.Value)
	assert.True(t, res.Checkpoint.Checkpoint)
	data, reason := rec.Finish()
	require.Empty(t, reason)
	decoded, err := scriptrec.Decode(data)
	require.NoError(t, err)
	return decoded
}

func runMirrorTests(t *testing.T, tests string) *Report {
	t.Helper()
	rec := recordMirror(t)
	report, err := Run(context.Background(), Request{
		Source: mirror + tests, Name: "mirror",
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

// TestAFailedRunsStateIsReportedAsDiscarded is #2002: a test asserting the
// save_state of a run that failed fails, because no real run saves it; the
// discard and the checkpoint are what it can assert.
func TestAFailedRunsStateIsReportedAsDiscarded(t *testing.T) {
	report := runMirrorTests(t, `
def test_stuck_page_discards_save_state():
    testing.replay("run_1")
    assert.fails(main)
    out = testing.outputs()
    assert.eq(out.state, None)
    assert.eq(out.state_discarded, {"through": 2})
    assert.eq(out.checkpoint, {"through": 2})

def test_asserting_a_failed_runs_state_fails():
    testing.replay("run_1")
    assert.fails(main)
    assert.eq(testing.outputs().state, {"through": 2})
`)
	require.Len(t, report.Tests, 2)
	assert.True(t, report.Tests[0].Passed, report.Tests[0].Failure)
	assert.False(t, report.Tests[1].Passed, "a failed run's save_state cannot be asserted as saved")
}

// TestRemainingMSReplaysAndCanBeSet is #2004: a test reads the time the run
// had, in the order the run read it, and testing.set_run(remaining_ms=) takes
// the test down the out-of-time branch.
func TestRemainingMSReplaysAndCanBeSet(t *testing.T) {
	report := runMirrorTests(t, `
def test_replayed_time_runs_every_page():
    testing.replay("run_1")
    assert.fails(main)
    assert.eq(len(testing.outputs().calls), 3)

def test_out_of_time_checkpoints_and_stops():
    testing.replay("run_1")
    testing.set_run(state = {"through": 1}, remaining_ms = 10)
    main()
    out = testing.outputs()
    assert.eq(out.checkpoint, {"through": 1})
    assert.eq(len(out.calls), 0)
`)
	require.Len(t, report.Tests, 2)
	for _, r := range report.Tests {
		assert.True(t, r.Passed, "%s: %s", r.Name, r.Failure)
	}
}

func TestSetRunRefusesABadRemainingMS(t *testing.T) {
	report := runMirrorTests(t, `
def test_bad_value():
    testing.replay("run_1")
    testing.set_run(remaining_ms = -1)
`)
	require.Len(t, report.Tests, 1)
	assert.False(t, report.Tests[0].Passed)
	assert.Contains(t, report.Tests[0].Failure, "remaining_ms")
}
