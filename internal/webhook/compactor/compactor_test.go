package compactor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

var (
	ctx     = context.Background()
	window  = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	now     = window.Add(3 * time.Hour)
	errBoom = errors.New("boom")
	esp     = whsource.Source{Name: "esp", Enabled: true, Connection: "scratch"}
	tgESP   = whtable.Target{Connection: "scratch", Catalog: "scratch_resources", Schema: "uploads"}
)

// fakeWindows is an in-memory WindowStore that follows the real store's rules for
// generations, claims and retention.
type fakeWindows struct {
	mu       sync.Mutex
	rows     map[string]*windowRow
	failures []string
	err      map[string]error
	pruned   bool
}

type windowRow struct {
	whstore.Window
	compactedGen int64
	compacted    bool
	rawDeleted   bool
	unregistered bool
	expired      bool
	compaction   whstore.Compaction
}

func newWindows() *fakeWindows {
	return &fakeWindows{rows: map[string]*windowRow{}, err: map[string]error{}}
}

func (f *fakeWindows) add(source string, h time.Time, gen int64) *windowRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &windowRow{Window: whstore.Window{Source: source, Start: h, Length: time.Hour, Generation: gen}}
	f.rows[source+h.String()] = r
	return r
}

func (f *fakeWindows) row(h whstore.Window) *windowRow { return f.rows[h.Source+h.Start.String()] }

func (f *fakeWindows) ClaimOwed(_ context.Context, endedBy time.Time, _ time.Duration, limit int) ([]whstore.Window, error) {
	if err := f.err["claim"]; err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []whstore.Window
	for _, r := range f.rows {
		if r.Generation != r.compactedGen && !r.expired && !r.Start.Add(r.Length).After(endedBy) && len(out) < limit {
			r.Attempts++
			out = append(out, r.Window)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (f *fakeWindows) RecordLocation(_ context.Context, h whstore.Window, id, loc string) error {
	if err := f.err["location"]; err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row(h).ResourceID, f.row(h).Location = id, loc
	return nil
}

func (f *fakeWindows) RecordCompacted(_ context.Context, h whstore.Window, c whstore.Compaction) error {
	if err := f.err["compacted"]; err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(h)
	r.compactedGen, r.compacted, r.compaction, r.Attempts = h.Generation, true, c, 0
	return nil
}

func (f *fakeWindows) RecordFailure(_ context.Context, _ whstore.Window, reason string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, reason)
	return f.err["failure"]
}

func (f *fakeWindows) RawDeletable(_ context.Context, source string, cutoff time.Time) ([]whstore.Window, error) {
	if err := f.err["rawdeletable"]; err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []whstore.Window
	for _, r := range f.rows {
		if r.Source == source && !r.expired && r.compacted && r.Generation == r.compactedGen && !r.rawDeleted &&
			!r.Start.Add(r.Length).After(cutoff) {
			out = append(out, r.Window)
		}
	}
	return out, nil
}

func (f *fakeWindows) Expirable(_ context.Context, source string, cutoff time.Time) ([]whstore.Window, error) {
	if err := f.err["expirable"]; err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []whstore.Window
	for _, r := range f.rows {
		if r.Source == source && !r.expired && !r.Start.Add(r.Length).After(cutoff) {
			out = append(out, r.Window)
		}
	}
	return out, nil
}

func (f *fakeWindows) MarkRawDeleted(_ context.Context, h whstore.Window) error {
	if err := f.err["rawdeleted"]; err != nil {
		return err
	}
	f.row(h).rawDeleted = true
	return nil
}

func (f *fakeWindows) MarkUnregistered(_ context.Context, h whstore.Window) error {
	if err := f.err["unregistered"]; err != nil {
		return err
	}
	f.row(h).unregistered = true
	return nil
}

func (f *fakeWindows) MarkExpired(_ context.Context, h whstore.Window) error {
	r := f.row(h)
	r.expired, r.ResourceID, r.Location = true, "", ""
	return nil
}

func (f *fakeWindows) PruneCounts(context.Context, time.Time) error {
	f.pruned = true
	return f.err["prune"]
}

type fakeSources struct {
	list []whsource.Source
	err  error
}

func (f *fakeSources) Get(_ context.Context, name string) (whsource.Source, error) {
	if f.err != nil {
		return whsource.Source{}, f.err
	}
	for _, s := range f.list {
		if s.Name == name {
			return s, nil
		}
	}
	return whsource.Source{}, whsource.ErrNotFound
}

func (f *fakeSources) List(context.Context) ([]whsource.Source, error) { return f.list, f.err }

type fakeObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     map[string]error
}

func newObjects() *fakeObjects {
	return &fakeObjects{objects: map[string][]byte{}, err: map[string]error{}}
}

func (f *fakeObjects) ListKeys(_ context.Context, _, prefix string) ([]string, error) {
	if err := f.err["list"]; err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeObjects) GetObject(_ context.Context, _, key string) ([]byte, error) {
	if err := f.err["get"]; err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.objects[key]
	if !ok {
		return nil, errors.New("no such key")
	}
	return d, nil
}

func (f *fakeObjects) DeleteObject(_ context.Context, _, key string) error {
	if err := f.err["delete"]; err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func (f *fakeObjects) put(key string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
}

type fakeTables struct {
	registered map[string]string
	syncs      int
	err        map[string]error
}

func newTables() *fakeTables {
	return &fakeTables{registered: map[string]string{}, err: map[string]error{}}
}

func (f *fakeTables) TargetFor(string) (whtable.Target, error) {
	if err := f.err["target"]; err != nil {
		return whtable.Target{}, err
	}
	return tgESP, nil
}

func (*fakeTables) S3Location(prefix string) string { return "s3://managed-resources/" + prefix }

func (f *fakeTables) RegisterWindow(_ context.Context, _ whtable.Target, src whsource.Source, h time.Time, loc string) error {
	if err := f.err["register"]; err != nil {
		return err
	}
	f.registered[src.Name+h.String()] = loc
	return nil
}

func (f *fakeTables) UnregisterWindow(_ context.Context, _ whtable.Target, src whsource.Source, h time.Time) error {
	if err := f.err["unregister"]; err != nil {
		return err
	}
	delete(f.registered, src.Name+h.String())
	return nil
}

func (f *fakeTables) SyncRaw(context.Context, whtable.Target, whsource.Source) error {
	f.syncs++
	return f.err["sync"]
}

// fakeResources keeps each resource's file in the fake object store, under a
// fresh directory per version, the way the resource store does.
type fakeResources struct {
	objects *fakeObjects
	seq     int
	keys    map[string]string
	err     map[string]error
}

func (f *fakeResources) Put(_ context.Context, src whsource.Source, h time.Time, existing string, content []byte) (StoredWindow, error) {
	source := src.Name
	if err := f.err["put"]; err != nil {
		return StoredWindow{}, err
	}
	f.seq++
	id := existing
	if _, ok := f.keys[id]; !ok {
		id = "res-" + source + "-" + h.Format("15")
	}
	key := "resources/global/global/" + id + "/v" + strconv.Itoa(f.seq) + "/" + h.Format("15") + ".parquet"
	f.keys[id] = key
	f.objects.put(key, content)
	return StoredWindow{ResourceID: id, Key: key}, nil
}

func (f *fakeResources) Key(_ context.Context, id string) (key string, ok bool, err error) {
	err = f.err["key"]
	if err != nil {
		return "", false, err
	}
	key, ok = f.keys[id]
	return key, ok, nil
}

func (f *fakeResources) Delete(_ context.Context, id string) error {
	if err := f.err["delete"]; err != nil {
		return err
	}
	delete(f.keys, id)
	return nil
}

type fakeMetrics struct {
	results map[string]int
	dropped int64
}

func (m *fakeMetrics) WebhookCompaction(_ context.Context, _, result string) { m.results[result]++ }
func (m *fakeMetrics) WebhookDuplicatesDropped(_ context.Context, _ string, n int64) {
	m.dropped += n
}

type rig struct {
	w         *Worker
	windows   *fakeWindows
	sources   *fakeSources
	objects   *fakeObjects
	tables    *fakeTables
	resources *fakeResources
	metrics   *fakeMetrics
	clock     time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{
		windows: newWindows(), sources: &fakeSources{list: []whsource.Source{esp}},
		objects: newObjects(), tables: newTables(), metrics: &fakeMetrics{results: map[string]int{}},
		clock: now,
	}
	r.resources = &fakeResources{objects: r.objects, keys: map[string]string{}, err: map[string]error{}}
	r.w = New(Tuning{RetentionEvery: time.Hour}, Deps{
		Windows: r.windows, Sources: r.sources, Objects: r.objects, Bucket: "managed-resources",
		Tables: r.tables, Resources: r.resources, Metrics: r.metrics,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return r.clock },
	})
	require.NotNil(t, r.w)
	return r
}

// segAt names one raw segment: the replica that wrote it, its sequence number,
// and the offset in seconds into the window its events were received at.
type segAt struct {
	writer string
	seq    uint64
	offset int
}

// segment writes a raw segment for window holding events with the given ids,
// received at at.offset seconds into the window.
func (r *rig) segment(t *testing.T, h time.Time, at segAt, ids ...string) {
	t.Helper()
	evs := make([]whevent.Event, 0, len(ids))
	for i, id := range ids {
		evs = append(evs, whevent.Event{
			ReceivedAt: h.Add(time.Duration(at.offset+i) * time.Second), EventID: id,
			Payload: []byte(`{"id":"` + id + `"}`), Replica: at.writer,
		})
	}
	data, err := whevent.EncodeSegment(evs, h.Add(time.Duration(at.offset)*time.Second))
	require.NoError(t, err)
	r.objects.put(whlayout.SegmentKey("esp", h, at.writer, at.seq), data)
}

func (r *rig) parquetIDs(t *testing.T, id string) []string {
	t.Helper()
	key := r.resources.keys[id]
	data, err := r.objects.GetObject(ctx, "", key)
	require.NoError(t, err)
	evs, err := decodeWindow(data)
	require.NoError(t, err)
	ids := make([]string, 0, len(evs))
	for _, e := range evs {
		ids = append(ids, e.EventID)
	}
	return ids
}

func TestCompactDedupsAcrossReplicas(t *testing.T) {
	r := newRig(t)
	r.windows.add("esp", window, 3)
	r.segment(t, window, segAt{"replica-a", 1, 0}, "a", "b", "c")
	r.segment(t, window, segAt{"replica-b", 1, 10}, "b", "d")

	assert.True(t, r.w.Pass(ctx))
	row := r.windows.row(whstore.Window{Source: "esp", Start: window})
	require.True(t, row.compacted)
	assert.Equal(t, int64(3), row.compactedGen)
	assert.Equal(t, 2, row.compaction.Segments)
	assert.Equal(t, int64(4), row.compaction.Events)
	assert.Equal(t, int64(1), row.compaction.Duplicates)
	assert.Len(t, row.compaction.Digest, 64)
	assert.Equal(t, []string{"a", "b", "c", "d"}, r.parquetIDs(t, row.ResourceID))
	assert.Equal(t, "s3://managed-resources/"+dirOf(r.resources.keys[row.ResourceID]), r.tables.registered["esp"+window.String()],
		"the window's partition is registered at its resource's directory")
	assert.Equal(t, 1, r.metrics.results[ResultCompacted])
	assert.Equal(t, int64(1), r.metrics.dropped)
}

func TestDedupKeepsEarliest(t *testing.T) {
	late := whevent.Stored{Event: whevent.Event{EventID: "x", ReceivedAt: window.Add(time.Minute), Replica: "b"}}
	early := whevent.Stored{Event: whevent.Event{EventID: "x", ReceivedAt: window, Replica: "a"}}
	tie := whevent.Stored{Event: whevent.Event{EventID: "y", ReceivedAt: window}, LandedAt: window.Add(2 * time.Second)}
	tieEarly := whevent.Stored{Event: whevent.Event{EventID: "y", ReceivedAt: window, Replica: "first"}, LandedAt: window.Add(time.Second)}
	kept, dropped := dedup([]whevent.Stored{late, early, tie, tieEarly})
	assert.Equal(t, int64(2), dropped)
	require.Len(t, kept, 2)
	assert.Equal(t, "a", kept[0].Replica)
	assert.Equal(t, "first", kept[1].Replica)
}

func TestLateSegmentRecompactsFromEverything(t *testing.T) {
	r := newRig(t)
	row := r.windows.add("esp", window, 1)
	r.segment(t, window, segAt{"a", 1, 0}, "a", "b")
	r.w.Pass(ctx)
	require.True(t, row.compacted)
	firstKey := r.resources.keys[row.ResourceID]

	// Retention releases the raw segments, then a late segment lands.
	for k := range r.objects.objects {
		if strings.HasPrefix(k, "webhooks/") {
			delete(r.objects.objects, k)
		}
	}
	r.segment(t, window, segAt{"b", 1, 30}, "b", "c")
	row.Generation = 2

	r.w.Pass(ctx)
	assert.Equal(t, int64(2), row.compactedGen)
	assert.Equal(t, []string{"a", "b", "c"}, r.parquetIDs(t, row.ResourceID),
		"events whose segments retention deleted are kept from the previous file")
	assert.NotEqual(t, firstKey, r.resources.keys[row.ResourceID], "a recompaction is a new version")
	assert.Equal(t, int64(1), row.compaction.Duplicates)
}

func TestCompactFailuresAreRecorded(t *testing.T) {
	cases := map[string]func(*rig){
		"target":      func(r *rig) { r.tables.err["target"] = errBoom },
		"list":        func(r *rig) { r.objects.err["list"] = errBoom },
		"get":         func(r *rig) { r.objects.err["get"] = errBoom },
		"put":         func(r *rig) { r.resources.err["put"] = errBoom },
		"location":    func(r *rig) { r.windows.err["location"] = errBoom },
		"register":    func(r *rig) { r.tables.err["register"] = errBoom },
		"compacted":   func(r *rig) { r.windows.err["compacted"] = errBoom },
		"source":      func(r *rig) { r.sources.err = errBoom },
		"failure too": func(r *rig) { r.tables.err["target"] = errBoom; r.windows.err["failure"] = errBoom },
		"bad segment": func(r *rig) { r.objects.put(whlayout.WindowPrefix("esp", window)+"x.jsonl.gz", []byte("junk")) },
		"previous key": func(r *rig) {
			r.windows.row(whstore.Window{Source: "esp", Start: window}).ResourceID = "gone"
			r.resources.err["key"] = errBoom
		},
		"previous corrupt": func(r *rig) {
			r.resources.keys["old"] = "old.parquet"
			r.objects.put("old.parquet", []byte("x"))
			r.windows.row(whstore.Window{Source: "esp", Start: window}).ResourceID = "old"
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.windows.add("esp", window, 1)
			r.segment(t, window, segAt{"a", 1, 0}, "a")
			breakIt(r)
			r.w.Pass(ctx)
			row := r.windows.row(whstore.Window{Source: "esp", Start: window})
			assert.False(t, row.compacted)
			assert.Len(t, r.windows.failures, 1)
			assert.Equal(t, 1, r.metrics.results[ResultFailed])
		})
	}
}

func TestDeletedSourceIsSkipped(t *testing.T) {
	r := newRig(t)
	r.windows.add("gone", window, 1)
	r.w.Pass(ctx)
	assert.Empty(t, r.windows.failures)
}

func TestNothingOwedWhileTheWindowRuns(t *testing.T) {
	r := newRig(t)
	current := r.clock.Truncate(time.Hour)
	r.windows.add("esp", current, 1)
	assert.False(t, r.w.Pass(ctx), "the window events are still landing in is never compacted")
	r.clock = current.Add(time.Hour + time.Minute)
	assert.False(t, r.w.Pass(ctx), "nor during the grace period after it ends")
	r.clock = current.Add(time.Hour + DefaultGrace)
	r.segment(t, current, segAt{"a", 1, 0}, "a")
	assert.True(t, r.w.Pass(ctx))
}

func TestOneMinuteWindow(t *testing.T) {
	r := newRig(t)
	start := r.clock.Truncate(time.Hour).Add(7 * time.Minute)
	row := r.windows.add("esp", start, 1)
	row.Length = time.Minute
	r.segment(t, start, segAt{"a", 1, 0}, "a", "b")
	r.clock = start.Add(time.Minute + DefaultGrace - time.Second)
	assert.False(t, r.w.Pass(ctx), "a one-minute window waits out its grace like any other")
	r.clock = start.Add(time.Minute + DefaultGrace)
	assert.True(t, r.w.Pass(ctx), "and is compacted once its minute and the grace have passed, not its hour")
	assert.True(t, row.compacted)
	assert.Equal(t, int64(2), row.compaction.Events)
	assert.Contains(t, r.tables.registered, "esp"+start.String())
}

func TestClaimFailure(t *testing.T) {
	r := newRig(t)
	r.windows.err["claim"] = errBoom
	assert.False(t, r.w.Pass(ctx))
}

func TestRawRetention(t *testing.T) {
	r := newRig(t)
	old := window.Add(-10 * 24 * time.Hour)
	compacted := r.windows.add("esp", old, 1)
	compacted.compacted, compacted.compactedGen = true, 1
	dirty := r.windows.add("esp", old.Add(time.Hour), 2)
	dirty.compacted, dirty.compactedGen = true, 1
	pending := r.windows.add("esp", old.Add(2*time.Hour), 1)
	r.segment(t, old, segAt{"a", 1, 0}, "a")
	r.segment(t, old.Add(time.Hour), segAt{"a", 2, 0}, "b")
	r.segment(t, old.Add(2*time.Hour), segAt{"a", 3, 0}, "c")

	r.w.Retention(ctx)
	assert.True(t, compacted.rawDeleted)
	assert.False(t, dirty.rawDeleted, "a dirty window's segments are never deleted")
	assert.False(t, pending.rawDeleted, "an uncompacted window's segments are never deleted")
	keys, _ := r.objects.ListKeys(ctx, "", "webhooks/esp/raw/")
	assert.Len(t, keys, 2)
	assert.Equal(t, 1, r.tables.syncs, "the raw partitions are synced after segments go")
	assert.True(t, r.windows.pruned)
}

func TestCompactedRetentionResumes(t *testing.T) {
	r := newRig(t)
	old := window.Add(-500 * 24 * time.Hour)
	row := r.windows.add("esp", old, 1)
	r.segment(t, old, segAt{"a", 1, 0}, "a")
	r.windows.err["claim"] = nil
	r.clock = old.Add(3 * time.Hour)
	r.w.Pass(ctx)
	require.True(t, row.compacted)
	require.NotEmpty(t, r.tables.registered)
	r.clock = now

	// The pass stops between unregistering and deleting.
	r.resources.err["delete"] = errBoom
	r.w.Retention(ctx)
	assert.True(t, row.unregistered)
	assert.Empty(t, r.tables.registered, "the view stops serving the window first")
	assert.False(t, row.expired)

	delete(r.resources.err, "delete")
	r.w.Retention(ctx)
	assert.True(t, row.expired, "the next pass finishes the job")
	assert.Empty(t, r.resources.keys)
	keys, _ := r.objects.ListKeys(ctx, "", "webhooks/")
	assert.Empty(t, keys)
}

func TestRetentionForeverKeepsHours(t *testing.T) {
	r := newRig(t)
	zero := 0
	src := esp
	src.Config.CompactedRetentionDays = &zero
	r.sources.list = []whsource.Source{src}
	row := r.windows.add("esp", window.Add(-5000*24*time.Hour), 1)
	r.w.Retention(ctx)
	assert.False(t, row.expired)
}

func TestRetentionFailures(t *testing.T) {
	old := window.Add(-500 * 24 * time.Hour)
	cases := map[string]func(*rig){
		"sources":      func(r *rig) { r.sources.err = errBoom },
		"target":       func(r *rig) { r.tables.err["target"] = errBoom },
		"expirable":    func(r *rig) { r.windows.err["expirable"] = errBoom },
		"unregister":   func(r *rig) { r.tables.err["unregister"] = errBoom },
		"unregistered": func(r *rig) { r.windows.err["unregistered"] = errBoom },
		"list":         func(r *rig) { r.objects.err["list"] = errBoom },
		"delete":       func(r *rig) { r.objects.err["delete"] = errBoom },
		"rawdeletable": func(r *rig) { r.windows.err["rawdeletable"] = errBoom },
		"rawdeleted":   func(r *rig) { r.windows.err["rawdeleted"] = errBoom },
		"sync":         func(r *rig) { r.tables.err["sync"] = errBoom },
		"prune":        func(r *rig) { r.windows.err["prune"] = errBoom },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			exp := r.windows.add("esp", old, 1)
			exp.compacted, exp.compactedGen = true, 1
			raw := r.windows.add("esp", window.Add(-10*24*time.Hour), 1)
			raw.compacted, raw.compactedGen = true, 1
			r.segment(t, raw.Start, segAt{"a", 1, 0}, "a")
			breakIt(r)
			r.w.Retention(ctx)
		})
	}
}

func TestLifecycle(t *testing.T) {
	assert.Nil(t, New(Tuning{}, Deps{}))
	var nilWorker *Worker
	nilWorker.Start(ctx)
	nilWorker.Stop()

	r := newRig(t)
	r.w.tuning.Poll = time.Millisecond
	r.w.Start(ctx)
	r.w.Stop()
	r.w.Stop()

	cctx, cancel := context.WithCancel(ctx)
	r2 := newRig(t)
	r2.w.Start(cctx)
	cancel()
	<-r2.w.done

	tn := Tuning{}.withDefaults()
	assert.Equal(t, DefaultPoll, tn.Poll)
	assert.Equal(t, DefaultBatch, tn.Batch)
	assert.Equal(t, "", dirOf("flat"))
}

func TestParquetRoundTrip(t *testing.T) {
	in := []whevent.Stored{{
		Event: whevent.Event{
			ReceivedAt: window.Add(123456 * time.Microsecond), EventID: "e", EventType: "t", Key: "k",
			ContentHash: "h", Replica: "r", Payload: []byte(`{"a":1}`),
		},
		LandedAt: window.Add(time.Second),
	}, {Event: whevent.Event{EventID: "empty", ReceivedAt: window}, LandedAt: window}}
	data, err := encodeWindow(in)
	require.NoError(t, err)
	out, err := decodeWindow(data)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, in[0].ReceivedAt, out[0].ReceivedAt)
	assert.Equal(t, in[0].LandedAt, out[0].LandedAt)
	assert.Equal(t, in[0].Payload, out[0].Payload)
	assert.Equal(t, "k", out[0].Key)
	assert.Equal(t, "empty", out[1].EventID)

	_, err = decodeWindow([]byte("not parquet"))
	assert.Error(t, err)
}

func TestDecodeHourRefusesAFileWithoutTheColumns(t *testing.T) {
	typ, err := tabletype.Parse("varchar")
	require.NoError(t, err)
	other, err := tableparquet.Write([]tabletype.Column{{Name: "something_else", Type: typ}}, [][]any{{"x"}})
	require.NoError(t, err)
	_, err = decodeWindow(other)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no column received_at")
}

func TestDecodeHourReadsNullsAsEmpty(t *testing.T) {
	data, err := tableparquet.Write(windowColumns, [][]any{{nil, nil, "id", nil, nil, nil, nil, nil}})
	require.NoError(t, err)
	out, err := decodeWindow(data)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "id", out[0].EventID)
	assert.True(t, out[0].ReceivedAt.IsZero())
	assert.Empty(t, out[0].Key)
}

func TestParquetRoundTripAcrossReadBatches(t *testing.T) {
	in := make([]whevent.Stored, 600)
	for i := range in {
		in[i] = whevent.Stored{Event: whevent.Event{EventID: strconv.Itoa(i), ReceivedAt: window}, LandedAt: window}
	}
	data, err := encodeWindow(in)
	require.NoError(t, err)
	out, err := decodeWindow(data)
	require.NoError(t, err)
	require.Len(t, out, 600, "every row is read, however many read batches it takes")
	assert.Equal(t, "599", out[599].EventID)
}
