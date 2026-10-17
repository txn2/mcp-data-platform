package whstore

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordReplicaMinutes(t *testing.T) {
	st, mock := newMock(t)
	require.NoError(t, st.RecordReplicaMinutes(ctx, nil))
	m := ReplicaMinute{
		Replica: "a", Minute: start.Add(20 * time.Second), ReceivedEvents: 5, DrainedEvents: 4, DrainedBytes: 400,
		SpoolBytes: 10, SpoolEvents: 1, OldestAgeSeconds: 3,
	}
	mock.ExpectExec(`INSERT INTO webhook_replica_minutes`).
		WithArgs("a", start.Truncate(time.Minute), int64(5), int64(4), int64(400), int64(10), int64(1), int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordReplicaMinutes(ctx, []ReplicaMinute{m}))
	mock.ExpectExec(`INSERT INTO webhook_replica_minutes`).WillReturnError(errDown)
	require.ErrorIs(t, st.RecordReplicaMinutes(ctx, []ReplicaMinute{m}), errDown)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordCompactionRun(t *testing.T) {
	st, mock := newMock(t)
	run := CompactionRun{
		Source: "esp", WindowStart: start, CompactedAt: start.Add(time.Hour), DurationMS: 900, LagSeconds: 140,
		Segments: 3, Events: 10, InputBytes: 100, DecodedBytes: 2000, OutputBytes: 300,
	}
	mock.ExpectExec(`INSERT INTO webhook_compactions`).
		WithArgs("esp", start, start.Add(time.Hour), int64(900), int64(140), 3, int64(10), int64(100), int64(2000), int64(300)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordCompactionRun(ctx, run))
	mock.ExpectExec(`INSERT INTO webhook_compactions`).WillReturnError(errDown)
	require.ErrorIs(t, st.RecordCompactionRun(ctx, run), errDown)
}

// TestRollupTelemetry folds each table at the hour before the cutoff, in one
// statement each, and names the table a failure was in.
func TestRollupTelemetry(t *testing.T) {
	st, mock := newMock(t)
	cutoff := start.Add(-48*time.Hour + 17*time.Minute)
	hour := cutoff.Truncate(time.Hour)
	for _, table := range []string{"webhook_request_counts", "webhook_status_counts", "webhook_replica_minutes", "webhook_ack_latency"} {
		mock.ExpectExec(`DELETE FROM ` + table + `\s+WHERE minute < \$1 AND minute <> date_bin`).
			WithArgs(hour).WillReturnResult(sqlmock.NewResult(0, 3))
	}
	require.NoError(t, st.RollupTelemetry(ctx, cutoff))
	mock.ExpectExec(`DELETE FROM webhook_request_counts`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM webhook_status_counts`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM webhook_replica_minutes`).WillReturnError(errDown)
	err := st.RollupTelemetry(ctx, cutoff)
	require.ErrorIs(t, err, errDown)
	assert.Contains(t, err.Error(), "receiver minutes")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTelemetry(t *testing.T) {
	st, mock := newMock(t)
	until := start.Add(time.Hour)
	mock.ExpectQuery(`FROM webhook_status_counts`).WithArgs(start, 60, "esp", until).
		WillReturnRows(sqlmock.NewRows([]string{"at", "source", "status", "count"}).AddRow(start, "esp", 202, int64(9)).AddRow(start, "esp", 0, int64(1)))
	mock.ExpectQuery(`FROM webhook_replica_minutes`).WithArgs(start, 60, until).
		WillReturnRows(sqlmock.NewRows([]string{"at", "replica", "r", "d", "b", "sb", "se", "a"}).AddRow(start, "a", 9, 4, 400, 10, 1, 30))
	buckets := make(pq.Int64Array, ackBuckets)
	buckets[0], buckets[6] = 98, 2
	mock.ExpectQuery(`WITH binned AS`).WithArgs(start, 60, "esp", until).
		WillReturnRows(sqlmock.NewRows([]string{"at", "source", "max", "bs", "ms", "ps", "buckets"}).
			AddRow(start, "esp", int64(1800), int64(200), int64(100), int64(5000), buckets).
			AddRow(start.Add(time.Minute), "esp", int64(0), int64(0), int64(0), int64(0), make(pq.Int64Array, ackBuckets)))
	mock.ExpectQuery(`FROM webhook_compactions`).WithArgs(start, "esp", maxCompactionRuns, until).
		WillReturnRows(sqlmock.NewRows([]string{"s", "w", "c", "d", "l", "sg", "e", "i", "dc", "o"}).
			AddRow("esp", start, start.Add(3*time.Minute), int64(800), int64(180), 4, int64(100), int64(9000), int64(64000), int64(5000)))
	mock.ExpectQuery(`COALESCE\(SUM\(raw_bytes\)`).WithArgs("esp").
		WillReturnRows(sqlmock.NewRows([]string{"s", "rb", "rs", "cb", "cw"}).AddRow("esp", int64(9000), int64(40), int64(5000), int64(3)))

	got, err := st.Telemetry(ctx, "esp", start, until, 30*time.Second)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []StatusPoint{{At: start, Source: "esp", Status: 202, Count: 9}, {At: start, Source: "esp", Status: 0, Count: 1}}, got.Statuses)
	assert.Equal(t, int64(9), got.Replicas[0].ReceivedEvents)
	assert.Equal(t, int64(30), got.Replicas[0].OldestAgeSeconds)
	require.Len(t, got.Ack, 2)
	ack := got.Ack[0]
	assert.Equal(t, int64(100), ack.Count)
	assert.Equal(t, int64(10), ack.P50MS)
	assert.Equal(t, int64(1000), ack.P99MS)
	assert.InDelta(t, 2.0, ack.BufferMS, 0.001, "the stage sum over the deliveries is their average")
	assert.InDelta(t, 50.0, ack.PutMS, 0.001)
	assert.Zero(t, got.Ack[1].BufferMS, "a bucket with no deliveries has no average")
	assert.Equal(t, int64(64000), got.Compactions[0].DecodedBytes)
	assert.Equal(t, []SourceStorage{{Source: "esp", RawBytes: 9000, RawSegments: 40, CompactedBytes: 5000, CompactedWindows: 3}}, got.Storage)
}

func TestTelemetryFailures(t *testing.T) {
	reads := []struct{ query, cols string }{
		{`FROM webhook_status_counts`, "at,source,status,count"},
		{`FROM webhook_replica_minutes`, "at,replica,r,d,b,sb,se,a"},
		{`WITH binned AS`, "at,source,max,bs,ms,ps,buckets"},
		{`FROM webhook_compactions`, "s,w,c,d,l,sg,e,i,dc,o"},
		{`COALESCE\(SUM\(raw_bytes\)`, "s,rb,rs,cb,cw"},
	}
	cols := func(c string) []string {
		out := []string{}
		cur := ""
		for _, r := range c + "," {
			if r == ',' {
				out = append(out, cur)
				cur = ""
				continue
			}
			cur += string(r)
		}
		return out
	}
	for i := range reads {
		for _, mode := range []string{"query", "scan"} {
			t.Run(reads[i].query+"/"+mode, func(t *testing.T) {
				st, mock := newMock(t)
				for j := range i {
					mock.ExpectQuery(reads[j].query).WillReturnRows(sqlmock.NewRows(cols(reads[j].cols)))
				}
				if mode == "query" {
					mock.ExpectQuery(reads[i].query).WillReturnError(errDown)
				} else {
					mock.ExpectQuery(reads[i].query).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow(1))
				}
				_, err := st.Telemetry(ctx, "", start, start.Add(time.Hour), time.Minute)
				require.Error(t, err)
			})
		}
	}
}
