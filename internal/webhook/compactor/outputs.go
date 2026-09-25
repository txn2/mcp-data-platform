package compactor

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// The ports for what a compaction produces: the window's resource, and the
// metrics it is counted in.

// StoredWindow is where a compacted window's resource keeps its file.
type StoredWindow struct {
	ResourceID string
	// Key is the object key of the file, in the managed-resources bucket.
	// Its directory is what the window's partition is registered at.
	Key string
}

// WindowResources writes and deletes the managed resource each compacted window
// is.
type WindowResources interface {
	// Put writes a window's Parquet file: a new resource when existingID is
	// empty or names one that is gone, and a new version of it otherwise. The
	// source decides whose library a new resource is in.
	Put(ctx context.Context, src whsource.Source, window time.Time, existingID string, content []byte) (StoredWindow, error)
	// Key returns where a resource's current file is, and false when the
	// resource is gone.
	Key(ctx context.Context, id string) (string, bool, error)
	// Delete removes a resource and every version of its file. A resource
	// already gone is not an error.
	Delete(ctx context.Context, id string) error
}

// Metrics is what the compactor reports. A nil Metrics reports nothing.
type Metrics interface {
	WebhookCompaction(ctx context.Context, source, result string)
	WebhookDuplicatesDropped(ctx context.Context, source string, n int64)
}
