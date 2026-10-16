package compactor

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
)

// openRig is a rig whose clock stands ten minutes into the open window, with
// that window recorded.
func openRig(t *testing.T) (*rig, time.Time) {
	t.Helper()
	r := newRig(t)
	open := now.Truncate(time.Hour)
	r.clock = open.Add(10 * time.Minute)
	r.windows.add("esp", open, 3)
	return r, open
}

// kinds counts the open window's objects by what they are.
func kinds(r *rig, open time.Time) (segments, rollups, manifests []string) {
	keys, _ := r.objects.ListKeys(ctx, "", whlayout.WindowPrefix("esp", open))
	for _, k := range keys {
		switch kind, _ := whlayout.Classify(k); kind {
		case whlayout.KindRollup:
			rollups = append(rollups, k)
		case whlayout.KindManifest:
			manifests = append(manifests, k)
		default:
			segments = append(segments, k)
		}
	}
	return segments, rollups, manifests
}

// TestRollupMergesTheOpenWindow is #2132's acceptance at the unit level: the
// open window's segments are merged into one object the raw table reads as a
// segment, holding every event, with a manifest the query engine skips; the
// segments stay until the delete delay has passed, and then go.
func TestRollupMergesTheOpenWindow(t *testing.T) {
	r, open := openRig(t)
	r.segment(t, open, segAt{"a", 1, 0}, "e1", "e2")
	r.segment(t, open, segAt{"a", 2, 60}, "e3")
	r.segment(t, open, segAt{"b", 1, 120}, "e4")

	require.NoError(t, r.w.Rollup(ctx))
	segments, rollups, manifests := kinds(r, open)
	require.Len(t, rollups, 1)
	require.Len(t, manifests, 1)
	assert.Len(t, segments, 3, "the merged segments stay until the delete delay has passed")
	assert.True(t, strings.HasPrefix(manifests[0][strings.LastIndexByte(manifests[0], '/')+1:], "."),
		"the manifest's name is one the query engine skips")

	data, _ := r.objects.GetObject(ctx, "", rollups[0])
	evs, err := whevent.DecodeSegment(data)
	require.NoError(t, err, "the rollup reads as a segment")
	ids := make([]string, 0, len(evs))
	for _, e := range evs {
		ids = append(ids, e.EventID)
	}
	assert.ElementsMatch(t, []string{"e1", "e2", "e3", "e4"}, ids)

	r.clock = r.clock.Add(DefaultRollupDeleteDelay - time.Second)
	require.NoError(t, r.w.Rollup(ctx))
	segments, _, _ = kinds(r, open)
	assert.Len(t, segments, 3, "not before the delay")

	r.segment(t, open, segAt{"a", 3, 300}, "e5")
	r.clock = r.clock.Add(time.Second)
	require.NoError(t, r.w.Rollup(ctx))
	segments, rollups, manifests = kinds(r, open)
	assert.Len(t, segments, 1, "the merged segments are gone; the one written since stays")
	assert.Len(t, rollups, 1, "a second rollup waits for rollup_every")
	assert.Empty(t, manifests)
}

// TestRollupMergesOnlyWhatIsNew: a second rollup, once rollup_every has
// passed, merges the segments written since the first and never the first
// rollup or a segment it already merged.
func TestRollupMergesOnlyWhatIsNew(t *testing.T) {
	r, open := openRig(t)
	r.segment(t, open, segAt{"a", 1, 0}, "e1")
	r.segment(t, open, segAt{"a", 2, 1}, "e2")
	require.NoError(t, r.w.Rollup(ctx))

	r.segment(t, open, segAt{"a", 3, 2}, "e3")
	r.segment(t, open, segAt{"a", 4, 3}, "e4")
	r.clock = r.clock.Add(time.Minute)
	require.NoError(t, r.w.Rollup(ctx))
	_, rollups, _ := kinds(r, open)
	assert.Len(t, rollups, 1, "not before rollup_every")

	r.clock = r.clock.Add(5 * time.Minute)
	require.NoError(t, r.w.Rollup(ctx))
	_, rollups, _ = kinds(r, open)
	require.Len(t, rollups, 2)
	data, _ := r.objects.GetObject(ctx, "", rollups[1])
	evs, err := whevent.DecodeSegment(data)
	require.NoError(t, err)
	require.Len(t, evs, 2)
	assert.Equal(t, "e3", evs[0].EventID)
}

// TestRollupLeavesWhatItShould: nothing is merged in a window with too few
// segments, in a window another replica holds, or in a window that has
// ended, which is compaction's.
func TestRollupLeavesWhatItShould(t *testing.T) {
	t.Run("one segment", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		require.NoError(t, r.w.Rollup(ctx))
		_, rollups, _ := kinds(r, open)
		assert.Empty(t, rollups)
	})
	t.Run("held by another replica", func(t *testing.T) {
		r, open := openRig(t)
		r.windows.held = true
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		r.segment(t, open, segAt{"a", 2, 1}, "e2")
		require.NoError(t, r.w.Rollup(ctx))
		_, rollups, _ := kinds(r, open)
		assert.Empty(t, rollups)
	})
	t.Run("window closing before a deletion could run", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		r.segment(t, open, segAt{"a", 2, 1}, "e2")
		r.clock = open.Add(time.Hour - DefaultRollupDeleteDelay)
		require.NoError(t, r.w.Rollup(ctx))
		_, rollups, _ := kinds(r, open)
		assert.Empty(t, rollups, "its segments could not be deleted before compaction takes the window")
	})
	t.Run("ended window", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		r.segment(t, open, segAt{"a", 2, 1}, "e2")
		r.clock = open.Add(time.Hour + time.Minute)
		require.NoError(t, r.w.Rollup(ctx))
		_, rollups, _ := kinds(r, open)
		assert.Empty(t, rollups, "the window that ended is compaction's")
	})
}

// TestCompactionReadsARolledUpWindow: compaction reads rollups as segments,
// skips the manifest, and drops by event id what a rollup and a segment it has
// not yet deleted both hold.
func TestCompactionReadsARolledUpWindow(t *testing.T) {
	r, open := openRig(t)
	r.segment(t, open, segAt{"a", 1, 0}, "e1", "e2")
	r.segment(t, open, segAt{"a", 2, 60}, "e3")
	require.NoError(t, r.w.Rollup(ctx))

	r.clock = open.Add(time.Hour + DefaultGrace)
	require.True(t, r.w.Pass(ctx))
	row := r.windows.row(r.windows.rows["esp"+open.String()].Window)
	require.True(t, row.compacted)
	assert.Equal(t, int64(3), row.compaction.Events)
	assert.Equal(t, int64(3), row.compaction.Duplicates)
}

// TestRollupFailures: each step's failure is returned naming the step.
func TestRollupFailures(t *testing.T) {
	for key, want := range map[string]string{
		"list":        "listing the open window",
		"claimrollup": "boom",
		"get":         "reading",
		"put":         "writing the rollup",
	} {
		t.Run(key, func(t *testing.T) {
			r, open := openRig(t)
			r.segment(t, open, segAt{"a", 1, 0}, "e1")
			r.segment(t, open, segAt{"a", 2, 1}, "e2")
			if key == "claimrollup" {
				r.windows.err[key] = errBoom
			} else {
				r.objects.err[key] = errBoom
			}
			err := r.w.Rollup(ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
	t.Run("sources", func(t *testing.T) {
		r, _ := openRig(t)
		r.sources.err = errBoom
		require.Error(t, r.w.Rollup(ctx))
	})
	t.Run("delete", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		r.segment(t, open, segAt{"a", 2, 1}, "e2")
		require.NoError(t, r.w.Rollup(ctx))
		r.clock = r.clock.Add(DefaultRollupDeleteDelay)
		r.objects.err["delete"] = errBoom
		err := r.w.Rollup(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deleting merged segment")
	})
	t.Run("not a segment", func(t *testing.T) {
		r, open := openRig(t)
		r.objects.put(whlayout.SegmentKey("esp", open, "a", 1), []byte("not gzip"))
		r.objects.put(whlayout.SegmentKey("esp", open, "a", 2), []byte("not gzip"))
		require.Error(t, r.w.Rollup(ctx))
	})
}

// TestRollupDueOncePerMinute: the pass runs at most once a minute.
func TestRollupDueOncePerMinute(t *testing.T) {
	w := &Worker{tuning: Tuning{}.withDefaults()}
	assert.True(t, w.rollupDue(now))
	assert.False(t, w.rollupDue(now.Add(30*time.Second)))
	assert.True(t, w.rollupDue(now.Add(time.Minute)))
}

// TestRollupMoreFailures: a manifest that cannot be read or written, a
// segment whose last line has no newline, and a segment cut short.
func TestRollupMoreFailures(t *testing.T) {
	t.Run("manifest write", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		r.segment(t, open, segAt{"a", 2, 1}, "e2")
		r.objects.err["putmanifest"] = errBoom
		err := r.w.Rollup(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "writing the rollup manifest")
	})
	t.Run("manifest read", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		r.segment(t, open, segAt{"a", 2, 1}, "e2")
		require.NoError(t, r.w.Rollup(ctx))
		r.objects.err["get"] = errBoom
		err := r.w.Rollup(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading rollup manifest")
	})
	t.Run("cut short", func(t *testing.T) {
		r, open := openRig(t)
		r.segment(t, open, segAt{"a", 1, 0}, "e1")
		var whole bytes.Buffer
		zw := gzip.NewWriter(&whole)
		_, _ = zw.Write([]byte(strings.Repeat(`{"event_id":"x"}`+"\n", 200)))
		_ = zw.Close()
		r.objects.put(whlayout.SegmentKey("esp", open, "a", 2), whole.Bytes()[:whole.Len()/2])
		err := r.w.Rollup(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading segment")
	})
}

// TestAppendLinesEndsEveryLine: a segment whose last line has no newline is
// copied with one, so the next segment's first line starts a line of its own.
func TestAppendLinesEndsEveryLine(t *testing.T) {
	var src bytes.Buffer
	zw := gzip.NewWriter(&src)
	_, _ = zw.Write([]byte("{\"a\":1}\n\n{\"b\":2}"))
	_ = zw.Close()
	var out bytes.Buffer
	require.NoError(t, appendLines(&out, src.Bytes()))
	assert.Equal(t, "{\"a\":1}\n{\"b\":2}\n", out.String(), "blank lines dropped, the last line ended")
}
