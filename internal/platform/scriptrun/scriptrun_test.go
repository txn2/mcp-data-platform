package scriptrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// recordingCaller answers every tool call with a canned query result and
// records what it was asked, so a test can assert on the SQL the host actually
// sent rather than on the SQL the script wrote.
type recordingCaller struct {
	calls []recordedCall
	rows  []any
	err   error
}

type recordedCall struct {
	name string
	args map[string]any
}

func (c *recordingCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, recordedCall{name: name, args: args})
	if c.err != nil {
		return nil, c.err
	}
	rows := c.rows
	if rows == nil {
		rows = []any{map[string]any{"region": "west", "total": float64(42)}}
	}
	return map[string]any{
		"columns":   []any{map[string]any{"name": "region", "type": "varchar"}},
		"rows":      rows,
		"row_count": float64(len(rows)),
	}, nil
}

// fireTime is the pinned instant every test run is given.
var fireTime = time.Date(2026, 8, 13, 7, 30, 0, 0, time.UTC)

// execute runs source with the standard test options.
func execute(t *testing.T, source string, caller Caller, params map[string]any) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "run_1",
		FireTime: fireTime, Params: params, Caller: caller,
	})
}

func TestRun_QueryRowsAndLog(t *testing.T) {
	caller := &recordingCaller{}
	result, err := execute(t, `
res = platform.query(connection="primary", sql="SELECT region FROM t")
print("rows: %d" % res["row_count"])
for row in res["rows"]:
    print(row["region"])
`, caller, nil)

	require.NoError(t, err)
	assert.Equal(t, "rows: 1\nwest\n", result.Log)
	assert.False(t, result.LogTruncated)
	assert.Equal(t, 1, result.Queries)
	assert.Positive(t, result.Steps)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, toolQuery, caller.calls[0].name)
	assert.Equal(t, "primary", caller.calls[0].args["connection"])
	assert.Equal(t, DraftMaxRows, caller.calls[0].args["limit"],
		"the row cap is pushed down to the query rather than trusted to come back small")
}

// TestRun_ParametersAreBoundNotSpliced is the security-relevant one: a value
// that looks like SQL is a value.
func TestRun_ParametersAreBoundNotSpliced(t *testing.T) {
	caller := &recordingCaller{}
	_, err := execute(t, `
platform.query(sql="SELECT * FROM t WHERE region = :r", params={"r": run.params["region"]})
`, caller, map[string]any{"region": "x' OR '1'='1"})

	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, "SELECT * FROM t WHERE region = 'x'' OR ''1''=''1'", caller.calls[0].args["sql"])
}

// TestRun_FrozenRunRecord proves the script cannot rewrite the pinned time, and
// that no clock or RNG is reachable at all.
func TestRun_FrozenRunRecord(t *testing.T) {
	result, err := execute(t, `
print(run.run_id)
print(run.fire_time)
print(run.params["day"])
`, nil, map[string]any{"day": "2026-08-12"})
	require.NoError(t, err)
	assert.Equal(t, "run_1\n2026-08-13T07:30:00Z\n2026-08-12\n", result.Log)

	_, err = execute(t, `run.params["day"] = "tampered"`, nil, map[string]any{"day": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frozen")

	for _, forbidden := range []string{"time.now()", "datetime.now()", "random.random()", "open('x')"} {
		_, err := execute(t, "x = "+forbidden, nil, nil)
		assert.Error(t, err, "%s must not resolve", forbidden)
	}
}

// TestRun_DialectRestrictions pins the two switches that are about safety, and
// the two that are deliberately relaxed for authoring.
func TestRun_DialectRestrictions(t *testing.T) {
	_, err := execute(t, "x = 1\nwhile x:\n    x = 0\n", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "while")

	_, err = execute(t, "def f(n):\n    return f(n)\nf(1)\n", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recursi")

	// Top-level control flow and accumulating into a top-level name are the
	// authoring shapes the Bazel defaults would have refused.
	result, err := execute(t, `
total = 0
for n in [1, 2, 3]:
    if n > 1:
        total = total + n
print(total)
`, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "5\n", result.Log)
}

func TestRun_StepLimit(t *testing.T) {
	result, err := Run(context.Background(), Options{
		Source: "x = 0\nfor n in range(100000):\n    x = x + n\n", Name: "test",
		MaxSteps: 500, FireTime: fireTime,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStepLimit)
	assert.Contains(t, err.Error(), "move the work into SQL")
	require.NotNil(t, result)
	assert.Positive(t, result.Steps)
}

func TestRun_Timeout(t *testing.T) {
	// The caller blocks past the deadline, so the run is stopped by the wall
	// clock while it is inside a host call rather than by the step limit.
	blocking := blockingCaller{}
	result, err := Run(context.Background(), Options{
		Source: `platform.query(sql="SELECT 1")`, Name: "test",
		Timeout: 50 * time.Millisecond, FireTime: fireTime, Caller: blocking,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimeout)
	assert.NotNil(t, result)
}

type blockingCaller struct{}

func (blockingCaller) CallTool(ctx context.Context, _ string, _ map[string]any) (map[string]any, error) {
	<-ctx.Done()
	return nil, fmt.Errorf("blocked past the deadline: %w", ctx.Err())
}

func TestRun_LogTruncation(t *testing.T) {
	result, err := Run(context.Background(), Options{
		Source: "for n in range(100):\n    print('x' * 100)\n", Name: "test",
		FireTime: fireTime, MaxLogBytes: 256,
	})
	require.NoError(t, err)
	assert.True(t, result.LogTruncated)
	assert.Contains(t, result.Log, "log truncated")
	assert.Less(t, len(result.Log), 512)
	assert.True(t, strings.HasPrefix(result.Log, strings.Repeat("x", 100)),
		"the head of the log is kept: the first lines explain how a run got where it did")
}

func TestRun_ResultCaps(t *testing.T) {
	many := make([]any, 5)
	for i := range many {
		many[i] = map[string]any{"n": float64(i)}
	}
	_, err := Run(context.Background(), Options{
		Source: `platform.query(sql="SELECT 1")`, Name: "test", FireTime: fireTime,
		Caller: &recordingCaller{rows: many}, MaxRows: 2,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the 2-row cap")

	_, err = Run(context.Background(), Options{
		Source: `platform.query(sql="SELECT 1")`, Name: "test", FireTime: fireTime,
		Caller: &recordingCaller{}, MaxResultBytes: 4,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "byte cap")
}

func TestRun_QueryFailuresSurfaceToTheScript(t *testing.T) {
	_, err := execute(t, `platform.query(sql="SELECT 1")`, &recordingCaller{err: errors.New("trino unreachable")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trino unreachable")

	// With no caller wired, the binding exists but refuses, rather than being
	// absent and producing a confusing "undefined" error.
	_, err = execute(t, `platform.query(sql="SELECT 1")`, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

// TestRun_WriteSQLIsRefusedInTheScriptSurfaceVocabulary pins #1356: the query
// tool refuses a write with advice to call trino_execute, and neither that tool
// nor trino_query exists inside a script. The refusal names what an author can
// actually reach.
func TestRun_WriteSQLIsRefusedInTheScriptSurfaceVocabulary(t *testing.T) {
	writes := []string{
		`platform.query(sql="DELETE FROM sales WHERE day = '2026-08-18'")`,
		`platform.query(sql="INSERT INTO sales VALUES (1)")`,
		`platform.query(sql="CREATE TABLE t AS SELECT 1")`,
	}
	for _, source := range writes {
		caller := &recordingCaller{}
		_, err := execute(t, source, caller, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "platform.query is read-only")
		assert.Contains(t, err.Error(), "platform.export")
		assert.NotContains(t, err.Error(), "trino_query")
		assert.NotContains(t, err.Error(), "trino_execute")
		assert.Empty(t, caller.calls, "the refusal precedes the tool call")
	}

	// The refusal is exactly the query tool's own predicate, so a SELECT that
	// merely mentions one of those words is not caught by it.
	caller := &recordingCaller{}
	_, err := execute(t, `platform.query(sql="SELECT deleted FROM sales")`, caller, nil)
	require.NoError(t, err)
	assert.Len(t, caller.calls, 1)
}

func TestRun_ExportRecordsAndValidates(t *testing.T) {
	result, err := execute(t, `
out = platform.export(name="daily", rows=[{"a": 1}], format="json")
print(json.encode(out))
`, nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	// The reported size is the size a real write produces, not an estimate
	// standing in for it (#1354): the same formatter, over the same rows.
	written, _, err := FormatOutput(ExportRequest{
		Name: "daily", Format: "json", Columns: []string{"a"}, Rows: []any{map[string]any{"a": int64(1)}},
	})
	require.NoError(t, err)
	assert.Equal(t, ExportRecord{
		Name: "daily", Destination: script.DestinationPortal,
		Format: "json", RowCount: 1, Bytes: len(written), Preview: true,
	}, result.Exports[0],
		"a run given no Exporter previews: it measures the output and writes nothing")
	assert.Contains(t, result.Log, `"preview":true`)

	cases := []struct {
		name    string
		source  string
		wantErr string
	}{
		{"unknown format", `platform.export(name="d", rows=[], format="parquet")`, "is not one of"},
		{"blank name", `platform.export(name="  ", rows=[])`, "name is required"},
		{"rows not a list", `platform.export(name="d", rows="nope")`, "must be a list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := execute(t, tc.source, nil, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestFormatOutput_RefusalsNameTheOutput pins that a serialization failure
// carries the output it happened to, since a run may write several and the
// author needs to know which one to fix.
func TestFormatOutput_RefusalsNameTheOutput(t *testing.T) {
	_, _, err := FormatOutput(ExportRequest{Name: "daily", Format: "parquet"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output "daily"`)
	assert.Contains(t, err.Error(), "unsupported format")

	// A cell no encoder can represent cannot come from Starlark, whose values
	// all convert to JSON-safe Go primitives. The arm still answers, because
	// FormatOutput is the writer's serializer as well as the preview's.
	_, _, err = FormatOutput(ExportRequest{
		Name: "daily", Format: "json", Columns: []string{"a"},
		Rows: []any{map[string]any{"a": make(chan int)}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `formatting output "daily"`)
}

// TestFormatOutput_RefusesAnOversizedOutput keeps a script from writing an
// asset a person could not have exported by hand, and pins that a DRAFT is
// refused on the same terms: the ceiling belongs to the serializer both runs
// share, so an output too large to write is refused while the author can still
// do something about it rather than at the first fire after an approval.
func TestFormatOutput_RefusesAnOversizedOutput(t *testing.T) {
	big := strings.Repeat("x", 1024)
	rows := make([]any, 0, 128*1024)
	for range 128 * 1024 {
		rows = append(rows, map[string]any{"blob": big})
	}
	req := ExportRequest{Name: "huge", Format: "csv", Columns: []string{"blob"}, Rows: rows}

	_, _, err := FormatOutput(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the")

	host := &hostState{ctx: context.Background()}
	_, err = host.persistOrPreview(starlark.NewBuiltin("platform.export", nil), req)
	require.Error(t, err, "a draft run measures against the ceiling it would be written under")
	assert.Contains(t, err.Error(), "over the")
}

// TestTabular_ProjectsRowsOntoTheColumnOrder pins the projection every output
// format shares. It sits beside the formatter it feeds: one serializer writes a
// script's output, whether the run persists it or only measures it.
func TestTabular_ProjectsRowsOntoTheColumnOrder(t *testing.T) {
	rows := tabular([]string{"a", "b"}, []any{
		map[string]any{"b": 2, "a": 1},
		map[string]any{"a": 3},
		"not a dict",
	})
	assert.Equal(t, [][]any{{1, 2}, {3, nil}, {nil, nil}}, rows,
		"a missing column is an empty cell, not a shifted row")
}

func TestRun_ExportCountCap(t *testing.T) {
	_, err := execute(t, `
for n in range(20):
    platform.export(name="out-%d" % n, rows=[])
`, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 16 outputs")
}

// TestRun_SameInputsSameOutput is the determinism contract at the one place the
// platform fully controls it: identical source, parameters, and tool results
// produce a byte-identical log, including the ordering of dict keys that Go's
// randomized map iteration would otherwise scramble.
func TestRun_SameInputsSameOutput(t *testing.T) {
	source := `
res = platform.query(sql="SELECT 1")
print(json.encode(res["rows"]))
print(json.encode(run.params))
`
	params := map[string]any{"b": "two", "a": "one", "c": "three"}
	rows := []any{map[string]any{"z": float64(1), "a": "x", "m": true}}

	first, err := Run(context.Background(), Options{
		Source: source, Name: "t", RunID: "r", FireTime: fireTime,
		Params: params, Caller: &recordingCaller{rows: rows},
	})
	require.NoError(t, err)
	for range 25 {
		next, err := Run(context.Background(), Options{
			Source: source, Name: "t", RunID: "r", FireTime: fireTime,
			Params: params, Caller: &recordingCaller{rows: rows},
		})
		require.NoError(t, err)
		require.Equal(t, first.Log, next.Log)
		require.Equal(t, first.Steps, next.Steps)
	}
}

func TestRun_ParseErrorCarriesTheBacktrace(t *testing.T) {
	_, err := execute(t, "x = (", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "got end of file")
}

func TestRun_FailStopsTheRun(t *testing.T) {
	result, err := execute(t, "print('before')\nfail('no regions supplied')\n", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no regions supplied")
	assert.Equal(t, "before\n", result.Log, "a failed run still returns the log it produced")
}

// TestRun_QueryToolAnsweringAnUnexpectedShape covers the case where the tool
// returns something the binding cannot read: the author gets a message naming
// the real problem instead of a type error one line later.
func TestRun_QueryToolAnsweringAnUnexpectedShape(t *testing.T) {
	_, err := execute(t, `platform.query(sql="SELECT 1")`, shapelessCaller{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rows field")
}

type shapelessCaller struct{}

func (shapelessCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{"message": "done"}, nil
}

// TestRun_TruncatedResultFailsTheRun is the correctness case a row-count check
// cannot catch: the row cap is pushed down as the query's limit, so the engine
// stops at exactly that many rows and len(rows) never exceeds the cap. A script
// summing the first N rows of a larger result would report a wrong total with
// nothing to show that anything was missing, which is precisely what the
// determinism contract promises will not happen.
func TestRun_TruncatedResultFailsTheRun(t *testing.T) {
	_, err := execute(t, `platform.query(sql="SELECT 1")`, truncatingCaller{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was truncated")
	assert.Contains(t, err.Error(), "aggregate in SQL")
}

type truncatingCaller struct{}

func (truncatingCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{
		"columns":   []any{},
		"rows":      []any{map[string]any{"n": float64(1)}},
		"row_count": float64(1),
		"stats":     map[string]any{"truncated": true, "limit_applied": float64(1)},
	}, nil
}

// TestRun_CompleteResultWithStatsIsAccepted keeps the truncation check from
// firing on the ordinary case, and on a tool that reports no stats at all.
func TestRun_CompleteResultWithStatsIsAccepted(t *testing.T) {
	result, err := execute(t, `print(platform.query(sql="SELECT 1")["row_count"])`, completeCaller{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "1\n", result.Log)

	// The recording caller returns no stats field; absence is not truncation.
	_, err = execute(t, `platform.query(sql="SELECT 1")`, &recordingCaller{}, nil)
	assert.NoError(t, err)
}

type completeCaller struct{}

func (completeCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{
		"columns":   []any{},
		"rows":      []any{map[string]any{"n": float64(1)}},
		"row_count": float64(1),
		"stats":     map[string]any{"truncated": false},
	}, nil
}

// TestRun_LogTruncationKeepsRunesIntact: cutting at a byte offset through a
// multi-byte character leaves an invalid byte that json.Marshal silently
// rewrites to U+FFFD in the response.
func TestRun_LogTruncationKeepsRunesIntact(t *testing.T) {
	result, err := Run(context.Background(), Options{
		Source: "for n in range(50):\n    print('日本語テキスト')\n", Name: "test",
		FireTime: fireTime, MaxLogBytes: 100,
	})
	require.NoError(t, err)
	require.True(t, result.LogTruncated)
	assert.True(t, utf8.ValidString(result.Log), "a truncated log must still be valid UTF-8")
}
