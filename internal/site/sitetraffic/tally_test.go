package sitetraffic

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var prod = Key{Kind: KindSite, Subject: "site_1", Environment: "prod"}

func TestTally_CountsClassesAndBuckets(t *testing.T) {
	tl := NewTally()
	for range 50 {
		tl.Record(prod, 200, 3*time.Millisecond)
	}
	for range 10 {
		tl.Record(prod, 404, 40*time.Millisecond)
	}
	tl.Record(Key{Kind: KindPages, Subject: "shop"}, 503, 20*time.Second)
	d := tl.Drain()
	got := d.Keys[prod]
	assert.Equal(t, int64(50), got.Classes[0])
	assert.Equal(t, int64(10), got.Classes[2])
	assert.Equal(t, int64(50), got.Buckets[0])
	assert.Equal(t, int64(10), got.Buckets[3])
	assert.Equal(t, int64(61), d.Replica.Total(), "the replica counts every request across every site")
	assert.Equal(t, int64(1), d.Keys[Key{Kind: KindPages, Subject: "shop"}].Buckets[NumBuckets-1])
	assert.Empty(t, tl.Drain().Keys, "a drain takes the counts; the next has none")
}

func TestTally_OffRecordsNothing(t *testing.T) {
	tl := NewTally()
	tl.SetEnabled(false)
	tl.Record(prod, 200, time.Millisecond)
	assert.Empty(t, tl.Drain().Keys)
	assert.False(t, tl.Enabled())
	var none *Tally
	none.Record(prod, 200, time.Millisecond)
	none.SetEnabled(true)
	assert.False(t, none.Enabled())
}

func TestTally_LetsGoOfIdleKeysAndBoundsThem(t *testing.T) {
	tl := NewTally()
	tl.Record(prod, 200, time.Millisecond)
	for range idleFlushes + 1 {
		tl.Drain()
	}
	assert.Empty(t, *tl.keys.Load(), "a key idle for idleFlushes drains is let go")

	tl.maxKeys = 100
	for i := range tl.maxKeys {
		tl.Record(Key{Kind: KindSite, Subject: strconv.Itoa(i)}, 200, time.Millisecond)
	}
	tl.Record(Key{Kind: KindSite, Subject: "one more"}, 200, time.Millisecond)
	d := tl.Drain()
	assert.Len(t, d.Keys, 100)
	assert.Equal(t, int64(1), d.Dropped, "a key past the bound is counted as dropped")
}

func TestClassAndBucketOf(t *testing.T) {
	for status, want := range map[int]int{200: 0, 301: 1, 404: 2, 503: 3, 101: 4, 0: 4} {
		assert.Equal(t, want, ClassOf(status), status)
	}
	assert.Equal(t, 0, BucketOf(5*time.Millisecond))
	assert.Equal(t, 1, BucketOf(6*time.Millisecond))
	assert.Equal(t, NumBuckets-1, BucketOf(time.Minute))
}

func TestPercentile(t *testing.T) {
	var b [NumBuckets]int64
	assert.Zero(t, Percentile(b, 0.5))
	b[0] = 100 // all at <=5ms
	assert.InDelta(t, 2.5, Percentile(b, 0.5), 0.01)
	b[4] = 100 // 100 more at 50-100ms
	assert.InDelta(t, 5, Percentile(b, 0.5), 0.01)
	assert.InDelta(t, 95, Percentile(b, 0.95), 0.01)
	b[NumBuckets-1] = 1000
	assert.Equal(t, Bounds[len(Bounds)-1], Percentile(b, 0.99))
	tot := TotalsOf(Counts{Classes: [NumClasses]int64{3, 0, 1, 0, 0}, Buckets: [NumBuckets]int64{4}})
	assert.Equal(t, int64(4), tot.Requests)
	assert.Equal(t, int64(1), tot.Classes["4xx"])
}

func TestRangeOf(t *testing.T) {
	r, err := RangeOf("")
	require.NoError(t, err)
	assert.Equal(t, "24h", r.Name)
	_, err = RangeOf("2y")
	require.Error(t, err)
	now := time.Date(2026, 11, 14, 13, 37, 0, 0, time.UTC)
	week, _ := RangeOf("7d")
	assert.Equal(t, time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC), week.Since(now))
}

// BenchmarkTally_Record is #2647's criterion 9: the per-request tally
// allocates nothing, and runs in parallel without contention between sites.
func BenchmarkTally_Record(b *testing.B) {
	tl := NewTally()
	keys := make([]Key, 64)
	for i := range keys {
		keys[i] = Key{Kind: KindSite, Subject: "site_" + strconv.Itoa(i), Environment: "prod"}
		tl.Record(keys[i], 200, time.Millisecond)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tl.Record(keys[i%len(keys)], 200, 3*time.Millisecond)
			i++
		}
	})
}

// TestTally_RecordAllocatesNothing holds the benchmark's promise in the unit
// run.
func TestTally_RecordAllocatesNothing(t *testing.T) {
	tl := NewTally()
	tl.Record(prod, 200, time.Millisecond)
	assert.Zero(t, testing.AllocsPerRun(1000, func() { tl.Record(prod, 200, 3*time.Millisecond) }))
}

func TestTally_ConcurrentRecordAndDrainLoseNothing(t *testing.T) {
	tl := NewTally()
	var wg sync.WaitGroup
	var drained Counts
	var mu sync.Mutex
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				d := tl.Drain()
				mu.Lock()
				drained.Add(d.Replica)
				mu.Unlock()
			}
		}
	})
	var writers sync.WaitGroup
	for w := range 8 {
		writers.Go(func() {
			k := Key{Kind: KindSite, Subject: strconv.Itoa(w)}
			for range 1000 {
				tl.Record(k, 200, time.Millisecond)
			}
		})
	}
	writers.Wait()
	close(stop)
	wg.Wait()
	drained.Add(tl.Drain().Replica)
	assert.Equal(t, int64(8000), drained.Total())
}

// fakeAdder records flushes, failing as told.
type fakeAdder struct {
	calls    int
	rows     [][]Row
	replicas [][]ReplicaRow
	err      error
	unknown  bool
}

func (f *fakeAdder) Add(_ context.Context, rows []Row, replicas []ReplicaRow) (bool, error) {
	f.calls++
	if f.err != nil {
		return f.unknown, f.err
	}
	f.rows, f.replicas = append(f.rows, rows), append(f.replicas, replicas)
	return false, nil
}

func flusherAt(adder *fakeAdder, at *time.Time) (*Flusher, *Tally) {
	tl := NewTally()
	return &Flusher{Tally: tl, Store: adder, Instance: "host-a:8080", Now: func() time.Time { return *at }}, tl
}

// TestFlusher_OneRowPerKeyAndGrain pins #2647's criterion 11 at the flush:
// 10,000 requests in a minute are one minute row and one hour row.
func TestFlusher_OneRowPerKeyAndGrain(t *testing.T) {
	at := time.Date(2026, 11, 14, 13, 5, 30, 0, time.UTC)
	adder := &fakeAdder{}
	f, tl := flusherAt(adder, &at)
	for range 10000 {
		tl.Record(prod, 200, time.Millisecond)
	}
	require.NoError(t, f.Flush(context.Background()))
	require.Equal(t, 1, adder.calls)
	require.Len(t, adder.rows[0], 2, "a minute row and an hour row")
	for _, r := range adder.rows[0] {
		assert.Equal(t, int64(10000), r.Counts.Total())
		assert.Equal(t, prod, r.Key)
	}
	require.Len(t, adder.replicas[0], 2)
	assert.Equal(t, "host-a:8080", adder.replicas[0][0].Instance)

	require.NoError(t, f.Flush(context.Background()))
	assert.Equal(t, 1, adder.calls, "an idle minute writes nothing")
}

// TestFlusher_HoldsAFailedMinuteAndNeverCountsOneTwice pins the retry rules.
func TestFlusher_HoldsAFailedMinuteAndNeverCountsOneTwice(t *testing.T) {
	at := time.Date(2026, 11, 14, 13, 5, 0, 0, time.UTC)
	adder := &fakeAdder{err: errors.New("database down")}
	f, tl := flusherAt(adder, &at)
	tl.Record(prod, 200, time.Millisecond)
	require.Error(t, f.Flush(context.Background()))
	assert.Equal(t, 1, f.Held(), "a flush that failed before its commit is held")

	adder.err = nil
	at = at.Add(time.Minute)
	tl.Record(prod, 200, time.Millisecond)
	require.NoError(t, f.Flush(context.Background()))
	assert.Zero(t, f.Held())
	var hour int64
	for _, r := range adder.rows[0] {
		if r.Grain == GrainHour {
			hour += r.Counts.Total()
		}
	}
	assert.Equal(t, int64(2), hour, "two minutes of one hour add into one hour row, once")
	assert.Len(t, adder.rows[0], 3, "two minute rows and one hour row")

	adder.err, adder.unknown = errors.New("connection lost at commit"), true
	tl.Record(prod, 200, time.Millisecond)
	require.Error(t, f.Flush(context.Background()))
	assert.Zero(t, f.Held(), "an unknown outcome is dropped, never retried")
	assert.Equal(t, int64(1), f.Unknown.Load())

	adder.unknown = false
	for i := range MaxHeld + 5 {
		at = at.Add(time.Minute)
		tl.Record(prod, 200, time.Millisecond)
		_ = f.Flush(context.Background())
		_ = i
	}
	assert.Equal(t, MaxHeld, f.Held(), "the held minutes are bounded")
	assert.Equal(t, int64(5), f.Dropped.Load())
}

func TestFlusher_OffDropsAndRecordsNothing(t *testing.T) {
	at := time.Date(2026, 11, 14, 13, 5, 0, 0, time.UTC)
	adder := &fakeAdder{}
	f, tl := flusherAt(adder, &at)
	f.Enabled = func() bool { return false }
	tl.Record(prod, 200, time.Millisecond)
	require.NoError(t, f.Flush(context.Background()))
	assert.Zero(t, adder.calls)
	assert.False(t, tl.Enabled(), "the flush turns the tally off with the setting")
}

// TestFlusher_FiveHundredSitesAreOneWrite is #2647's criterion 12 at the
// flush: 500 sites taking traffic in one minute are one write of two
// statements, as one site is.
func TestFlusher_FiveHundredSitesAreOneWrite(t *testing.T) {
	at := time.Date(2026, 11, 14, 13, 5, 0, 0, time.UTC)
	adder := &fakeAdder{}
	f, tl := flusherAt(adder, &at)
	for i := range 500 {
		tl.Record(Key{Kind: KindSite, Subject: "site_" + strconv.Itoa(i), Environment: "prod"}, 200, time.Millisecond)
	}
	require.NoError(t, f.Flush(context.Background()))
	assert.Equal(t, 1, adder.calls)
	assert.Len(t, adder.rows[0], 1000, "a minute row and an hour row for each site, in the one write")
}
