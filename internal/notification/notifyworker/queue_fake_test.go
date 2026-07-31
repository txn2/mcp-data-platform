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
}

func (*fakeQueueStore) Enqueue(context.Context, notification.Notification) error { return nil }

func (f *fakeQueueStore) ClaimImmediate(_ context.Context, _ time.Duration) (*notification.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	return &batch[0], nil
}

func (f *fakeQueueStore) ClaimDigest(_ context.Context, _ time.Duration) ([]notification.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	return batch, nil
}

func (f *fakeQueueStore) MarkSent(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ids)
	return f.opErr
}

func (f *fakeQueueStore) Retry(_ context.Context, ids []int64, _ string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried = append(f.retried, ids)
	return f.opErr
}

func (f *fakeQueueStore) Fail(_ context.Context, ids []int64, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, ids)
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
