package indexjobs

import (
	"context"
	"time"
)

// noopStore is a Store whose every method is a trivial no-op. Test
// stubs embed it and override only the methods the test exercises,
// which keeps each stub focused and avoids a wall of unused-receiver
// boilerplate.
type noopStore struct{}

func (*noopStore) Enqueue(context.Context, Key, Trigger) (bool, error)            { return true, nil }
func (*noopStore) Claim(context.Context, string) (*Job, error)                    { return nil, ErrNoJob }
func (*noopStore) Complete(context.Context, int64, string) error                  { return nil }
func (*noopStore) Retry(context.Context, int64, string, string) error             { return nil }
func (*noopStore) Fail(context.Context, int64, string, string) error              { return nil }
func (*noopStore) ReleaseExpiredLeases(context.Context) (int, error)              { return 0, nil }
func (*noopStore) RenewLease(context.Context, int64, string, time.Duration) error { return nil }
func (*noopStore) UpdateProgress(context.Context, int64, string, int) error       { return nil }
func (*noopStore) Get(context.Context, int64) (*Job, error)                       { return nil, ErrNotFound }
func (*noopStore) List(context.Context, ListFilter) ([]Job, error)                { return nil, nil }
func (*noopStore) Counts(context.Context, string) (*KindCounts, error)            { return &KindCounts{}, nil }
