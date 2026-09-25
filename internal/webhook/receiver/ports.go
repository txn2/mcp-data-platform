package receiver

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// The ports the receiver acts through, declared here so a test can stand in
// for each and see exactly what it was asked to do.

// SourceLister lists every source. whsource.Store satisfies it.
type SourceLister interface {
	List(ctx context.Context) ([]whsource.Source, error)
}

// ObjectWriter writes one object. The managed-resources S3 client satisfies
// it.
type ObjectWriter interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
}

// Recorder is the control data the receiver writes. whstore.Store satisfies
// it.
type Recorder interface {
	MarkSegment(ctx context.Context, source string, start time.Time, length time.Duration) error
	RecordCounts(ctx context.Context, counts []whstore.Count) error
	RecordRejections(ctx context.Context, rejections []whstore.Rejection) error
}

// RawWindows registers a new window's raw partition, so an event is queryable
// as soon as it is acknowledged. A nil RawWindows leaves new windows to the
// compactor's sync.
type RawWindows interface {
	EnsureRawWindow(ctx context.Context, src whsource.Source, start time.Time) error
}

// Metrics is what the receiver reports to the platform's metrics registry. A
// nil Metrics reports nothing.
type Metrics interface {
	WebhookRequest(ctx context.Context, source, outcome string)
	WebhookEvents(ctx context.Context, source string, n int)
	WebhookSegmentWritten(ctx context.Context, source string)
	WebhookAck(ctx context.Context, source string, d time.Duration)
	// WebhookBuffer reports how many events a source holds in memory: those
	// waiting to be written and those in a write under way.
	WebhookBuffer(ctx context.Context, source string, events int)
}
