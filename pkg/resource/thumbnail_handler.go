package resource

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// A resource's thumbnail: the capture a browser took of it, stored beside the
// resource's own object and served from a route of its own (#1554).
//
// Nothing on a server can rasterize a document, so the image is made in a
// portal tab and uploaded here. That is the same arrangement an asset has had
// since #1431, and these routes are its counterpart: what the queue asks for,
// what it uploads, what the library reads, and how a wrong tile is cleared.

const (
	// MaxThumbnailUploadBytes bounds a capture upload. A tile is 400x300 PNG;
	// anything approaching this is not one.
	MaxThumbnailUploadBytes = 2 << 20 // 2 MB

	// thumbnailMIMEType is the only form a capture is accepted in. One type
	// means the serving route needs no stored type of its own.
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

// deriveThumbnailKey is where a capture is stored: beside the resource's own
// object, under a hidden name.
//
// It is the resource's own copy of the rule portal assets apply to their
// objects. The two live in different prefixes and neither reads the other's
// keys, but the filenames are deliberately the same so a bucket shows one
// convention rather than two.
func deriveThumbnailKey(s3Key, variant string) string {
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

// handlePendingThumbnails handles GET /api/v1/resources/thumbnails/pending.
//
// @Summary      List resources needing a thumbnail
// @Description  Resources the caller may read whose thumbnail is missing or older than the file it was captured from, most recently changed first. Nothing on a server can rasterize a document, so this is the work list a portal tab does on the deployment's behalf.
// @Tags         Resources
// @Produce      json
// @Param        limit  query  int  false  "Max results to return (default 100, max 200)"
// @Success      200  {object}  resource.listResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/thumbnails/pending [get]
func (h *Handler) handlePendingThumbnails(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	scopes, allScopes := ListScopes(*claims, "", "")
	resources, err := h.deps.Store.PendingThumbnails(
		r.Context(), Filter{Scopes: scopes, AllScopes: allScopes}, thumbnailLimit(r),
	)
	if err != nil {
		slog.Error("pending thumbnail list failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "listing pending thumbnails")
		return
	}
	if resources == nil {
		resources = []Resource{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"resources": resources, "total": len(resources)})
}

// thumbnailLimit is how many pending resources one poll asks for, clamped the
// way the listing clamps its own page.
func thumbnailLimit(r *http.Request) int {
	limit := DefaultListLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	return limit
}

// handleUploadThumbnail handles PUT /api/v1/resources/{id}/thumbnail.
//
// @Summary      Store a resource's thumbnail
// @Description  Upload a captured PNG for a resource. Capturing is the authority to change the file, which is its uploader or an administrator of its library -- the same rule replacing its content runs under.
// @Tags         Resources
// @Accept       png
// @Produce      json
// @Param        id       path   string  true   "Resource ID"
// @Param        variant  query  string  false  "Which capture this is"  Enums(light, dark)
// @Success      200  {object}  resource.Resource
// @Failure      400  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      413  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/thumbnail [put]
func (h *Handler) handleUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.thumbnailTarget(w, r)
	if !ok {
		return
	}
	// Capturing is the authority to change the file, not to read it: the image
	// stands for the resource everywhere it is listed.
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "not allowed to change this resource")
		return
	}
	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}

	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != thumbnailMIMEType {
		writeError(w, http.StatusBadRequest, "thumbnail must be image/png")
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, MaxThumbnailUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading the thumbnail")
		return
	}
	if int64(len(data)) > MaxThumbnailUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "thumbnail is too large")
		return
	}

	variant := readVariant(r)
	key := deriveThumbnailKey(res.S3Key, variant)
	if err := h.deps.S3Client.PutObject(r.Context(), h.deps.S3Bucket, key, data, thumbnailMIMEType); err != nil {
		slog.Error("thumbnail upload failed", msgError, err)
		writeError(w, http.StatusServiceUnavailable, "storing the thumbnail")
		return
	}

	// Stamped with the resource's own UpdatedAt rather than with now. The
	// capture is of the file as it stands, and a wall-clock time a moment later
	// would make a capture look newer than content written between the read and
	// this write -- which is exactly the file that still needs capturing.
	capture := ThumbnailCapture{Variant: variant, S3Key: key, CapturedAt: res.UpdatedAt}
	if err := h.deps.Store.SetThumbnail(r.Context(), res.ID, capture); err != nil {
		slog.Error("recording thumbnail failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "recording the thumbnail")
		return
	}

	applyCapture(res, capture)
	writeJSON(w, http.StatusOK, res)
}

// applyCapture reflects a stored capture onto the resource being returned, so
// the response says what the row now holds rather than what it held on read.
func applyCapture(res *Resource, c ThumbnailCapture) {
	at := c.CapturedAt
	if c.Variant == ThumbnailVariantDark {
		res.ThumbnailDarkS3Key, res.ThumbnailDarkCapturedAt = c.S3Key, &at
		return
	}
	res.ThumbnailS3Key, res.ThumbnailCapturedAt = c.S3Key, &at
}

// handleGetThumbnail handles GET /api/v1/resources/{id}/thumbnail.
//
// @Summary      Read a resource's thumbnail
// @Description  Serve the captured PNG for a resource. Reading a tile is the authority to read the resource; a resource with no capture answers 404, which is what tells a card to draw its content-type icon instead.
// @Tags         Resources
// @Produce      png
// @Param        id       path   string  true   "Resource ID"
// @Param        variant  query  string  false  "Which capture to serve"  Enums(light, dark)
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
		writeError(w, http.StatusNotFound, "no thumbnail has been captured for this resource")
		return
	}

	body, _, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, key)
	if err != nil {
		// The row points at an object the bucket no longer holds. It is a 404
		// rather than a 500 because the answer a caller acts on is the same one
		// a resource with no capture gives: draw the icon.
		slog.Warn("thumbnail object missing", msgError, err)
		writeError(w, http.StatusNotFound, "no thumbnail has been captured for this resource")
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
// views of one file, and a reader asking for the tile to be taken again means
// the tile, not the half of it their color mode happens to be showing -- the
// same rule the asset route applies (pkg/portal.clearThumbnail). Clearing the
// light one alone would be enough to put the resource back on the pending list,
// but it would leave a themeable file serving the stale dark capture until the
// replacement landed, which is exactly the wrong picture being complained about.
//
// A capture writes a deterministic key beside the resource's own object, so the
// next one overwrites what this forgets and leaving the objects in place
// orphans nothing.
//
// @Summary      Clear a resource's thumbnail
// @Description  Forget a resource's captures, both variants, which leaves it pending and asks the portal to take them again. It is the way back from a tile that is wrong.
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
