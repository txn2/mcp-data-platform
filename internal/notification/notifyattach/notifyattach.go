// Package notifyattach reads the files a channel document carries, at delivery.
//
// A queued document names its attachments -- a portal asset at a version, an
// asset's preview tile, or a managed resource -- and never holds their bytes,
// so the queue row stays small. The send worker reads each one here when it
// delivers. The sender's entitlement to each file was checked when the
// document was queued (internal/platform/notifylayer); nothing here asks again,
// because the worker acts for nobody.
package notifyattach

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// ErrTooLarge is returned for an attachment over notification.MaxAttachmentBytes.
// The worker sends its link instead.
var ErrTooLarge = errors.New("notifyattach: attachment is over the size a channel carries")

// ErrUnavailable is returned for an attachment this deployment cannot read: no
// store for its kind, or the file is gone.
var ErrUnavailable = errors.New("notifyattach: attachment is unavailable")

// previewContentType is the type of an asset's tile image.
const previewContentType = "image/png"

// Assets is the portal read an asset attachment needs.
type Assets interface {
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
}

// Versions reads the version an attachment was pinned to.
type Versions interface {
	GetByVersion(ctx context.Context, assetID string, version int) (*portaldomain.AssetVersion, error)
}

// Objects reads a stored object.
type Objects interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
}

// Resources is the managed-resource read a resource attachment needs.
type Resources interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// Loader reads attachments. Any field may be nil; an attachment of a kind
// whose stores are absent is ErrUnavailable.
type Loader struct {
	Assets         Assets
	Versions       Versions
	AssetObjects   Objects
	Resources      Resources
	ResourceObjs   Objects
	ResourceBucket string
}

// Load reads one attachment's content.
func (l *Loader) Load(ctx context.Context, att notification.Attachment) (notification.File, error) {
	if l == nil {
		return notification.File{}, ErrUnavailable
	}
	switch {
	case att.AssetID != "" && att.Preview:
		return l.loadPreview(ctx, att)
	case att.AssetID != "":
		return l.loadAsset(ctx, att)
	case att.ResourceID != "":
		return l.loadResource(ctx, att)
	default:
		return notification.File{}, ErrUnavailable
	}
}

// object is one stored file and what the reader is shown of it.
type object struct {
	bucket, key, name, contentType string
	size                           int64
}

// loadAsset reads the asset's content at the version the document pinned, or
// its current content for an attachment queued with no version.
func (l *Loader) loadAsset(ctx context.Context, att notification.Attachment) (notification.File, error) {
	if l.Assets == nil || l.AssetObjects == nil {
		return notification.File{}, ErrUnavailable
	}
	asset, err := l.Assets.Get(ctx, att.AssetID)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return notification.File{}, fmt.Errorf("asset %s: %w", att.AssetID, ErrUnavailable)
	}
	obj, err := l.assetObject(ctx, asset, att)
	if err != nil {
		return notification.File{}, err
	}
	return read(ctx, l.AssetObjects, obj)
}

// assetObject is the stored file an asset attachment names: the pinned
// version's when it differs from the current one.
func (l *Loader) assetObject(ctx context.Context, asset *portaldomain.Asset, att notification.Attachment) (object, error) {
	obj := object{bucket: asset.S3Bucket, key: asset.S3Key, name: att.Name, contentType: asset.ContentType, size: asset.SizeBytes}
	if att.Version <= 0 || att.Version == asset.CurrentVersion || l.Versions == nil {
		return obj, nil
	}
	v, err := l.Versions.GetByVersion(ctx, att.AssetID, att.Version)
	if err != nil || v == nil {
		return object{}, fmt.Errorf("asset %s version %d: %w", att.AssetID, att.Version, ErrUnavailable)
	}
	obj.bucket, obj.key, obj.contentType, obj.size = v.S3Bucket, v.S3Key, v.ContentType, v.SizeBytes
	return obj, nil
}

// loadPreview reads the asset's light tile.
func (l *Loader) loadPreview(ctx context.Context, att notification.Attachment) (notification.File, error) {
	if l.Assets == nil || l.AssetObjects == nil {
		return notification.File{}, ErrUnavailable
	}
	asset, err := l.Assets.Get(ctx, att.AssetID)
	if err != nil || asset == nil || asset.DeletedAt != nil || asset.ThumbnailS3Key == "" {
		return notification.File{}, fmt.Errorf("preview of asset %s: %w", att.AssetID, ErrUnavailable)
	}
	return read(ctx, l.AssetObjects, object{bucket: asset.S3Bucket, key: asset.ThumbnailS3Key, name: att.Name, contentType: previewContentType})
}

// loadResource reads a managed resource's current content.
func (l *Loader) loadResource(ctx context.Context, att notification.Attachment) (notification.File, error) {
	if l.Resources == nil || l.ResourceObjs == nil {
		return notification.File{}, ErrUnavailable
	}
	res, err := l.Resources.Get(ctx, att.ResourceID)
	if err != nil || res == nil {
		return notification.File{}, fmt.Errorf("resource %s: %w", att.ResourceID, ErrUnavailable)
	}
	name := att.Name
	if name == "" {
		name = res.Filename
	}
	return read(ctx, l.ResourceObjs, object{bucket: l.ResourceBucket, key: res.S3Key, name: name, contentType: res.MIMEType, size: res.SizeBytes})
}

// read fetches an object, refusing one the record already says is too large
// before reading it, and one that turned out too large after.
func read(ctx context.Context, objs Objects, obj object) (notification.File, error) {
	if obj.size > notification.MaxAttachmentBytes {
		return notification.File{}, ErrTooLarge
	}
	data, storedType, err := objs.GetObject(ctx, obj.bucket, obj.key)
	if err != nil {
		return notification.File{}, fmt.Errorf("reading %s: %w", obj.key, errors.Join(ErrUnavailable, err))
	}
	if len(data) > notification.MaxAttachmentBytes {
		return notification.File{}, ErrTooLarge
	}
	contentType := obj.contentType
	if contentType == "" {
		contentType = storedType
	}
	return notification.File{Name: obj.name, ContentType: contentType, Data: data}, nil
}
