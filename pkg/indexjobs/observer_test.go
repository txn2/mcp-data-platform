package indexjobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingObserver is an Observer that keeps every report, so a test can
// assert what the queue told the metrics backend.
type recordingObserver struct {
	mu       sync.Mutex
	enqueued []string // "kind/trigger/created"
	started  []string
	finished []string // "kind/trigger/outcome"
	items    []string // "kind/embedded/reused"
	calls    []string // "kind/texts/status"
	released []int
	deferred map[string]int
}

func (o *recordingObserver) IndexJobEnqueued(_ context.Context, kind, trigger string, created bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.enqueued = append(o.enqueued, fmt.Sprintf("%s/%s/%v", kind, trigger, created))
}

func (o *recordingObserver) IndexJobStarted(_ context.Context, kind string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, kind)
}

func (o *recordingObserver) IndexJobFinished(_ context.Context, kind, trigger, outcome string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d < 0 {
		panic("negative duration")
	}
	o.finished = append(o.finished, kind+"/"+trigger+"/"+outcome)
}

func (o *recordingObserver) IndexJobItems(_ context.Context, kind string, embedded, reused int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = append(o.items, fmt.Sprintf("%s/%d/%d", kind, embedded, reused))
}

func (o *recordingObserver) IndexEmbedCall(_ context.Context, kind string, texts int, status string, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, fmt.Sprintf("%s/%d/%s", kind, texts, status))
}

func (o *recordingObserver) IndexLeasesReleased(_ context.Context, released int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.released = append(o.released, released)
}

func (o *recordingObserver) IndexUnitsDeferred(_ context.Context, kind string, deferred int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.deferred == nil {
		o.deferred = map[string]int{}
	}
	o.deferred[kind] += deferred
}

// settleStore fails the settling writes on demand, for the lease_lost and
// store_error outcomes.
type settleStore struct {
	recordingStore
	completeErr error
	retryErr    error
	failErr     error
}

func (s *settleStore) Complete(ctx context.Context, id int64, w string) error {
	_ = s.recordingStore.Complete(ctx, id, w)
	return s.completeErr
}

func (s *settleStore) Retry(ctx context.Context, id int64, w, m string) error {
	_ = s.recordingStore.Retry(ctx, id, w, m)
	return s.retryErr
}

func (s *settleStore) Fail(ctx context.Context, id int64, w, m string) error {
	_ = s.recordingStore.Fail(ctx, id, w, m)
	return s.failErr
}

func observedWorker(store Store, reg *Registry, obs Observer, emb *fakeEmbedder) *Worker {
	return NewWorker(WorkerConfig{Store: store, Registry: reg, Embedder: emb, WorkerID: "w1", Observer: obs})
}

// TestWorker_ReportsEveryOutcome pins what one claimed job reports, for each
// way a pass can end (#1837): started once, finished once, with the outcome
// that names what the settling write did.
func TestWorker_ReportsEveryOutcome(t *testing.T) {
	t.Parallel()
	gone := fmt.Errorf("deleted: %w", ErrSourceGone)
	tests := []struct {
		name     string
		src      Source
		sink     *stubSink
		store    *settleStore
		attempts int
		kindReg  bool
		want     string
	}{
		{"success", &stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k"}, &settleStore{}, 1, true, OutcomeSucceeded},
		{"retryable error", &stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k", upErr: errors.New("x")}, &settleStore{}, 1, true, OutcomeRetried},
		{"attempts exhausted", &stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k", upErr: errors.New("x")}, &settleStore{}, MaxAttempts, true, OutcomeFailed},
		{"unreadable source", &stubSource{kind: "k", err: errors.New("x")}, &stubSink{kind: "k"}, &settleStore{}, 1, true, OutcomeFailed},
		{"unregistered kind", nil, nil, &settleStore{}, 1, false, OutcomeFailed},
		{"source gone", &stubSource{kind: "k", err: gone}, &stubSink{kind: "k"}, &settleStore{}, 1, true, OutcomeSourceGone},
		{"lease rotated before complete", &stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k"}, &settleStore{completeErr: ErrNotFound}, 1, true, OutcomeLeaseLost},
		{"complete write failed", &stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k"}, &settleStore{completeErr: errors.New("db down")}, 1, true, OutcomeStoreError},
		{"retry write failed", &stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k", upErr: errors.New("x")}, &settleStore{retryErr: errors.New("db down")}, 1, true, OutcomeStoreError},
		{"fail write lost the lease", &stubSource{kind: "k", err: errors.New("x")}, &stubSink{kind: "k"}, &settleStore{failErr: ErrNotFound}, 1, true, OutcomeLeaseLost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := NewRegistry()
			if tt.kindReg {
				require.NoError(t, reg.Register(tt.src, tt.sink))
			}
			obs := &recordingObserver{}
			w := observedWorker(tt.store, reg, obs, newFakeEmbedder())
			job := writeJob("k")
			job.Attempts = tt.attempts

			w.process(context.Background(), job)

			assert.Equal(t, []string{"k"}, obs.started)
			assert.Equal(t, []string{"k/write/" + tt.want}, obs.finished)
		})
	}
}

// TestWorker_ReportsThePlanAndEachEmbedCall: a pass with one reusable vector
// and one changed item reports one embedded and one reused, and the single
// batch call that embedded it.
func TestWorker_ReportsThePlanAndEachEmbedCall(t *testing.T) {
	t.Parallel()
	items := twoItems()
	emb := newFakeEmbedder()
	kept := make([]float32, emb.dim)
	snk := &stubSink{kind: "k", existing: map[string]Vector{
		"a": {ItemID: "a", TextHash: TextHash(items[0].Text), Embedding: kept, Dim: emb.dim},
	}}
	obs := &recordingObserver{}
	w := observedWorker(&recordingStore{}, registryWith(&stubSource{kind: "k", items: items}, snk), obs, emb)

	w.process(context.Background(), writeJob("k"))

	assert.Equal(t, []string{"k/1/1"}, obs.items)
	assert.Equal(t, []string{"k/1/ok"}, obs.calls)
	assert.Equal(t, []string{"k/write/succeeded"}, obs.finished)
}

// TestWorker_ReportsAFailedEmbedCall: the provider's error is reported as an
// error call, and the pass as retried.
func TestWorker_ReportsAFailedEmbedCall(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	emb.failBatch.Store(true)
	obs := &recordingObserver{}
	w := observedWorker(&recordingStore{},
		registryWith(&stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k"}), obs, emb)

	w.process(context.Background(), writeJob("k"))

	assert.Equal(t, []string{"k/2/error"}, obs.calls)
	assert.Equal(t, []string{"k/write/retried"}, obs.finished)
}

// TestWorker_NoObserverRecordsNothing: a worker built without an observer runs
// the same pass (the nil check is the only difference).
func TestWorker_NoObserverRecordsNothing(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(&stubSource{kind: "k", items: twoItems()}, &stubSink{kind: "k"}))
	w.process(context.Background(), writeJob("k"))
	assert.True(t, store.completed)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

func TestEmbedStatus(t *testing.T) {
	t.Parallel()
	assert.Equal(t, EmbedStatusOK, embedStatus(nil))
	assert.Equal(t, EmbedStatusTimeout, embedStatus(context.DeadlineExceeded))
	assert.Equal(t, EmbedStatusTimeout, embedStatus(fmt.Errorf("post: %w", timeoutError{})))
	assert.Equal(t, EmbedStatusError, embedStatus(context.Canceled))
	assert.Equal(t, EmbedStatusError, embedStatus(errors.New("500")))
}

// TestStore_ObservesEnqueue: a created row and a folded one are both reported,
// with the trigger that asked.
func TestStore_ObservesEnqueue(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	obs := &recordingObserver{}
	s := NewPostgresStore(db, WithObserver(obs))

	mock.ExpectQuery("INSERT INTO index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT INTO index_jobs").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("INSERT INTO index_jobs").WillReturnError(errors.New("db down"))

	_, err = s.Enqueue(context.Background(), Key{SourceKind: "calls", SourceID: "1"}, TriggerWrite)
	require.NoError(t, err)
	_, err = s.Enqueue(context.Background(), Key{SourceKind: "calls", SourceID: "1"}, TriggerReconciler)
	require.NoError(t, err)
	_, err = s.Enqueue(context.Background(), Key{SourceKind: "calls", SourceID: "2"}, TriggerWrite)
	require.Error(t, err)

	assert.Equal(t, []string{"calls/write/true", "calls/reconciler/false"}, obs.enqueued,
		"a failed insert enqueued nothing and reports nothing")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_ObservesReleasedLeases: a sweep that released rows reports them;
// one that released none reports nothing.
func TestStore_ObservesReleasedLeases(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	obs := &recordingObserver{}
	s := NewPostgresStore(db, WithObserver(obs))
	mock.ExpectExec("UPDATE index_jobs").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("UPDATE index_jobs").WillReturnResult(sqlmock.NewResult(0, 0))

	n, err := s.ReleaseExpiredLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	_, err = s.ReleaseExpiredLeases(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []int{3}, obs.released)
}

// TestReconciler_ObservesDeferredUnits: the parked units a sweep skipped are
// reported per kind.
func TestReconciler_ObservesDeferredUnits(t *testing.T) {
	t.Parallel()
	store := &parkStore{candidates: []ParkCandidate{
		{Key: Key{SourceKind: "k", SourceID: "a"}, Occurrences: ParkThreshold, LastFailedAt: time.Now()},
		{Key: Key{SourceKind: "k", SourceID: "b"}, Occurrences: ParkThreshold, LastFailedAt: time.Now()},
	}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"a", "b", "c"}})
	obs := &recordingObserver{}

	NewReconciler(store, reg, time.Second, WithReconcilerObserver(obs)).reconcileOnce()

	assert.Equal(t, map[string]int{"k": 2}, obs.deferred)
	assert.Equal(t, []string{"c"}, keyIDs(store.enqueuedKeys()))
}

func keyIDs(keys []Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.SourceID)
	}
	return out
}

// TestStore_QueueDepth scans the one cross-kind statement into per-kind open
// work, and returns an empty (not nil) slice when nothing is open.
func TestStore_QueueDepth(t *testing.T) {
	t.Parallel()
	s, mock, done := newMockStore(t)
	defer done()
	cols := []string{"source_kind", "pending", "running", "retrying", "failed_units", "wait"}
	mock.ExpectQuery(`FROM index_jobs\s+WHERE status IN \('pending', 'running'\)\s+OR \(status = 'failed' AND resolved_at IS NULL\)\s+GROUP BY source_kind`).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("calls", 2558, 2, 7, 1, 12.5).
			AddRow("resources", 1992, 2, 0, 0, 0.0))
	mock.ExpectQuery("GROUP BY source_kind").WillReturnRows(sqlmock.NewRows(cols))
	mock.ExpectQuery("GROUP BY source_kind").WillReturnError(errors.New("db down"))

	got, err := s.QueueDepth(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, QueueDepth{
		SourceKind: "calls", Pending: 2558, Running: 2, Retrying: 7,
		FailedUnits: 1, OldestRunnableWait: 12500 * time.Millisecond,
	}, got[0])
	assert.Equal(t, "resources", got[1].SourceKind)
	assert.Zero(t, got[1].OldestRunnableWait)

	empty, err := s.QueueDepth(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	_, err = s.QueueDepth(context.Background())
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_ListRetrying: the Retrying filter narrows to pending jobs with an
// attempt behind them, alongside any other predicate.
func TestStore_ListRetrying(t *testing.T) {
	t.Parallel()
	where, args := buildListPredicates(ListFilter{SourceKind: "calls", Retrying: true})
	assert.Equal(t, " WHERE source_kind = $1 AND status = 'pending' AND attempts > 0", where)
	assert.Equal(t, []any{"calls"}, args)

	where, args = buildListPredicates(ListFilter{Retrying: true})
	assert.Equal(t, " WHERE status = 'pending' AND attempts > 0", where)
	assert.Empty(t, args)
}
