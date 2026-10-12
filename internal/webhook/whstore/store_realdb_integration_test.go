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

	// Sixty rejections two minutes apart: too far apart to be one run, so
	// each is a row and the trim keeps the outcome's newest 50.
	rej := make([]Rejection, 0, 61)
	for i := range 60 {
		rej = append(rej, Rejection{Source: "esp", At: now.Add(time.Duration(i-60) * 2 * time.Minute), Outcome: "unauthorized", Reason: "the signature does not match"})
	}
	rej = append(rej, Rejection{Source: "gone", At: now, Outcome: "unauthorized"})
	require.NoError(t, st.RecordRejections(ctx, rej))
	require.NoError(t, st.RecordRejections(ctx, nil))

	status, err := st.Status(ctx, "esp", now)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"accepted": 5}, status.LastHour)
	assert.Equal(t, map[string]int64{"accepted": 5, "unauthorized": 4}, status.LastDay)
	assert.Len(t, status.Rejections, maxRejections, "a source keeps its newest 50 rejections of an outcome")
	assert.Nil(t, status.LastSegmentAt)
	assert.Equal(t, 0, status.Pending)

	require.NoError(t, st.PruneCounts(ctx, now.Add(-time.Hour)))
	status, err = st.Status(ctx, "esp", now)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"accepted": 5}, status.LastDay)
}

// TestDeleteExpiredRealDB covers #1904: the record of a window expired before
// the cutoff is removed; one expired after it, one never expired, and one a
// segment landed in after it expired are kept.
func TestDeleteExpiredRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)
	old := time.Now().UTC().AddDate(0, 0, -100)
	_, err = db.ExecContext(ctx, `INSERT INTO webhook_windows (source, window_start, window_seconds, last_segment_at, expired_at) VALUES
		('esp', $1::timestamptz, 3600, $1::timestamptz, $1::timestamptz + interval '1 hour'),
		('esp', $1::timestamptz + interval '1 hour', 3600, $1::timestamptz, NOW()),
		('esp', $1::timestamptz + interval '2 hour', 3600, $1::timestamptz, NULL),
		('esp', $1::timestamptz + interval '3 hour', 3600, NOW(), $1::timestamptz + interval '1 hour')`, old)
	require.NoError(t, err)

	n, err := st.DeleteExpired(ctx, time.Now().UTC().AddDate(0, 0, -90))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	var left int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_windows`).Scan(&left))
	assert.Equal(t, 3, left)
	var gone int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_windows WHERE window_start = $1`, old).Scan(&gone))
	assert.Zero(t, gone, "the window expired before the cutoff is the one removed")
}

// TestWebhookOverviewRealDB reads the overview of every source against a
// migrated database (#1979): each source's counts and windows in one pass, a
// failing window's count and error, the series whose totals over the hour are
// the counts table's for the same bound, and the newest rejections of every
// source with the source each came from.
func TestWebhookOverviewRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	now := time.Now().UTC()

	empty, err := st.Overview(ctx, "", now, time.Hour, time.Minute)
	require.NoError(t, err)
	assert.Empty(t, empty.Summaries)
	assert.NotNil(t, empty.Volume)
	assert.Empty(t, empty.Volume)
	assert.NotNil(t, empty.Rejections)
	assert.Empty(t, empty.Rejections)

	_, err = db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch'), ('crm', 'scratch'), ('quiet', 'scratch')`)
	require.NoError(t, err)
	require.NoError(t, st.RecordCounts(ctx, []Count{
		{Source: "esp", Minute: now, Outcome: "accepted", Count: 3},
		{Source: "esp", Minute: now.Add(-2 * time.Minute), Outcome: "accepted", Count: 2},
		{Source: "esp", Minute: now.Add(-5 * time.Minute), Outcome: "unauthorized", Count: 1},
		{Source: "esp", Minute: now.Add(-3 * time.Hour), Outcome: "accepted", Count: 7},
		{Source: "crm", Minute: now.Add(-10 * time.Minute), Outcome: "accepted", Count: 4},
	}))
	window := now.Truncate(time.Minute).Add(-10 * time.Minute)
	require.NoError(t, st.MarkSegment(ctx, "esp", window, time.Minute))
	claimed, err := st.ClaimOwed(ctx, now, time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, st.RecordFailure(ctx, claimed[0], "segment k: not gzip", time.Hour))
	require.NoError(t, st.RecordRejections(ctx, []Rejection{
		{Source: "esp", At: now.Add(-time.Second), Outcome: "unauthorized", Reason: "the signature does not match"},
		{Source: "crm", At: now, Outcome: "too_large", Reason: "the body is over the limit"},
	}))

	ov, err := st.Overview(ctx, "", now, time.Hour, time.Minute)
	require.NoError(t, err)

	esp := ov.Summaries["esp"]
	assert.Equal(t, map[string]int64{"accepted": 5, "unauthorized": 1}, esp.LastHour)
	assert.Equal(t, map[string]int64{"accepted": 12, "unauthorized": 1}, esp.LastDay)
	require.NotNil(t, esp.LastSegmentAt)
	assert.Equal(t, 1, esp.Pending)
	assert.Equal(t, 1, esp.Failing)
	assert.Equal(t, "segment k: not gzip", esp.LastError)

	crm := ov.Summaries["crm"]
	assert.Equal(t, map[string]int64{"accepted": 4}, crm.LastHour)
	assert.Nil(t, crm.LastSegmentAt)
	_, hasQuiet := ov.Summaries["quiet"]
	assert.False(t, hasQuiet, "a source with neither counts nor windows has no summary")

	var series, table int64
	for _, p := range ov.Volume {
		series += p.Count
		assert.Equal(t, p.At, p.At.Truncate(time.Minute), "a one-minute bucket starts on a minute")
	}
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count), 0) FROM webhook_request_counts WHERE minute >= $1`, ov.Since).Scan(&table))
	assert.Equal(t, int64(10), table)
	assert.Equal(t, table, series, "the series over the hour sums to the counts table's rows for the same bound")

	day, err := st.Overview(ctx, "", now, 24*time.Hour, 15*time.Minute)
	require.NoError(t, err)
	series = 0
	for _, p := range day.Volume {
		series += p.Count
		assert.Zero(t, p.At.Unix()%900, "a fifteen-minute bucket starts on a multiple of fifteen minutes")
	}
	assert.Equal(t, int64(17), series)

	require.Len(t, ov.Rejections, 2)
	assert.Equal(t, "crm", ov.Rejections[0].Source, "newest first")
	assert.Equal(t, "the body is over the limit", ov.Rejections[0].Reason)
	assert.Equal(t, "esp", ov.Rejections[1].Source)
	assert.Equal(t, "the signature does not match", ov.Rejections[1].Reason)
}

// TestRejectionsSurviveABurstRealDB is #2001's acceptance against a migrated
// database: five unauthorized rejections, then five hundred rate_limited ones
// in batches the way the receiver flushes them. The source's page and the
// overview still list all five unauthorized rows with their reasons, and the
// burst is one row that counts it.
func TestRejectionsSurviveABurstRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch')`)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)

	reasons := []string{
		"the signature header is missing",
		"the signature does not match",
		"the timestamp is outside the tolerance window",
		"the timestamp header is missing",
		"the signature does not match",
	}
	for i, reason := range reasons {
		// A minute and more apart, so each attempt is a row of its own.
		require.NoError(t, st.RecordRejections(ctx, []Rejection{{
			Source: "esp", At: now.Add(time.Duration(i-10) * 2 * time.Minute), Outcome: "unauthorized", Reason: reason,
		}}))
	}
	for batch := range 10 {
		burst := make([]Rejection, 0, 50)
		for i := range 50 {
			burst = append(burst, Rejection{
				Source: "esp", At: now.Add(time.Duration(batch*50+i) * time.Millisecond),
				Outcome: "rate_limited", Reason: "the source's rate limit was reached",
			})
		}
		require.NoError(t, st.RecordRejections(ctx, burst))
	}

	unauthorized := func(rs []Rejection) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			if r.Outcome == "unauthorized" {
				out = append(out, r.Reason)
			}
		}
		return out
	}
	want := []string{reasons[4], reasons[3], reasons[2], reasons[1], reasons[0]}

	status, err := st.Status(ctx, "esp", now)
	require.NoError(t, err)
	require.Len(t, status.Rejections, 6, "five unauthorized rows and one for the burst")
	assert.Equal(t, "rate_limited", status.Rejections[0].Outcome, "newest first")
	assert.Equal(t, int64(500), status.Rejections[0].Count)
	assert.Equal(t, now, status.Rejections[0].FirstAt)
	assert.Equal(t, now.Add(499*time.Millisecond), status.Rejections[0].At)
	assert.Equal(t, want, unauthorized(status.Rejections))

	ov, err := st.Overview(ctx, "", now, time.Hour, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, want, unauthorized(ov.Rejections))
}

// TestAckLatencyRealDB writes two replicas' latency for one minute and a
// third for another, and reads it back: bucket counts summed, the slowest
// kept with its stages, the hour told apart from the day, a source filter
// applied, and the minute pruned with the counts (#2107).
func TestAckLatencyRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch'), ('crm', 'scratch')`)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Minute)
	recent, earlier := now.Add(-5*time.Minute), now.Add(-3*time.Hour)
	one := func(source string, minute time.Time, acks ...SlowAck) Latency {
		l := Latency{Source: source, Minute: minute}
		for _, a := range acks {
			l.Observe(a)
		}
		return l
	}
	require.NoError(t, st.RecordLatency(ctx, []Latency{
		one("esp", recent, SlowAck{At: recent, TotalMS: 8}, SlowAck{At: recent, TotalMS: 40, BufferMS: 5, MarkMS: 10, PutMS: 25}),
		one("esp", earlier, SlowAck{At: earlier, TotalMS: 2600, BufferMS: 1, MarkMS: 2500, PutMS: 99}),
		one("crm", recent, SlowAck{At: recent, TotalMS: 3}),
		one("gone", recent, SlowAck{At: recent, TotalMS: 3}),
	}))
	// A second replica's deliveries in the same minute add to the row.
	require.NoError(t, st.RecordLatency(ctx, []Latency{
		one("esp", recent, SlowAck{At: recent.Add(time.Second), TotalMS: 9}, SlowAck{At: recent, TotalMS: 20}),
	}))

	status, err := st.Status(ctx, "esp", now)
	require.NoError(t, err)
	assert.Equal(t, int64(4), status.AckLastHour.Count, "both replicas' deliveries in the minute are summed")
	assert.Equal(t, int64(40), status.AckLastHour.MaxMS, "the slower of the two replicas' slowest is kept")
	require.NotNil(t, status.AckLastHour.Slowest)
	assert.Equal(t, SlowAck{At: recent, TotalMS: 40, BufferMS: 5, MarkMS: 10, PutMS: 25}, *status.AckLastHour.Slowest)
	assert.Equal(t, int64(10), status.AckLastHour.P50MS)
	assert.Equal(t, int64(5), status.AckLastDay.Count)
	assert.Equal(t, int64(2600), status.AckLastDay.MaxMS)
	assert.Equal(t, int64(2500), status.AckLastDay.Slowest.MarkMS, "the day's slowest names the stage that was slow")

	ov, err := st.Overview(ctx, "crm", now, time.Hour, time.Minute)
	require.NoError(t, err)
	require.Len(t, ov.Summaries, 1, "a source filter reads that source alone")
	assert.Equal(t, int64(1), ov.Summaries["crm"].AckLastDay.Count)

	require.NoError(t, st.PruneCounts(ctx, now.Add(-time.Hour)))
	status, err = st.Status(ctx, "esp", now)
	require.NoError(t, err)
	assert.Equal(t, int64(4), status.AckLastDay.Count, "the minute older than the prune bound is gone")
}

// TestIdentifiedRejectionsRealDB keeps a refused delivery's hash and event
// ids, never folds it into a run, and reads it back only within the range and
// for the source asked for (#2107).
func TestIdentifiedRejectionsRealDB(t *testing.T) {
	db := testdb.New(t)
	st := New(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('esp', 'scratch'), ('crm', 'scratch')`)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, st.RecordRejections(ctx, []Rejection{
		{Source: "esp", At: now.Add(-3 * time.Hour), Outcome: "sender_gone", Reason: "gone", RequestHash: "h0", EventIDs: []string{"old"}},
		{Source: "esp", At: now.Add(-2 * time.Second), Outcome: "sender_gone", Reason: "gone", RequestHash: "h1", EventIDs: []string{"a", "b"}},
		{Source: "esp", At: now.Add(-time.Second), Outcome: "sender_gone", Reason: "gone", RequestHash: "h2", EventIDs: []string{"c"}},
		{Source: "crm", At: now, Outcome: "unauthorized", Reason: "bad"},
	}))
	require.NoError(t, st.RecordRejections(ctx, []Rejection{{Source: "esp", At: now, Outcome: "sender_gone", Reason: "gone"}}))

	status, err := st.Status(ctx, "esp", now)
	require.NoError(t, err)
	require.Len(t, status.Rejections, 4, "no rejection is folded into one that names its events")
	assert.Empty(t, status.Rejections[0].EventIDs)
	assert.NotNil(t, status.Rejections[0].EventIDs, "no ids is an empty list")
	assert.Equal(t, []string{"c"}, status.Rejections[1].EventIDs)
	assert.Equal(t, "h1", status.Rejections[2].RequestHash)
	assert.Equal(t, []string{"a", "b"}, status.Rejections[2].EventIDs)

	ov, err := st.Overview(ctx, "esp", now, time.Hour, time.Minute)
	require.NoError(t, err)
	assert.Len(t, ov.Rejections, 3, "the rejection older than the range is left out")
	for _, r := range ov.Rejections {
		assert.Equal(t, "esp", r.Source)
	}
	all, err := st.Overview(ctx, "", now, 24*time.Hour, 15*time.Minute)
	require.NoError(t, err)
	assert.Len(t, all.Rejections, 5)
}
