package scriptsave

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptbehavior"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// upstream answers a query with two regions and anything else with an
// acknowledgement.
type upstream struct{}

func (upstream) CallTool(_ context.Context, name string, _ map[string]any) (map[string]any, error) {
	if name == scriptrun.ToolQuery {
		return map[string]any{
			"columns": []any{"region", "n"},
			"rows":    []any{map[string]any{"region": "east", "n": 1.0}, map[string]any{"region": "west", "n": 2.0}},
			"stats":   map[string]any{"row_count": 2.0, "truncated": false},
		}, nil
	}
	return map[string]any{"ok": true}, nil
}

// store is an in-memory scriptrec.Store.
type store struct {
	recs   []scriptrec.Stored
	kept   []string
	keptBy string
	err    error
}

func (s *store) Save(_ context.Context, rec scriptrec.Stored) error {
	s.recs = append(s.recs, rec)
	return nil
}

func (s *store) Get(_ context.Context, id string) (*scriptrec.Stored, error) {
	for _, r := range s.recs {
		if r.RunID == id {
			out := r
			return &out, nil
		}
	}
	return nil, scriptrec.ErrNotFound
}

func (s *store) Recent(_ context.Context, scriptID string, _ int) ([]scriptrec.Stored, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []scriptrec.Stored
	for _, r := range s.recs {
		if r.ScriptID == scriptID && r.Kind == scriptrec.KindRun {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *store) Keep(_ context.Context, _, author string, ids []string) error {
	s.kept, s.keptBy = ids, author
	return nil
}

func (*store) Purge(context.Context, time.Duration) (int64, error) { return 0, nil }

// record runs source against the upstream and stores its recording as runID
// of the script scriptID, a run when scriptID is set and a draft otherwise.
func record(t *testing.T, st *store, runID, scriptID, source string) {
	t.Helper()
	fire := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	rec := scriptrec.NewRecorder(scriptrec.Header{RunID: runID, FireTime: fire, MaxRows: scriptrun.DraftMaxRows, Preview: true})
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: source, Name: "weekly", RunID: runID, FireTime: fire, Caller: upstream{}, OnCall: rec.OnCall,
	})
	require.NoError(t, err)
	kind := scriptrec.KindDraft
	if scriptID != "" {
		kind = scriptrec.KindRun
	}
	require.True(t, rec.SaveTo(context.Background(), st, scriptrec.Meta{
		RunID: runID, ScriptID: scriptID, Kind: kind, RecordedBy: "jane@example.com", Succeeded: true,
	}))
}

// weekly exports the regions a query returns, and fails on none, a branch no
// recording takes.
const weekly = `def regions():
    """The weekly regions."""
    return platform.query("select region, n from weekly")["rows"]

def main():
    """Export the weekly regions."""
    rows = regions()
    if not rows:
        fail("no rows")
    platform.export(name = "weekly", rows = rows, format = "csv")
`

// replays is the test weekly is saved with.
const replays = `
def test_weekly():
    """The recorded run exports two regions."""
    testing.replay("run_1")
    main()
    assert.eq(testing.outputs().exports[0].row_count, 2)
    assert.eq([r["region"] for r in testing.outputs().exports[0].rows], ["east", "west"])
    assert.eq([r["n"] for r in testing.outputs().exports[0].rows], [1, 2])
`

var jane = Caller{Email: "jane@example.com"}

func TestANewScriptIsSavedOnlyWithPassingTests(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	g := &Gate{Recordings: st}

	res := g.Check(context.Background(), Request{Name: "weekly", Source: weekly, Caller: jane})
	assert.Contains(t, res.Refusal, "has no tests")

	failing := weekly + strings.Replace(replays, "row_count, 2)", "row_count, 3)", 1)
	res = g.Check(context.Background(), Request{Name: "weekly", Source: failing, Caller: jane})
	assert.Contains(t, res.Refusal, "a test failed")
	assert.Contains(t, res.Refusal, "test_weekly (line 16): assert.eq: got 2, want 3")

	res = g.Check(context.Background(), Request{Name: "weekly", Source: weekly + replays, Caller: jane})
	assert.Empty(t, res.Refusal)
	require.NotNil(t, res.Tests)
	assert.Equal(t, 1, res.Tests.Passed)
}

func TestCoverageUnderTheMinimumIsRefusedNamingTheLines(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	g := &Gate{Recordings: st}
	// Six more statements the recorded run never reaches take the share
	// under the minimum.
	branchy := strings.Replace(weekly, `        fail("no rows")`, `        a = 1
        b = a + 1
        c = b + 1
        d = c + 1
        print(d)
        fail("no rows")`, 1)
	res := g.Check(context.Background(), Request{Name: "weekly", Source: branchy + replays, Caller: jane})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, "under the 80% a script is saved with")
	assert.Contains(t, res.Refusal, "add tests that reach lines 9, 10, 11, 12, 13, 14")
}

func TestAScriptSavedBeforeTestsSavesWithoutThemAndKeepsItsCoverage(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	g := &Gate{Recordings: st}
	existing := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: weekly, TestsOptional: true}

	res := g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: weekly, Caller: jane})
	assert.Empty(t, res.Refusal, "it saves without tests")

	withTests := *existing
	withTests.Source = weekly + replays
	lower := weekly + `
def test_regions():
    """Only the query."""
    testing.replay("run_1")
    assert.eq(len(regions()), 2)
`
	res = g.Check(context.Background(), Request{Existing: &withTests, Name: "weekly", Source: lower, Caller: jane})
	assert.Contains(t, res.Refusal, "may not lower its coverage")

	res = g.Check(context.Background(), Request{
		Existing: &withTests, Name: "weekly",
		Source: weekly + strings.Replace(replays, "row_count, 2)", "row_count, 5)", 1), Caller: jane,
	})
	assert.Contains(t, res.Refusal, "a test failed", "its tests must keep passing")
}

func TestABehaviorChangeNeedsASummaryAndAgreement(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	record(t, st, "run_9", "s1", weekly)
	g := &Gate{Recordings: st}
	existing := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: weekly + replays}

	// A refactor: the helper is renamed and the outputs do not change.
	renamed := strings.ReplaceAll(weekly, "regions()", "weekly_regions()")
	res := g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: renamed + replays, Caller: jane})
	assert.Empty(t, res.Refusal)
	assert.Empty(t, res.Differences)
	assert.Equal(t, []string{"run_9"}, res.Replayed)

	// A column the export no longer carries, whose test no longer reads it.
	dropped := strings.Replace(weekly, `rows = rows, format`, `rows = [{"region": r["region"]} for r in rows], format`, 1) +
		strings.Replace(replays, "    assert.eq([r[\"n\"] for r in testing.outputs().exports[0].rows], [1, 2])\n", "", 1)
	res = g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: dropped, Caller: jane})
	require.True(t, res.ChangeNeeded)
	assert.Contains(t, res.Refusal, `output "weekly" no longer has column "n"`)
	assert.Contains(t, res.Refusal, "change_summary")

	res = g.Check(context.Background(), Request{
		Existing: existing, Name: "weekly", Source: dropped, Caller: jane,
		ChangeSummary: "The weekly file no longer carries the count.", Agreed: true,
	})
	assert.Empty(t, res.Refusal)
	assert.Equal(t, "The weekly file no longer carries the count.", res.ChangeSummary)
	assert.Equal(t, "jane@example.com", res.ChangeAgreedBy)

	// A summary without the agreement is not enough.
	res = g.Check(context.Background(), Request{
		Existing: existing, Name: "weekly", Source: dropped, Caller: jane, ChangeSummary: "x",
	})
	assert.True(t, res.ChangeNeeded)
}

func TestANewToolIsAChangeNamingTheTool(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	g := &Gate{Recordings: st}
	existing := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: weekly + replays}
	calls := strings.Replace(weekly, `    platform.export(`, `    platform.call("s3_object", {"action": "list"})
    platform.export(`, 1)
	// The test replays a recording that lacks the new call, so the source
	// is compared with no recorded run of the script: the reach alone.
	res := g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: calls + replays, Caller: jane})
	assert.Contains(t, res.Refusal, `the recording holds no answer for platform.call("s3_object"`)

	st.recs = nil
	record(t, st, "run_1", "", calls)
	res = g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: calls + replays, Caller: jane})
	require.True(t, res.ChangeNeeded, res.Refusal)
	assert.Contains(t, res.Refusal, `reaches tool "s3_object", which the saved version does not`)
}

func TestANewCallTheRecordedRunsDoNotHoldIsADifference(t *testing.T) {
	st := &store{}
	record(t, st, "run_9", "s1", weekly)
	before, after := weekly, strings.Replace(weekly, `"select region, n from weekly"`, `"select region, n from weekly_v2"`, 1)
	diffs, compared := scriptbehavior.Compare("run_9",
		replay(t, st, before), replay(t, st, after))
	require.True(t, compared)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0].String(), `makes platform.query("select region, n from weekly_v2")`)
}

func TestARecordedRunTheSavedVersionCannotReplayIsNotCompared(t *testing.T) {
	st := &store{}
	older := strings.Replace(weekly, `"select region, n from weekly"`, `"select * from old"`, 1)
	record(t, st, "run_9", "s1", older)
	g := &Gate{Recordings: st}
	existing := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: weekly, TestsOptional: true}
	res := g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: weekly + "\n# a comment\n", Caller: jane})
	assert.Empty(t, res.Refusal)
	assert.Equal(t, []string{"run_9"}, res.NotCompared)
}

func TestUnreadableRecordedRunsAreADifference(t *testing.T) {
	g := &Gate{Recordings: &store{err: errors.New("down")}}
	existing := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: weekly, TestsOptional: true}
	res := g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: weekly + "\n# a comment\n", Caller: jane})
	assert.True(t, res.ChangeNeeded)
	assert.Contains(t, res.Refusal, "could not be read")
}

func TestARecordingIsReadByItsOwnerAndAnAdministratorOnly(t *testing.T) {
	sc := &script.Script{ID: "s1", OwnerEmail: "jane@example.com"}
	run := scriptrec.Meta{ScriptID: "s1", Kind: scriptrec.KindRun, RecordedBy: "jane@example.com"}
	assert.True(t, Readable(run, sc, jane))
	assert.False(t, Readable(run, &script.Script{ID: "s1", OwnerEmail: "bob@example.com"}, jane),
		"a run is not its former owner's once the script moved")
	assert.True(t, Readable(scriptrec.Meta{Kind: scriptrec.KindDraft, RecordedBy: "jane@example.com"}, nil, jane),
		"a draft of a script not saved yet is its author's")
	assert.True(t, Readable(run, sc, Caller{Email: "root@example.com", Admin: true}))
	assert.False(t, Readable(run, sc, Caller{Email: "bob@example.com"}))
	moved := &script.Script{ID: "s1", OwnerEmail: "bob@example.com"}
	assert.True(t, Readable(run, moved, Caller{Email: "bob@example.com"}), "the script's new owner reads it")
	assert.False(t, Readable(scriptrec.Meta{ScriptID: "s2", RecordedBy: "carol@example.com"}, moved, Caller{Email: "bob@example.com"}))

	st := &store{}
	record(t, st, "run_1", "", weekly)
	st.recs[0].Reason = "too large"
	_, err := (&Gate{Recordings: st}).Loader(nil, jane)(context.Background(), "run_1")
	assert.ErrorContains(t, err, "kept no recording: too large")
	_, err = (&Gate{Recordings: st}).Loader(nil, Caller{Email: "bob@example.com"})(context.Background(), "run_1")
	assert.ErrorIs(t, err, scriptrec.ErrNotFound)
	_, err = (&Gate{}).Loader(nil, jane)(context.Background(), "run_1")
	assert.ErrorIs(t, err, scriptrec.ErrNotFound)
}

func TestKeepNamesTheRecordingsTheSavedTestsName(t *testing.T) {
	st := &store{}
	g := &Gate{Recordings: st}
	require.NoError(t, g.Keep(context.Background(), &script.Script{ID: "s1", Source: weekly + replays}, "jane@example.com"))
	assert.Equal(t, []string{"run_1"}, st.kept)
	assert.Equal(t, "jane@example.com", st.keptBy)
	assert.NoError(t, (&Gate{}).Keep(context.Background(), &script.Script{ID: "s1"}, "x"))
}

func TestALintFindingRefusesBeforeTheTestsRun(t *testing.T) {
	res := (&Gate{}).Check(context.Background(), Request{Name: "x", Source: "print(1)\n", Caller: jane})
	assert.True(t, res.Refused())
	assert.Nil(t, res.Tests)
	assert.Contains(t, res.Refusal, "top-level-work")
}

// replay runs source against run_9.
func replay(t *testing.T, st *store, source string) scripttest.Outcome {
	t.Helper()
	stored, err := st.Get(context.Background(), "run_9")
	require.NoError(t, err)
	rec, err := scriptrec.Decode(stored.Data)
	require.NoError(t, err)
	return scripttest.Replay(context.Background(), scripttest.Request{Source: source, Name: "weekly"}, rec)
}

// Apply sets what a save stores and the change it carries.
func TestApplySetsTheSourceAndTheChange(t *testing.T) {
	sc := &script.Script{}
	Result{Lint: scriptlint.Result{Source: "x"}, ChangeSummary: "s", ChangeAgreedBy: "a"}.Apply(sc)
	assert.Equal(t, "x", sc.Source)
	assert.Equal(t, "s", sc.ChangeSummary)
	assert.Equal(t, "a", sc.ChangeAgreedBy)
}

// The saved version's tests that cannot be run leave the coverage rule
// nothing to compare against.
func TestCoverageIsKeptOnlyAgainstTestsThatRan(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	g := &Gate{Recordings: st}
	existing := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: "def (", TestsOptional: true}
	res := g.Check(context.Background(), Request{Existing: existing, Name: "weekly", Source: weekly + replays, Caller: jane})
	assert.NotContains(t, res.Refusal, "coverage")
}

func TestANewScriptsTestsMustReadEveryOutputTheyProduce(t *testing.T) {
	st := &store{}
	record(t, st, "run_1", "", weekly)
	g := &Gate{Recordings: st}
	countOnly := weekly + `
def test_weekly():
    """The recorded run exports two regions."""
    testing.replay("run_1")
    main()
    assert.eq(testing.outputs().exports[0].row_count, 2)
`
	res := g.Check(context.Background(), Request{Name: "weekly", Source: countOnly, Caller: jane})
	require.True(t, res.Refused())
	assert.Contains(t, res.Refusal, `output "weekly" column "region" is never asserted on; output "weekly" column "n" is never asserted on`)
	assert.Equal(t, []string{`output "weekly" column "region"`, `output "weekly" column "n"`}, res.Tests.Unread)

	res = g.Check(context.Background(), Request{Name: "weekly", Source: weekly + replays, Caller: jane})
	assert.Empty(t, res.Refusal, "reading both columns saves")

	// A script created before tests had to read their outputs keeps saving
	// as it did.
	older := &script.Script{ID: "s1", Name: "weekly", OwnerEmail: "jane@example.com", Source: countOnly, OutputsReadOptional: true}
	res = g.Check(context.Background(), Request{Existing: older, Name: "weekly", Source: countOnly, Caller: jane})
	assert.Empty(t, res.Refusal)
}
