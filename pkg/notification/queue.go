package notification

import (
	"context"
	"time"
)

// QueueStore persists and claims queued notifications. The enqueue path writes
// through it; a send worker claims from it under a lease and resolves what it
// claimed. internal/notification/notifyqueue holds the PostgreSQL
// implementation.
type QueueStore interface {
	// Enqueue inserts a pending row and nudges the send worker.
	Enqueue(ctx context.Context, n Notification) error
	// ClaimImmediate claims the next due non-digest row under a lease,
	// returning ErrNoWork when none is due.
	ClaimImmediate(ctx context.Context, lease time.Duration) (*Notification, error)
	// ClaimDigest claims every due digest row for one recipient under a
	// lease, returning ErrNoWork when none is due.
	ClaimDigest(ctx context.Context, lease time.Duration) ([]Notification, error)
	// MarkSent transitions claimed rows to sent.
	MarkSent(ctx context.Context, ids []int64) error
	// Retry returns claimed rows to pending after backoff, recording the error.
	Retry(ctx context.Context, ids []int64, sendErr string, backoff time.Duration) error
	// Fail marks claimed rows permanently failed, recording the error.
	Fail(ctx context.Context, ids []int64, sendErr string) error
	// PurgeOld bounds table growth: it deletes resolved (sent/failed) rows
	// older than resolvedRetention and unresolved rows older than
	// pendingTTL. The latter also guarantees that enabling SMTP on a
	// deployment that queued for months does not deliver an ancient
	// backlog. Returns the number of rows deleted.
	PurgeOld(ctx context.Context, resolvedRetention, pendingTTL time.Duration) (int64, error)
}
