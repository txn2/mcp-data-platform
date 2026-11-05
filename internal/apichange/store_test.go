package apichange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

var errDown = errors.New("db down")

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

var watchCols = []string{
	"catalog_id", "spec_name", "refresh_interval_seconds", "hold_changes", "upstream_url",
	"upstream_etag", "upstream_content", "next_check_at", "last_checked_at", "last_error", "updated_by", "updated_at",
}

func TestStore_Watch(t *testing.T) {
	db, mock := mockDB(t)
	s := Store{DB: db}
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	mock.ExpectQuery("FROM api_catalog_spec_watch").WithArgs("acme", "pets").WillReturnRows(sqlmock.NewRows(watchCols))
	w, err := s.GetWatch(t.Context(), key)
	require.NoError(t, err)
	assert.Equal(t, Watch{SpecKey: key}, w, "a spec never configured is the zero watch")

	mock.ExpectQuery("FROM api_catalog_spec_watch").WillReturnRows(sqlmock.NewRows(watchCols).
		AddRow("acme", "pets", 604800, true, "", "", "", now, now, "boom", "a@example.com", now))
	w, err = s.GetWatch(t.Context(), key)
	require.NoError(t, err)
	assert.Equal(t, 7*24*time.Hour, w.RefreshInterval)
	assert.True(t, w.HoldChanges)
	assert.Equal(t, now, *w.NextCheckAt)
	assert.Equal(t, "boom", w.LastError)

	mock.ExpectQuery("FROM api_catalog_spec_watch").WillReturnError(errDown)
	_, err = s.GetWatch(t.Context(), key)
	require.ErrorIs(t, err, errDown)

	mock.ExpectExec("INSERT INTO api_catalog_spec_watch").
		WithArgs("acme", "pets", 3600, false, "https://x.example/s.yaml", "a@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SaveSettings(t.Context(), Watch{SpecKey: key, RefreshInterval: time.Hour, UpstreamURL: "https://x.example/s.yaml"}, "a@example.com"))
	mock.ExpectExec("INSERT INTO api_catalog_spec_watch").WillReturnError(errDown)
	require.ErrorIs(t, s.SaveSettings(t.Context(), Watch{SpecKey: key}, ""), errDown)

	mock.ExpectExec("INSERT INTO api_catalog_spec_watch").
		WithArgs("acme", "pets", "failed", "", "", false).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordCheck(t.Context(), key, CheckRecord{Err: "failed"}))
	mock.ExpectExec("INSERT INTO api_catalog_spec_watch").WillReturnError(errDown)
	require.ErrorIs(t, s.RecordCheck(t.Context(), key, CheckRecord{}), errDown)

	mock.ExpectQuery("FROM api_catalog_spec_watch").WithArgs(20).
		WillReturnRows(sqlmock.NewRows([]string{"catalog_id", "spec_name"}).AddRow("acme", "pets"))
	due, err := s.Due(t.Context(), 20)
	require.NoError(t, err)
	assert.Equal(t, []SpecKey{key}, due)
	mock.ExpectQuery("FROM api_catalog_spec_watch").WillReturnError(errDown)
	_, err = s.Due(t.Context(), 20)
	require.ErrorIs(t, err, errDown)
	mock.ExpectQuery("FROM api_catalog_spec_watch").
		WillReturnRows(sqlmock.NewRows([]string{"catalog_id", "spec_name"}).AddRow("a", "b").RowError(0, errDown))
	_, err = s.Due(t.Context(), 20)
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

var changeCols = []string{
	"id", "catalog_id", "spec_name", "status", "trigger_kind", "report",
	"pending_content", "pending_etag", "created_at", "decided_by", "decided_at",
}

func reportJSON(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(Report{Added: []Op{{ID: "createPet"}}, Removed: []Op{}, Changed: []Changed{}, Deprecated: []Op{}})
	require.NoError(t, err)
	return b
}

func TestStore_Changes(t *testing.T) {
	db, mock := mockDB(t)
	s := Store{DB: db}
	now := time.Now().UTC()

	// A pending change retires the waiting one in the same transaction.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(supersedeSQL)).WithArgs("acme", "pets").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO api_catalog_spec_changes").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.AddChange(t.Context(), Change{ID: "c1", CatalogID: "acme", SpecName: "pets", Status: StatusPending, Trigger: TriggerSchedule}))

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO api_catalog_spec_changes").WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorIs(t, s.AddChange(t.Context(), Change{ID: "c2", Status: StatusApplied}), errDown)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(supersedeSQL)).WillReturnError(errDown)
	mock.ExpectRollback()
	require.ErrorIs(t, s.AddChange(t.Context(), Change{ID: "c3", Status: StatusPending}), errDown)

	mock.ExpectBegin().WillReturnError(errDown)
	require.ErrorIs(t, s.AddChange(t.Context(), Change{ID: "c4"}), errDown)

	mock.ExpectQuery("FROM api_catalog_spec_changes").WithArgs("acme", "pets", 50).
		WillReturnRows(sqlmock.NewRows(changeCols).
			AddRow("c1", "acme", "pets", "applied", "manual", reportJSON(t), "", "", now, "a@example.com", now))
	list, err := s.ListChanges(t.Context(), key, 50)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "1 added, 0 removed, 0 changed, 0 deprecated", list[0].Summary)
	assert.NotNil(t, list[0].DecidedAt)

	mock.ExpectQuery("FROM api_catalog_spec_changes").WillReturnRows(sqlmock.NewRows(changeCols).
		AddRow("c1", "acme", "pets", "applied", "manual", []byte("{"), "", "", now, "", nil))
	_, err = s.ListChanges(t.Context(), key, 50)
	require.Error(t, err, "a report that does not decode")
	mock.ExpectQuery("FROM api_catalog_spec_changes").WillReturnError(errDown)
	_, err = s.ListChanges(t.Context(), key, 50)
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery("FROM api_catalog_spec_changes").WithArgs("c1", "acme", "pets").
		WillReturnRows(sqlmock.NewRows(changeCols).
			AddRow("c1", "acme", "pets", "pending", "schedule", reportJSON(t), "doc", `"v2"`, now, "", nil))
	c, err := s.GetChange(t.Context(), key, "c1")
	require.NoError(t, err)
	assert.Equal(t, "doc", c.PendingContent)
	mock.ExpectQuery("FROM api_catalog_spec_changes").WillReturnRows(sqlmock.NewRows(changeCols))
	_, err = s.GetChange(t.Context(), key, "nope")
	require.ErrorIs(t, err, ErrNoChange)
	mock.ExpectQuery("FROM api_catalog_spec_changes").WillReturnError(errDown)
	_, err = s.GetChange(t.Context(), key, "c1")
	require.ErrorIs(t, err, errDown)

	mock.ExpectExec(regexp.QuoteMeta(decideSQL)).WithArgs("c1", "acme", "pets", StatusApplied, "a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Decide(t.Context(), key, "c1", StatusApplied, "a"))
	mock.ExpectExec(regexp.QuoteMeta(decideSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Decide(t.Context(), key, "c1", StatusApplied, "a"), ErrNotPending)
	mock.ExpectExec(regexp.QuoteMeta(decideSQL)).WillReturnError(errDown)
	require.ErrorIs(t, s.Decide(t.Context(), key, "c1", StatusApplied, "a"), errDown)

	mock.ExpectQuery("status = 'pending'").WithArgs("acme").
		WillReturnRows(sqlmock.NewRows([]string{"spec_name", "id"}).AddRow("pets", "c1"))
	pending, err := s.PendingFor(t.Context(), "acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"pets": "c1"}, pending)
	mock.ExpectQuery("status = 'pending'").WillReturnError(errDown)
	_, err = s.PendingFor(t.Context(), "acme")
	require.ErrorIs(t, err, errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoop_ChecksTheDueSpecsUnderTheLock(t *testing.T) {
	db, mock := mockDB(t)
	g := newRig(t)
	due := &dueStore{fakeChanges: g.store, due: []SpecKey{key}}
	g.r.Store = due
	l := &Loop{Refresher: g.r, DB: db}

	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectExec("DELETE FROM api_operation_callers").WithArgs(callerRetentionDays).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, l.RunOnce(t.Context()))
	assert.Len(t, g.written, 1, "the due spec was checked and its change applied")

	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(false))
	require.ErrorIs(t, l.RunOnce(t.Context()), bgloop.ErrSkipped, "another replica holds the pass")

	due.dueErr = errDown
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, l.RunOnce(t.Context()), errDown)
}

func TestLoop_StartStop(t *testing.T) {
	db, mock := mockDB(t)
	mock.MatchExpectationsInOrder(false)
	for range 3 {
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(false))
	}
	g := newRig(t)
	l := &Loop{Refresher: g.r, DB: db, poll: time.Hour}
	l.Start(context.Background())
	l.Start(context.Background()) // a second start is a no-op
	l.Stop()
	l.Stop()
	var none *Loop
	none.Start(context.Background())
	none.Stop()
}

// dueStore answers Due with a fixed list.
type dueStore struct {
	*fakeChanges
	due    []SpecKey
	dueErr error
}

func (d *dueStore) Due(context.Context, int) ([]SpecKey, error) { return d.due, d.dueErr }
