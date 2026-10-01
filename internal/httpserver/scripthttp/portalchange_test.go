package scripthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/draftview"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
)

// warehouse answers carol's query with two rows.
type warehouse struct{}

func (warehouse) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{
		"columns": []any{"region", "n"},
		"rows":    []any{map[string]any{"region": "east", "n": 1.0}, map[string]any{"region": "west", "n": 2.0}},
	}, nil
}

// recordings holds one recorded run of carol's script.
type recordings struct {
	runs []scriptrec.Stored
	kept []string
}

func (r *recordings) Save(_ context.Context, rec scriptrec.Stored) error {
	r.runs = append(r.runs, rec)
	return nil
}

func (*recordings) Get(context.Context, string) (*scriptrec.Stored, error) {
	return nil, scriptrec.ErrNotFound
}

func (r *recordings) Recent(context.Context, string, int) ([]scriptrec.Stored, error) {
	return r.runs, nil
}

func (r *recordings) Keep(_ context.Context, _, _ string, ids []string) error {
	r.kept = ids
	return nil
}
func (*recordings) Purge(context.Context, time.Duration) (int64, error) { return 0, nil }

// recordedCarol is a gate over one recorded run of carol's saved script.
func recordedCarol(t *testing.T) (*scriptsave.Gate, *recordings) {
	t.Helper()
	st := &recordings{}
	rec := scriptrec.NewRecorder(scriptrec.Header{RunID: "srun_1", MaxRows: scriptrun.DraftMaxRows, Preview: true})
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: carolsSource, Name: "carols-report", RunID: "srun_1", Caller: warehouse{}, OnCall: rec.OnCall,
	})
	require.NoError(t, err)
	require.True(t, rec.SaveTo(context.Background(), st, scriptrec.Meta{
		RunID: "srun_1", ScriptID: "script_2", Kind: scriptrec.KindRun, Succeeded: true,
	}))
	return &scriptsave.Gate{Recordings: st}, st
}

// A portal save that changes what the automation does is refused with 409
// and the differences, and saves once it carries the summary the person
// agreed to (#1942).
func TestPortalSetSource_ABehaviorChangeIsA409UntilAgreed(t *testing.T) {
	gate, st := recordedCarol(t)
	store := newEditStore()
	deps := editDeps(store, carol)
	deps.Gate = gate
	changed := strings.Replace(carolsSource, `rows = res["rows"]`, `rows = res["rows"][:1]`, 1) + dailyTest

	rec := servePortalRequest(t, deps, http.MethodPut, sourcePath, `{"source":`+strconv.Quote(changed)+`}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var problem changeNeededProblem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Contains(t, problem.Type, "behavior_change")
	require.Len(t, problem.Differences, 1)
	assert.Equal(t, `run srun_1: output "daily" has 1 rows, where it had 2`, problem.Differences[0].String())
	assert.Nil(t, store.updated, "nothing was saved")

	body := `{"source":` + strconv.Quote(changed) + `,"change_summary":"Only the first region is exported.","user_agreed":true}`
	rec = servePortalRequest(t, deps, http.MethodPut, sourcePath, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, store.updated)
	assert.Equal(t, "Only the first region is exported.", store.updated.ChangeSummary)
	assert.Equal(t, "carol@example.com", store.updated.ChangeAgreedBy)
	assert.Empty(t, st.kept, "the saved source's tests name no recording")
}

// A portal edit is held to its tests like the tool's save.
func TestPortalSetSource_RefusesAScriptWithoutTests(t *testing.T) {
	store := newEditStore()
	rec := servePortalRequest(t, editDeps(store, carol), http.MethodPut, sourcePath, `{"source":`+strconv.Quote(carolsSource)+`}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "has no tests")
}

// validate reports the tests and the differences a save would be told.
func TestPortalValidateSource_ReportsTheTestsAndTheDifferences(t *testing.T) {
	gate, _ := recordedCarol(t)
	deps, _, _ := draftDeps(portalStore(), carol)
	deps.Gate = gate
	changed := strings.Replace(carolsSource, `rows = res["rows"]`, `rows = res["rows"][:1]`, 1) + dailyTest
	rec := servePortalRequest(t, deps, http.MethodPost, validatePath, draftBody(changed))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body validateResponse
	decodeInto(t, rec, &body)
	assert.False(t, body.OK)
	require.NotNil(t, body.Tests)
	require.Len(t, body.Differences, 1)
	assert.Contains(t, body.SaveRefusal, "changes what the automation does")
}

// A dry run names the recording a test replays it by (#1939).
func TestDraftOutcome_NamesTheRecording(t *testing.T) {
	assert.Equal(t, "dpx_1", draftview.Of(&scriptdraft.Outcome{RunID: "dpx_1", Recorded: true}).Recording)
	assert.Empty(t, draftview.Of(&scriptdraft.Outcome{RunID: "dpx_1"}).Recording)
}
