package assetrefs

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The query a reader asks for a reference's tile with, rather than its bytes.
//
// It is the same route and the same token because it is the same grant, read
// down: a tile is a 400x300 picture of a file the token already serves in
// full. An asset's References panel shows a reference through this route
// rather than through the target's own, because a reader of a shared asset may
// have no access to the target at all (#1794).
const (
	thumbnailQueryKey   = "thumbnail"
	thumbnailQueryValue = "1"
	variantQueryKey     = "variant"
	// thumbnailContentType is what the tile writer stores and the only thing
	// this route serves, whatever the target's own type is.
	thumbnailContentType = "image/png"
	msgNoThumbnail       = "no thumbnail has been drawn for the referenced file"
)

// wantsThumbnail reports whether this request asks for the reference's tile.
func wantsThumbnail(r *http.Request) bool {
	return r.URL.Query().Get(thumbnailQueryKey) == thumbnailQueryValue
}

// thumbnailVariant is which capture the request is about. Anything but the
// dark one is the light one: the light one is what a caller naming nothing
// wants, and an unknown value is not worth a refusal on a read of an image.
// Which object a variant resolves to is each kind's own rule --
// portaldomain.Asset.StoredThumbnailKey and resource.StoredThumbnailKey --
// including the fall back to the light capture for a family that stores one.
func thumbnailVariant(r *http.Request) string {
	if r.URL.Query().Get(variantQueryKey) == portaldomain.ThumbnailVariantDark {
		return portaldomain.ThumbnailVariantDark
	}
	return portaldomain.ThumbnailVariantLight
}

// thumbnailContent reads the stored tile of whatever the reference points at.
//
// A target with no tile answers 404, which is what tells the panel to show a
// content-type icon instead -- the same answer the target's own thumbnail
// routes give, so a caller cannot tell "no tile" apart by which route it asked
// through.
func (s *Server) thumbnailContent(
	w http.ResponseWriter, r *http.Request, ref *Ref,
) (blobserve.Options, bool) {
	switch ref.TargetKind {
	case TargetResource:
		return s.resourceThumbnail(w, r, ref.TargetID)
	case TargetAsset:
		return s.assetThumbnail(w, r, ref.TargetID)
	default:
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
		return blobserve.Options{}, false
	}
}

// resourceThumbnail serves a referenced managed resource's stored tile.
func (s *Server) resourceThumbnail(
	w http.ResponseWriter, r *http.Request, resourceID string,
) (blobserve.Options, bool) {
	if !s.resourcesReady() {
		httpjson.WriteError(w, http.StatusServiceUnavailable, msgStorageNotReady)
		return blobserve.Options{}, false
	}
	res, err := s.deps.Resources.Get(r.Context(), resourceID)
	if err != nil || res == nil {
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
		return blobserve.Options{}, false
	}
	key := resource.StoredThumbnailKey(res, thumbnailVariant(r))
	if key == "" {
		httpjson.WriteError(w, http.StatusNotFound, msgNoThumbnail)
		return blobserve.Options{}, false
	}
	return tile(w, r, storedTile{blobs: s.deps.Blobs, bucket: s.deps.Bucket, key: key, modTime: res.UpdatedAt})
}

// assetThumbnail serves a referenced asset's stored tile.
func (s *Server) assetThumbnail(
	w http.ResponseWriter, r *http.Request, assetID string,
) (blobserve.Options, bool) {
	if !s.assetsReady() {
		httpjson.WriteError(w, http.StatusServiceUnavailable, msgStorageNotReady)
		return blobserve.Options{}, false
	}
	asset, err := s.deps.Assets.Get(r.Context(), assetID)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, msgResourceMissing)
		return blobserve.Options{}, false
	}
	key := asset.StoredThumbnailKey(thumbnailVariant(r))
	if key == "" {
		httpjson.WriteError(w, http.StatusNotFound, msgNoThumbnail)
		return blobserve.Options{}, false
	}
	return tile(w, r, storedTile{blobs: s.deps.AssetBlobs, bucket: asset.S3Bucket, key: key, modTime: asset.UpdatedAt})
}

// storedTile names one capture: where it lives and what it is a picture of.
type storedTile struct {
	blobs   BlobReader
	bucket  string
	key     string
	modTime time.Time
}

// tile reads one stored capture and describes it to the writer.
//
// A key recorded on the row whose object is gone answers 404 rather than 500:
// the reference is intact and the picture is missing, which is the same thing
// the reader is told when no tile was ever drawn.
func tile(
	w http.ResponseWriter, r *http.Request, t storedTile,
) (blobserve.Options, bool) {
	body, _, err := t.blobs.GetObject(r.Context(), t.bucket, t.key)
	if err != nil {
		if resource.IsObjectNotFound(err) {
			httpjson.WriteError(w, http.StatusNotFound, msgNoThumbnail)
			return blobserve.Options{}, false
		}
		contentFailed(w, "thumbnail_key", t.key, err)
		return blobserve.Options{}, false
	}
	return blobserve.Options{
		Name:        "thumbnail.png",
		ContentType: thumbnailContentType,
		ModTime:     t.modTime,
		Data:        body,
	}, true
}
