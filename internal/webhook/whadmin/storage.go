package whadmin

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// The ports for what a source wrote to object storage.

// Objects lists and deletes what a source wrote outside the resource store.
type Objects interface {
	ListKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Resources deletes a compacted window's resource, the attachments an imap
// source saved (#2247), and the stored folders a source's files left (#2836).
type Resources interface {
	Delete(ctx context.Context, id string) error
	DeleteAttachments(ctx context.Context, src whsource.Source) error
	DeleteSourceFolder(ctx context.Context, src whsource.Source) error
}
