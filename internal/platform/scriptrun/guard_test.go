package scriptrun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// throttlingCaller answers the first `throttled` calls with an upstream 429
// the host is told it may retry, and every call after with a 200.
type throttlingCaller struct {
	throttled int
	after     float64
	attempts  int
}

func (c *throttlingCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	c.attempts++
	if c.attempts <= c.throttled {
		return map[string]any{
			"upstream_status": float64(429), "upstream_retryable": true,
			"retry_after_seconds": c.after, "resource_unchanged": true,
		}, nil
	}
	return map[string]any{"upstream_status": float64(200)}, nil
}

const exportSource = `exp = platform.call("api_export", {"connection": "c", "path": "/x", "name": "n"})
print("status", exp["upstream_status"])
`

// TestRun_WaitsOutAnUpstream429 holds #1859: an answer the upstream said to
// retry is issued again after its interval, each wait is in the log, and the
// script sees the answer that got through.
func TestRun_WaitsOutAnUpstream429(t *testing.T) {
	caller := &throttlingCaller{throttled: 2, after: 0.01}
	result, err := execute(t, exportSource, caller, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, caller.attempts)
	assert.Equal(t, 2, strings.Count(result.Log, "upstream answered 429 Too Many Requests to api_export; waited 10ms"))
	assert.Contains(t, result.Log, "(2 of 3)")
	assert.Contains(t, result.Log, "status 200")
	assert.NotContains(t, result.Log, "still answered", "the upstream let the call through")
}

// TestRun_HandsTheScriptTheLast429 holds the bound: after three retries the
// script has the upstream's refusal as data and decides what to do with it.
func TestRun_HandsTheScriptTheLast429(t *testing.T) {
	caller := &throttlingCaller{throttled: 99, after: 0.001}
	result, err := execute(t, exportSource, caller, nil)
	require.NoError(t, err, "a 429 is data, not a failure")
	assert.Equal(t, 1+upstreamretry.MaxRetries, caller.attempts)
	assert.Contains(t, result.Log, "api_export: the upstream still answered 429 Too Many Requests after 3 retries")
	assert.Contains(t, result.Log, "status 429")
}

// TestRun_DoesNotWaitPastTheDeadline: an interval the run's deadline cannot
// hold is not waited; the script has the refusal at once.
func TestRun_DoesNotWaitPastTheDeadline(t *testing.T) {
	caller := &throttlingCaller{throttled: 1, after: 30}
	result, err := Run(context.Background(), Options{
		Source: exportSource, Name: "test", FireTime: fireTime, Caller: caller, Timeout: time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, caller.attempts)
	assert.Contains(t, result.Log, "status 429")
}

// TestRun_AStopDuringAnUpstreamWaitIsAnUpstreamFailure: a run stopped while
// the host waits on an upstream is recorded as the upstream's failure.
func TestRun_AStopDuringAnUpstreamWaitIsAnUpstreamFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err := Run(ctx, Options{
		Source: exportSource, Name: "test", FireTime: fireTime,
		Caller: &throttlingCaller{throttled: 1, after: 20}, Timeout: time.Hour,
	})
	require.Error(t, err)
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))
	assert.Contains(t, err.Error(), "waiting 20s to retry api_export after the upstream answered 429")
}

// TestRun_AnUnreachableUpstreamIsRetryable holds the classification: a call
// refused with upstream_unavailable ends the run as an upstream failure, in
// the tool's own words, and any other refusal stays the script's.
func TestRun_AnUnreachableUpstreamIsRetryable(t *testing.T) {
	unreachable := &RefusalError{Code: upstreamretry.CodeUnavailable, text: "upstream request: i/o timeout"}
	_, err := execute(t, exportSource, &refusingCaller{refuse: map[int]error{1: unreachable}}, nil)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseUpstream, scriptguard.Cause(err))
	assert.Contains(t, err.Error(), "upstream request: i/o timeout")

	notFound := &RefusalError{Code: middleware.CodeNotFound, text: "no such connection"}
	_, err = execute(t, exportSource, &refusingCaller{refuse: map[int]error{1: notFound}}, nil)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))

	_, err = execute(t, `fail("bad input")`, &refusingCaller{}, nil)
	require.Error(t, err)
	assert.Equal(t, runstate.CauseScript, scriptguard.Cause(err))
}

// TestRun_FailsARunOverItsMemoryBudget holds #1861 at the engine: a run that
// holds more than its budget fails at the next host call, naming the budget,
// and reports the peak it reached.
func TestRun_FailsARunOverItsMemoryBudget(t *testing.T) {
	result, err := Run(context.Background(), Options{
		Source: `held = ["x" * 1024 + str(i) for i in range(2000)]
platform.query(sql="SELECT 1")
`,
		Name: "test", FireTime: fireTime, Caller: &recordingCaller{}, MaxMemoryBytes: 1 << 20,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, scriptguard.ErrMemoryBudget)
	assert.Equal(t, runstate.CauseMemory, scriptguard.Cause(err))
	assert.Contains(t, err.Error(), "in platform.query: the run exceeded its 1 MiB memory budget")
	assert.Greater(t, result.PeakMemory, int64(2000*1024))
}

// TestRun_ReportsThePeakWithNoBudget: every run is measured, budget or not,
// including what it built after its last host call.
func TestRun_ReportsThePeakWithNoBudget(t *testing.T) {
	result, err := execute(t, `rows = [{"n": i} for i in range(1000)]`, &recordingCaller{}, nil)
	require.NoError(t, err)
	assert.Greater(t, result.PeakMemory, int64(1000*512), "a thousand small dicts")
}

// TestRun_AResultOverTheBudgetIsRefused: a result that would take the run
// over its budget fails the call that fetched it.
func TestRun_AResultOverTheBudgetIsRefused(t *testing.T) {
	rows := make([]any, 0, 3000)
	for i := range 3000 {
		rows = append(rows, map[string]any{"region": strings.Repeat("r", 200), "i": float64(i)})
	}
	_, err := Run(context.Background(), Options{
		Source: `res = platform.call("trino_query", {"sql": "SELECT 1"})`,
		Name:   "test", FireTime: fireTime, Caller: &recordingCaller{rows: rows}, MaxMemoryBytes: 1 << 20,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, scriptguard.ErrMemoryBudget)
	assert.Contains(t, err.Error(), "after 1 trino_query result")
}

const appendSource = `for p in range(3):
    rows = [{"page": p, "n": i} for i in range(2)]
    out = platform.export(name="pages", rows=rows, format="jsonl", append=True)
    print(out["row_count"], out["appending"])
`

// TestRun_AppendedPagesAreWrittenOnceAsOneOutput holds #1861 item 4: three
// appended pages are one output, written when the script finishes, and each
// call reports what the output holds so far.
func TestRun_AppendedPagesAreWrittenOnceAsOneOutput(t *testing.T) {
	exporter := &recordingExporter{}
	result, err := exporterRun(t, appendSource, exporter)
	require.NoError(t, err)
	assert.Equal(t, "2 True\n4 True\n6 True\n", result.Log)
	require.Len(t, exporter.requests, 1, "one output, written once")
	req := exporter.requests[0]
	assert.Equal(t, 6, req.RowCount())
	data, _, err := FormatOutput(req)
	require.NoError(t, err)
	assert.Equal(t, 6, strings.Count(string(data), "\n"))
	assert.Contains(t, string(data), `{"page":2,"n":1}`)
	require.Len(t, result.Exports, 1)
	assert.Equal(t, 6, result.Exports[0].RowCount)
	assert.False(t, result.Exports[0].Preview)
}

// TestRun_ADraftPreviewsAnAppendedOutput: with no exporter the appended
// output is measured, as every draft output is.
func TestRun_ADraftPreviewsAnAppendedOutput(t *testing.T) {
	result, err := draftPublish(t, appendSource)
	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview)
	assert.Equal(t, 6, result.Exports[0].RowCount)
	assert.Positive(t, result.Exports[0].Bytes)
}

// TestRun_AFailedRunWritesNoneOfItsAppendedOutput: a partial file under a
// registered table is a dataset that lost its tail, so a run that fails
// part-way writes nothing it appended.
func TestRun_AFailedRunWritesNoneOfItsAppendedOutput(t *testing.T) {
	exporter := &recordingExporter{}
	_, err := exporterRun(t, `platform.export(name="pages", rows=[{"n": 1}], format="jsonl", append=True)
fail("the upstream went away")
`, exporter)
	require.Error(t, err)
	assert.Empty(t, exporter.requests)
}

func TestRun_AppendRefusals(t *testing.T) {
	cases := map[string]struct {
		source, want string
		// whole is the outputs written whole before the refusal.
		whole int
	}{
		"a format whose pages do not join": {
			source: `platform.export(name="p", rows=[{"n": 1}], format="parquet", append=True)`, want: "csv or jsonl",
		},
		"a document": {
			source: `platform.export(name="p", rows="# hi", format="markdown", append=True)`, want: "adds rows",
		},
		"a page without append after the first": {
			source: `platform.export(name="p", rows=[{"n": 1}], format="jsonl", append=True)
platform.export(name="p", rows=[{"n": 2}], format="jsonl")`, want: "pass append=True on every call",
		},
		"a page to another key": {
			source: `platform.export(name="p", rows=[{"n": 1}], format="jsonl", destination="acme-drop", key="a.jsonl", append=True)
platform.export(name="p", rows=[{"n": 2}], format="jsonl", destination="acme-drop", key="b.jsonl", append=True)`,
			want: `was started at key "a.jsonl"`,
		},
		"appending to an output already written whole": {
			source: `platform.export(name="p", rows=[{"n": 1}], format="jsonl")
platform.export(name="p", rows=[{"n": 2}], format="jsonl", append=True)`, want: "already written", whole: 1,
		},
		"a CSV page with a new column": {
			source: `platform.export(name="p", rows=[{"a": 1}], format="csv", append=True)
platform.export(name="p", rows=[{"a": 2, "b": 3}], format="csv", append=True)`, want: "column(s) b",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exporter := &recordingExporter{}
			_, err := Run(context.Background(), Options{
				Source: tc.source, Name: "test", RunID: "dpx_1", FireTime: fireTime,
				Caller: &recordingCaller{}, Exporter: exporter,
				Destinations: []script.Destination{{Name: "acme-drop", Connection: "s3", Bucket: "b"}},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Len(t, exporter.requests, tc.whole, "a refused run writes nothing it appended")
		})
	}
}

// TestRun_AppendedOutputsCountAgainstTheOutputBudget: an output being
// appended to is one of the run's outputs from its first page.
func TestRun_AppendedOutputsCountAgainstTheOutputBudget(t *testing.T) {
	var src strings.Builder
	for i := range maxExports {
		_, _ = src.WriteString(`platform.export(name="p` + string(rune('a'+i)) + `", rows=[{"n": 1}], format="jsonl", append=True)` + "\n")
	}
	_, _ = src.WriteString(`platform.export(name="last", rows=[{"n": 1}], format="jsonl", append=True)` + "\n")
	_, err := exporterRun(t, src.String(), &recordingExporter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 16 outputs")
}

// TestRun_AnAppendedOutputThatFailsToLandFailsTheRun: the write happens after
// the script, and a write that fails is the run's failure.
func TestRun_AnAppendedOutputThatFailsToLandFailsTheRun(t *testing.T) {
	_, err := exporterRun(t, `platform.export(name="p", rows=[{"n": 1}], format="jsonl", append=True)`,
		&recordingExporter{err: errors.New("the portal store is down")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the portal store is down")
}

// TestRun_AnAppendedOutputRegistersItsTable: register= on the first page is
// made over the file once it is written.
func TestRun_AnAppendedOutputRegistersItsTable(t *testing.T) {
	caller := &recordingCaller{}
	_, err := Run(context.Background(), Options{
		Source: `platform.export(name="p", rows=[{"n": 1}], format="jsonl", append=True,
    register={"connection": "scratch", "table_name": "pages"})
platform.export(name="p", rows=[{"n": 2}], format="jsonl", append=True)
`,
		Name: "test", RunID: "dpx_1", FireTime: fireTime, Caller: caller, Exporter: &recordingExporter{},
	})
	require.NoError(t, err)
	require.NotEmpty(t, caller.calls)
	last := caller.calls[len(caller.calls)-1]
	assert.Equal(t, "manage_table", last.name)
}
