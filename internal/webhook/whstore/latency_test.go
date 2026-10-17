package whstore

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	qLatencyBuckets = `FROM webhook_ack_latency l, unnest`
	qSlowest        = `AS day_rank`
)

func latencyBucketRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"source", "i", "hour", "day"})
}

func slowestRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"source", "in_hour", "day_best", "max_ms", "slowest_at", "buffer", "mark", "put"})
}

func rejectionRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"source", "first_at", "rejected_at", "count", "outcome", "reason", "request_hash", "event_ids"})
}

func TestAckBucket(t *testing.T) {
	cases := map[int64]int{0: 0, 10: 0, 11: 1, 50: 2, 999: 6, 1000: 6, 30000: 10, 30001: 11}
	for ms, want := range cases {
		assert.Equal(t, want, AckBucket(ms), "%dms", ms)
	}
}

func TestSummarize(t *testing.T) {
	assert.Equal(t, AckLatency{}, summarize([ackBuckets]int64{}, nil), "no deliveries reports nothing")

	var b [ackBuckets]int64
	b[AckBucket(8)] = 98    // 98 deliveries within 10ms
	b[AckBucket(400)] = 1   // one within 500ms
	b[AckBucket(40000)] = 1 // one slower than every bound
	slow := &SlowAck{TotalMS: 41000}
	got := summarize(b, slow)
	assert.Equal(t, int64(100), got.Count)
	assert.Equal(t, int64(10), got.P50MS, "the median is in the first bucket")
	assert.Equal(t, int64(500), got.P99MS, "the 99th delivery is in the 500ms bucket")
	assert.Equal(t, int64(41000), got.MaxMS)
	assert.Same(t, slow, got.Slowest)

	var open [ackBuckets]int64
	open[ackBuckets-1] = 2
	assert.Equal(t, int64(45000), summarize(open, &SlowAck{TotalMS: 45000}).P50MS,
		"a percentile in the last bucket is the slowest delivery")

	var capped [ackBuckets]int64
	capped[AckBucket(900)] = 3
	assert.Equal(t, int64(900), summarize(capped, &SlowAck{TotalMS: 900}).P99MS,
		"a bound above the slowest delivery is capped at it")
	assert.Zero(t, percentile(capped, 0, 0.5, 7), "no deliveries is zero")
}

func TestLatencyObserveAndMerge(t *testing.T) {
	var l Latency
	l.Observe(SlowAck{TotalMS: 30, PutMS: 20})
	l.Observe(SlowAck{TotalMS: 5})
	assert.Equal(t, int64(30), l.Slowest.TotalMS)
	assert.Equal(t, int64(1), l.Buckets[AckBucket(30)])
	assert.Equal(t, int64(1), l.Buckets[AckBucket(5)])

	var o Latency
	o.Observe(SlowAck{TotalMS: 3000, MarkMS: 2900})
	l.Merge(o)
	assert.Equal(t, int64(3000), l.Slowest.TotalMS, "the slower of the two is kept")
	assert.Equal(t, int64(2900), l.Slowest.MarkMS)
	l.Merge(Latency{Slowest: SlowAck{TotalMS: 1}})
	assert.Equal(t, int64(3000), l.Slowest.TotalMS)
}

func TestRecordLatency(t *testing.T) {
	st, mock := newMock(t)
	require.NoError(t, st.RecordLatency(ctx, nil))

	var l Latency
	l.Source, l.Minute = "esp", start.Add(17*time.Second)
	l.Observe(SlowAck{At: start, TotalMS: 40, BufferMS: 1, MarkMS: 2, PutMS: 30})
	l.Observe(SlowAck{At: start, TotalMS: 10, BufferMS: 3, MarkMS: 1, PutMS: 5})
	assert.Equal(t, [3]int64{4, 3, 35}, [3]int64{l.BufferMS, l.MarkMS, l.PutMS}, "each stage is summed over the minute (#2150)")
	mock.ExpectExec(`INSERT INTO webhook_ack_latency`).
		WithArgs("esp", start, sqlmock.AnyArg(), int64(40), start, int64(1), int64(2), int64(30), int64(4), int64(3), int64(35)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordLatency(ctx, []Latency{l}))

	mock.ExpectExec(`INSERT INTO webhook_ack_latency`).WillReturnError(errDown)
	assert.Error(t, st.RecordLatency(ctx, []Latency{l}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReadLatency(t *testing.T) {
	st, mock := newUnorderedMock(t)
	hourAt, dayAt := start.Add(-10*time.Minute), start.Add(-5*time.Hour)
	mock.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows().
		AddRow("esp", 1, 3, 3).
		AddRow("esp", 8, 0, 1).
		AddRow("crm", 2, 0, 2).
		AddRow("odd", 99, 1, 1))
	mock.ExpectQuery(qSlowest).WillReturnRows(slowestRows().
		AddRow("esp", false, true, 2400, dayAt, 0, 2300, 100).
		AddRow("esp", true, false, 9, hourAt, 1, 2, 6).
		AddRow("crm", false, true, 20, dayAt, 0, 0, 20))
	got, err := st.readLatency(ctx, "", start)
	require.NoError(t, err)

	esp := got["esp"]
	assert.Equal(t, int64(3), esp.hour.Count)
	require.NotNil(t, esp.hour.Slowest)
	assert.Equal(t, hourAt, esp.hour.Slowest.At, "the hour's slowest is the slowest within it")
	assert.Equal(t, int64(4), esp.day.Count)
	assert.Equal(t, int64(2400), esp.day.MaxMS)
	assert.Equal(t, int64(2300), esp.day.Slowest.MarkMS, "the day's slowest carries its stages")

	crm := got["crm"]
	assert.Zero(t, crm.hour.Count, "a source with nothing in the hour reports nothing for it")
	assert.Nil(t, crm.hour.Slowest)
	assert.Equal(t, int64(2), crm.day.Count)
	assert.Zero(t, got["odd"].day.Count, "a bucket index past the last is ignored")
}

func TestReadLatencyFailures(t *testing.T) {
	cases := map[string]func(sqlmock.Sqlmock){
		"buckets query": func(m sqlmock.Sqlmock) { m.ExpectQuery(qLatencyBuckets).WillReturnError(errDown) },
		"buckets scan": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows().AddRow("esp", "x", 1, 1))
		},
		"buckets iterate": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows().AddRow("esp", 1, 1, 1).RowError(0, errDown))
		},
		"slowest query": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows())
			m.ExpectQuery(qSlowest).WillReturnError(errDown)
		},
		"slowest scan": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows())
			m.ExpectQuery(qSlowest).WillReturnRows(slowestRows().AddRow("esp", true, true, "x", start, 0, 0, 0))
		},
		"slowest iterate": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qLatencyBuckets).WillReturnRows(latencyBucketRows())
			m.ExpectQuery(qSlowest).WillReturnRows(slowestRows().AddRow("esp", true, true, 1, start, 0, 0, 0).RowError(0, errDown))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			st, mock := newMock(t)
			setup(mock)
			_, err := st.readLatency(ctx, "esp", start)
			assert.Error(t, err)
		})
	}
}

func TestPruneCountsPrunesLatency(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectExec(`DELETE FROM webhook_request_counts`).WithArgs(start).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_status_counts`).WithArgs(start).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_ack_latency`).WithArgs(start).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_replica_minutes`).WithArgs(start).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_compactions`).WithArgs(start).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.PruneCounts(ctx, start))

	mock.ExpectExec(`DELETE FROM webhook_request_counts`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_status_counts`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM webhook_ack_latency`).WillReturnError(errDown)
	assert.Error(t, st.PruneCounts(ctx, start))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIdentifiedRejectionsAreTheirOwnRows(t *testing.T) {
	at := func(sec int) time.Time { return start.Add(time.Duration(sec) * time.Second) }
	gone := func(sec int, id string) Rejection {
		return Rejection{
			Source: "esp", At: at(sec), Outcome: "sender_gone", Reason: "the sender disconnected",
			RequestHash: "h" + id, EventIDs: []string{id},
		}
	}
	plain := Rejection{Source: "esp", At: at(0), Outcome: "sender_gone", Reason: "the sender disconnected"}
	got := collapseRejections([]Rejection{plain, gone(1, "a"), gone(2, "b"), plain})
	require.Len(t, got, 4, "a rejection naming its events is never folded, and ends the run before it")
	assert.Equal(t, []string{"a"}, got[1].EventIDs)
	assert.Equal(t, at(1), got[1].FirstAt)
	assert.Equal(t, int64(1), got[1].Count)

	burst := make([]Rejection, 0, maxRejections+3)
	for i := range maxRejections + 3 {
		burst = append(burst, gone(i, string(rune('a'+i%26))))
	}
	kept := keepNewestIdentities(burst)
	assert.Empty(t, kept[0].EventIDs, "only the newest maxRejections keep their ids")
	assert.Empty(t, kept[2].RequestHash)
	assert.NotEmpty(t, kept[3].EventIDs)
	assert.NotEmpty(t, burst[0].EventIDs, "the batch handed in is not changed")
	assert.Len(t, collapseRejections(burst), maxRejections+1, "the stripped ones fold into one run")
}

func TestRecordIdentifiedRejection(t *testing.T) {
	st, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO webhook_rejections`).
		WithArgs("esp", start, start, int64(1), "sender_gone", "gone", "abc", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM webhook_rejections`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	require.NoError(t, st.RecordRejections(ctx, []Rejection{{
		Source: "esp", At: start, Outcome: "sender_gone", Reason: "gone", RequestHash: "abc", EventIDs: []string{"e1"},
	}}))
	require.NoError(t, mock.ExpectationsWereMet(), "a rejection naming its events is inserted without looking for a run")
}
