//go:build integration

package whstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestTelemetryRollupRealDB is #2150's history against a migrated database:
// counts by status, latency stage sums, receiver minutes and compaction runs
// are written per minute, read back as series, and folded into hours past the
// cutoff with every count kept once.
func TestTelemetryRollupRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)

	hour := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
	for i := range 3 {
		minute := hour.Add(time.Duration(i*10) * time.Minute)
		require.NoError(t, st.RecordCounts(ctx, []Count{
			{Source: "esp", Minute: minute, Outcome: "accepted", Status: 202, Count: 5},
			{Source: "esp", Minute: minute, Outcome: "unauthorized", Status: 401, Count: 1},
		}))
		var l Latency
		l.Source, l.Minute = "esp", minute
		l.Observe(SlowAck{At: minute, TotalMS: int64(40 * (i + 1)), BufferMS: 1, MarkMS: 2, PutMS: 30})
		require.NoError(t, st.RecordLatency(ctx, []Latency{l}))
		require.NoError(t, st.RecordReplicaMinutes(ctx, []ReplicaMinute{{
			Replica: "a", Minute: minute, ReceivedEvents: 5, DrainedEvents: 4, DrainedBytes: 400, SpoolBytes: int64(10 * (i + 1)),
		}}))
	}
	require.NoError(t, st.RecordCompactionRun(ctx, CompactionRun{
		Source: "esp", WindowStart: hour, CompactedAt: hour.Add(70 * time.Minute), DurationMS: 900, LagSeconds: 600,
		Segments: 3, Events: 15, InputBytes: 100, DecodedBytes: 2000, OutputBytes: 300,
	}))

	before, err := st.Telemetry(ctx, "esp", hour, hour.Add(2*time.Hour), time.Hour)
	require.NoError(t, err)

	require.NoError(t, st.RollupTelemetry(ctx, time.Now().Add(-48*time.Hour)))
	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_request_counts WHERE minute <> date_bin('1 hour', minute, TIMESTAMPTZ 'epoch')`).Scan(&rows))
	assert.Zero(t, rows, "every minute row past the cutoff is folded into its hour")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_ack_latency`).Scan(&rows))
	assert.Equal(t, 1, rows)
	require.NoError(t, st.RollupTelemetry(ctx, time.Now().Add(-48*time.Hour)), "a second pass changes nothing")

	after, err := st.Telemetry(ctx, "esp", hour, hour.Add(2*time.Hour), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, before.Statuses, after.Statuses, "the hour reads the same before and after the rollup")
	assert.Equal(t, before.Replicas, after.Replicas)
	require.Len(t, after.Ack, 1)
	assert.Equal(t, before.Ack, after.Ack)
	assert.Equal(t, int64(3), after.Ack[0].Count)
	assert.Equal(t, int64(120), after.Ack[0].MaxMS)
	assert.InDelta(t, 30.0, after.Ack[0].PutMS, 0.001)
	require.Len(t, after.Statuses, 2)
	assert.Equal(t, int64(15), after.Statuses[0].Count)
	assert.Equal(t, int64(30), after.Replicas[0].SpoolBytes, "depth is the greatest of the hour")
	require.Len(t, after.Compactions, 1)

	require.NoError(t, st.PruneCounts(ctx, time.Now()))
	gone, err := st.Telemetry(ctx, "esp", hour, time.Now(), time.Hour)
	require.NoError(t, err)
	assert.Empty(t, gone.Statuses)
	assert.Empty(t, gone.Replicas)
	assert.Empty(t, gone.Compactions)
}

// TestWindowStorageAndAlertReadingsRealDB: a window counts its segments'
// bytes until they are released, a compaction records its file's size, and
// the alert readings see counts, latency and the owed window.
func TestWindowStorageAndAlertReadingsRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)

	window := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)
	require.NoError(t, st.MarkSegment(ctx, "esp", window, time.Hour, 0))
	require.NoError(t, st.MarkSegment(ctx, "esp", window, time.Hour, 1000))
	require.NoError(t, st.MarkSegment(ctx, "esp", window, time.Hour, 500))
	got, err := st.Storage(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []SourceStorage{{Source: "esp", RawBytes: 1500, RawSegments: 2}}, got)

	now := time.Now().UTC()
	require.NoError(t, st.RecordCounts(ctx, []Count{{Source: "esp", Minute: now, Outcome: "spool_full", Status: 503, Count: 2}}))
	var l Latency
	l.Source, l.Minute = "esp", now
	l.Observe(SlowAck{At: now, TotalMS: 3000})
	require.NoError(t, st.RecordLatency(ctx, []Latency{l}))
	readings, err := st.AlertReadings(ctx, now.Add(-15*time.Minute), now.Add(-5*time.Minute))
	require.NoError(t, err)
	esp := readings["esp"]
	require.NotNil(t, esp)
	assert.Equal(t, int64(2), esp.Outcomes[now.Truncate(time.Minute)]["spool_full"])
	assert.Equal(t, int64(3000), esp.Ack.P99MS)
	require.NotNil(t, esp.OldestOwedEnd, "the window is owed a compaction")
	assert.Equal(t, window.Add(time.Hour), *esp.OldestOwedEnd)

	claimed, err := st.ClaimOwed(ctx, time.Now(), time.Minute, 5)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, st.RecordCompacted(ctx, claimed[0], Compaction{Segments: 2, Events: 3, Digest: "d", Bytes: 777}))
	require.NoError(t, st.MarkRawDeleted(ctx, claimed[0]))
	got, err = st.Storage(ctx, "esp")
	require.NoError(t, err)
	assert.Equal(t, []SourceStorage{{Source: "esp", CompactedBytes: 777, CompactedWindows: 1}}, got,
		"released segments leave the raw tally, the file stays")
}
