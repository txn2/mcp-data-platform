package notifyworker

import (
	"context"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// fakeQueueStore serves canned claims and records how each batch was resolved,
// which is the whole of what the worker does to a queue. Enqueue is present
// only to satisfy notification.QueueStore: the worker never writes rows.
type fakeQueueStore struct {
	mu sync.Mutex

	immediate [][]notification.Notification // successive ClaimImmediate results (nil = ErrNoWork)
	digests   [][]notification.Notification // successive ClaimDigest results (nil = ErrNoWork)
	claimErr  error

	sent    [][]int64
	retried [][]int64
	failed  [][]int64
	purges  int
	opErr   error

	// filters records the transport filter of every claim, so a test can
	// assert which transports the worker asked for.
	filters []notification.TransportFilter
	// lastError is the text the worker recorded on the last resolved batch:
	// what an operator reads in the delivery history.
	lastError string
}

func (*fakeQueueStore) Enqueue(context.Context, notification.Notification) error { return nil }

func (f *fakeQueueStore) ClaimImmediate(_ context.Context, _ time.Duration, filter notification.TransportFilter) (*notification.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if len(f.immediate) == 0 || f.immediate[0] == nil {
		if len(f.immediate) > 0 {
			f.immediate = f.immediate[1:]
		}
		return nil, notification.ErrNoWork
	}
	batch := f.immediate[0]
	f.immediate = f.immediate[1:]
	// The real store never hands back a row the filter excludes, so neither
	// does this one: a fake that served an email row to a channel-only claim
	// would let a broken transport predicate pass its own test.
	if !admits(filter, batch[0]) {
		return nil, notification.ErrNoWork
	}
	return &batch[0], nil
}

// admits reports whether filter would have claimed n, by the rule the
// PostgreSQL store's transportClause applies: a row addressed to a channel
// destination needs the channel transport, everything else needs email.
func admits(filter notification.TransportFilter, n notification.Notification) bool {
	if _, addressed := notification.ChannelName(n.Recipient); addressed {
		return filter.Channel
	}
	return filter.Email
}

func (f *fakeQueueStore) ClaimDigest(_ context.Context, _ time.Duration, filter notification.TransportFilter) ([]notification.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if len(f.digests) == 0 || f.digests[0] == nil {
		if len(f.digests) > 0 {
			f.digests = f.digests[1:]
		}
		return nil, notification.ErrNoWork
	}
	batch := f.digests[0]
	f.digests = f.digests[1:]
	if len(batch) > 0 && !admits(filter, batch[0]) {
		return nil, notification.ErrNoWork
	}
	return batch, nil
}

func (f *fakeQueueStore) MarkSent(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ids)
	return f.opErr
}

func (f *fakeQueueStore) Retry(_ context.Context, ids []int64, sendErr string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried = append(f.retried, ids)
	f.lastError = sendErr
	return f.opErr
}

func (f *fakeQueueStore) Fail(_ context.Context, ids []int64, sendErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, ids)
	f.lastError = sendErr
	return f.opErr
}

func (f *fakeQueueStore) PurgeOld(_ context.Context, _, _ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges++
	return 0, f.opErr
}

// Verify interface compliance: the fake must stay a real QueueStore, or the
// worker tests would prove nothing about the contract it drains.
var _ notification.QueueStore = (*fakeQueueStore)(nil)
