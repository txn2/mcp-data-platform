package scriptstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// scoredSelectColumns is the ranked result-set shape: the script columns with
// the relevance score appended.
var scoredSelectColumns = append(append([]string{}, scriptSelectColumns...), "score")

// scoredRow returns one ranked row.
func scoredRow(spec rowSpec, score float64) []driver.Value {
	return append(scriptRow(spec), score)
}

// TestSearch_NoQueryTextIsNoQuery proves an empty intent costs no database
// round trip: the provider is text-path only and must not scan the table for a
// query it cannot rank.
func TestSearch_NoQueryTextIsNoQuery(t *testing.T) {
	s, mock := newMock(t)

	got, err := s.Search(context.Background(), script.SearchQuery{})

	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSearch_AppliesVisibilityAndLifecycleInSQL proves both filters are
// predicates rather than post-processing. A script the caller cannot see must
// cost neither a row nor a decision, and the lifecycle arm keeps dead ends
// (deprecated, superseded) out of the ranking.
func TestSearch_AppliesVisibilityAndLifecycleInSQL(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("script_fts(display_name, name, description, category, tags, params)")).
		WithArgs("sales report", sqlmock.AnyArg(), "jane@example.com", script.DefaultSearchLimit).
		WillReturnRows(sqlmock.NewRows(scoredSelectColumns).
			AddRow(scoredRow(rowSpec{
				id: "script_1", name: "daily-sales", owner: "jane@example.com", paramsJSON: emptyParams(t),
			}, 0.75)...))

	got, err := s.Search(context.Background(), script.SearchQuery{
		QueryText:  "sales report",
		OwnerEmail: "jane@example.com",
	})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "daily-sales", got[0].Script.Name)
	assert.InDelta(t, 0.75, got[0].Score, 0.0001)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSearch_DiscoverableStatusesExcludeDeadEnds pins the ranking rule: a
// deprecated script must not be executed and a superseded one names its
// replacement, so neither belongs in a discovery result. Active is the only
// discoverable state left now that a saved script runs without review.
func TestSearch_DiscoverableStatusesExcludeDeadEnds(t *testing.T) {
	assert.Equal(t, []string{script.StatusActive}, discoverableStatuses)
}

func TestSearch_QueryErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("script_fts").WillReturnError(errors.New("boom"))

	_, err := s.Search(context.Background(), script.SearchQuery{QueryText: "x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "search scripts")
}

// TestContract_ComposesTheWholeDocument proves one Contract call assembles the
// three reads a caller would otherwise have to make, and that the parameter
// contract reported is the live record's — the latest saved version, which is
// what a run executes.
func TestContract_ComposesTheWholeDocument(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{
				id: "script_1", name: "daily-sales", owner: "jane@example.com",
				paramsJSON: []byte(`[{"name":"report_date","type":"date","required":true}]`),
			})...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules WHERE script_id = $1")).
		WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(rowTime)...))
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY finished_at DESC NULLS LAST LIMIT 1")).
		WithArgs("script_1", script.RunStatusSucceeded).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).
			AddRow(runRow(script.RunStatusSucceeded, 1, []byte(`[{"name":"sales","asset_id":"asset_7","asset_version":4}]`))...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_state WHERE script_id = $1")).
		WillReturnRows(sqlmock.NewRows(stateSelectColumns).AddRow(stateRow(3)...))

	got, err := s.Contract(context.Background(), "script_1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "daily-sales", got.Name)
	require.Len(t, got.Params, 1)
	assert.Equal(t, "report_date", got.Params[0].Name, "the live record's contract binds the next run")
	assert.Equal(t, 1, got.Version, "the version a run executes is the latest saved one")
	assert.Empty(t, got.Refusal, "an enabled, active script admits a run")
	require.NotNil(t, got.Schedule)
	require.NotNil(t, got.LastRun)
	require.Len(t, got.LastRun.Outputs, 1)
	assert.Equal(t, script.OutputKindAsset, got.LastRun.Outputs[0].Kind)
	require.NotNil(t, got.State)
	assert.Equal(t, int64(3), got.State.Revision, "the contract carries the state's revision")
	require.NotNil(t, got.State.UpdatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestContract_MissingScriptIsNilNil pins the not-found contract the fetch path
// branches on to produce a clean "that reference is gone".
func TestContract_MissingScriptIsNilNil(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns))

	got, err := s.Contract(context.Background(), "gone")

	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestContract_ToleratesNoScheduleAndNoRun proves the two ordinary absences are
// reported as absences rather than as failures: most scripts have no schedule,
// and a newly approved one has never run.
func TestContract_ToleratesNoScheduleAndNoRun(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{
				id: "script_1", name: "daily-sales", owner: "jane@example.com", paramsJSON: emptyParams(t),
			})...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
		WillReturnRows(sqlmock.NewRows(scheduleSelectColumns))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_state")).
		WillReturnRows(sqlmock.NewRows(stateSelectColumns))

	got, err := s.Contract(context.Background(), "script_1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.Schedule)
	assert.Nil(t, got.LastRun)
	require.NotNil(t, got.State, "the contract always says what the script does with state")
	assert.Zero(t, got.State.Revision, "no state row is revision 0")
	assert.False(t, got.State.Reads)
	assert.False(t, got.State.Saves)
	assert.Empty(t, got.Refusal, "never having run is not a refusal")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestContract_ScheduleReadFailureIsAFailure proves a store outage is not
// mistaken for "this script has no schedule", which would report a scheduled
// automation as an on-demand one.
func TestContract_ScheduleReadFailureIsAFailure(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{
				id: "script_1", name: "daily-sales", owner: "jane@example.com", paramsJSON: emptyParams(t),
			})...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).WillReturnError(errors.New("down"))

	_, err := s.Contract(context.Background(), "script_1")

	require.Error(t, err)
}

// TestNewDiscoveryStore proves the composition root's one expression: a
// deployment with no database contributes no searcher at all, rather than a
// non-nil interface wrapping a nil store that would panic on first use.
func TestNewDiscoveryStore(t *testing.T) {
	assert.Nil(t, NewDiscoveryStore(nil))

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	store := NewDiscoveryStore(db)
	require.NotNil(t, store)
	_, isSearcher := store.(script.Searcher)
	assert.True(t, isSearcher, "the discovery store must satisfy the searcher capability")
}

// hybridSelectColumns is the hybrid arms' result-set shape: the script columns
// followed by the cosine score and the lexical-match flag.
var hybridSelectColumns = append(append([]string{}, scriptSelectColumns...), "vec_score", "lex_match")

// hybridRow returns one hybrid-arm row.
func hybridRow(spec rowSpec, vecScore float64, lexMatch bool) []driver.Value {
	return append(scriptRow(spec), vecScore, lexMatch)
}

// TestSearch_HybridRunsBothIndexBackedArms proves a query carrying a vector
// takes the hybrid path: the vector arm and the lexical arm are UNIONed so each
// keeps its own index, and a script matched by both is returned once, carrying
// the higher fused score rather than appearing twice.
func TestSearch_HybridRunsBothIndexBackedArms(t *testing.T) {
	s, mock := newMock(t)
	both := rowSpec{
		id: "script_1", name: "daily-sales", owner: "jane@example.com", paramsJSON: emptyParams(t),
	}
	vectorOnly := rowSpec{
		id: "script_2", name: "weekly-churn", owner: "jane@example.com", paramsJSON: emptyParams(t),
	}
	mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).
		WithArgs(sqlmock.AnyArg(), "refresh the regional sales numbers",
			sqlmock.AnyArg(), "jane@example.com").
		WillReturnRows(sqlmock.NewRows(hybridSelectColumns).
			AddRow(hybridRow(both, 0.8, false)...).
			AddRow(hybridRow(vectorOnly, 0.6, false)...).
			AddRow(hybridRow(both, 0.8, true)...))

	got, err := s.Search(context.Background(), script.SearchQuery{
		Embedding:  []float32{0.1, 0.2},
		QueryText:  "refresh the regional sales numbers",
		OwnerEmail: "jane@example.com",
	})

	require.NoError(t, err)
	require.Len(t, got, 2, "a script matched by both arms is one result, not two")
	assert.Equal(t, "script_1", got[0].Script.ID)
	// 0.6*((0.8+1)/2) + 0.4*1: the lexical arm's copy wins the dedup, which is
	// the point of keeping the higher fused score.
	assert.InDelta(t, 0.94, got[0].Score, 1e-9)
	assert.Equal(t, "script_2", got[1].Script.ID)
	assert.InDelta(t, 0.48, got[1].Score, 1e-9)
}

// TestSearch_HybridOrdersSemanticOnlyBelowAnExactMatch is the ranking property
// the ticket exists for and its limit: a script whose wording nobody typed is
// still returned, and a script that also matches the words outranks it.
func TestSearch_HybridOrdersSemanticOnlyBelowAnExactMatch(t *testing.T) {
	s, mock := newMock(t)
	lexical := rowSpec{id: "script_1", name: "a-lexical", paramsJSON: emptyParams(t)}
	semantic := rowSpec{id: "script_2", name: "b-semantic", paramsJSON: emptyParams(t)}
	mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).
		WillReturnRows(sqlmock.NewRows(hybridSelectColumns).
			// The semantically nearer script, with no term in common.
			AddRow(hybridRow(semantic, 0.95, false)...).
			// The weaker vector match that does contain the words.
			AddRow(hybridRow(lexical, 0.10, true)...))

	got, err := s.Search(context.Background(), script.SearchQuery{
		Embedding: []float32{0.1}, QueryText: "sales",
	})

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "script_1", got[0].Script.ID, "an exact-term match takes a decisive boost")
	assert.Equal(t, "script_2", got[1].Script.ID, "a semantic-only match still ranks, which is the whole point")
}

// TestSearch_HybridQueryErrorIsWrapped keeps a failed hybrid query legible as
// one, since the two paths fail in different places.
func TestSearch_HybridQueryErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).WillReturnError(errors.New("boom"))

	_, err := s.Search(context.Background(), script.SearchQuery{
		Embedding: []float32{0.1}, QueryText: "sales",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "search scripts (hybrid)")
}

// TestSearch_HybridRowErrorIsSurfaced covers the iteration failure, which a
// fused-in-Go ranker has to check explicitly or it silently returns a partial
// ranking as a complete one.
func TestSearch_HybridRowErrorIsSurfaced(t *testing.T) {
	s, mock := newMock(t)
	spec := rowSpec{id: "script_1", name: "daily", paramsJSON: emptyParams(t)}
	mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).
		WillReturnRows(sqlmock.NewRows(hybridSelectColumns).
			AddRow(hybridRow(spec, 0.5, true)...).RowError(0, errors.New("boom")))

	_, err := s.Search(context.Background(), script.SearchQuery{
		Embedding: []float32{0.1}, QueryText: "sales",
	})

	require.Error(t, err)
}

// TestSearch_HybridScanErrorIsSurfaced covers a row the script scanner cannot
// read, which is a different failure from the iteration one above.
func TestSearch_HybridScanErrorIsSurfaced(t *testing.T) {
	s, mock := newMock(t)
	spec := rowSpec{id: "script_1", name: "daily", paramsJSON: []byte("not json")}
	mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).
		WillReturnRows(sqlmock.NewRows(hybridSelectColumns).AddRow(hybridRow(spec, 0.5, true)...))

	_, err := s.Search(context.Background(), script.SearchQuery{
		Embedding: []float32{0.1}, QueryText: "sales",
	})

	require.Error(t, err)
}

// TestSearch_HybridTruncatesToTheEffectiveLimit proves the fused set is trimmed
// after the union: each arm returns up to the limit, so their union can hold
// twice it.
func TestSearch_HybridTruncatesToTheEffectiveLimit(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows(hybridSelectColumns)
	for _, id := range []string{"script_1", "script_2", "script_3"} {
		rows.AddRow(hybridRow(rowSpec{
			id: id, name: id, paramsJSON: emptyParams(t),
		}, 0.5, true)...)
	}
	mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).WillReturnRows(rows)

	got, err := s.Search(context.Background(), script.SearchQuery{
		Embedding: []float32{0.1}, QueryText: "sales", Limit: 2,
	})

	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// TestSearch_HybridOrdersTiesDeterministically pins the last tie-break. The
// fused set is collected from a map, so two scripts with the same score and the
// same name — names are unique only within an owner — would otherwise come back
// in map iteration order, and a search that reorders itself between identical
// calls is a search an agent cannot cite.
func TestSearch_HybridOrdersTiesDeterministically(t *testing.T) {
	rows := func() *sqlmock.Rows {
		r := sqlmock.NewRows(hybridSelectColumns)
		for _, id := range []string{"script_b", "script_a"} {
			r.AddRow(hybridRow(rowSpec{
				id: id, name: "same-name", paramsJSON: emptyParams(t),
			}, 0.5, true)...)
		}
		return r
	}

	for range 5 {
		s, mock := newMock(t)
		mock.ExpectQuery(regexp.QuoteMeta("UNION ALL")).WillReturnRows(rows())

		got, err := s.Search(context.Background(), script.SearchQuery{
			Embedding: []float32{0.1}, QueryText: "sales",
		})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "script_a", got[0].Script.ID)
		assert.Equal(t, "script_b", got[1].Script.ID)
	}
}
