package thumbworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

const tileContentType = "image/png"

// errTileTooLarge is a render that returned more than a tile can be.
var errTileTooLarge = errors.New("the drawn tile is larger than a tile can be")

// variantsOf is the tiles a document has: light, and dark for the families
// drawn on a forced background.
func variantsOf(contentType string) []string {
	if thumbtypes.IsThemeable(contentType) {
		return []string{portaldomain.ThumbnailVariantLight, portaldomain.ThumbnailVariantDark}
	}
	return []string{portaldomain.ThumbnailVariantLight}
}

// drawn is one variant stored.
type drawn struct {
	variant string
	key     string
}

// target is where one document's tiles go.
type target struct {
	src    tileSource
	bucket string
	blobs  Blobs
	keyFor func(variant string) string
}

// drawVariants renders and stores each variant in turn. It stops at the first
// variant that fails: unfinished is why the attempt did not finish -- the
// renderer or storage was unavailable -- and reason is a failure the document
// owns, which is recorded. Either way the variants already stored are kept.
func (w *Worker) drawVariants(ctx context.Context, t target, variants []string) (stored []drawn, reason string, unfinished error) {
	for _, v := range variants {
		src := t.src
		src.dark = v == portaldomain.ThumbnailVariantDark
		png, err := w.render(ctx, w.tilePage(src))
		if retry, why := outcome(err); retry || why != "" {
			if retry {
				return stored, "", err
			}
			return stored, why, nil
		}
		key := t.keyFor(v)
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		err = t.blobs.PutObject(sctx, t.bucket, key, png, tileContentType)
		cancel()
		if err != nil {
			return stored, "", fmt.Errorf("storing the tile: %w", err)
		}
		stored = append(stored, drawn{variant: v, key: key})
	}
	return stored, "", nil
}

// render draws one page, bounded by the render timeout, and refuses a result
// that is not tile-sized.
func (w *Worker) render(ctx context.Context, page headless.Page) ([]byte, error) {
	rctx, cancel := w.renderCtx(ctx)
	defer cancel()
	png, err := w.deps.Drawer.Render(rctx, page)
	if err != nil {
		return nil, fmt.Errorf("rendering: %w", err)
	}
	if len(png) > portaldomain.MaxThumbnailBytes {
		return nil, errTileTooLarge
	}
	return png, nil
}

// readObject reads one object, bounded.
func readObject(ctx context.Context, blobs Blobs, bucket, key string) ([]byte, error) {
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	data, _, err := blobs.GetObject(sctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	return data, nil
}

// unreadable is a document's stored file that could not be read. It is
// counted against the document like a renderer that stopped answering: a file
// that is gone never becomes readable, and one that is briefly unreachable is
// read on a later attempt (#1868).
func unreadable(err error) error {
	return fmt.Errorf("the stored file could not be read: %w", err)
}

// drawAsset draws an asset's tiles and records them against the version it
// claimed. An asset rewritten while it was drawn records the older version and
// stays owed, so its new content is drawn next. It returns why the attempt did
// not finish, having recorded any variant it drew without ending the claim.
func (w *Worker) drawAsset(ctx context.Context, a portaldomain.Asset) error {
	data, err := readObject(ctx, w.deps.AssetBlobs, a.S3Bucket, a.S3Key)
	if err != nil {
		return unreadable(err)
	}
	// The head is taken before the references are rewritten, so a document
	// drawn from its head is scanned for references over the bytes that reach
	// the tile page rather than over the whole file.
	data = headFor(a.ContentType, data)
	if !isBinary(a.ContentType) {
		data = w.rewriteRefs(ctx, a.ID, a.ContentType, data)
	}
	stored, reason, unfinished := w.drawVariants(ctx, target{
		src:    tileSource{contentType: a.ContentType, content: data, name: a.Name},
		bucket: a.S3Bucket,
		blobs:  w.deps.AssetBlobs,
		keyFor: func(v string) string { return portaldomain.DeriveThumbnailKeyVariant(a.S3Key, v) },
	}, variantsOf(a.ContentType))
	if unfinished != nil && len(stored) == 0 {
		return unfinished
	}
	w.recordAsset(ctx, a, stored, reason, unfinished == nil)
	return unfinished
}

// rewriteRefs points a document's declared references at the platform's
// reference route, root-relative, which the tile page's origin resolves to
// the route answered in-process. A document whose references cannot be listed
// is drawn as stored, which is what a reader is served in the same case.
func (w *Worker) rewriteRefs(ctx context.Context, assetID, contentType string, data []byte) []byte {
	if w.deps.Refs == nil {
		return data
	}
	refs, err := w.deps.Refs.ListByAsset(ctx, assetID)
	if err != nil {
		slog.Warn("thumbnails: listing references failed; drawing the document as stored",
			"asset", logsan.SanitizeForLog(assetID), logKeyError, logsan.SanitizeForLog(err.Error()))
		return data
	}
	return assetrefs.Rewrite(data, contentType, "", assetID, refs)
}

// recordAsset writes what was drawn: the stored variants, and either the
// renderer generation with any earlier failure cleared, or the reason drawing
// stopped. When the attempt finished the claim ends with the write; when it
// did not, the claim is left for the attempt to end, with its count. A
// superseded tile object is removed after the row stops naming it, so a
// failed delete leaves an orphan rather than an asset with no tile.
func (w *Worker) recordAsset(ctx context.Context, a portaldomain.Asset, stored []drawn, reason string, finished bool) {
	version := a.CurrentVersion
	u := portaldomain.AssetUpdate{ReleaseThumbnailClaim: finished}
	for i := range stored {
		key := stored[i].key
		if stored[i].variant == portaldomain.ThumbnailVariantDark {
			u.ThumbnailDarkS3Key, u.ThumbnailDarkVersion = &key, &version
		} else {
			u.ThumbnailS3Key, u.ThumbnailVersion = &key, &version
		}
	}
	if reason != "" {
		u.ThumbnailFailure, u.ThumbnailFailedVersion = &reason, &version
	} else {
		renderer, cleared, zero := Renderer, "", 0
		u.ThumbnailRenderer, u.ThumbnailFailure, u.ThumbnailFailedVersion = &renderer, &cleared, &zero
	}
	if err := w.deps.Assets.Update(ctx, a.ID, u); err != nil {
		slog.Error("thumbnails: recording an asset's tile failed", "asset", logsan.SanitizeForLog(a.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	for _, d := range stored {
		old := a.ThumbnailS3Key
		if d.variant == portaldomain.ThumbnailVariantDark {
			old = a.ThumbnailDarkS3Key
		}
		w.removeSuperseded(ctx, w.deps.AssetBlobs, a.S3Bucket, old, d.key)
	}
}

func (*Worker) removeSuperseded(ctx context.Context, blobs Blobs, bucket, old, current string) {
	if old == "" || old == current {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	if err := blobs.DeleteObject(sctx, bucket, old); err != nil {
		slog.Warn("thumbnails: removing a superseded tile failed", "key", logsan.SanitizeForLog(old), logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// drawResource draws a file's tiles and dates them by the file as it stood
// when claimed, so a file written while it was drawn stays owed. It ends the
// claim once every variant is drawn or the file's own failure is recorded, and
// otherwise returns why the attempt did not finish, having recorded any
// variant it drew.
func (w *Worker) drawResource(ctx context.Context, r resource.Resource) error {
	data, err := readObject(ctx, w.deps.ResourceBlobs, w.deps.ResourceBucket, r.S3Key)
	if err != nil {
		return unreadable(err)
	}
	stored, reason, unfinished := w.drawVariants(ctx, target{
		src:    tileSource{contentType: r.MIMEType, content: headFor(r.MIMEType, data), name: r.DisplayName},
		bucket: w.deps.ResourceBucket,
		blobs:  w.deps.ResourceBlobs,
		keyFor: func(v string) string { return resource.ThumbnailKeyFor(r.S3Key, v) },
	}, variantsOf(r.MIMEType))
	for _, d := range stored {
		if err := w.deps.Resources.SetThumbnail(ctx, r.ID, resource.ThumbnailCapture{
			Variant: d.variant, S3Key: d.key, CapturedAt: r.UpdatedAt, Renderer: Renderer,
		}); err != nil {
			slog.Error("thumbnails: recording a resource's tile failed", "resource", logsan.SanitizeForLog(r.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
			return nil
		}
	}
	switch {
	case unfinished != nil:
		return unfinished
	case reason != "":
		if err := w.deps.Resources.RecordThumbnailFailure(ctx, r.ID, reason, r.UpdatedAt); err != nil {
			slog.Error("thumbnails: recording a resource's failure failed", "resource", logsan.SanitizeForLog(r.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	default:
		if err := w.deps.Resources.HoldThumbnailWork(ctx, r.ID, 0, 0); err != nil {
			slog.Warn("thumbnails: ending a resource's claim failed; it lapses with its lease", "resource", logsan.SanitizeForLog(r.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
	return nil
}

// drawCollection composes a collection's mosaics from the member tiles its
// source names -- a light one from their light tiles and a dark one from their
// dark tiles, a member without one lending its light tile -- or clears the
// mosaics it holds when no member has a tile. Both are stored before the row
// is written, so a recorded key always has its dark neighbor (#1789). A
// mosaic the renderer refuses is recorded as not composable from this source
// (#1868); one that did not finish returns why.
func (w *Worker) drawCollection(ctx context.Context, c portaldomain.CollectionThumbnailWork) error {
	if c.Source == "" {
		w.clearCollection(ctx, c)
		return nil
	}
	light, dark := w.memberTiles(ctx, c.Source)
	if len(light) == 0 {
		return errNoMemberTile
	}
	for _, v := range []struct {
		variant string
		tiles   [][]byte
	}{{portaldomain.ThumbnailVariantLight, light}, {portaldomain.ThumbnailVariantDark, dark}} {
		reason, unfinished := w.storeMosaic(ctx, c.ID, v.variant, v.tiles)
		if unfinished != nil {
			return unfinished
		}
		if reason != "" {
			if err := w.deps.Collections.RecordCollectionThumbnailFailure(ctx, c.ID, c.Source, reason); err != nil {
				slog.Error("thumbnails: recording a collection's failure failed", logKeyCollection, logsan.SanitizeForLog(c.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
			}
			return nil
		}
	}
	key := portaldomain.CollectionThumbnailKey(c.ID, portaldomain.ThumbnailVariantLight)
	if err := w.deps.Collections.RecordCollectionThumbnail(ctx, c.ID, key, c.Source); err != nil {
		slog.Error("thumbnails: recording a collection's tile failed", logKeyCollection, logsan.SanitizeForLog(c.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
	}
	return nil
}

// errNoMemberTile is a collection whose source names member tiles none of
// which could be read.
var errNoMemberTile = errors.New("none of the member tiles its mosaic is composed from could be read")

// storeMosaic composes one variant of a collection's mosaic and stores it. It
// returns the renderer's refusal, which is the mosaic's, or why the attempt
// did not finish.
func (w *Worker) storeMosaic(ctx context.Context, id, variant string, tiles [][]byte) (reason string, unfinished error) {
	png, err := w.render(ctx, mosaicPage(tiles))
	if retry, why := outcome(err); retry {
		return "", err
	} else if why != "" {
		return why, nil
	}
	key := portaldomain.CollectionThumbnailKey(id, variant)
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err = w.deps.AssetBlobs.PutObject(sctx, w.deps.CollectionBucket, key, png, tileContentType)
	cancel()
	if err != nil {
		return "", fmt.Errorf("storing the mosaic: %w", err)
	}
	return "", nil
}

func (w *Worker) clearCollection(ctx context.Context, c portaldomain.CollectionThumbnailWork) {
	if err := w.deps.Collections.RecordCollectionThumbnail(ctx, c.ID, "", ""); err != nil {
		slog.Error("thumbnails: clearing a collection's tile failed", logKeyCollection, logsan.SanitizeForLog(c.ID), logKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	w.removeSuperseded(ctx, w.deps.AssetBlobs, w.deps.CollectionBucket, c.ThumbnailS3Key, "")
	if c.ThumbnailS3Key != "" {
		w.removeSuperseded(ctx, w.deps.AssetBlobs, w.deps.CollectionBucket, portaldomain.CollectionThumbnailKey(c.ID, portaldomain.ThumbnailVariantDark), "")
	}
}

// memberTiles reads the tiles of each member the source names, in order: its
// light tile, and its dark tile or, for a member stored once, the light tile
// again. A member whose tile cannot be read is left out; the source still
// records it, so the mosaic is composed again when that member's tile is next
// redrawn.
func (w *Worker) memberTiles(ctx context.Context, source string) (light, dark [][]byte) {
	for entry := range strings.SplitSeq(source, ",") {
		id, _, _ := strings.Cut(entry, ":")
		a, err := w.deps.Assets.Get(ctx, id)
		if err != nil || a == nil || a.ThumbnailS3Key == "" {
			continue
		}
		l, err := readObject(ctx, w.deps.AssetBlobs, a.S3Bucket, a.ThumbnailS3Key)
		if err != nil {
			continue
		}
		d := l
		if a.ThumbnailDarkS3Key != "" {
			if data, err := readObject(ctx, w.deps.AssetBlobs, a.S3Bucket, a.ThumbnailDarkS3Key); err == nil {
				d = data
			}
		}
		light, dark = append(light, l), append(dark, d)
	}
	return light, dark
}
