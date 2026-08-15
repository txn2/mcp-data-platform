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
	mock.ExpectQuery(regexp.QuoteMeta("script_fts(display_name, name, description, tags, params)")).
		WithArgs("sales report", sqlmock.AnyArg(), sqlmock.AnyArg(), "jane@example.com", script.DefaultSearchLimit).
		WillReturnRows(sqlmock.NewRows(scoredSelectColumns).
			AddRow(scoredRow(rowSpec{
				id: "script_1", name: "daily-sales", scope: "global",
				owner: "jane@example.com", paramsJSON: emptyParams(t),
			}, 0.75)...))

	got, err := s.Search(context.Background(), script.SearchQuery{
		QueryText:  "sales report",
		OwnerEmail: "jane@example.com",
		Personas:   []string{"analyst"},
	})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "daily-sales", got[0].Script.Name)
	assert.InDelta(t, 0.75, got[0].Score, 0.0001)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSearch_DiscoverableStatusesExcludeDeadEnds pins the ranking rule: a
// deprecated script must not be executed and a superseded one names its
// replacement, so neither belongs in a discovery result. A draft does: it is a
// solved process waiting for a reviewer.
func TestSearch_DiscoverableStatusesExcludeDeadEnds(t *testing.T) {
	assert.Equal(t, []string{script.StatusDraft, script.StatusActive}, discoverableStatuses)
}

func TestSearch_QueryErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("script_fts").WillReturnError(errors.New("boom"))

	_, err := s.Search(context.Background(), script.SearchQuery{QueryText: "x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "search scripts")
}

// TestContract_ComposesTheWholeDocument proves one Contract call assembles the
// four reads a caller would otherwise have to make, and that the parameter
// contract reported is the APPROVED version's.
func TestContract_ComposesTheWholeDocument(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{
				id: "script_1", name: "daily-sales", scope: "global",
				owner: "jane@example.com", paramsJSON: emptyParams(t), approvedVersion: "sver_1",
			})...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_versions WHERE id = $1")).
		WithArgs("sver_1").
		WillReturnRows(sqlmock.NewRows(versionSelectColumns).
			AddRow(approvedVersionRow(t)...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules WHERE script_id = $1")).
		WillReturnRows(sqlmock.NewRows(scheduleSelectColumns).AddRow(scheduleRow(rowTime)...))
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY finished_at DESC NULLS LAST LIMIT 1")).
		WithArgs("script_1", script.RunStatusSucceeded).
		WillReturnRows(sqlmock.NewRows(runSelectColumns).
			AddRow(runRow(script.RunStatusSucceeded, 1, []byte(`[{"name":"sales","asset_id":"asset_7","asset_version":4}]`))...))

	got, err := s.Contract(context.Background(), "script_1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "daily-sales", got.Name)
	require.Len(t, got.Params, 1)
	assert.Equal(t, "report_date", got.Params[0].Name, "the approved version's contract, not the live row's")
	assert.True(t, got.Approval.Approved)
	require.NotNil(t, got.Schedule)
	require.NotNil(t, got.LastRun)
	require.Len(t, got.LastRun.Outputs, 1)
	assert.Equal(t, script.OutputKindAsset, got.LastRun.Outputs[0].Kind)
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
				id: "script_1", name: "daily-sales", scope: "global",
				owner: "jane@example.com", paramsJSON: emptyParams(t),
			})...))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_schedules")).
		WillReturnRows(sqlmock.NewRows(scheduleSelectColumns))
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_runs")).
		WillReturnRows(sqlmock.NewRows(runSelectColumns))

	got, err := s.Contract(context.Background(), "script_1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.Schedule)
	assert.Nil(t, got.LastRun)
	assert.False(t, got.Approval.Approved)
	assert.Contains(t, got.Approval.Refusal, "no approved version")
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
				id: "script_1", name: "daily-sales", scope: "global",
				owner: "jane@example.com", paramsJSON: emptyParams(t),
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

// approvedVersionRow returns a version row carrying an approval stamp and the
// report_date parameter contract.
func approvedVersionRow(t *testing.T) []driver.Value {
	t.Helper()
	row := versionRow(3, "print(1)", script.VersionStatusApplied,
		[]byte(`[{"name":"report_date","type":"date","required":true}]`))
	row[11] = "admin@example.com" // approved_by
	row[12] = rowTime             // approved_at
	row[13] = []byte(`{"connections":["warehouse"]}`)
	return row
}
