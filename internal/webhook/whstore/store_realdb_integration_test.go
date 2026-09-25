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

// TestWebhookWindowsRealDB runs a window through its whole life against a
// migrated database: segments recorded, claimed once the window has ended,
// dirtied by a segment written during the compaction, compacted again, its raw
// segments released to retention, and expired.
func TestWebhookWindowsRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)

	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	current := time.Now().UTC().Truncate(time.Hour)
	require.NoError(t, st.MarkSegment(ctx, "esp", hour, time.Hour))
	require.NoError(t, st.MarkSegment(ctx, "esp", hour, time.Hour))
	require.NoError(t, st.MarkSegment(ctx, "esp", current, time.Hour))

	claimed, err := st.ClaimOwed(ctx, time.Now(), time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "the window still receiving events is never claimed")
	h := claimed[0]
	assert.Equal(t, hour, h.Start)
	assert.Equal(t, time.Hour, h.Length)
	assert.Equal(t, int64(2), h.Generation)
	assert.Equal(t, 1, h.Attempts)

	again, err := st.ClaimOwed(ctx, time.Now(), time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, again, "a claimed window is not claimed twice")

	// A segment lands while the compaction runs.
	require.NoError(t, st.MarkSegment(ctx, "esp", hour, time.Hour))
	require.NoError(t, st.RecordLocation(ctx, h, "res-1", "s3://b/resources/global/global/res-1/"))
	require.NoError(t, st.RecordCompacted(ctx, h, Compaction{
		Segments: 2, Events: 10, Duplicates: 1, Digest: "d", ResourceID: "res-1",
		Location: "s3://b/resources/global/global/res-1/",
	}))
	dirty, err := st.ClaimOwed(ctx, time.Now(), time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, dirty, 1, "the segment written during the compaction leaves the window owed another")
	assert.Equal(t, int64(3), dirty[0].Generation)
	assert.Equal(t, "res-1", dirty[0].ResourceID)

	none, err := st.RawDeletable(ctx, "esp", time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, none, "a dirty window's raw segments are never released")

	require.NoError(t, st.RecordFailure(ctx, dirty[0], "boom", time.Hour))
	held, err := st.ClaimOwed(ctx, time.Now(), time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, held, "a failed window is held back")

	status, err := st.Status(ctx, "esp", time.Now())
	require.NoError(t, err)
	assert.Equal(t, 2, status.Pending)
	assert.Equal(t, 1, status.Failing)
	assert.Equal(t, "boom", status.LastError)
	require.NotNil(t, status.OldestWindow)
	assert.Equal(t, hour, *status.OldestWindow)
	assert.Nil(t, status.LastCompactedWindow)

	require.NoError(t, st.RecordCompacted(ctx, dirty[0], Compaction{Segments: 3, Events: 11, ResourceID: "res-1", Location: "loc"}))
	status, err = st.Status(ctx, "esp", time.Now())
	require.NoError(t, err)
	require.NotNil(t, status.LastCompactedWindow)
	assert.Equal(t, hour, *status.LastCompactedWindow)
	assert.Empty(t, status.LastError)

	raw, err := st.RawDeletable(ctx, "esp", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, raw, 1)
	require.NoError(t, st.MarkRawDeleted(ctx, raw[0]))
	raw, err = st.RawDeletable(ctx, "esp", time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, raw)

	ids, err := st.ResourceIDs(ctx, "esp")
	require.NoError(t, err)
	assert.Equal(t, []string{"res-1"}, ids)

	byRes, err := st.SourcesForResources(ctx, []string{"res-1", "other"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"res-1": "esp"}, byRes)

	exp, err := st.Expirable(ctx, "esp", time.Now())
	require.NoError(t, err)
	require.Len(t, exp, 1, "only the window that has ended is expirable")
	require.NoError(t, st.MarkUnregistered(ctx, exp[0]))
	require.NoError(t, st.MarkExpired(ctx, exp[0]))
	exp, err = st.Expirable(ctx, "esp", time.Now())
	require.NoError(t, err)
	assert.Empty(t, exp)

	// A segment landing in an expired window makes it expirable again, so its
	// raw objects are deleted rather than served by the view.
	require.NoError(t, st.MarkSegment(ctx, "esp", hour, time.Hour))
	exp, err = st.Expirable(ctx, "esp", time.Now())
	require.NoError(t, err)
	assert.Len(t, exp, 1)
	late, err := st.ClaimOwed(ctx, time.Now(), time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, late, "an expired window is never compacted again")

	byRes, err = st.SourcesForResources(ctx, []string{"res-1"})
	require.NoError(t, err)
	assert.Empty(t, byRes)

	assert.ErrorIs(t, st.MarkExpired(ctx, Window{Source: "esp", Start: hour.Add(-10 * time.Hour)}), ErrNotFound)
}

// TestWebhookWindowLengthRealDB proves the claim reads each window's own
// length: a one-minute window is owed a compaction a minute after it starts,
// an hour window only once its hour has ended, and a window a longer setting
// lands a segment in is lengthened rather than compacted early.
func TestWebhookWindowLengthRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)

	start := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	require.NoError(t, st.MarkSegment(ctx, "esp", start, time.Minute))
	require.NoError(t, st.MarkSegment(ctx, "esp", start.Add(time.Minute), time.Minute))

	got, err := st.ClaimOwed(ctx, start.Add(59*time.Second), time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, got, "a one-minute window is not owed before its minute ends")
	got, err = st.ClaimOwed(ctx, start.Add(time.Minute), time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, start, got[0].Start)
	assert.Equal(t, time.Minute, got[0].Length)

	// The setting is raised to an hour and a segment lands in the window that
	// started at 10:01: it becomes an hour long and is held until 11:01.
	require.NoError(t, st.MarkSegment(ctx, "esp", start.Add(time.Minute), time.Hour))
	got, err = st.ClaimOwed(ctx, start.Add(30*time.Minute), time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, got, "a lengthened window is not compacted while its new length runs")
	got, err = st.ClaimOwed(ctx, start.Add(61*time.Minute), time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, time.Hour, got[0].Length)

	// Lowering it never shortens a window already recorded.
	require.NoError(t, st.MarkSegment(ctx, "esp", start.Add(2*time.Hour), time.Hour))
	require.NoError(t, st.MarkSegment(ctx, "esp", start.Add(2*time.Hour), time.Minute))
	exp, err := st.Expirable(ctx, "esp", start.Add(2*time.Hour+30*time.Minute))
	require.NoError(t, err)
	for _, w := range exp {
		assert.NotEqual(t, start.Add(2*time.Hour), w.Start, "a window is expirable only once its full length has ended")
	}
}

// TestWebhookStatsRealDB records counts and rejections and reads them back
// the way the source's page does.
func TestWebhookStatsRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)
	now := time.Now().UTC()

	require.NoError(t, st.RecordCounts(ctx, []Count{
		{Source: "esp", Minute: now, Outcome: "accepted", Count: 3},
		{Source: "esp", Minute: now, Outcome: "accepted", Count: 2},
		{Source: "esp", Minute: now.Add(-3 * time.Hour), Outcome: "unauthorized", Count: 4},
		{Source: "gone", Minute: now, Outcome: "accepted", Count: 9},
	}))
	require.NoError(t, st.RecordCounts(ctx, nil))

	rej := make([]Rejection, 0, 60)
	for i := range 60 {
		rej = append(rej, Rejection{Source: "esp", At: now.Add(time.Duration(i) * time.Second), Outcome: "unauthorized", Reason: "the signature does not match"})
	}
	rej = append(rej, Rejection{Source: "gone", At: now, Outcome: "unauthorized"})
	require.NoError(t, st.RecordRejections(ctx, rej))
	require.NoError(t, st.RecordRejections(ctx, nil))

	status, err := st.Status(ctx, "esp", now)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"accepted": 5}, status.LastHour)
	assert.Equal(t, map[string]int64{"accepted": 5, "unauthorized": 4}, status.LastDay)
	assert.Len(t, status.Rejections, maxRejections, "a source keeps its newest 50 rejections")
	assert.Nil(t, status.LastSegmentAt)
	assert.Equal(t, 0, status.Pending)

	require.NoError(t, st.PruneCounts(ctx, now.Add(-time.Hour)))
	status, err = st.Status(ctx, "esp", now)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"accepted": 5}, status.LastDay)
}
