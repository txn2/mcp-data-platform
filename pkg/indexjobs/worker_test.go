package indexjobs

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// recordingStore is a Store stub that records the terminal outcome of
// process() (which of Complete / Retry / Fail was called) so worker
// tests can assert the state-machine branch taken without a database.
// It embeds noopStore for the methods process() does not drive.
type recordingStore struct {
	noopStore
	completed bool
	retried   bool
	failed    bool
	progress  []int
}

func (s *recordingStore) Complete(context.Context, int64, string) error {
	s.completed = true
	return nil
}

func (s *recordingStore) Retry(context.Context, int64, string, string) error {
	s.retried = true
	return nil
}

func (s *recordingStore) Fail(context.Context, int64, string, string) error {
	s.failed = true
	return nil
}

func (s *recordingStore) UpdateProgress(_ context.Context, _ int64, _ string, n int) error {
	s.progress = append(s.progress, n)
	return nil
}

func newTestWorker(store Store, reg *Registry) *Worker {
	return NewWorker(WorkerConfig{
		Store: store, Registry: reg, Embedder: newFakeEmbedder(), WorkerID: "w1",
	})
}

func registryWith(src Source, snk Sink) *Registry {
	r := NewRegistry()
	_ = r.Register(src, snk)
	return r
}

func writeJob(kind string) *Job {
	return &Job{ID: 1, SourceKind: kind, SourceID: "u1", Trigger: TriggerWrite, Status: StatusRunning, Attempts: 1}
}

func TestWorkerProcess_Success(t *testing.T) {
	t.Parallel()
	var reloaded string
	src := &stubSource{kind: "k", items: twoItems(), onOK: func(id string) { reloaded = id }}
	snk := &stubSink{kind: "k"}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	w.process(context.Background(), writeJob("k"))

	if !store.completed {
		t.Error("successful job should call Complete")
	}
	if store.retried || store.failed {
		t.Error("successful job should not retry or fail")
	}
	if len(snk.upserted) != 2 {
		t.Errorf("sink should receive 2 vectors; got %d", len(snk.upserted))
	}
	if snk.stamped != 2 {
		t.Errorf("StampExpected = %d; want 2", snk.stamped)
	}
	if reloaded != "u1" {
		t.Errorf("OnSucceeded got %q; want u1", reloaded)
	}
}

func TestWorkerProcess_UnregisteredKindFails(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	w := newTestWorker(store, NewRegistry()) // empty registry

	w.process(context.Background(), writeJob("unknown"))

	if !store.failed {
		t.Error("job for unregistered kind should be terminated (Fail)")
	}
	if store.completed || store.retried {
		t.Error("unregistered kind should not complete or retry")
	}
}

func TestWorkerProcess_LoadItemsErrorTerminates(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", err: errors.New("spec gone")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, &stubSink{kind: "k"}))

	w.process(context.Background(), writeJob("k"))

	if !store.failed {
		t.Error("LoadItems error should terminate the job (Fail)")
	}
	if store.retried {
		t.Error("LoadItems error is terminal, not retryable")
	}
}

func TestWorkerProcess_SourceGoneResolvesInsteadOfFailing(t *testing.T) {
	t.Parallel()
	var reloaded bool
	src := &stubSource{
		kind: "k",
		err:  fmt.Errorf("spec %q deleted: %w", "u1", ErrSourceGone),
		onOK: func(string) { reloaded = true },
	}
	snk := &stubSink{kind: "k"}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	w.process(context.Background(), writeJob("k"))

	if !store.completed {
		t.Error("source-gone job should Complete (resolving open failures), not fail")
	}
	if store.failed || store.retried {
		t.Error("source-gone must not record a failure or retry: nothing can ever supersede it")
	}
	if snk.upsertCalls != 1 || len(snk.upserted) != 0 {
		t.Errorf("gone unit's vectors should be cleared with one empty Upsert; calls=%d rows=%d",
			snk.upsertCalls, len(snk.upserted))
	}
	if snk.stamped != 0 {
		t.Errorf("StampExpected should not run for a gone unit; stamped=%d", snk.stamped)
	}
	if reloaded {
		t.Error("OnSucceeded should not fire for a gone unit")
	}
}

// TestWorkerProcess_SourceGoneCompletesDespiteUpsertError: clearing a
// gone unit's vectors is best-effort (kinds with FK cascade have
// already dropped them); a sink error must not turn the resolution
// back into residue.
func TestWorkerProcess_SourceGoneCompletesDespiteUpsertError(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", err: fmt.Errorf("gone: %w", ErrSourceGone)}
	snk := &stubSink{kind: "k", upErr: errors.New("vector table down")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	w.process(context.Background(), writeJob("k"))

	if !store.completed || store.failed || store.retried {
		t.Errorf("source-gone must Complete despite Upsert error; completed=%v failed=%v retried=%v",
			store.completed, store.failed, store.retried)
	}
}

func TestWorkerProcess_ListExistingErrorRetries(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", items: twoItems()}
	snk := &stubSink{kind: "k", listErr: errors.New("db blip")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	w.process(context.Background(), writeJob("k"))

	if !store.retried {
		t.Error("ListExisting error should retry (DB read is retryable)")
	}
}

func TestWorkerProcess_UpsertErrorRetries(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", items: twoItems()}
	snk := &stubSink{kind: "k", upErr: errors.New("write failed")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	w.process(context.Background(), writeJob("k"))

	if !store.retried {
		t.Error("Upsert error should retry")
	}
}

func TestWorkerProcess_ExhaustedAttemptsFails(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", items: twoItems()}
	snk := &stubSink{kind: "k", upErr: errors.New("write failed")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	job := writeJob("k")
	job.Attempts = MaxAttempts // already at the cap
	w.process(context.Background(), job)

	if !store.failed {
		t.Error("error at MaxAttempts should fail terminally")
	}
	if store.retried {
		t.Error("should not retry once attempts are exhausted")
	}
}

// vanishingSource returns items on the first LoadItems call and
// ErrSourceGone afterwards, modeling a source deleted mid-attempt.
type vanishingSource struct {
	stubSource
	calls int
}

func (s *vanishingSource) LoadItems(_ context.Context, _ string) ([]Item, error) {
	s.calls++
	if s.calls == 1 {
		return twoItems(), nil
	}
	return nil, fmt.Errorf("deleted mid-attempt: %w", ErrSourceGone)
}

// TestWorkerProcess_ExhaustedAttemptsProbesForGoneSource pins the
// race half of #998: the source is deleted while the final attempt is
// mid-flight (LoadItems already succeeded), so the delete-side cancel
// saw a running job and nothing to resolve. A terminal Fail recorded
// now could never be superseded; the exhausted-attempts path must
// re-probe the source and resolve instead.
func TestWorkerProcess_ExhaustedAttemptsProbesForGoneSource(t *testing.T) {
	t.Parallel()
	src := &vanishingSource{stubSource: stubSource{kind: "k"}}
	snk := &stubSink{kind: "k", upErr: errors.New("fk violation: spec row cascaded away")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	job := writeJob("k")
	job.Attempts = MaxAttempts
	w.process(context.Background(), job)

	if !store.completed {
		t.Error("final-attempt failure with a gone source should resolve (Complete), not Fail")
	}
	if store.failed || store.retried {
		t.Errorf("no terminal failure or retry expected; failed=%v retried=%v", store.failed, store.retried)
	}
	if src.calls != 2 {
		t.Errorf("LoadItems calls = %d; want 2 (initial load + terminal probe)", src.calls)
	}
}

func TestWorkerProcess_ManualRetrySkipsDedup(t *testing.T) {
	t.Parallel()
	// ListExisting would error, but manual_retry must skip it, so the
	// job still completes. A non-manual job would retry on the same
	// listErr (covered by TestWorkerProcess_ListExistingErrorRetries).
	src := &stubSource{kind: "k", items: twoItems()}
	snk := &stubSink{kind: "k", listErr: errors.New("should not be called")}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	job := writeJob("k")
	job.Trigger = TriggerManualRetry
	w.process(context.Background(), job)

	if !store.completed {
		t.Error("manual_retry should skip ListExisting and complete")
	}
	if store.retried {
		t.Error("manual_retry should not hit the ListExisting error path")
	}
}

func TestWorkerProcess_EmptyItemsCompletesWithZeroVectors(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", items: nil} // source row vanished -> no items
	snk := &stubSink{kind: "k"}
	store := &recordingStore{}
	w := newTestWorker(store, registryWith(src, snk))

	w.process(context.Background(), writeJob("k"))

	if !store.completed {
		t.Error("zero-item job should complete cleanly")
	}
	if snk.stamped != 0 {
		t.Errorf("StampExpected = %d; want 0", snk.stamped)
	}
}

func TestNewWorker_Defaults(t *testing.T) {
	t.Parallel()
	w := NewWorker(WorkerConfig{})
	if w.cfg.WorkerID == "" {
		t.Error("WorkerID should auto-generate")
	}
	if w.cfg.Concurrency != 1 {
		t.Errorf("Concurrency default = %d; want 1", w.cfg.Concurrency)
	}
	if w.cfg.LeaseDuration != DefaultLeaseDuration {
		t.Errorf("LeaseDuration default = %v; want %v", w.cfg.LeaseDuration, DefaultLeaseDuration)
	}
	if w.cfg.BatchSize != DefaultEmbedBatchSize {
		t.Errorf("BatchSize default = %d; want %d", w.cfg.BatchSize, DefaultEmbedBatchSize)
	}
	if w.Concurrency() != 1 {
		t.Errorf("Concurrency() = %d; want 1", w.Concurrency())
	}
}

// loopStore returns one claimable job, then ErrNoJob, so the full
// run -> drainQueue -> process -> Complete -> Stop path is exercised
// without a database. completion is an atomic signal (not the plain
// recordingStore.completed bool) so the polling test goroutine does
// not race the worker goroutine's Complete write.
type loopStore struct {
	recordingStore
	claimed atomic.Bool
	done    atomic.Bool
}

func (s *loopStore) Claim(context.Context, string) (*Job, error) {
	if s.claimed.CompareAndSwap(false, true) {
		return &Job{ID: 1, SourceKind: "k", SourceID: "u1", Trigger: TriggerWrite, Status: StatusRunning, Attempts: 1}, nil
	}
	return nil, ErrNoJob
}

func (s *loopStore) Complete(context.Context, int64, string) error {
	s.done.Store(true)
	return nil
}

func TestWorker_StartProcessesQueuedJobThenStops(t *testing.T) {
	t.Parallel()
	src := &stubSource{kind: "k", items: twoItems()}
	store := &loopStore{}
	w := NewWorker(WorkerConfig{
		Store: store, Registry: registryWith(src, &stubSink{kind: "k"}),
		Embedder: newFakeEmbedder(), WorkerID: "w1", PollEvery: 10 * time.Millisecond,
	})
	w.Start(context.Background())
	w.Start(context.Background()) // second Start is a no-op (idempotent)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !store.done.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop()
	w.Stop() // idempotent

	if !store.done.Load() {
		t.Error("worker loop should have claimed and completed the queued job")
	}
}

func TestWorker_NotifyCoalesces(t *testing.T) {
	t.Parallel()
	w := NewWorker(WorkerConfig{})
	w.Notify()
	w.Notify() // second notify coalesces into the buffered slot, no block
	select {
	case <-w.wakeup:
	default:
		t.Error("Notify should have signaled the wakeup channel")
	}
}
