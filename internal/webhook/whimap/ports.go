package whimap

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whwrite"
)

// Ports.
type (
	// Sources lists the stored sources, secrets decrypted.
	Sources interface {
		List(ctx context.Context) ([]whsource.Source, error)
	}
	// Objects writes a segment to the managed-resources bucket.
	Objects = whwrite.Objects
	// Recorder records a window a segment was written to.
	Recorder = whwrite.Recorder
	// RawWindows registers a window's raw partition so its events are
	// queryable before compaction.
	RawWindows = whwrite.RawWindows
	// Rejections keeps what a poll skipped, in the source's rejections list
	// beside the receiver's refusals (#2459).
	Rejections interface {
		RecordRejections(ctx context.Context, rejections []whstore.Rejection) error
	}
	// Metrics counts what a poll skipped, under webhook_requests_total.
	Metrics interface {
		WebhookRequest(ctx context.Context, source, outcome string)
	}
	// MarkStore keeps each folder's watermark.
	MarkStore interface {
		Get(ctx context.Context, source, folder string) (Mark, error)
		Put(ctx context.Context, mk Mark) error
	}
)
