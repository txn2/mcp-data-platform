package indexjobs

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// enqueueTimeout bounds one write-path enqueue. The insert is a single
// statement against the same database the write that triggered it just
// committed to, so this is a backstop against a stalled connection
// holding a request open, not a normal deadline.
const enqueueTimeout = 5 * time.Second

// Enqueuer is the write-path subset of Store: the one call a consumer's
// write path makes after a successful row mutation. Declared separately
// from Store so a Producer can be bound to a two-line fake in tests
// without standing up the whole queue.
type Enqueuer interface {
	Enqueue(ctx context.Context, key Key, trigger Trigger) (created bool, err error)
}

// Producer is the write-path enqueue seam for one source kind: a
// consumer's store calls NotifyWrite after a row mutation commits, and
// the row enters ranked search in roughly the time one embed takes
// instead of up to a full ReconcilerInterval. It is the TriggerWrite path
// for the DB-backed consumers, the counterpart to the api-catalog
// consumer's EnqueueBestEffort.
//
// It is a distinct object from the queue store because the two ends are
// constructed in the opposite order from how they are used: a consumer's
// store is built while the platform assembles its subsystems, and the
// queue that serves it is assembled afterwards (it needs those
// subsystems to know which consumers to register). A store therefore
// takes an unbound Producer at construction and the queue Binds itself
// once it exists. Until then — and forever, on a deployment with no
// worker to run the jobs — NotifyWrite is a no-op and the reconciler
// remains the only path to the index.
//
// Every method is safe on a nil *Producer, so a store built without one
// needs no nil checks around its notify calls.
//
// One write, one job: the enqueue is idempotent through the
// index_jobs_open partial unique index, so a write landing while a job
// for the same row is pending collapses onto that job, which reads the
// row's committed text when it runs. A write landing while a job for the
// same row is RUNNING collapses the same way and the in-flight job's
// snapshot of the row wins; that unit's next convergence signal is its
// next write, a provider model swap, or an operator re-index, and until
// one lands the row still matches lexically. That is the framework's
// pre-existing one-open-job-per-unit contract, not a property of the
// write path.
type Producer struct {
	kind string
	enq  atomic.Pointer[binding]
}

// binding boxes the bound Enqueuer so it can live in an atomic.Pointer
// (an interface value cannot). Bind runs at startup while requests may
// already be in flight, so the binding is read atomically per write.
type binding struct {
	enq Enqueuer
}

// NewProducer returns an unbound Producer for the given source kind.
// Callers pass the kind's own SourceKind constant so the kind string
// keeps one definition per consumer.
func NewProducer(kind string) *Producer {
	return &Producer{kind: kind}
}

// Kind reports the source kind this Producer enqueues for. The queue
// reads it to bind only the producers whose consumer actually registered.
func (p *Producer) Kind() string {
	if p == nil {
		return ""
	}
	return p.kind
}

// Bind attaches the queue's job store. Calling it again replaces the
// binding; passing nil unbinds, which is how a queue that failed to
// assemble leaves the write paths as they were.
func (p *Producer) Bind(e Enqueuer) {
	if p == nil {
		return
	}
	if e == nil {
		p.enq.Store(nil)
		return
	}
	p.enq.Store(&binding{enq: e})
}

// NotifyWrite enqueues a TriggerWrite job for sourceID. Best-effort by
// contract: a nil receiver, an unbound producer, or a failed insert logs
// (at most) and returns, leaving the row for the reconciler to converge
// on its next sweep. The originating write has already committed and must
// never be failed by an indexing concern.
//
// The enqueue runs on a context detached from the caller's cancellation
// (bounded by enqueueTimeout) because the write it follows has committed:
// a client that disconnects the moment its write returns would otherwise
// cancel the enqueue and leave the row indexed only on the next sweep,
// which is the latency this path exists to remove.
func (p *Producer) NotifyWrite(ctx context.Context, sourceID string) {
	if p == nil {
		return
	}
	b := p.enq.Load()
	if b == nil || b.enq == nil {
		return
	}
	enqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enqueueTimeout)
	defer cancel()
	if _, err := b.enq.Enqueue(enqCtx, Key{SourceKind: p.kind, SourceID: sourceID}, TriggerWrite); err != nil {
		slog.Warn("indexjobs: write-path enqueue failed; leaving the row to the reconciler",
			logKeySourceKind, p.kind, logKeySourceID, logsan.SanitizeForLog(sourceID), logKeyError, err)
	}
}

// StoreOption configures a consumer store's optional dependencies at
// construction. One definition serves every kind: each DB-backed
// consumer's store constructor takes `opts ...indexjobs.StoreOption`, so
// a caller with no queue keeps the single-argument form and that store's
// write path stays reconciler-only.
type StoreOption func(*StoreOptions)

// StoreOptions is the resolved set of optional dependencies a consumer
// store was constructed with.
type StoreOptions struct {
	// Producer receives a write-path enqueue after each row mutation the
	// store commits. Nil means no queue is wired.
	Producer *Producer
}

// WithProducer sets the Producer a store's write path notifies after a
// successful mutation. A nil Producer is accepted and leaves the store's
// write path a no-op, so callers can pass an accessor's result without a
// nil check.
func WithProducer(p *Producer) StoreOption {
	return func(o *StoreOptions) { o.Producer = p }
}

// ResolveStoreOptions applies opts in order and returns the result. Store
// constructors call it on their variadic parameter.
func ResolveStoreOptions(opts []StoreOption) StoreOptions {
	var resolved StoreOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&resolved)
		}
	}
	return resolved
}
