package receiver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
)

// spoolHarness is a receiver with a spool at dir, over objects. Each one is
// a process: stop ends it the way a replica stopping does.
type spoolHarness struct {
	*harness
	spool *whspool.Spool
	done  bool
}

func newSpoolHarness(t *testing.T, dir string, objects *fakeObjects, opts whspool.Options, sources ...whsource.Source) *spoolHarness {
	t.Helper()
	opts.Dir, opts.Replica = dir, "host-a:8080"
	spool, err := whspool.Open(opts)
	require.NoError(t, err)
	h := &harness{
		objects: objects, recorder: newRecorder(), metrics: &fakeMetrics{requests: map[string]int{}},
		sources: &staticSources{list: sources}, raw: &fakeRawWindows{called: make(chan error, 256)}, logs: &logBuffer{},
	}
	h.r = New(Config{
		Sources: h.sources, Objects: h.objects, Bucket: "managed-resources", Recorder: h.recorder, RawWindows: h.raw,
		Metrics: h.metrics, Replica: "host-a:8080", Logger: slog.New(slog.NewTextHandler(h.logs, nil)),
		WriteTimeout: 500 * time.Millisecond, Refresh: time.Hour, StatsFlush: time.Hour, RegisterRetry: 10 * time.Millisecond,
		MissRefresh: 20 * time.Millisecond, Spool: spool, SpoolLimits: SpoolLimits{MaxBytes: opts.MaxBytes, MaxAge: opts.MaxAge},
	})
	h.r.Start(context.Background())
	h.srv = httptest.NewServer(h.r)
	sh := &spoolHarness{harness: h, spool: spool}
	t.Cleanup(sh.stop)
	return sh
}

// stop ends the process: the listener, the receiver, then the spool.
func (s *spoolHarness) stop() {
	if s.done {
		return
	}
	s.done = true
	s.srv.Close()
	s.r.Stop()
	_ = s.spool.Close()
}

// eventIDs are the ids of every event in object storage, sorted, with each
// one as many times as it was stored.
func eventIDs(t *testing.T, objects *fakeObjects) []string {
	t.Helper()
	evs := objects.events(t)
	ids := make([]string, 0, len(evs))
	for _, e := range evs {
		ids = append(ids, e.EventID)
	}
	sort.Strings(ids)
	return ids
}

// eventsAt are events with the given ids, received at at.
func eventsAt(at time.Time, ids ...string) []whevent.Event {
	out := make([]whevent.Event, 0, len(ids))
	for _, id := range ids {
		out = append(out, whevent.Event{ReceivedAt: at, EventID: id, EventType: "open", Replica: "host-a:8080", Payload: []byte(body(id))})
	}
	return out
}

func body(id string) string { return `{"id":"` + id + `","type":"open"}` }

// With object storage stalled, a request is answered once its events are on
// this replica's disk, and they land once storage answers again (#2121).
func TestSpool_AnswersWhileObjectStorageStalls(t *testing.T) {
	objects := newObjects()
	stall := make(chan struct{})
	objects.stall = stall
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{}, hmacSource("esp"))

	began := time.Now()
	for i := range 3 {
		require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body(fmt.Sprint(i))).StatusCode)
	}
	assert.Less(t, time.Since(began), 2*time.Second, "the answer waited on object storage")
	assert.Zero(t, objects.count(), "nothing reached object storage while it stalled")
	assert.Equal(t, int64(3), h.spool.Stats().Events)

	objects.mu.Lock()
	objects.stall = nil
	objects.mu.Unlock()
	close(stall)
	require.Eventually(t, func() bool { return len(eventIDs(t, objects)) == 3 }, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, []string{"0", "1", "2"}, eventIDs(t, objects))
	require.Eventually(t, func() bool { return h.spool.Stats().Files == 0 }, 5*time.Second, 20*time.Millisecond,
		"a drained file is removed")
	assert.Equal(t, 3, h.metrics.requests["esp/accepted"])
}

// A replica stopped while object storage refuses keeps what it acknowledged
// on disk; the next process drains it, and every event lands once.
func TestSpool_ARestartDrainsWhatWasAcknowledged(t *testing.T) {
	dir := t.TempDir()
	objects := newObjects()
	objects.fail = errors.New("object store unavailable")
	first := newSpoolHarness(t, dir, objects, whspool.Options{}, hmacSource("esp"))
	for i := range 4 {
		require.Equal(t, http.StatusAccepted, first.signed(t, "esp", body(fmt.Sprint(i))).StatusCode)
	}
	require.Eventually(t, func() bool {
		first.metrics.mu.Lock()
		defer first.metrics.mu.Unlock()
		return first.metrics.drainFail > 0
	}, 5*time.Second, 20*time.Millisecond, "the drain was tried and failed")
	first.stop()
	assert.Zero(t, objects.count())

	objects.mu.Lock()
	objects.fail = nil
	objects.mu.Unlock()
	second := newSpoolHarness(t, dir, objects, whspool.Options{}, hmacSource("esp"))
	require.Eventually(t, func() bool { return len(eventIDs(t, objects)) == 4 }, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, []string{"0", "1", "2", "3"}, eventIDs(t, objects))
	require.Eventually(t, func() bool { return second.spool.Stats().Files == 0 }, 5*time.Second, 20*time.Millisecond)
}

// A file drained and then left behind -- the process died between writing
// its segments and removing it -- is drained again under the same names, so
// it overwrites what the first drain wrote rather than adding to it.
func TestSpool_DrainingAFileTwiceStoresItOnce(t *testing.T) {
	dir := t.TempDir()
	objects := newObjects()
	src := hmacSource("esp")

	spool, err := whspool.Open(whspool.Options{Dir: dir, Replica: "host-a:8080"})
	require.NoError(t, err)
	at := time.Now().UTC()
	_, err = spool.Append("esp", whspool.Record{Hash: "h", Events: eventsAt(at, "a", "b")})
	require.NoError(t, err)
	require.NoError(t, spool.Seal("esp"))
	seg := spool.Sealed("esp")[0]
	path := filepath.Join(dir, "esp", fmt.Sprintf("%s-%010d.wal", seg.Writer, seg.Seq))
	kept, err := os.ReadFile(path) // #nosec G304 -- the test's own temporary spool
	require.NoError(t, err)
	require.NoError(t, spool.Close())

	first := newSpoolHarness(t, dir, objects, whspool.Options{}, src)
	require.Eventually(t, func() bool { return first.spool.Stats().Files == 0 }, 5*time.Second, 20*time.Millisecond)
	first.stop()
	keys := objects.count()
	require.Positive(t, keys)

	require.NoError(t, os.WriteFile(path, kept, 0o600)) // #nosec G703 -- the test's own temporary spool
	second := newSpoolHarness(t, dir, objects, whspool.Options{}, src)
	require.Eventually(t, func() bool { return second.spool.Stats().Files == 0 }, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, keys, objects.count(), "a second drain wrote new objects")
	assert.Equal(t, []string{"a", "b"}, eventIDs(t, objects))
}

// A full spool answers 503 with Retry-After, so the sender retries, and
// stores nothing for the request it refused.
func TestSpool_AFullSpoolAsksTheSenderToRetry(t *testing.T) {
	objects := newObjects()
	objects.stall = make(chan struct{})
	// One request's record is a few hundred bytes; the bound holds one.
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{MaxBytes: 500}, hmacSource("esp"))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body("1")).StatusCode)
	require.Greater(t, h.spool.Stats().Bytes, int64(250), "the bound must hold one record and not two")
	resp := h.signed(t, "esp", body("2"))
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "5", resp.Header.Get("Retry-After"))
	assert.Equal(t, 1, h.metrics.requests["esp/"+OutcomeSpoolFull])
	assert.Equal(t, int64(1), h.spool.Stats().Events)
	close(objects.stall)
}

// The stats flush records the spool's state for the admin page and reports
// it as metrics.
func TestSpool_StateIsRecorded(t *testing.T) {
	objects := newObjects()
	objects.stall = make(chan struct{})
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{MaxBytes: 1 << 20, MaxAge: time.Hour}, hmacSource("esp"))
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body("1")).StatusCode)
	require.NoError(t, h.r.flushStats(context.Background()))

	h.recorder.mu.Lock()
	require.Len(t, h.recorder.spool, 1)
	st := h.recorder.spool[0]
	h.recorder.mu.Unlock()
	assert.Equal(t, "host-a:8080", st.Replica)
	assert.Equal(t, h.spool.Dir(), st.Dir)
	assert.Equal(t, int64(1), st.Events)
	assert.NotNil(t, st.Oldest)
	assert.Equal(t, int64(1<<20), st.MaxBytes)
	assert.Equal(t, time.Hour, st.MaxAge)
	assert.Equal(t, 1, h.metrics.spoolSeen)
	close(objects.stall)
}

// A request whose body held no events is answered without a spool write.
func TestSpool_NoEventsIsAnsweredWithoutAWrite(t *testing.T) {
	src := hmacSource("esp")
	src.Config.Split = "$.items"
	h := newSpoolHarness(t, t.TempDir(), newObjects(), whspool.Options{}, src)
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", `{"items":[]}`).StatusCode)
	assert.Zero(t, h.spool.Stats().Files)
}

// failingSpool is a spool whose appends fail with err.
type failingSpool struct {
	*whspool.Spool
	err error
}

func (f failingSpool) Append(string, whspool.Record) (time.Duration, error) { return 0, f.err }

// An append the spool refuses for a reason other than its bound is a write
// that failed, answered 503 with Retry-After; one refused because the spool
// is closed says the platform is shutting down.
func TestSpool_AFailedAppendIsAnsweredWithRetry(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, reason string
		err                   error
	}{
		{"disk", OutcomeWriteFailed, "could not be written to the receiver's spool", errors.New("no space left on device")},
		{"closed", OutcomeWriteFailed, "shutting down", whspool.ErrClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpoolHarness(t, t.TempDir(), newObjects(), whspool.Options{}, hmacSource("esp"))
			h.r.cfg.Spool = failingSpool{Spool: h.spool, err: tc.err}
			resp := h.signed(t, "esp", body("1"))
			assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
			assert.NotEmpty(t, resp.Header.Get("Retry-After"))
			assert.Equal(t, 1, h.metrics.requests["esp/"+tc.outcome])
			assert.Contains(t, strings.Join(h.logs.lines("request not accepted"), "\n"), tc.reason)
		})
	}
}

// The drainer backs off after a failed drain, drains at once when a sealed
// file waits, waits until the active file is due by the source's flush
// interval, and an hour when nothing is spooled. The drainer measured is one
// of its own, so nothing else reads its state while the test sets it.
func TestSpool_DrainerPacing(t *testing.T) {
	src := hmacSource("esp")
	src.Config.FlushMaxIntervalMS = 60_000
	objects := newObjects()
	objects.stall = make(chan struct{})
	defer close(objects.stall)
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{}, src)
	d := &drainer{source: "esp", r: h.r}
	assert.Equal(t, time.Hour, d.nextWait(), "nothing spooled")

	_, err := h.spool.Append("esp", whspool.Record{Hash: "h", Events: eventsAt(time.Now(), "a")})
	require.NoError(t, err)
	wait := d.nextWait()
	assert.Greater(t, wait, 30*time.Second, "an active file not yet due waits for the flush interval")
	assert.LessOrEqual(t, wait, time.Minute)

	require.NoError(t, h.spool.Seal("esp"))
	assert.Zero(t, d.nextWait(), "a sealed file waits")

	d.failures = 3
	assert.Equal(t, 4*time.Second, d.nextWait())
	d.failures = 40
	assert.Equal(t, drainRetryMax, d.nextWait())
}

// A source deleted while its events waited in the spool has had its data
// removed with it: its file is discarded, saying so, instead of failing every
// drain and holding the spool for every other source. A source the store
// still has, or a store that cannot be read, is never taken for deleted.
func TestSpool_ADeletedSourcesFileIsDiscarded(t *testing.T) {
	objects := newObjects()
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{}, hmacSource("esp"))
	_, err := h.spool.Append("gone", whspool.Record{Hash: "h", Events: eventsAt(time.Now(), "a")})
	require.NoError(t, err)
	require.NoError(t, h.spool.Seal("gone"))
	d := h.r.drainerFor("gone")
	require.NotNil(t, d)
	d.wake()
	// The file is removed, then the line is written: both are waited for.
	require.Eventually(t, func() bool {
		return h.spool.Stats().Files == 0 && len(h.logs.lines("spooled events of a deleted source discarded")) > 0
	}, 5*time.Second, 20*time.Millisecond)
	assert.Zero(t, objects.count(), "nothing was written for a deleted source")

	assert.False(t, h.r.sourceDeleted(context.Background(), "esp"), "a served source")
	h.sources.mu.Lock()
	h.sources.err = errors.New("database down")
	h.sources.mu.Unlock()
	assert.False(t, h.r.sourceDeleted(context.Background(), "unknown"), "a store that cannot be read is no proof")
}
