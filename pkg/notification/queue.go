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
	// ClaimImmediate claims the next due non-digest row deliverable under
	// filter, returning ErrNoWork when none is due.
	ClaimImmediate(ctx context.Context, lease time.Duration, filter TransportFilter) (*Notification, error)
	// ClaimDigest claims every due digest row for one recipient under a
	// lease, returning ErrNoWork when none is due.
	ClaimDigest(ctx context.Context, lease time.Duration, filter TransportFilter) ([]Notification, error)
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

// TransportFilter narrows a claim to the rows the worker can currently
// deliver. It exists because the two transports fail independently: a
// deployment that has configured channels but no mail server delivers to chat
// and leaves its email rows pending, and a deployment whose api gateway is
// still wiring delivers email and leaves its chat rows pending.
//
// Before channels the worker asked one question -- is SMTP usable? -- and
// answered no by draining nothing. That answer is wrong once a row can be
// deliverable by another route, so the question moved into the claim.
type TransportFilter struct {
	// Email includes every row delivered over SMTP: rows addressed to a
	// person, whether or not an email channel put them there.
	Email bool
	// Channel includes rows addressed to a channel destination, delivered
	// over the channel kind's HTTP transport.
	Channel bool
}

// Deliverable reports whether the filter admits any row at all. A worker whose
// filter admits nothing skips the claim rather than issuing a query that
// cannot match.
func (f TransportFilter) Deliverable() bool { return f.Email || f.Channel }
