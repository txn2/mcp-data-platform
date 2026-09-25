package whstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real-database tests (store_realdb_integration_test.go) prove the SQL.
// These prove what each method does with what the database answers,
// including every way it can fail.

var (
	ctx     = context.Background()
	start   = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	errDown = errors.New("down")
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func windowRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"source", "window_start", "window_seconds", "generation", "resource_id", "location", "attempts"})
}

func TestMarkSegment(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectExec(`INSERT INTO webhook_windows`).WithArgs("esp", start, 300).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.MarkSegment(ctx, "esp", start, 5*time.Minute))
	mock.ExpectExec(`INSERT INTO webhook_windows`).WillReturnError(errDown)
	assert.Error(t, st.MarkSegment(ctx, "esp", start, 5*time.Minute))
}

func TestClaimOwed(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectQuery(`UPDATE webhook_windows h`).WillReturnRows(windowRows().AddRow("esp", start, 300, 3, "r", "l", 1))
	got, err := st.ClaimOwed(ctx, start, time.Minute, 5)
	require.NoError(t, err)
	assert.Equal(t, []Window{{Source: "esp", Start: start, Length: 5 * time.Minute, Generation: 3, ResourceID: "r", Location: "l", Attempts: 1}}, got)

	mock.ExpectQuery(`UPDATE webhook_windows h`).WillReturnError(errDown)
	_, err = st.ClaimOwed(ctx, start, time.Minute, 5)
	assert.Error(t, err)

	mock.ExpectQuery(`UPDATE webhook_windows h`).WillReturnRows(sqlmock.NewRows([]string{"source"}).AddRow("esp"))
	_, err = st.ClaimOwed(ctx, start, time.Minute, 5)
	assert.Error(t, err)

	mock.ExpectQuery(`UPDATE webhook_windows h`).WillReturnRows(windowRows().AddRow("esp", start, 300, 3, "r", "l", 1).RowError(0, errDown))
	_, err = st.ClaimOwed(ctx, start, time.Minute, 5)
	assert.Error(t, err)
}

func TestRecordWrites(t *testing.T) {
	st, mock := newMock(t)
	h := Window{Source: "esp", Start: start, Length: time.Hour, Generation: 2}

	mock.ExpectExec(`UPDATE webhook_windows\s+SET compacted_generation`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordCompacted(ctx, h, Compaction{}))
	mock.ExpectExec(`UPDATE webhook_windows\s+SET compacted_generation`).WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, st.RecordCompacted(ctx, h, Compaction{}), ErrNotFound)
	mock.ExpectExec(`UPDATE webhook_windows\s+SET compacted_generation`).WillReturnError(errDown)
	assert.Error(t, st.RecordCompacted(ctx, h, Compaction{}))

	mock.ExpectExec(`SET resource_id`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordLocation(ctx, h, "r", "l"))
	mock.ExpectExec(`SET resource_id`).WillReturnError(errDown)
	assert.Error(t, st.RecordLocation(ctx, h, "r", "l"))

	mock.ExpectExec(`SET last_error`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordFailure(ctx, h, "x", time.Minute))
	mock.ExpectExec(`SET last_error`).WillReturnError(errDown)
	assert.Error(t, st.RecordFailure(ctx, h, "x", time.Minute))

	mock.ExpectExec(`SET raw_deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.MarkRawDeleted(ctx, h))
	mock.ExpectExec(`SET unregistered_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.MarkUnregistered(ctx, h))
	mock.ExpectExec(`SET expired_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.MarkExpired(ctx, h))
	mock.ExpectExec(`SET expired_at`).WillReturnError(errDown)
	assert.Error(t, st.MarkExpired(ctx, h))
	mock.ExpectExec(`SET expired_at`).WillReturnResult(sqlmock.NewErrorResult(errDown))
	assert.Error(t, st.MarkExpired(ctx, h))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRetentionReads(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(windowRows())
	got, err := st.RawDeletable(ctx, "esp", start)
	require.NoError(t, err)
	assert.NotNil(t, got)

	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(windowRows().AddRow("esp", start, 3600, 1, "", "", 0))
	got, err = st.Expirable(ctx, "esp", start)
	require.NoError(t, err)
	assert.Len(t, got, 1)

	mock.ExpectQuery(`FROM webhook_windows`).WillReturnError(errDown)
	_, err = st.Expirable(ctx, "esp", start)
	assert.Error(t, err)
	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	_, err = st.Expirable(ctx, "esp", start)
	assert.Error(t, err)
	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(windowRows().AddRow("esp", start, 3600, 1, "", "", 0).RowError(0, errDown))
	_, err = st.Expirable(ctx, "esp", start)
	assert.Error(t, err)
}

func TestStats(t *testing.T) {
	st, mock := newMock(t)
	require.NoError(t, st.RecordCounts(ctx, nil))
	mock.ExpectExec(`INSERT INTO webhook_request_counts`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordCounts(ctx, []Count{{Source: "esp", Minute: start, Outcome: "accepted", Count: 1}}))
	mock.ExpectExec(`INSERT INTO webhook_request_counts`).WillReturnError(errDown)
	assert.Error(t, st.RecordCounts(ctx, []Count{{Source: "esp"}}))

	rej := []Rejection{{Source: "esp", At: start, Outcome: "unauthorized"}}
	require.NoError(t, st.RecordRejections(ctx, nil))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO webhook_rejections`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM webhook_rejections`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	require.NoError(t, st.RecordRejections(ctx, rej))

	mock.ExpectBegin().WillReturnError(errDown)
	assert.Error(t, st.RecordRejections(ctx, rej))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO webhook_rejections`).WillReturnError(errDown)
	mock.ExpectRollback()
	assert.Error(t, st.RecordRejections(ctx, rej))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO webhook_rejections`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM webhook_rejections`).WillReturnError(errDown)
	mock.ExpectRollback()
	assert.Error(t, st.RecordRejections(ctx, rej))
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO webhook_rejections`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM webhook_rejections`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(errDown)
	assert.Error(t, st.RecordRejections(ctx, rej))

	mock.ExpectExec(`DELETE FROM webhook_request_counts`).WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, st.PruneCounts(ctx, start))
	mock.ExpectExec(`DELETE FROM webhook_request_counts`).WillReturnError(errDown)
	assert.Error(t, st.PruneCounts(ctx, start))
	require.NoError(t, mock.ExpectationsWereMet())
}

func countRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"outcome", "hour", "day"})
}

func summaryRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"last_segment", "last_compacted", "pending", "failing", "oldest", "last_error"})
}

func TestStatus(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectQuery(`FROM webhook_request_counts`).WillReturnRows(countRows().AddRow("accepted", 2, 5).AddRow("unauthorized", 0, 1))
	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(summaryRows().AddRow(start, start, 1, 0, start, nil))
	mock.ExpectQuery(`FROM webhook_rejections`).WillReturnRows(
		sqlmock.NewRows([]string{"at", "outcome", "reason"}).AddRow(start, "unauthorized", "the token does not match"))
	s, err := st.Status(ctx, "esp", start)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"accepted": 2}, s.LastHour)
	assert.Equal(t, map[string]int64{"accepted": 5, "unauthorized": 1}, s.LastDay)
	require.NotNil(t, s.LastSegmentAt)
	assert.Equal(t, 1, s.Pending)
	require.Len(t, s.Rejections, 1)
	assert.Equal(t, "the token does not match", s.Rejections[0].Reason)
}

func TestStatusFailures(t *testing.T) {
	steps := []func(sqlmock.Sqlmock){
		func(m sqlmock.Sqlmock) { m.ExpectQuery(`webhook_request_counts`).WillReturnError(errDown) },
		func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`webhook_request_counts`).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`webhook_request_counts`).WillReturnRows(countRows().AddRow("a", 1, 1).RowError(0, errDown))
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`webhook_request_counts`).WillReturnRows(countRows())
			m.ExpectQuery(`FROM webhook_windows`).WillReturnError(errDown)
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`webhook_request_counts`).WillReturnRows(countRows())
			m.ExpectQuery(`FROM webhook_windows`).WillReturnRows(summaryRows().AddRow(nil, nil, 0, 0, nil, nil))
			m.ExpectQuery(`FROM webhook_rejections`).WillReturnError(errDown)
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`webhook_request_counts`).WillReturnRows(countRows())
			m.ExpectQuery(`FROM webhook_windows`).WillReturnRows(summaryRows().AddRow(nil, nil, 0, 0, nil, nil))
			m.ExpectQuery(`FROM webhook_rejections`).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectQuery(`webhook_request_counts`).WillReturnRows(countRows())
			m.ExpectQuery(`FROM webhook_windows`).WillReturnRows(summaryRows().AddRow(nil, nil, 0, 0, nil, nil))
			m.ExpectQuery(`FROM webhook_rejections`).WillReturnRows(
				sqlmock.NewRows([]string{"at", "outcome", "reason"}).AddRow(start, "a", "b").RowError(0, errDown))
		},
	}
	for i, step := range steps {
		st, mock := newMock(t)
		step(mock)
		_, err := st.Status(ctx, "esp", start)
		assert.Error(t, err, "step %d", i)
	}
}

func TestSourcesForResources(t *testing.T) {
	st, mock := newMock(t)
	got, err := st.SourcesForResources(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(sqlmock.NewRows([]string{"resource_id", "source"}).AddRow("r", "esp"))
	got, err = st.SourcesForResources(ctx, []string{"r"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"r": "esp"}, got)

	mock.ExpectQuery(`FROM webhook_windows`).WillReturnError(errDown)
	_, err = st.SourcesForResources(ctx, []string{"r"})
	assert.Error(t, err)
	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
	_, err = st.SourcesForResources(ctx, []string{"r"})
	assert.Error(t, err)
	mock.ExpectQuery(`FROM webhook_windows`).WillReturnRows(
		sqlmock.NewRows([]string{"resource_id", "source"}).AddRow("r", "esp").RowError(0, errDown))
	_, err = st.SourcesForResources(ctx, []string{"r"})
	assert.Error(t, err)
}

func TestResourceIDs(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectQuery(`SELECT resource_id FROM webhook_windows`).WithArgs("esp").
		WillReturnRows(sqlmock.NewRows([]string{"resource_id"}).AddRow("a").AddRow("b"))
	got, err := st.ResourceIDs(ctx, "esp")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, got)

	mock.ExpectQuery(`SELECT resource_id`).WillReturnError(errDown)
	_, err = st.ResourceIDs(ctx, "esp")
	assert.Error(t, err)
	mock.ExpectQuery(`SELECT resource_id`).WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow(1, 2))
	_, err = st.ResourceIDs(ctx, "esp")
	assert.Error(t, err)
	mock.ExpectQuery(`SELECT resource_id`).WillReturnRows(sqlmock.NewRows([]string{"resource_id"}).AddRow("a").RowError(0, errDown))
	_, err = st.ResourceIDs(ctx, "esp")
	assert.Error(t, err)
}
