package indexjobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// recordingEnqueuer captures what a Producer handed the queue, including
// whether the enqueue context was still live when it arrived.
type recordingEnqueuer struct {
	mu       sync.Mutex
	keys     []indexjobs.Key
	triggers []indexjobs.Trigger
	ctxErrs  []error
	err      error
}

func (r *recordingEnqueuer) Enqueue(ctx context.Context, key indexjobs.Key, trigger indexjobs.Trigger) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	r.triggers = append(r.triggers, trigger)
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
	if r.err != nil {
		return false, r.err
	}
	return true, nil
}

func (r *recordingEnqueuer) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.keys)
}

// TestProducerNotifyWriteEnqueuesWriteJob is the core of #1256: a write path's
// notify becomes one TriggerWrite job for that row's (kind, id).
func TestProducerNotifyWriteEnqueuesWriteJob(t *testing.T) {
	enq := &recordingEnqueuer{}
	p := indexjobs.NewProducer("portal-knowledge-pages")
	p.Bind(enq)

	p.NotifyWrite(context.Background(), "kp_42")

	require.Equal(t, 1, enq.calls())
	assert.Equal(t, indexjobs.Key{SourceKind: "portal-knowledge-pages", SourceID: "kp_42"}, enq.keys[0])
	assert.Equal(t, indexjobs.TriggerWrite, enq.triggers[0],
		"a write-path job must carry TriggerWrite so the admin history and the worker's dedup pass treat it as one")
}

// TestProducerNotifyWriteUnboundIsNoop covers the deployment shape the queue
// never assembles for (no database, or no configured embedder): the store still
// calls notify on every write and nothing happens.
func TestProducerNotifyWriteUnboundIsNoop(t *testing.T) {
	p := indexjobs.NewProducer("prompts")
	assert.NotPanics(t, func() { p.NotifyWrite(context.Background(), "p_1") })

	// Binding, then unbinding, returns to the same no-op state.
	enq := &recordingEnqueuer{}
	p.Bind(enq)
	p.NotifyWrite(context.Background(), "p_1")
	p.Bind(nil)
	p.NotifyWrite(context.Background(), "p_2")
	assert.Equal(t, 1, enq.calls(), "an unbound producer must not reach the queue")
}

// TestProducerNilReceiverIsSafe pins the contract every store's write path
// relies on: a store built without a producer calls the same method and needs no
// nil check around it.
func TestProducerNilReceiverIsSafe(t *testing.T) {
	var p *indexjobs.Producer
	assert.NotPanics(t, func() {
		p.NotifyWrite(context.Background(), "id")
		p.Bind(&recordingEnqueuer{})
		p.NotifyWrite(context.Background(), "id")
	})
	assert.Empty(t, p.Kind())
}

// TestProducerNotifyWriteSwallowsQueueError proves the best-effort contract: a
// queue insert that fails must not surface to the write that triggered it. The
// row stays a gap the reconciler converges on its next sweep.
func TestProducerNotifyWriteSwallowsQueueError(t *testing.T) {
	enq := &recordingEnqueuer{err: errors.New("queue unavailable")}
	p := indexjobs.NewProducer("resources")
	p.Bind(enq)

	assert.NotPanics(t, func() { p.NotifyWrite(context.Background(), "res_1") })
	assert.Equal(t, 1, enq.calls())
}

// TestProducerNotifyWriteSurvivesCanceledCaller is the reason the enqueue runs
// on a detached context. The write it follows has already committed, so a client
// that disconnects the moment its write returns must not cancel the enqueue and
// push the row back to a sweep-interval wait.
func TestProducerNotifyWriteSurvivesCanceledCaller(t *testing.T) {
	enq := &recordingEnqueuer{}
	p := indexjobs.NewProducer("portal-assets")
	p.Bind(enq)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.NotifyWrite(ctx, "asset_1")

	require.Equal(t, 1, enq.calls())
	assert.NoError(t, enq.ctxErrs[0],
		"the enqueue must run on a context detached from the caller's cancellation")
}

// TestProducerKindAndRebind covers the accessor the queue binds by, and that a
// later Bind replaces the earlier binding rather than fanning out to both.
func TestProducerKindAndRebind(t *testing.T) {
	p := indexjobs.NewProducer("portal-collections")
	assert.Equal(t, "portal-collections", p.Kind())

	first, second := &recordingEnqueuer{}, &recordingEnqueuer{}
	p.Bind(first)
	p.Bind(second)
	p.NotifyWrite(context.Background(), "coll_1")

	assert.Zero(t, first.calls())
	assert.Equal(t, 1, second.calls())
}

// TestProducerConcurrentNotifyDuringBind exercises the shape Bind actually runs
// in: the queue binds at startup while request-path writes are already calling
// notify. Under -race this is the assertion that the binding is published
// safely.
func TestProducerConcurrentNotifyDuringBind(t *testing.T) {
	enq := &recordingEnqueuer{}
	p := indexjobs.NewProducer("memory")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { p.NotifyWrite(context.Background(), "mem_1") })
	}
	wg.Go(func() { p.Bind(enq) })
	wg.Wait()

	assert.LessOrEqual(t, enq.calls(), 8)
}

// TestResolveStoreOptions covers the shared optional-dependency plumbing every
// consumer store constructor runs, including the no-option form that leaves a
// store's write path reconciler-only.
func TestResolveStoreOptions(t *testing.T) {
	assert.Nil(t, indexjobs.ResolveStoreOptions(nil).Producer)

	p := indexjobs.NewProducer("prompts")
	resolved := indexjobs.ResolveStoreOptions([]indexjobs.StoreOption{
		nil, // a nil option is skipped rather than panicking
		indexjobs.WithProducer(nil),
		indexjobs.WithProducer(p),
	})
	assert.Same(t, p, resolved.Producer, "the last option wins")
}
