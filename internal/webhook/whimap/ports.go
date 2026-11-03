package whimap

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
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
	// MarkStore keeps each folder's watermark.
	MarkStore interface {
		Get(ctx context.Context, source, folder string) (Mark, error)
		Put(ctx context.Context, mk Mark) error
	}
)
