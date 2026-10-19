package whstore

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertReadings(t *testing.T) {
	st, mock := newMock(t)
	minute := start.Truncate(time.Minute)
	mock.ExpectQuery(`FROM webhook_request_counts\s+WHERE minute >= \$1 GROUP BY source, minute, outcome`).
		WithArgs(start.Add(-15 * time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"source", "minute", "outcome", "n"}).
			AddRow("esp", minute, "accepted", int64(9)).
			AddRow("esp", minute, "unauthorized", int64(1)).
			AddRow("esp", minute.Add(-time.Minute), "spool_full", int64(2)))
	mock.ExpectQuery(`FROM webhook_ack_latency l, unnest\(l.buckets\)`).
		WithArgs(start.Add(-5 * time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"source", "i", "n", "max"}).
			AddRow("esp", 1, int64(98), int64(1800)).
			AddRow("esp", 7, int64(2), int64(1800)).
			AddRow("esp", 99, int64(5), int64(1800)).
			AddRow("other", 3, int64(1), int64(40)))
	mock.ExpectQuery(`FROM webhook_windows GROUP BY source`).
		WillReturnRows(sqlmock.NewRows([]string{"source", "owed", "failing", "err", "last"}).
			AddRow("esp", start.Add(-time.Hour), 1, "boom", start).
			AddRow("quiet", nil, 0, "", nil))
	got, err := st.AlertReadings(ctx, start.Add(-15*time.Minute), start.Add(-5*time.Minute))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	esp := got["esp"]
	require.NotNil(t, esp)
	assert.Equal(t, map[string]int64{"accepted": 9, "unauthorized": 1}, esp.Outcomes[minute.UTC()])
	assert.Equal(t, int64(2), esp.Outcomes[minute.Add(-time.Minute).UTC()]["spool_full"])
	assert.Equal(t, int64(100), esp.Ack.Count)
	assert.Equal(t, int64(1000), esp.Ack.P99MS, "the p99 is the bound of the bucket it falls in")
	assert.Equal(t, int64(1800), esp.Ack.MaxMS)
	require.NotNil(t, esp.OldestOwedEnd)
	assert.Equal(t, 1, esp.FailingWindows)
	assert.Equal(t, "boom", esp.LastError)
	assert.NotNil(t, esp.LastSegmentAt)
	assert.Nil(t, got["quiet"].OldestOwedEnd)
	assert.Equal(t, int64(1), got["other"].Ack.Count)
}

func TestAlertReadingsFailures(t *testing.T) {
	counts := `FROM webhook_request_counts`
	ack := `FROM webhook_ack_latency`
	windows := `FROM webhook_windows GROUP BY source`
	empty := func(q string, cols ...string) func(sqlmock.Sqlmock) {
		return func(m sqlmock.Sqlmock) { m.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols)) }
	}
	okCounts := empty(counts, "source", "minute", "outcome", "n")
	okAck := empty(ack, "source", "i", "n", "max")
	for name, steps := range map[string][]func(sqlmock.Sqlmock){
		"counts": {func(m sqlmock.Sqlmock) { m.ExpectQuery(counts).WillReturnError(errDown) }},
		"counts scan": {func(m sqlmock.Sqlmock) {
			m.ExpectQuery(counts).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		}},
		"ack":      {okCounts, func(m sqlmock.Sqlmock) { m.ExpectQuery(ack).WillReturnError(errDown) }},
		"ack scan": {okCounts, func(m sqlmock.Sqlmock) { m.ExpectQuery(ack).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1)) }},
		"windows":  {okCounts, okAck, func(m sqlmock.Sqlmock) { m.ExpectQuery(windows).WillReturnError(errDown) }},
		"windows scan": {okCounts, okAck, func(m sqlmock.Sqlmock) {
			m.ExpectQuery(windows).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
		}},
	} {
		t.Run(name, func(t *testing.T) {
			st, mock := newMock(t)
			for _, step := range steps {
				step(mock)
			}
			_, err := st.AlertReadings(ctx, start, start)
			require.Error(t, err)
		})
	}
}
