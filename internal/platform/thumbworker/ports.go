package thumbworker

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// The worker's ports: the browser it draws in and the stores it claims from
// and records to. Each is the narrow slice of a real store the worker calls,
// so a fake in a test is the contract and nothing more.

// Drawer is the browser a tile is drawn in.
type Drawer interface {
	Ping(ctx context.Context) error
	Render(ctx context.Context, p headless.Page) ([]byte, error)
}

// Blobs is object storage.
type Blobs interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	DeleteObject(ctx context.Context, bucket, key string) error
}

// AssetWork is what the worker asks of the asset store.
type AssetWork interface {
	ClaimThumbnailWork(ctx context.Context, renderer int, lease time.Duration, limit int) ([]portaldomain.Asset, error)
	HoldThumbnailWork(ctx context.Context, id string, hold time.Duration, attempts int) error
	Update(ctx context.Context, id string, u portaldomain.AssetUpdate) error
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
}

// CollectionWork is what the worker asks of the collection store.
type CollectionWork interface {
	ClaimCollectionThumbnailWork(ctx context.Context, lease time.Duration, limit int) ([]portaldomain.CollectionThumbnailWork, error)
	RecordCollectionThumbnail(ctx context.Context, id, key, source string) error
	RecordCollectionThumbnailFailure(ctx context.Context, id, source, reason string) error
	HoldCollectionThumbnailWork(ctx context.Context, id string, hold time.Duration, attempts int) error
}

// RefLister lists the references an asset declared.
type RefLister interface {
	ListByAsset(ctx context.Context, assetID string) ([]assetrefs.Ref, error)
}

// tileBlobs reports every object operation the worker makes, the reads of a
// document's stored file included, as thumbnail work (internal/objectobs),
// whichever bucket's client it runs through.
type tileBlobs struct{ b Blobs }

// thumbnailBlobs wraps b, keeping a nil store nil.
func thumbnailBlobs(b Blobs) Blobs {
	if b == nil {
		return nil
	}
	return tileBlobs{b: b}
}

func thumbnailCtx(ctx context.Context) context.Context {
	return objectobs.WithPurpose(ctx, observability.StoragePurposeThumbnails)
}

// GetObject reads one object.
func (t tileBlobs) GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error) {
	return t.b.GetObject(thumbnailCtx(ctx), bucket, key) //nolint:wrapcheck // a pass-through
}

// PutObject stores one object.
func (t tileBlobs) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	return t.b.PutObject(thumbnailCtx(ctx), bucket, key, data, contentType) //nolint:wrapcheck // a pass-through
}

// DeleteObject removes one object.
func (t tileBlobs) DeleteObject(ctx context.Context, bucket, key string) error {
	return t.b.DeleteObject(thumbnailCtx(ctx), bucket, key) //nolint:wrapcheck // a pass-through
}
