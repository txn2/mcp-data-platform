package compactor

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

// The ports the compactor acts through.

// WindowStore is the compactor's half of the control data. whstore.Store
// satisfies it.
type WindowStore interface {
	ClaimOwed(ctx context.Context, endedBy time.Time, lease time.Duration, limit int) ([]whstore.Window, error)
	RecordLocation(ctx context.Context, h whstore.Window, resourceID, location string) error
	RecordCompacted(ctx context.Context, h whstore.Window, c whstore.Compaction) error
	RecordFailure(ctx context.Context, h whstore.Window, reason string, hold time.Duration) error
	RawDeletable(ctx context.Context, source string, cutoff time.Time) ([]whstore.Window, error)
	Expirable(ctx context.Context, source string, cutoff time.Time) ([]whstore.Window, error)
	MarkRawDeleted(ctx context.Context, h whstore.Window) error
	MarkUnregistered(ctx context.Context, h whstore.Window) error
	MarkExpired(ctx context.Context, h whstore.Window) error
	PruneCounts(ctx context.Context, before time.Time) error
}

// Sources reads source definitions. whsource.Store satisfies it.
type Sources interface {
	Get(ctx context.Context, name string) (whsource.Source, error)
	List(ctx context.Context) ([]whsource.Source, error)
}

// Objects reads, lists and deletes objects in the managed-resources bucket.
type Objects interface {
	// ListKeys returns every key under prefix, across as many pages as the
	// listing takes.
	ListKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Tables is what the compactor does in the query engine. whtable.Tables
// satisfies it.
type Tables interface {
	TargetFor(connection string) (whtable.Target, error)
	S3Location(prefix string) string
	RegisterWindow(ctx context.Context, tg whtable.Target, src whsource.Source, window time.Time, location string) error
	UnregisterWindow(ctx context.Context, tg whtable.Target, src whsource.Source, window time.Time) error
	SyncRaw(ctx context.Context, tg whtable.Target, src whsource.Source) error
}
