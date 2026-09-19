package resource

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// A resource's thumbnail: the tile the platform's renderer drew of it, stored
// beside the resource's own object and served from a route of its own (#1554,
// #1787). These routes are what the library reads and how a tile is asked to
// be drawn again; the renderer writes the tile itself.

const (
	// thumbnailMIMEType is the only form a tile is stored in. One type means
	// the serving route needs no stored type of its own.
	thumbnailMIMEType = "image/png"

	// thumbnailLightFilename and thumbnailDarkFilename are what a capture is
	// stored as, beside the resource's own object.
	//
	// The leading dot matters beyond tidiness: a query engine reading an
	// external location treats every non-hidden object under it as data, so a
	// visible thumbnail beside a CSV would be read as rows of that table.
	// Portal assets use the same two names for the same reason.
	thumbnailLightFilename = ".thumbnail.png"
	thumbnailDarkFilename  = ".thumbnail_dark.png"
)

// ThumbnailKeyFor is where a tile is stored: beside the resource's own
// object, under a hidden name.
//
// It is the resource's own copy of the rule portal assets apply to their
// objects. The two live in different prefixes and neither reads the other's
// keys, but the filenames are deliberately the same so a bucket shows one
// convention rather than two.
func ThumbnailKeyFor(s3Key, variant string) string {
	filename := thumbnailLightFilename
	if variant == ThumbnailVariantDark {
		filename = thumbnailDarkFilename
	}
	idx := strings.LastIndex(s3Key, "/")
	if idx < 0 {
		return filename
	}
	return s3Key[:idx+1] + filename
}

// readVariant reads which capture a request is about. Anything but the dark one
// is the light one, because the light one is what a caller naming nothing wants
// and an unknown value is not worth a refusal on a read of an image.
func readVariant(r *http.Request) string {
	if r.URL.Query().Get("variant") == ThumbnailVariantDark {
		return ThumbnailVariantDark
	}
	return ThumbnailVariantLight
}

// storedThumbnailKey is the object a variant is served from, or empty when none
// has been captured.
//
// The dark variant falls back to the light one: a content type carrying its own
// colors stores a single image and serves it in both modes, so its empty dark
// key means "use the light one" rather than "no thumbnail".
func storedThumbnailKey(r *Resource, variant string) string {
	if variant == ThumbnailVariantDark && r.ThumbnailDarkS3Key != "" {
		return r.ThumbnailDarkS3Key
	}
	return r.ThumbnailS3Key
}

// handleGetThumbnail handles GET /api/v1/resources/{id}/thumbnail.
//
// @Summary      Read a resource's thumbnail
// @Description  Serve the PNG the platform drew for a resource. Reading a tile is the authority to read the resource; a resource with no tile answers 404, which is what tells a card to draw its content-type icon instead.
// @Tags         Resources
// @Produce      png
// @Param        id       path   string  true   "Resource ID"
// @Param        variant  query  string  false  "Which tile to serve"  Enums(light, dark)
// @Success      200  {file}  binary
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/thumbnail [get]
func (h *Handler) handleGetThumbnail(w http.ResponseWriter, r *http.Request) {
	res, _, ok := h.thumbnailTarget(w, r)
	if !ok {
		return
	}
	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}

	key := storedThumbnailKey(res, readVariant(r))
	if key == "" {
		writeError(w, http.StatusNotFound, "no thumbnail has been drawn for this resource")
		return
	}

	body, _, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, key)
	if err != nil {
		// The row points at an object the bucket no longer holds. It is a 404
		// rather than a 500 because the answer a caller acts on is the same one
		// a resource with no capture gives: draw the icon.
		slog.Warn("thumbnail object missing", msgError, err)
		writeError(w, http.StatusNotFound, "no thumbnail has been drawn for this resource")
		return
	}

	// A capture is immutable once written: the URL carries the moment it was
	// taken, so a re-capture is a different URL and this one can be held.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	blobserve.Serve(w, r, blobserve.Options{
		Name:        thumbnailLightFilename,
		ContentType: thumbnailMIMEType,
		ModTime:     time.Now().UTC(),
		Data:        body,
	})
}

// handleClearThumbnail handles DELETE /api/v1/resources/{id}/thumbnail.
//
// Both variants go together, which is why this takes no variant. They are two
// views of one file, and a reader asking for the tile to be drawn again means
// the tile, not the half of it their color mode happens to be showing -- the
// same rule the asset route applies (pkg/portal.clearThumbnail). Clearing the
// light one alone would be enough to have the renderer draw the resource again,
// but it would leave a themeable file serving the stale dark tile until the
// replacement landed, which is exactly the wrong picture being complained about.
//
// A tile is written under a deterministic key beside the resource's own object,
// so the next one overwrites what this forgets and leaving the objects in place
// orphans nothing.
//
// @Summary      Clear a resource's thumbnail
// @Description  Forget a resource's tiles, both variants, and any failure recorded against them, so the platform's renderer draws the file again. It is the way back from a tile that is wrong.
// @Tags         Resources
// @Produce      json
// @Param        id       path   string  true   "Resource ID"
// @Success      204
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/thumbnail [delete]
func (h *Handler) handleClearThumbnail(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.thumbnailTarget(w, r)
	if !ok {
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "not allowed to change this resource")
		return
	}

	for _, variant := range []string{ThumbnailVariantLight, ThumbnailVariantDark} {
		if err := h.deps.Store.ClearThumbnail(r.Context(), res.ID, variant); err != nil {
			slog.Error("clearing thumbnail failed", msgError, err)
			writeError(w, http.StatusInternalServerError, "clearing the thumbnail")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// thumbnailTarget resolves the resource a thumbnail route names and the caller
// asking, refusing a caller who may not see it at all.
//
// Visibility is CanAccessResource rather than CanReadResource, the same rule
// the resource's own GET applies: an administrator who may write a library they
// are not a member of must be able to read what is in it.
func (h *Handler) thumbnailTarget(w http.ResponseWriter, r *http.Request) (*Resource, *Claims, bool) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return nil, nil, false
	}

	res, err := h.deps.Store.Get(r.Context(), r.PathValue(pathParamID))
	if err != nil || res == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return nil, nil, false
	}
	if !CanAccessResource(*claims, res) {
		// The same answer a resource that does not exist gives: which resources
		// exist in a library the caller cannot see is not theirs to learn.
		writeError(w, http.StatusNotFound, "resource not found")
		return nil, nil, false
	}
	return res, claims, true
}
