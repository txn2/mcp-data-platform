package whimap

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Ports.
type (
	// Sources lists the stored sources, secrets decrypted.
	Sources interface {
		List(ctx context.Context) ([]whsource.Source, error)
	}
	// Objects writes a segment to the managed-resources bucket.
	Objects interface {
		PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	}
	// Recorder records a window a segment was written to.
	Recorder interface {
		MarkSegment(ctx context.Context, source string, start time.Time, length time.Duration, bytes int64) error
	}
	// RawWindows registers a window's raw partition so its events are
	// queryable before compaction.
	RawWindows interface {
		EnsureRawWindow(ctx context.Context, src whsource.Source, start time.Time) error
	}
	// MarkStore keeps each folder's watermark.
	MarkStore interface {
		Get(ctx context.Context, source, folder string) (Mark, error)
		Put(ctx context.Context, mk Mark) error
	}
)
