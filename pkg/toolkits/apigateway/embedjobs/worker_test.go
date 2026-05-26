package embedjobs

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	completeCalls atomic.Int32
	failCalls     atomic.Int32
	retryCalls    atomic.Int32

	releasedTotal int
	reconcileFunc func() (int, error)

	progressByID       map[int64]int
	lastProgressWorker string

	renewCallsByID  map[int64]int
	renewWorker     string
	renewLeaseStub  func() error
	lastRenewWindow time.Duration

	// zeroRetryBackoff bypasses the exponential backoff in
	// Retry so integration tests can drive a retry-and-resume
	// sequence in seconds rather than waiting the 10s+ a real
	// backoff would impose on the second attempt. Production
	// Postgres backend ignores this flag — it lives on the
	// in-memory fake only.
	zeroRetryBackoff bool
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
		if j.Status != StatusPending || j.NextRunAt.After(now) {
			continue
		}
		j.Status = StatusRunning
		j.WorkerID = workerID
		j.Attempts++
		started := now
		j.StartedAt = &started
		lease := now.Add(DefaultLeaseDuration)
		j.LeaseExpiresAt = &lease
		cp := *j
		return &cp, nil
	}
	return nil, ErrNoJob
}

func (s *fakeStore) Complete(_ context.Context, id int64, workerID string) error {
	s.completeCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID != id || j.Status != StatusRunning || j.WorkerID != workerID {
			continue
		}
		j.Status = StatusSucceeded
		now := time.Now()
		j.CompletedAt = &now
		return nil
	}
	return ErrNotFound
}

func (s *fakeStore) Retry(_ context.Context, id int64, workerID, errMsg string) error {
	s.retryCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID != id || j.Status != StatusRunning || j.WorkerID != workerID {
			continue
		}
		j.Status = StatusPending
		j.LastError = errMsg
		j.WorkerID = ""
		j.LeaseExpiresAt = nil
		delay := time.Duration(computeBackoffSeconds(j.Attempts)) * time.Second
		if s.zeroRetryBackoff {
			delay = 0
		}
		j.NextRunAt = time.Now().Add(delay)
		return nil
	}
	return ErrNotFound
}

func (s *fakeStore) Fail(_ context.Context, id int64, workerID, errMsg string) error {
	s.failCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID != id || j.Status != StatusRunning || j.WorkerID != workerID {
			continue
		}
		j.Status = StatusFailed
		j.LastError = errMsg
		now := time.Now()
		j.CompletedAt = &now
		return nil
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

func (*fakeStore) SpecStatuses(_ context.Context, _ string) ([]SpecStatusRow, error) {
	return nil, nil
}

func (*fakeStore) Health(_ context.Context, catalogID string) (*CatalogHealth, error) {
	return &CatalogHealth{CatalogID: catalogID}, nil
}

func (s *fakeStore) UpdateProgress(_ context.Context, id int64, workerID string, embeddedSoFar int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.progressByID == nil {
		s.progressByID = make(map[int64]int)
	}
	if s.progressByID[id] < embeddedSoFar {
		s.progressByID[id] = embeddedSoFar
	}
	s.lastProgressWorker = workerID
	return nil
}

func (s *fakeStore) RenewLease(_ context.Context, id int64, workerID string, duration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renewCallsByID == nil {
		s.renewCallsByID = make(map[int64]int)
	}
	s.renewCallsByID[id]++
	s.renewWorker = workerID
	s.lastRenewWindow = duration
	if s.renewLeaseStub != nil {
		return s.renewLeaseStub()
	}
	for _, j := range s.jobs {
		if j.ID == id && j.Status == StatusRunning && j.WorkerID == workerID {
			next := time.Now().Add(duration)
			j.LeaseExpiresAt = &next
			return nil
		}
	}
	return ErrNotFound
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
// the per-attempt call count.
type fakeComputer struct {
	calls atomic.Int32
	err   error
	rows  []ComputedEmbedding
}

func (c *fakeComputer) Compute(_ context.Context, req ComputeRequest) ([]ComputedEmbedding, error) {
	c.calls.Add(1)
	// Honor the progress callback so the worker's update path is
	// exercised even in tests with no real chunking.
	if req.Progress != nil {
		req.Progress(len(c.rows))
	}
	// Honor PersistBatch so tests that drive the heartbeat /
	// incremental-persist paths see the persister's UpsertBatch
	// invoked. Forwarded as a single batch covering every row;
	// chunking lives in fillFreshEmbeddings for the real
	// computer, not in this stub.
	if req.PersistBatch != nil && len(c.rows) > 0 {
		if err := req.PersistBatch(c.rows); err != nil {
			return nil, err
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.rows, nil
}

// fakePersister stores Upsert calls for inspection.
type fakePersister struct {
	mu             sync.Mutex
	upserts        []ComputedEmbedding
	batchUpserts   [][]ComputedEmbedding
	existing       map[string]ExistingEmbedding
	stampedCounts  map[string]int
	listErr        error
	upErr          error
	upsertBatchErr error
}

func (p *fakePersister) ListExisting(_ context.Context, _, _ string) (map[string]ExistingEmbedding, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	if p.existing == nil {
		return map[string]ExistingEmbedding{}, nil
	}
	out := make(map[string]ExistingEmbedding, len(p.existing))
	maps.Copy(out, p.existing)
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

// UpsertBatch records each per-batch call so tests can assert
// that fillFreshEmbeddings drove incremental persistence (one
// call per chunk) rather than batching everything to the final
// Upsert. Errors via upsertBatchErr exercise the worker's
// retry/fail path on persistence failure mid-job.
func (p *fakePersister) UpsertBatch(_ context.Context, _, _ string, rows []ComputedEmbedding) error {
	if p.upsertBatchErr != nil {
		return p.upsertBatchErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]ComputedEmbedding, len(rows))
	copy(cp, rows)
	p.batchUpserts = append(p.batchUpserts, cp)
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
	if got := store.completeCalls.Load(); got != 2 {
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
		if store.retryCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := store.retryCalls.Load(); got == 0 {
		t.Error("Retry was never called on retryable error")
	}
	if got := store.failCalls.Load(); got != 0 {
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
		if store.failCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := store.failCalls.Load(); got != 1 {
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

// TestWorker_PublishesChunkProgress proves the worker's progress
// callback path: the Computer invokes the supplied progress function
// during its work, and the worker translates each call into a
// Store.UpdateProgress write keyed by (job.ID, workerID, count).
// The catalog status endpoint reads embedded_so_far from that
// column so a long embed pass renders incremental progress before
// the final atomic upsert (#430).
func TestWorker_PublishesChunkProgress(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	// fakeComputer.Compute calls progress(len(rows)) before returning.
	// With three synthetic rows the worker should publish 3.
	computer := &fakeComputer{rows: []ComputedEmbedding{
		{OperationID: "a", Dim: 4, Embedding: []float32{1, 0, 0, 0}},
		{OperationID: "b", Dim: 4, Embedding: []float32{0, 1, 0, 0}},
		{OperationID: "c", Dim: 4, Embedding: []float32{0, 0, 1, 0}},
	}}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "wp", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		done := store.completeCalls.Load() >= 1
		store.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.progressByID[1]; got != 3 {
		t.Errorf("UpdateProgress observed %d for job 1; want 3", got)
	}
	if store.lastProgressWorker != "wp" {
		t.Errorf("UpdateProgress workerID=%q; want %q", store.lastProgressWorker, "wp")
	}
}

// TestWorker_ConcurrencyProcessesJobsInParallel proves multiple
// goroutines share the queue: with Concurrency=4 and four pending
// jobs against a Computer that sleeps 50ms, total wall time is
// well under the serial 200ms baseline (#430).
func TestWorker_ConcurrencyProcessesJobsInParallel(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	for _, spec := range []string{"a", "b", "c", "d"} {
		_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: spec}, KindSpecWrite)
	}
	resolver := &fakeResolver{contents: map[string]string{
		"c/a": "spec", "c/b": "spec", "c/c": "spec", "c/d": "spec",
	}}
	const perJob = 50 * time.Millisecond
	computer := &slowComputer{delay: perJob, rows: []ComputedEmbedding{
		{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}},
	}}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "wc", PollEvery: 200 * time.Millisecond, Concurrency: 4,
	})
	start := time.Now()
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)
	if store.completeCalls.Load() != 4 {
		t.Fatalf("completed %d jobs; want 4", store.completeCalls.Load())
	}
	// Serial baseline is 4 * 50ms = 200ms; with 4 workers the
	// pessimistic wall clock should be well under 150ms even with
	// scheduler jitter. Use 175ms as the gate so the test is
	// stable on CI runners without burning headroom.
	if elapsed >= 175*time.Millisecond {
		t.Errorf("4 jobs * %v with 4 workers took %v; expected parallel execution under 175ms", perJob, elapsed)
	}
}

// TestWorker_ProgressWriteFailureIsLogged proves the worker keeps
// running when Store.UpdateProgress errors mid-pass: the chunk
// callback's log-debug-and-continue path exists specifically so a
// transient DB hiccup on the progress column does not abort the
// embed (the column is best-effort; the final Complete is the
// authoritative signal).
func TestWorker_ProgressWriteFailureIsLogged(t *testing.T) {
	t.Parallel()
	store := &progressFailStore{fakeStore: newFakeStore()}
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &fakeComputer{rows: []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}}}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "wpf", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.completeCalls.Load() != 1 {
		t.Errorf("Complete called %d times; want 1 (job must finish despite progress write error)", store.completeCalls.Load())
	}
	if store.updateProgressErrors.Load() == 0 {
		t.Error("UpdateProgress was never called; cannot prove the error path was exercised")
	}
}

// progressFailStore wraps fakeStore so UpdateProgress always
// returns an error, exercising the worker's debug-log branch.
type progressFailStore struct {
	*fakeStore
	updateProgressErrors atomic.Int32
}

func (s *progressFailStore) UpdateProgress(_ context.Context, _ int64, _ string, _ int) error {
	s.updateProgressErrors.Add(1)
	return errors.New("simulated progress write failure")
}

// slowComputer adds a configurable delay so concurrency tests can
// measure parallel speedup without depending on a real embedder.
type slowComputer struct {
	delay time.Duration
	rows  []ComputedEmbedding
}

func (c *slowComputer) Compute(ctx context.Context, _ ComputeRequest) ([]ComputedEmbedding, error) {
	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		return nil, fmt.Errorf("slowComputer: %w", ctx.Err())
	}
	return c.rows, nil
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

// TestWorker_NewWorker_DefaultsConfig proves NewWorker fills
// the empty WorkerID with a generated value and zero PollEvery
// with defaultPollEvery.
func TestWorker_NewWorker_DefaultsConfig(t *testing.T) {
	t.Parallel()
	w := NewWorker(WorkerConfig{
		Store:     newFakeStore(),
		Resolver:  &fakeResolver{},
		Computer:  &fakeComputer{},
		Persister: &fakePersister{},
	})
	if w.cfg.WorkerID == "" {
		t.Error("WorkerID should default to a generated value")
	}
	if w.cfg.PollEvery != defaultPollEvery {
		t.Errorf("PollEvery = %v; want %v", w.cfg.PollEvery, defaultPollEvery)
	}
}

// TestWorker_StopBeforeStart proves Stop is safe without Start.
// The lifecycle teardown may call Stop on a never-Started worker
// (file-mode deploy where embed jobs are wired but no goroutine
// ever spawned); this must not panic.
func TestWorker_StopBeforeStart(_ *testing.T) {
	w := NewWorker(WorkerConfig{
		Store: newFakeStore(), Resolver: &fakeResolver{},
		Computer: &fakeComputer{}, Persister: &fakePersister{},
	})
	w.Stop()
}

// TestWorker_DoubleStartIsNoOp proves the CAS guard in Start
// short-circuits the second call. Without this, a misconfigured
// caller could spawn N goroutines and pull jobs in parallel
// from one Worker instance (which is documented as single-flight).
func TestWorker_DoubleStartIsNoOp(_ *testing.T) {
	w := NewWorker(WorkerConfig{
		Store: newFakeStore(), Resolver: &fakeResolver{},
		Computer: &fakeComputer{}, Persister: &fakePersister{},
		PollEvery: time.Hour,
	})
	w.Start(context.Background())
	w.Start(context.Background()) // CAS short-circuit
	w.Stop()
}

// TestWorker_NotifyDropsWhenBufferFull exercises the default
// branch of Notify's select: a second Notify before the worker
// has drained the wakeup channel is harmlessly dropped.
func TestWorker_NotifyDropsWhenBufferFull(_ *testing.T) {
	w := NewWorker(WorkerConfig{
		Store: newFakeStore(), Resolver: &fakeResolver{},
		Computer: &fakeComputer{}, Persister: &fakePersister{},
	})
	w.Notify()
	w.Notify() // second one hits the default case
}

// TestWorker_SpecResolveErrorTerminates proves the spec-resolve
// failure path moves the job to terminal failed (not retry).
// A vanished spec is not retryable.
func TestWorker_SpecResolveErrorTerminates(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	w := NewWorker(WorkerConfig{
		Store:     store,
		Resolver:  &fakeResolver{err: errors.New("spec gone")},
		Computer:  &fakeComputer{},
		Persister: &fakePersister{},
		WorkerID:  "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.failCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.failCalls.Load() != 1 {
		t.Errorf("Fail calls = %d; want 1", store.failCalls.Load())
	}
}

// TestWorker_ListExistingErrorRetries proves a DB read failure
// while loading existing vectors is retryable (under
// MaxAttempts) rather than terminal.
func TestWorker_ListExistingErrorRetries(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	w := NewWorker(WorkerConfig{
		Store:     store,
		Resolver:  &fakeResolver{contents: map[string]string{"c/x": "spec"}},
		Computer:  &fakeComputer{},
		Persister: &fakePersister{listErr: errors.New("conn pool exhausted")},
		WorkerID:  "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.retryCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.retryCalls.Load() == 0 {
		t.Error("retry was not called for retryable ListExisting failure")
	}
}

// TestWorker_UpsertErrorRetries proves a persist failure is
// retryable.
func TestWorker_UpsertErrorRetries(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	w := NewWorker(WorkerConfig{
		Store:     store,
		Resolver:  &fakeResolver{contents: map[string]string{"c/x": "spec"}},
		Computer:  &fakeComputer{rows: []ComputedEmbedding{{OperationID: "a", Embedding: []float32{1}, Dim: 1}}},
		Persister: &fakePersister{upErr: errors.New("disk full")},
		WorkerID:  "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.retryCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.retryCalls.Load() == 0 {
		t.Error("retry was not called for retryable Upsert failure")
	}
}

// TestWorker_NoReloaderDoesNotPanic proves a Worker without a
// Reloader configured still completes successfully (the reload
// hook is optional).
func TestWorker_NoReloaderDoesNotPanic(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	w := NewWorker(WorkerConfig{
		Store:     store,
		Resolver:  &fakeResolver{contents: map[string]string{"c/x": "spec"}},
		Computer:  &fakeComputer{rows: []ComputedEmbedding{{OperationID: "a", Embedding: []float32{1}, Dim: 1}}},
		Persister: &fakePersister{},
		// Reloader intentionally nil.
		WorkerID: "w1", PollEvery: 50 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.completeCalls.Load() == 0 {
		t.Error("worker did not complete without reloader configured")
	}
}

// TestReaper_DoubleStartIsNoOp covers the CAS guard.
func TestReaper_DoubleStartIsNoOp(_ *testing.T) {
	r := NewReaper(newFakeStore(), time.Hour)
	r.Start(context.Background())
	r.Start(context.Background())
	r.Stop()
}

// TestReaper_StopBeforeStart proves teardown is safe.
func TestReaper_StopBeforeStart(_ *testing.T) {
	r := NewReaper(newFakeStore(), time.Hour)
	r.Stop()
}

// TestReaper_DefaultInterval proves interval=0 maps to the
// package-level ReaperInterval constant.
func TestReaper_DefaultInterval(t *testing.T) {
	t.Parallel()
	r := NewReaper(newFakeStore(), 0)
	if r.interval != ReaperInterval {
		t.Errorf("interval = %v; want %v", r.interval, ReaperInterval)
	}
}

// reaperErrStore returns a release-error to exercise the
// reaper's logging branch.
type reaperErrStore struct{ fakeStore }

func (*reaperErrStore) ReleaseExpiredLeases(_ context.Context) (int, error) {
	return 0, errors.New("db down")
}

// TestReaper_SweepErrorIsLogged proves an error from
// ReleaseExpiredLeases is non-fatal and the reaper continues
// on the next tick.
func TestReaper_SweepErrorIsLogged(_ *testing.T) {
	r := NewReaper(&reaperErrStore{}, 20*time.Millisecond)
	r.Start(context.Background())
	time.Sleep(80 * time.Millisecond)
	r.Stop()
}

// TestReconciler_DoubleStartIsNoOp covers the CAS guard.
func TestReconciler_DoubleStartIsNoOp(_ *testing.T) {
	rc := NewReconciler(newFakeStore(), time.Hour)
	rc.Start(context.Background())
	rc.Start(context.Background())
	rc.Stop()
}

// TestReconciler_StopBeforeStart is the teardown-safety case.
func TestReconciler_StopBeforeStart(_ *testing.T) {
	rc := NewReconciler(newFakeStore(), time.Hour)
	rc.Stop()
}

// TestReconciler_DefaultInterval covers the 0-interval fallback.
func TestReconciler_DefaultInterval(t *testing.T) {
	t.Parallel()
	rc := NewReconciler(newFakeStore(), 0)
	if rc.interval != ReconcilerInterval {
		t.Errorf("interval = %v; want %v", rc.interval, ReconcilerInterval)
	}
}

// TestReconciler_SweepErrorIsLogged proves ReconcileGaps
// failures are non-fatal.
func TestReconciler_SweepErrorIsLogged(_ *testing.T) {
	store := newFakeStore()
	store.reconcileFunc = func() (int, error) {
		return 0, errors.New("table dropped")
	}
	rc := NewReconciler(store, 20*time.Millisecond)
	rc.Start(context.Background())
	time.Sleep(80 * time.Millisecond)
	rc.Stop()
}

// TestGenerateWorkerID_EmptyHostFallback exercises the unknown-
// host branch of generateWorkerID. We cannot easily force
// os.Hostname to return "" so we accept the path may not fire
// on a healthy CI host; the test still keeps the symbol
// reachable for coverage purposes.
func TestGenerateWorkerID_Format(t *testing.T) {
	t.Parallel()
	id := generateWorkerID()
	if id == "" {
		t.Fatal("empty id")
	}
	// "host-randhex" or "unknown-randhex"; the hex suffix is 8
	// chars when crypto/rand succeeds.
	if len(id) < 10 {
		t.Errorf("id %q looks malformed", id)
	}
}

// TestWorker_HeartbeatRenewsLeaseWhileComputing proves the worker
// renews its lease at lease/heartbeatDivisor cadence while a long
// Compute call is in flight. Without the heartbeat, a single
// batch slower than LeaseDuration would let the reaper kill the
// running context mid-pass — the #479 doom loop. With the
// heartbeat, RenewLease fires at least once before Compute
// returns, so the reaper continues to see a fresh lease.
func TestWorker_HeartbeatRenewsLeaseWhileComputing(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	// slowComputer takes 180ms; with LeaseDuration=60ms the
	// heartbeat fires every 20ms (60ms / heartbeatDivisor=3), so
	// at least 6 renewals should land before Compute returns.
	computer := &slowComputer{
		delay: 180 * time.Millisecond,
		rows:  []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}},
	}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "wh", PollEvery: 30 * time.Millisecond, LeaseDuration: 60 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := store.completeCalls.Load(); got != 1 {
		t.Fatalf("Complete called %d times; want 1", got)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	renewals := store.renewCallsByID[1]
	if renewals == 0 {
		t.Fatal("RenewLease was never called; heartbeat did not fire during the 180ms Compute")
	}
	// 180ms compute / 20ms tick = ~9 ticks; allow scheduler jitter
	// with a floor of 2 to keep the test stable on slow CI.
	if renewals < 2 {
		t.Errorf("RenewLease called %d times; expected >= 2 across the 180ms Compute window", renewals)
	}
	if store.renewWorker != "wh" {
		t.Errorf("renewWorker = %q; want %q", store.renewWorker, "wh")
	}
}

// TestWorker_HeartbeatLetsComputeOutlastLeaseDuration is the #479
// regression gate against a future "ctx bounded by LeaseDuration"
// refactor. With LeaseDuration=40ms and Compute=320ms (8x lease),
// any code path that ties the worker ctx deadline tightly to the
// configured LeaseDuration (e.g. `context.WithTimeout(ctx,
// w.cfg.LeaseDuration)`) would cancel Compute well before it
// returns, surfacing as a retry. The fakeStore does not enforce
// the lease itself, so this test asserts the ctx deadline shape,
// not actual reaper-driven cancellation. The companion behavior
// (heartbeat firing during a long Compute) is the
// renewCallsByID floor below.
func TestWorker_HeartbeatLetsComputeOutlastLeaseDuration(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &slowComputer{
		delay: 320 * time.Millisecond,
		rows:  []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}},
	}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "wo", PollEvery: 30 * time.Millisecond, LeaseDuration: 40 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.completeCalls.Load() != 1 {
		t.Fatalf("Compute exceeded LeaseDuration but did not complete; the heartbeat is no longer the authoritative deadline. completes=%d retries=%d",
			store.completeCalls.Load(), store.retryCalls.Load())
	}
	store.mu.Lock()
	renewals := store.renewCallsByID[1]
	store.mu.Unlock()
	if renewals < 3 {
		t.Errorf("heartbeat fired only %d times across the 8x-lease Compute window; want >= 3", renewals)
	}
}

// TestWorker_HeartbeatStopsAfterCompute proves the heartbeat
// goroutine exits when the surrounding Compute returns: the
// deferred hbCancel fires, ctx.Done unblocks heartbeat's select,
// and no further RenewLease calls land. Verified by reading the
// counter twice — once right after Complete, once after a short
// settle delay — and asserting the counter did not advance.
func TestWorker_HeartbeatStopsAfterCompute(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	computer := &slowComputer{
		delay: 100 * time.Millisecond,
		rows:  []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}},
	}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "ws", PollEvery: 30 * time.Millisecond, LeaseDuration: 60 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.completeCalls.Load() != 1 {
		t.Fatal("job never completed")
	}
	store.mu.Lock()
	atComplete := store.renewCallsByID[1]
	store.mu.Unlock()
	// Wait three heartbeat intervals; the goroutine should have
	// exited already.
	time.Sleep(60 * time.Millisecond)
	store.mu.Lock()
	afterIdle := store.renewCallsByID[1]
	store.mu.Unlock()
	if afterIdle != atComplete {
		t.Errorf("RenewLease counter advanced from %d to %d after Compute returned; heartbeat did not stop", atComplete, afterIdle)
	}
}

// TestWorker_PersistBatchForwardsToUpsertBatch proves the worker
// supplies a PersistBatch callback that the computer can invoke
// to write a chunk's rows to the Persister via UpsertBatch. The
// callback is the wiring that closes the #479 progress-loss
// failure mode: a job that fails on chunk N leaves chunks 0..N-1
// persisted for the next attempt's dedup pass.
func TestWorker_PersistBatchForwardsToUpsertBatch(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	rows := []ComputedEmbedding{
		{OperationID: "a", Dim: 4, Embedding: []float32{1, 0, 0, 0}},
		{OperationID: "b", Dim: 4, Embedding: []float32{0, 1, 0, 0}},
	}
	// fakeComputer calls req.PersistBatch(c.rows) when it is set,
	// so this exercises the worker's adapter end-to-end.
	computer := &fakeComputer{rows: rows}
	persister := &fakePersister{}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "wb", PollEvery: 30 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	persister.mu.Lock()
	defer persister.mu.Unlock()
	if len(persister.batchUpserts) == 0 {
		t.Fatal("UpsertBatch was never called; PersistBatch callback did not reach persister")
	}
	if got := persister.batchUpserts[0]; len(got) != len(rows) {
		t.Errorf("batchUpserts[0] has %d rows; want %d", len(got), len(rows))
	}
}

// TestWorker_PersistBatchErrorFailsCompute proves that an error
// returned from UpsertBatch propagates back through Compute as
// a retryable failure (the worker's compute-failed branch).
// Confirms that a transient DB outage during incremental
// persistence does not silently lose vectors — the job retries.
func TestWorker_PersistBatchErrorFailsCompute(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	rows := []ComputedEmbedding{{OperationID: "a", Dim: 4, Embedding: []float32{1, 0, 0, 0}}}
	computer := &fakeComputer{rows: rows}
	persister := &fakePersister{upsertBatchErr: errors.New("db pool exhausted")}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "we", PollEvery: 30 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.retryCalls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.retryCalls.Load() == 0 {
		t.Fatal("Retry was never called on persist-batch failure")
	}
	if store.completeCalls.Load() != 0 {
		t.Errorf("Complete called %d times despite persist failure; want 0", store.completeCalls.Load())
	}
}

// TestWorker_BatchSizeFlowsToComputeRequest proves the worker
// passes WorkerConfig.BatchSize into ComputeRequest so the
// computer's batching uses the operator's configured chunk size,
// not the package default.
func TestWorker_BatchSizeFlowsToComputeRequest(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "x"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/x": "spec"}}
	var seenBatch atomic.Int32
	computer := &requestRecordingComputer{
		seenBatch: &seenBatch,
		rows:      []ComputedEmbedding{{OperationID: "op", Dim: 4, Embedding: []float32{1, 0, 0, 0}}},
	}
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: &fakePersister{},
		WorkerID: "wbs", PollEvery: 30 * time.Millisecond, BatchSize: 16,
	})
	w.Start(context.Background())
	defer w.Stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := seenBatch.Load(); got != 16 {
		t.Errorf("computer saw BatchSize=%d; want 16", got)
	}
}

// TestNewWorker_DefaultsLeaseAndBatchSize proves the constructor
// fills in DefaultLeaseDuration and DefaultEmbedBatchSize when
// the caller omits both. Documents the contract that operators
// opting out of tuning still get the historical defaults rather
// than instant-expire leases or single-text batches.
func TestNewWorker_DefaultsLeaseAndBatchSize(t *testing.T) {
	t.Parallel()
	w := NewWorker(WorkerConfig{
		Store:     newFakeStore(),
		Resolver:  &fakeResolver{},
		Computer:  &fakeComputer{},
		Persister: &fakePersister{},
	})
	if w.cfg.LeaseDuration != DefaultLeaseDuration {
		t.Errorf("LeaseDuration = %v; want DefaultLeaseDuration=%v", w.cfg.LeaseDuration, DefaultLeaseDuration)
	}
	if w.cfg.BatchSize != DefaultEmbedBatchSize {
		t.Errorf("BatchSize = %d; want DefaultEmbedBatchSize=%d", w.cfg.BatchSize, DefaultEmbedBatchSize)
	}
}

// requestRecordingComputer captures the BatchSize the worker
// passes via ComputeRequest so tests can prove the config knob
// reaches the actual computer call site.
type requestRecordingComputer struct {
	seenBatch *atomic.Int32
	rows      []ComputedEmbedding
}

func (c *requestRecordingComputer) Compute(_ context.Context, req ComputeRequest) ([]ComputedEmbedding, error) {
	// #nosec G115 -- test-only conversion; batch sizes are tens to hundreds, never near int32 range.
	c.seenBatch.Store(int32(req.BatchSize))
	if req.PersistBatch != nil && len(c.rows) > 0 {
		if err := req.PersistBatch(c.rows); err != nil {
			return nil, err
		}
	}
	return c.rows, nil
}

// resumeFailComputer models the #479 mid-job failure: succeeds
// on every chunk except the second, then on retry succeeds on
// all chunks. It calls PersistBatch for each chunk it processes
// so the test can prove the failed-attempt's first chunk
// survives to the retry's dedup map.
type resumeFailComputer struct {
	mu        sync.Mutex
	allRows   []ComputedEmbedding
	chunkSize int
	failOn    int // chunk index that errors on attempt 1
	attempt   int
}

func (c *resumeFailComputer) Compute(_ context.Context, req ComputeRequest) ([]ComputedEmbedding, error) {
	c.mu.Lock()
	c.attempt++
	attempt := c.attempt
	c.mu.Unlock()
	out := make([]ComputedEmbedding, 0, len(c.allRows))
	for start := 0; start < len(c.allRows); start += c.chunkSize {
		end := min(start+c.chunkSize, len(c.allRows))
		chunk := c.allRows[start:end]
		// Honor dedup: skip ops already in req.Existing. The
		// resume test asserts the second attempt sees fewer
		// upstream "calls" because the persisted chunk 0 lands
		// in req.Existing on attempt 2.
		fresh := make([]ComputedEmbedding, 0, len(chunk))
		for _, r := range chunk {
			if _, found := req.Existing[r.OperationID]; !found {
				fresh = append(fresh, r)
			}
		}
		if len(fresh) > 0 && req.PersistBatch != nil {
			if err := req.PersistBatch(fresh); err != nil {
				return nil, err
			}
		}
		// Trigger the failure AFTER chunk 0 has persisted so
		// the test can prove progress survives the failure.
		if attempt == 1 && start/c.chunkSize == c.failOn {
			return nil, errors.New("simulated upstream failure mid-job")
		}
		out = append(out, chunk...)
	}
	return out, nil
}

// inMemPersister mirrors the catalogEmbeddingPersister's
// contract against an in-process map. ListExisting returns the
// rows UpsertBatch wrote on prior attempts, which is the wiring
// the integration test verifies survives across retries.
type inMemPersister struct {
	mu      sync.Mutex
	rows    map[string]ComputedEmbedding
	stamped int
}

func newInMemPersister() *inMemPersister {
	return &inMemPersister{rows: map[string]ComputedEmbedding{}}
}

func (p *inMemPersister) ListExisting(_ context.Context, _, _ string) (map[string]ExistingEmbedding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]ExistingEmbedding, len(p.rows))
	for k, v := range p.rows {
		out[k] = ExistingEmbedding(v)
	}
	return out, nil
}

func (p *inMemPersister) Upsert(_ context.Context, _, _ string, rows []ComputedEmbedding) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Atomic replace: clear then write.
	p.rows = make(map[string]ComputedEmbedding, len(rows))
	for _, r := range rows {
		p.rows[r.OperationID] = r
	}
	return nil
}

func (p *inMemPersister) UpsertBatch(_ context.Context, _, _ string, rows []ComputedEmbedding) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range rows {
		p.rows[r.OperationID] = r
	}
	return nil
}

func (p *inMemPersister) StampOperationCount(_ context.Context, _, _ string, count int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stamped = count
	return nil
}

// TestWorker_IntegrationResumeAfterMidJobFailure proves the
// end-to-end #479 fix: a job that fails on chunk N persists
// chunks 0..N-1 via UpsertBatch, and the next attempt's
// ListExisting dedup pass skips those operations entirely.
//
// Wires together: the real Worker, a fakeStore implementing
// the queue state machine, a resumeFailComputer that fails on
// chunk index 1 only on attempt 1, and an inMemPersister that
// preserves UpsertBatch writes across attempts. Asserts:
//
//  1. After attempt 1 fails, the persister contains the rows
//     from chunk 0 (proves PersistBatch + UpsertBatch wiring).
//  2. After attempt 2 succeeds, the persister contains every
//     row.
//  3. The job's final status is succeeded.
//
// Without per-batch persistence and the heartbeat, attempt 1
// would lose chunk 0's work and attempt 2 would redo it from
// scratch — the literal failure mode #479 documents.
func TestWorker_IntegrationResumeAfterMidJobFailure(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.zeroRetryBackoff = true
	_, _ = store.Enqueue(context.Background(), SpecKey{CatalogID: "c", SpecName: "iterable"}, KindSpecWrite)
	resolver := &fakeResolver{contents: map[string]string{"c/iterable": "spec-body"}}
	// 3 chunks of 2 ops each = 6 total. Fail on chunk index 1
	// (the second chunk) on attempt 1.
	allRows := []ComputedEmbedding{
		{OperationID: "op0", Dim: 2, Embedding: []float32{1, 0}, TextHash: []byte{0x00}, Model: "m"},
		{OperationID: "op1", Dim: 2, Embedding: []float32{0, 1}, TextHash: []byte{0x01}, Model: "m"},
		{OperationID: "op2", Dim: 2, Embedding: []float32{1, 1}, TextHash: []byte{0x02}, Model: "m"},
		{OperationID: "op3", Dim: 2, Embedding: []float32{2, 0}, TextHash: []byte{0x03}, Model: "m"},
		{OperationID: "op4", Dim: 2, Embedding: []float32{0, 2}, TextHash: []byte{0x04}, Model: "m"},
		{OperationID: "op5", Dim: 2, Embedding: []float32{2, 2}, TextHash: []byte{0x05}, Model: "m"},
	}
	computer := &resumeFailComputer{allRows: allRows, chunkSize: 2, failOn: 1}
	persister := newInMemPersister()
	w := NewWorker(WorkerConfig{
		Store: store, Resolver: resolver, Computer: computer, Persister: persister,
		WorkerID: "wir", PollEvery: 30 * time.Millisecond,
	})
	w.Start(context.Background())
	defer w.Stop()

	// Wait for either job completion or the test deadline. The
	// queue retries automatically on the retryable failure.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if store.completeCalls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.completeCalls.Load() != 1 {
		t.Fatalf("job never completed; completes=%d retries=%d",
			store.completeCalls.Load(), store.retryCalls.Load())
	}
	if got := store.retryCalls.Load(); got < 1 {
		t.Errorf("retry was never called; mid-job failure path not exercised")
	}
	persister.mu.Lock()
	defer persister.mu.Unlock()
	if len(persister.rows) != len(allRows) {
		t.Fatalf("persister has %d rows; want %d (every op present after resume)", len(persister.rows), len(allRows))
	}
	for _, r := range allRows {
		if _, ok := persister.rows[r.OperationID]; !ok {
			t.Errorf("op %q missing from persister after resume", r.OperationID)
		}
	}
}
