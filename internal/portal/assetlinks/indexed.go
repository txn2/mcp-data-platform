package assetlinks

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/contentrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// maxIndexedBytes bounds the content read for links. A document past it is
// recorded as linking nothing rather than read whole on a write path.
const maxIndexedBytes = 4 << 20

// Getter reads one stored object.
type Getter interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
}

// Recorder records an asset's links; *Store is the one the platform wires.
type Recorder interface {
	Replace(ctx context.Context, fromID string, toIDs []string) error
}

// Indexed is a version store that records the assets each new version's
// content links to, after the version is written. Every write path records a
// version, so every write path records its links.
type Indexed struct {
	portaldomain.VersionStore
	links Recorder
	blobs Getter
}

// NewIndexed wraps versions so each version written records its links in
// links.
func NewIndexed(versions portaldomain.VersionStore, links *Store, blobs Getter) *Indexed {
	return indexedOver(versions, links, blobs)
}

// indexedOver is NewIndexed over any Recorder.
func indexedOver(versions portaldomain.VersionStore, links Recorder, blobs Getter) *Indexed {
	return &Indexed{VersionStore: versions, links: links, blobs: blobs}
}

// CreateVersion writes the version, then records what its content links to.
// A failure to record is logged and leaves the version written: the links
// are a list beside the asset, not part of its content.
func (i *Indexed) CreateVersion(ctx context.Context, v portaldomain.AssetVersion) (int, error) {
	n, err := i.VersionStore.CreateVersion(ctx, v)
	if err != nil {
		return n, err //nolint:wrapcheck // the wrapped store's own error
	}
	if err := i.links.Replace(ctx, v.AssetID, i.linked(ctx, v)); err != nil {
		slog.Warn("asset links not recorded", // #nosec G706 -- structured slog call; values sanitized
			"asset", logsan.SanitizeForLog(v.AssetID), "error", logsan.SanitizeForLog(err.Error()))
	}
	return n, nil
}

// linked reads the version's content and returns the assets it links to.
func (i *Indexed) linked(ctx context.Context, v portaldomain.AssetVersion) []string {
	if !Linkable(v.ContentType) || v.SizeBytes > maxIndexedBytes || i.blobs == nil {
		return nil
	}
	body, _, err := i.blobs.GetObject(ctx, v.S3Bucket, v.S3Key)
	if err != nil {
		slog.Warn("asset links not read", // #nosec G706 -- structured slog call; values sanitized
			"asset", logsan.SanitizeForLog(v.AssetID), "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return contentrefs.LinkedAssets(string(body), v.ContentType)
}

// Linkable reports whether content of this type can link an asset: HTML,
// XHTML, JSX, SVG and Markdown.
func Linkable(contentType string) bool {
	switch contenttype.Normalize(contentType) {
	case contenttype.HTML, contenttype.XHTML, contenttype.JSX, contenttype.SVG, contenttype.Markdown:
		return true
	}
	return false
}
