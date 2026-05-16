package embedjobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStore is an in-memory Store stand-in for worker / reaper /
// reconciler tests. The Postgres store has its own sqlmock
// suite; this fake exists to drive end-to-end behavior on the
// in-process types without spinning Docker.
type fakeStore struct {
	mu     sync.Mutex
	jobs   []*Job
	nextID int64

	claimErr      error
	completeCalls int32
	failCalls     int32
	retryCalls    int32

	releasedTotal int
	reconcileFunc func() (int, error)
}

func newFakeStore() *fakeStore { return &fakeStore{} }

func (s *fakeStore) Enqueue(_ context.Context, key SpecKey, kind Kind) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.CatalogID == key.CatalogID && j.SpecName == key.SpecName &&
			(j.Status == StatusPending || j.Status == StatusRunning) {
			return false, nil
		}
	}
	s.nextID++
	s.jobs = append(s.jobs, &Job{
		ID:        s.nextID,
		CatalogID: key.CatalogID,
		SpecName:  key.SpecName,
		Kind:      kind,
		Status:    StatusPending,
		NextRunAt: time.Now(),
		CreatedAt: time.Now(),
	})
	return true, nil
}

func (s *fakeStore) Claim(_ context.Context, workerID string) (*Job, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, j := range s.jobs {
		if j.Status == StatusPending && !j.NextRunAt.After(now) {
			j.Status = StatusRunning
			j.WorkerID = workerID
			j.Attempts++
			started := now
			j.StartedAt = &started
			lease := now.Add(LeaseDuration)
			j.LeaseExpiresAt = &lease
			cp := *j
			return &cp, nil
		}
	}
	return nil, ErrNoJob
}

func (s *fakeStore) Complete(_ context.Context, id int64, workerID string) error {
	atomic.AddInt32(&s.completeCalls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id && j.Status == StatusRunning && j.WorkerID == workerID {
			j.Status = StatusSucceeded
			now := time.Now()
			j.CompletedAt = &now
			return nil
		}
	}
	return ErrNotFound
}

func (s *fakeStore) Retry(_ context.Context, id int64, workerID, errMsg string) error {
	atomic.AddInt32(&s.retryCalls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id && j.Status == StatusRunning && j.WorkerID == workerID {
			j.Status = StatusPending
			j.LastError = errMsg
			j.WorkerID = ""
			j.LeaseExpiresAt = nil
			j.NextRunAt = time.Now().Add(time.Duration(computeBackoffSeconds(j.Attempts)) * time.Second)
			return nil
		}
	}
	return ErrNotFound
}

func (s *fakeStore) Fail(_ context.Context, id int64, workerID, errMsg string) error {
	atomic.AddInt32(&s.failCalls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id && j.Status == StatusRunning && j.WorkerID == workerID {
			j.Status = StatusFailed
			j.LastError = errMsg
			now := time.Now()
			j.CompletedAt = &now
			return nil
		}
	}
	return ErrNotFound
}

func (s *fakeStore) ReleaseExpiredLeases(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	released := 0
	for _, j := range s.jobs {
		if j.Status == StatusRunning && j.LeaseExpiresAt != nil && !j.LeaseExpiresAt.After(now) {
			j.Status = StatusPending
			j.WorkerID = ""
			j.LeaseExpiresAt = nil
			released++
		}
	}
	s.releasedTotal += released
	return released, nil
}

func (s *fakeStore) ReconcileGaps(_ context.Context) (int, error) {
	if s.reconcileFunc != nil {
		return s.reconcileFunc()
	}
	return 0, nil
}

func (s *fakeStore) Get(_ context.Context, id int64) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id {
			cp := *j
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *fakeStore) List(_ context.Context, _ ListFilter) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out, nil
}

func (s *fakeStore) SpecStatuses(_ context.Context, _ string) ([]SpecStatusRow, error) {
	return nil, nil
}

func (s *fakeStore) Health(_ context.Context, catalogID string) (*CatalogHealth, error) {
	return &CatalogHealth{CatalogID: catalogID}, nil
}

// fakeResolver is the test SpecResolver: returns a fixed
// content for the (catalog, spec) keys the test enqueues.
type fakeResolver struct {
	contents map[string]string
	err      error
}

func (r *fakeResolver) GetSpecContent(_ context.Context, catalogID, specName string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.contents[catalogID+"/"+specName], nil
}

// fakeComputer returns one synthetic embedding per operation
// the test asks for. Counts invocations so tests can assert
// "exactly one call per attempt."
type fakeComputer struct {
	calls atomic.Int32
	err   error
	rows  []ComputedEmbedding
}

func (c *fakeComputer) Compute(_ context.Context, _ string, _ string, _ map[string]ExistingEmbedding) ([]ComputedEmbedding, error) {
	c.calls.Add(1)
	if c.err != nil {
		return nil, c.err
	}
	return c.rows, nil
}

// fakePersister stores Upsert calls for inspection.
type fakePersister struct {
	mu            sync.Mutex
	upserts       []ComputedEmbedding
	existing      map[string]ExistingEmbedding
	stampedCounts map[string]int
	listErr       error
	upErr         error
}

func (p *fakePersister) ListExisting(_ context.Context, _, _ string) (map[string]ExistingEmbedding, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	if p.existing == nil {
		return map[string]ExistingEmbedding{}, nil
	}
	out := make(map[string]ExistingEmbedding, len(p.existing))
	for k, v := range p.existing {
		out[k] = v
	}
	return out, nil
}

func (p *fakePersister) Upsert(_ context.Context, _, _ string, rows []ComputedEmbedding) error {
	if p.upErr != nil {
		return p.upErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.upserts = append(p.upserts, rows...)
	return nil
}

// stampedCounts records every successful StampOperationCount
// call so tests can assert that the worker closes the
// reconciler convergence loop.
func (p *fakePersister) StampOperationCount(_ context.Context, catalogID, specName string, count int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stampedCounts == nil {
		p.stampedCounts = make(map[string]int)
	}
	p.stampedCounts[catalogID+"/"+specName] = count
	return nil
}

// fakeReloader records calls so tests can prove the worker
// nudged live connections after a successful embed.
type fakeReloader struct {
	calls atomic.Int32
}

func (r *fakeReloader) ReloadConnectionsByCatalog(_ string) {
	r.calls.Add(1)
}

// TestWorker_DrainsQueueOnStart proves a worker that starts
// with jobs in the queue picks them up immediately (without
// waiting for the poll tick or a notification). drainQueue
// loops until ErrNoJob.
func TestWorker_DrainsQueueOnStart(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "a"}, KindSpecWrite)
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "b"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/a": "spec-a", "c/b": "spec-b"}}
	computer := &fakeComputer{rows: []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}}}
	persister := &fakePersister{}
	reloader := &fakeReloader{}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister, Reloader: reloader,
		WorkerID: "test-worker", PollEvery: 100 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if computer.calls.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := computer.calls.Load(); got != 2 {
		t.Errorf("computer called %d times; want 2", got)
	}
	if got := reloader.calls.Load(); got != 2 {
		t.Errorf("reloader called %d times; want 2", got)
	}
	if got := atomic.LoadInt32(&store.completeCalls); got != 2 {
		t.Errorf("Complete called %d times; want 2", got)
	}
}

// TestWorker_RetryableErrorRetries proves a compute failure on
// an attempt below MaxAttempts hits Retry, not Fail.
func TestWorker_RetryableErrorRetries(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &fakeComputer{err: errors.New("provider down")}
	persister := &fakePersister{}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&store.retryCalls) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&store.retryCalls); got == 0 {
		t.Error("Retry was never called on retryable error")
	}
	if got := atomic.LoadInt32(&store.failCalls); got != 0 {
		t.Errorf("Fail called %d times before max attempts; want 0", got)
	}
}

// TestWorker_TerminalErrorAtMaxAttempts proves a compute
// failure on the MaxAttempts'th try moves the job to failed.
func TestWorker_TerminalErrorAtMaxAttempts(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	// Pre-set attempts so the next claim brings us to MaxAttempts.
	store.mu.Lock()
	store.jobs[0].Attempts = MaxAttempts - 1
	store.mu.Unlock()

	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &fakeComputer{err: errors.New("permanent")}
	persister := &fakePersister{}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&store.failCalls) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&store.failCalls); got != 1 {
		t.Errorf("Fail called %d times; want 1", got)
	}
}

// TestWorker_ManualRetrySkipsDedup proves a manual_retry job
// bypasses the text-hash dedup path so vectors are recomputed
// fresh. Operators reach for this when the embedding model
// changed externally (same model name, different behavior),
// and the documented escape hatch must actually replace stale
// vectors. Without the kind check the worker would call
// ListExisting, the computer would dedup, and the manual
// retry would be a no-op.
type recordingPersister struct {
	fakePersister
	listCalled int
}

func (p *recordingPersister) ListExisting(ctx context.Context, catalogID, specName string) (map[string]ExistingEmbedding, error) {
	p.listCalled++
	return p.fakePersister.ListExisting(ctx, catalogID, specName)
}

func TestWorker_ManualRetrySkipsDedup(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindManualRetry)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &fakeComputer{rows: []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}}}
	persister := &recordingPersister{}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if computer.calls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if computer.calls.Load() == 0 {
		t.Fatal("worker did not run compute")
	}
	if persister.listCalled != 0 {
		t.Errorf("manual_retry should skip ListExisting; got %d calls", persister.listCalled)
	}
}

// TestWorker_StampsOperationCount proves the worker closes the
// reconciler convergence loop: after a successful embed, the
// spec's operation_count is updated to len(rows) so the next
// reconciler tick sees a fully-indexed spec. Without this,
// legacy specs (operation_count default 0) re-enqueue forever.
func TestWorker_StampsOperationCount(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &fakeComputer{rows: []ComputedEmbedding{
		{OperationID: "a", Dim: 4, Embedding: []float32{1, 0, 0, 0}},
		{OperationID: "b", Dim: 4, Embedding: []float32{0, 1, 0, 0}},
		{OperationID: "c", Dim: 4, Embedding: []float32{0, 0, 1, 0}},
	}}
	persister := &fakePersister{}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		persister.mu.Lock()
		stamped := len(persister.stampedCounts)
		persister.mu.Unlock()
		if stamped > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	persister.mu.Lock()
	defer persister.mu.Unlock()
	if got := persister.stampedCounts["c/x"]; got != 3 {
		t.Errorf("operation_count stamped %d; want 3 (matches len(rows))", got)
	}
}

// TestWorker_NotifyWakesIdleWorker proves the LISTEN/NOTIFY
// adapter's wake path: a worker waiting on the poll tick drops
// out of the wait when Notify is called.
func TestWorker_NotifyWakesIdleWorker(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	resolver := &fakeResolver{contents: map[string]string{}}
	computer := &fakeComputer{rows: []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}}}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID:  "w1",
		PollEvery: 10 * time.Second, // long poll so wake is via Notify
	})
	w.Start(context.Background())
	defer w.Stop()

	// Initial drain finds nothing; worker is now waiting.
	time.Sleep(50 * time.Millisecond)
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver.contents["c/x"] = "spec"
	w.Notify()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if computer.calls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if computer.calls.Load() == 0 {
		t.Error("Notify did not wake the worker (computer never called)")
	}
}

// TestReaper_ReleasesExpiredLease proves a job whose
// lease_expires_at is in the past gets flipped back to pending
// by the reaper sweep.
func TestReaper_ReleasesExpiredLease(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	past := time.Now().Add(-1 * time.Minute)
	store.jobs = append(store.jobs, &Job{
		ID: 1, CatalogID: "c", SpecName: "x", Status: StatusRunning,
		WorkerID: "dead-pod", LeaseExpiresAt: &past,
	})
	r := NewReaper(store, 30*time.Millisecond)
	r.Start(context.Background())
	defer r.Stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		ready := store.jobs[0].Status == StatusPending
		store.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("reaper never released the expired lease")
}

// TestReaper_LeavesActiveLeaseAlone proves a job whose lease is
// still in the future stays running.
func TestReaper_LeavesActiveLeaseAlone(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	future := time.Now().Add(5 * time.Minute)
	store.jobs = append(store.jobs, &Job{
		ID: 1, CatalogID: "c", SpecName: "x", Status: StatusRunning,
		WorkerID: "live-pod", LeaseExpiresAt: &future,
	})
	r := NewReaper(store, 30*time.Millisecond)
	r.Start(context.Background())
	time.Sleep(150 * time.Millisecond)
	r.Stop()

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.jobs[0].Status != StatusRunning {
		t.Errorf("active lease was disturbed: status=%q", store.jobs[0].Status)
	}
}

// TestReconciler_RunsImmediatelyOnStart proves the initial
// sweep runs before the first tick. A pod that just booted
// converges gaps in the first second, not after the periodic
// interval.
func TestReconciler_RunsImmediatelyOnStart(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	called := atomic.Int32{}
	store.reconcileFunc = func() (int, error) {
		called.Add(1)
		return 0, nil
	}
	rc := NewReconciler(store, time.Hour) // long tick so only the initial run counts
	rc.Start(context.Background())
	defer rc.Stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if called.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("reconciler never ran the initial sweep")
}

// TestReconciler_TicksPeriodically proves the configured
// interval fires repeat sweeps.
func TestReconciler_TicksPeriodically(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	called := atomic.Int32{}
	store.reconcileFunc = func() (int, error) {
		called.Add(1)
		return 0, nil
	}
	rc := NewReconciler(store, 30*time.Millisecond)
	rc.Start(context.Background())
	time.Sleep(200 * time.Millisecond)
	rc.Stop()
	if called.Load() < 3 {
		t.Errorf("reconciler ran %d times; want >=3", called.Load())
	}
}

// TestGenerateWorkerID produces a non-empty unique-shaped id.
// Two concurrent calls produce different ids (hex suffix
// differentiates them even on the same host).
func TestGenerateWorkerID(t *testing.T) {
	t.Parallel()
	a := generateWorkerID()
	b := generateWorkerID()
	if a == "" || b == "" {
		t.Fatal("empty worker id")
	}
	if a == b {
		t.Errorf("workerID collision: %q == %q", a, b)
	}
}

// TestWorker_NotifierContractCompiles is a compile-time check
// that *Worker satisfies the notifier interface. The Listener
// constructor takes notifier values, so a regression that
// breaks the interface would not surface until runtime
// otherwise.
func TestWorker_NotifierContractCompiles(_ *testing.T) {
	var _ notifier = (*Worker)(nil)
}
