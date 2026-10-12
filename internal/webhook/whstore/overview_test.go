package whstore

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The five reads of Overview run concurrently, so the mocks here match them
// in any order.

const (
	qCounts     = `SELECT source, outcome`
	qWindows    = `FROM webhook_windows\s+WHERE expired_at IS NULL AND`
	qVolume     = `AS bucket`
	qRejections = `SELECT source, first_at, rejected_at, count, outcome, reason, request_hash, event_ids FROM \(`
)

func newUnorderedMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	st, mock := newMock(t)
	mock.MatchExpectationsInOrder(false)
	return st, mock
}

func countCols() []string { return []string{"source", "outcome", "hour", "day"} }
func windowCols() []string {
	return []string{"source", "last_segment_at", "pending", "failing", "last_error"}
}
func volumeCols() []string { return []string{"source", "bucket", "outcome", "count"} }
func rejectionCols() []string {
	return []string{"source", "first_at", "rejected_at", "count", "outcome", "reason", "request_hash", "event_ids"}
}

func TestOverview(t *testing.T) {
	st, mock := newUnorderedMock(t)
	seg := start.Add(-time.Minute)
	mock.ExpectQuery(qCounts).WithArgs(start.Add(-time.Hour), start.Add(-24*time.Hour), "").WillReturnRows(
		sqlmock.NewRows(countCols()).
			AddRow("esp", "accepted", 5, 9).
			AddRow("esp", "unauthorized", 0, 2).
			AddRow("crm", "accepted", 0, 1))
	mock.ExpectQuery(qWindows).WillReturnRows(
		sqlmock.NewRows(windowCols()).
			AddRow("esp", seg, 1, 1, "segment k: not gzip").
			AddRow("quiet", nil, 0, 0, ""))
	mock.ExpectQuery(qVolume).WithArgs(start.Add(-time.Hour), 60, "").WillReturnRows(
		sqlmock.NewRows(volumeCols()).AddRow("esp", start.Add(-time.Minute), "accepted", 5))
	mock.ExpectQuery(qRejections).WithArgs(maxOverviewRejections, start.Add(-time.Hour), "").WillReturnRows(
		sqlmock.NewRows(rejectionCols()).AddRow("esp", start.Add(-time.Second), start, 3, "unauthorized", "the signature does not match", "", "{}"))
	mock.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows().AddRow("esp", 1, 2, 2).AddRow("fresh", 1, 1, 1))
	mock.ExpectQuery(qSlowest).WillReturnRows(slowestRows().
		AddRow("esp", true, true, 8, start, 1, 2, 5).AddRow("fresh", true, true, 4, start, 0, 1, 3))

	ov, err := st.Overview(ctx, "", start, time.Hour, time.Minute)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, start.Add(-time.Hour), ov.Since)

	esp := ov.Summaries["esp"]
	assert.Equal(t, map[string]int64{"accepted": 5}, esp.LastHour, "an outcome with none in the hour is left out of it")
	assert.Equal(t, map[string]int64{"accepted": 9, "unauthorized": 2}, esp.LastDay)
	require.NotNil(t, esp.LastSegmentAt)
	assert.Equal(t, seg, *esp.LastSegmentAt)
	assert.Equal(t, 1, esp.Pending)
	assert.Equal(t, 1, esp.Failing)
	assert.Equal(t, "segment k: not gzip", esp.LastError)

	crm := ov.Summaries["crm"]
	assert.Nil(t, crm.LastSegmentAt, "a source with counts and no window has no last event")

	quiet := ov.Summaries["quiet"]
	assert.Equal(t, map[string]int64{}, quiet.LastHour, "a source with windows and no counts has empty counts")
	assert.Equal(t, map[string]int64{}, quiet.LastDay)
	assert.Nil(t, quiet.LastSegmentAt)

	assert.Equal(t, int64(2), esp.AckLastHour.Count)
	assert.Equal(t, int64(8), esp.AckLastDay.MaxMS)
	fresh := ov.Summaries["fresh"]
	assert.Equal(t, int64(1), fresh.AckLastDay.Count, "a source with latency alone still has a summary")
	assert.Equal(t, map[string]int64{}, fresh.LastHour)

	assert.Equal(t, []VolumePoint{{Source: "esp", At: start.Add(-time.Minute), Outcome: "accepted", Count: 5}}, ov.Volume)
	assert.Equal(t, []Rejection{{
		Source: "esp", FirstAt: start.Add(-time.Second), At: start, Count: 3,
		Outcome: "unauthorized", Reason: "the signature does not match", EventIDs: []string{},
	}}, ov.Rejections)
}

// TestOverviewOfOneSource passes the source and the range to every read, so
// the rejections follow both (#2107).
func TestOverviewOfOneSource(t *testing.T) {
	st, mock := newUnorderedMock(t)
	since := start.Add(-24 * time.Hour)
	mock.ExpectQuery(qCounts).WithArgs(start.Add(-time.Hour), since, "esp").WillReturnRows(sqlmock.NewRows(countCols()))
	mock.ExpectQuery(qWindows).WithArgs("esp").WillReturnRows(sqlmock.NewRows(windowCols()))
	mock.ExpectQuery(qVolume).WithArgs(since, 900, "esp").WillReturnRows(sqlmock.NewRows(volumeCols()))
	mock.ExpectQuery(qRejections).WithArgs(maxOverviewRejections, since, "esp").WillReturnRows(sqlmock.NewRows(rejectionCols()))
	mock.ExpectQuery(qLatencyBuckets).WithArgs(start.Add(-time.Hour), since, "esp").WillReturnRows(latencyBucketRows())
	mock.ExpectQuery(qSlowest).WithArgs(start.Add(-time.Hour), since, "esp").WillReturnRows(slowestRows())
	_, err := st.Overview(ctx, "esp", start, 24*time.Hour, 15*time.Minute)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOverviewEmpty(t *testing.T) {
	st, mock := newUnorderedMock(t)
	mock.ExpectQuery(qCounts).WillReturnRows(sqlmock.NewRows(countCols()))
	mock.ExpectQuery(qWindows).WillReturnRows(sqlmock.NewRows(windowCols()))
	mock.ExpectQuery(qVolume).WithArgs(start.Add(-24*time.Hour), 900, "").WillReturnRows(sqlmock.NewRows(volumeCols()))
	mock.ExpectQuery(qRejections).WillReturnRows(sqlmock.NewRows(rejectionCols()))
	mock.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows())
	mock.ExpectQuery(qSlowest).WillReturnRows(slowestRows())

	ov, err := st.Overview(ctx, "", start, 24*time.Hour, 15*time.Minute)
	require.NoError(t, err)
	assert.Empty(t, ov.Summaries)
	assert.NotNil(t, ov.Volume, "no requests is an empty list, never nil")
	assert.Empty(t, ov.Volume)
	assert.NotNil(t, ov.Rejections, "no rejections is an empty list, never nil")
	assert.Empty(t, ov.Rejections)
}

// TestOverviewFailures fails each read in turn, three ways: the query, a
// row that does not scan, and an error while iterating.
func TestOverviewFailures(t *testing.T) {
	type read struct {
		query string
		rows  func() *sqlmock.Rows
		good  func() *sqlmock.Rows
		bad   func() *sqlmock.Rows
	}
	reads := map[string]read{
		"counts": {
			query: qCounts,
			good:  func() *sqlmock.Rows { return sqlmock.NewRows(countCols()) },
			bad:   func() *sqlmock.Rows { return sqlmock.NewRows(countCols()).AddRow("esp", "accepted", "x", 1) },
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows(countCols()).AddRow("esp", "accepted", 1, 1).RowError(0, errDown)
			},
		},
		"windows": {
			query: qWindows,
			good:  func() *sqlmock.Rows { return sqlmock.NewRows(windowCols()) },
			bad:   func() *sqlmock.Rows { return sqlmock.NewRows(windowCols()).AddRow("esp", nil, "x", 0, "") },
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows(windowCols()).AddRow("esp", nil, 0, 0, "").RowError(0, errDown)
			},
		},
		"volume": {
			query: qVolume,
			good:  func() *sqlmock.Rows { return sqlmock.NewRows(volumeCols()) },
			bad:   func() *sqlmock.Rows { return sqlmock.NewRows(volumeCols()).AddRow("esp", "x", "accepted", 1) },
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows(volumeCols()).AddRow("esp", start, "accepted", 1).RowError(0, errDown)
			},
		},
		"rejections": {
			query: qRejections,
			good:  func() *sqlmock.Rows { return sqlmock.NewRows(rejectionCols()) },
			bad: func() *sqlmock.Rows {
				return sqlmock.NewRows(rejectionCols()).AddRow("esp", "x", "x", 1, "u", "r", "", "{}")
			},
			rows: func() *sqlmock.Rows {
				return sqlmock.NewRows(rejectionCols()).AddRow("esp", start, start, 1, "u", "r", "", "{}").RowError(0, errDown)
			},
		},
	}
	for failing, f := range reads {
		for _, mode := range []string{"query", "scan", "iterate"} {
			t.Run(failing+"/"+mode, func(t *testing.T) {
				st, mock := newUnorderedMock(t)
				mock.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows())
				mock.ExpectQuery(qSlowest).WillReturnRows(slowestRows())
				for name, r := range reads {
					e := mock.ExpectQuery(r.query)
					switch {
					case name != failing:
						e.WillReturnRows(r.good())
					case mode == "query":
						e.WillReturnError(errDown)
					case mode == "scan":
						e.WillReturnRows(f.bad())
					default:
						e.WillReturnRows(f.rows())
					}
				}
				_, err := st.Overview(ctx, "", start, time.Hour, time.Minute)
				assert.Error(t, err)
			})
		}
	}
}
