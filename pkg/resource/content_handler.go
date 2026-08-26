package resource

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// pathParamVersion is the path segment carrying a version number; it doubles as
// the log key for the version a line is about.
const pathParamVersion = "version"

// logKeyResourceID is the slog key carrying a resource's id.
const logKeyResourceID = "resource_id"

// msgVersioningUnavailable is returned when a deployment has no version store
// or no blob storage wired: content revision needs both, and saying so is more
// useful than a generic failure.
const msgVersioningUnavailable = "content revision not available"

// registerContentRoutes adds the content-revision and version-history routes.
// Split from the metadata CRUD routes so the two surfaces stay legible.
func (h *Handler) registerContentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/resources/{id}/content", h.handleReplaceContent)
	mux.HandleFunc("GET /api/v1/resources/{id}/versions", h.handleListVersions)
	mux.HandleFunc("GET /api/v1/resources/{id}/versions/{version}/content", h.handleGetVersionContent)
	mux.HandleFunc("POST /api/v1/resources/{id}/versions/{version}/restore", h.handleRestoreVersion)
}

// maxVersions returns the configured retention cap, normalized.
func (h *Handler) maxVersions() int {
	return NormalizeMaxVersions(h.deps.MaxVersions)
}

// resolveReadable loads the named resource and confirms the caller may see it.
// A resource the caller cannot access is reported as not found, never as
// forbidden: the two are indistinguishable to a caller who should not learn the
// resource exists.
func (h *Handler) resolveReadable(w http.ResponseWriter, r *http.Request) (*Resource, *Claims, bool) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return nil, nil, false
	}
	res, err := h.deps.Store.Get(r.Context(), r.PathValue(pathParamID))
	if err != nil {
		writeError(w, http.StatusNotFound, msgNotFound)
		return nil, nil, false
	}
	if !CanAccessResource(*claims, res) {
		writeError(w, http.StatusNotFound, msgNotFound)
		return nil, nil, false
	}
	return res, claims, true
}

// resolveRevisable loads the named resource, confirms the caller may revise it,
// and confirms the deployment can store a revision at all.
func (h *Handler) resolveRevisable(w http.ResponseWriter, r *http.Request) (*Resource, *Claims, bool) {
	res, claims, ok := h.resolveReadable(w, r)
	if !ok {
		return nil, nil, false
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return nil, nil, false
	}
	if h.deps.Versions == nil || h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, msgVersioningUnavailable)
		return nil, nil, false
	}
	return res, claims, true
}

// --- Replace content ---

// handleReplaceContent handles POST /api/v1/resources/{id}/content.
//
// @Summary      Replace resource content
// @Description  Uploads new content for an existing resource, recording a version and keeping the resource ID, URI, and filename stable so existing references and prompt attachments keep resolving.
// @Tags         Resources
// @Accept       multipart/form-data
// @Produce      json
// @Param        id    path      string  true  "Resource ID"
// @Param        file  formData  file    true  "Replacement file (max 100 MB)"
// @Success      200  {object}  resource.Resource
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/content [post]
func (h *Handler) handleReplaceContent(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.resolveRevisable(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(MaxMultipartMemory); err != nil { // #nosec G120 -- body bounded by MaxBytesReader above
		slog.Warn("resource revision: multipart parse failed", msgError, err) //nolint:gosec // structured slog, no injection
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	uf, err := readUploadedFile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The uploaded file's own name is deliberately ignored. The canonical URI
	// embeds the resource's filename, and a revision that changed the URI would
	// break every mcp:resource:<id> citation and prompt attachment pointing at
	// it — the exact breakage this route exists to end.
	updated, err := h.storeRevision(r.Context(), res, claims, RevisionUpload{Data: uf.data, MIMEType: uf.mimeType})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
	// The URI is unchanged, so this re-registers the same resource with its new
	// type and size and fires resources/list_changed; a client holding the old
	// content re-reads it.
	h.notifyCreate(updated)
}

// RevisionUpload is the content a revision writes: the bytes, the type they are
// stored under, and the version a restore re-promoted (nil for fresh content).
type RevisionUpload struct {
	Data     []byte
	MIMEType string
	// RestoredFrom names the version a restore re-promoted, nil otherwise.
	RestoredFrom *int
	// ChangeSummary says why the content changed, for a revision written on the
	// uploader's behalf. Empty for an upload the uploader picked themselves.
	ChangeSummary string
}

// ReviseContent writes the bytes to a fresh per-revision key, records the
// revision (which moves the head), prunes beyond the retention cap, and returns
// the updated resource. A failure after the blob is written removes it, so a
// failed revision leaves neither a dangling object nor a moved head.
//
// It is exported because the revision trail is where a corrected copy of a
// resource belongs, and the surface that corrects one is not this handler: a
// registration that has to rewrite a CSV before it can be read as a table
// writes the corrected bytes through here (#1441), so a revision made on
// somebody's behalf is the same kind of revision as one they uploaded, in the
// same trail, with the same retention.
func ReviseContent(
	ctx context.Context, deps Deps, res *Resource, claims *Claims, up RevisionUpload,
) (*Resource, *Version, error) {
	revisionID, err := GenerateID()
	if err != nil {
		return nil, nil, fmt.Errorf("generating revision ID: %w", err)
	}
	key := BuildRevisionS3Key(res.Scope, res.ScopeID, res.ID, revisionID, res.Filename)

	if err := deps.S3Client.PutObject(ctx, deps.S3Bucket, key, up.Data, up.MIMEType); err != nil {
		slog.Error("resource revision: s3 put failed", msgError, err)
		return nil, nil, fmt.Errorf("storing revision: %w", err)
	}

	version, err := deps.Versions.AddRevision(ctx, Revision{
		ResourceID:    res.ID,
		MIMEType:      up.MIMEType,
		SizeBytes:     int64(len(up.Data)),
		S3Key:         key,
		UploaderSub:   claims.Sub,
		UploaderEmail: PersonAddress(*claims),
		RestoredFrom:  up.RestoredFrom,
		ChangeSummary: up.ChangeSummary,
	})
	if err != nil {
		_ = deps.S3Client.DeleteObject(ctx, deps.S3Bucket, key)
		slog.Error("resource revision: recording revision failed", msgError, err)
		return nil, nil, fmt.Errorf("recording revision: %w", err)
	}
	slog.Info("resource revision recorded",
		logKeyResourceID, res.ID, // #nosec G706 -- server-generated ID
		pathParamVersion, version.Version,
		"size_bytes", version.SizeBytes,
	)

	pruneRevisions(ctx, deps, res.ID)

	updated, err := deps.Store.Get(ctx, res.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("reading revised resource: %w", err)
	}
	return updated, version, nil
}

// storeRevision writes a revision through the handler's own dependencies. The
// routes report the resource; the version number the revision took is what
// only a caller acting on somebody's behalf has to say back to them.
func (h *Handler) storeRevision(ctx context.Context, res *Resource, claims *Claims, up RevisionUpload) (*Resource, error) {
	updated, _, err := ReviseContent(ctx, h.deps, res, claims, up)
	return updated, err
}

// pruneRevisions enforces the retention cap, deleting the blobs of the versions
// the store dropped. It is best-effort on purpose: the revision itself has
// already committed, and failing the caller's request because an old object
// could not be removed would turn a storage-cleanup problem into a failed edit.
// A blob left behind is logged so it can be reclaimed.
func pruneRevisions(ctx context.Context, deps Deps, resourceID string) {
	pruned, err := deps.Versions.PruneVersions(ctx, resourceID, NormalizeMaxVersions(deps.MaxVersions))
	if err != nil {
		slog.Warn("resource revision: pruning old versions failed", msgError, err,
			logKeyResourceID, resourceID) // #nosec G706 -- server-generated ID
		return
	}
	for _, v := range pruned {
		if err := deps.S3Client.DeleteObject(ctx, deps.S3Bucket, v.S3Key); err != nil {
			slog.Warn("resource revision: pruned version blob not deleted", msgError, err,
				logKeyResourceID, resourceID, // #nosec G706 -- server-generated ID
				pathParamVersion, v.Version)
			continue
		}
		slog.Debug("resource revision: pruned version blob",
			logKeyResourceID, resourceID, pathParamVersion, v.Version) // #nosec G706 -- server-generated ID
	}
}

// --- Version history ---

// handleListVersions handles GET /api/v1/resources/{id}/versions.
//
// @Summary      List resource versions
// @Description  Lists the recorded content revisions of a resource, newest first.
// @Tags         Resources
// @Produce      json
// @Param        id   path  string  true  "Resource ID"
// @Success      200  {object}  resource.versionListResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/versions [get]
func (h *Handler) handleListVersions(w http.ResponseWriter, r *http.Request) {
	res, _, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}
	if h.deps.Versions == nil {
		writeError(w, http.StatusServiceUnavailable, msgVersioningUnavailable)
		return
	}

	versions, err := h.deps.Versions.ListVersions(r.Context(), res.ID)
	if err != nil {
		slog.Error("resource versions: list failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "listing versions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"versions":     versions,
		"current":      currentVersion(versions, res.S3Key),
		"max_versions": h.maxVersions(),
	})
}

// currentVersion returns the version number whose blob the resource head points
// at, or 0 when the trail records none (a resource whose history was pruned
// away cannot happen — the head's row is never pruned — so 0 means the store
// has no rows for it at all).
func currentVersion(versions []Version, headKey string) int {
	for _, v := range versions {
		if v.S3Key == headKey {
			return v.Version
		}
	}
	return 0
}

// versionListResponse is the JSON envelope returned by the versions endpoint.
// Used by swagger annotations only.
type versionListResponse struct { //nolint:unused // swagger model
	Versions    []Version `json:"versions"`
	Current     int       `json:"current" example:"3"`
	MaxVersions int       `json:"max_versions" example:"10"`
}

// handleGetVersionContent handles GET /api/v1/resources/{id}/versions/{version}/content.
//
// @Summary      Download a resource version
// @Description  Downloads the content of one recorded revision.
// @Tags         Resources
// @Produce      octet-stream
// @Param        id       path  string  true  "Resource ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {file}  binary
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/versions/{version}/content [get]
func (h *Handler) handleGetVersionContent(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}
	version, v, ok := h.resolveVersion(w, r, res)
	if !ok {
		return
	}

	body, contentType, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, v.S3Key)
	if err != nil {
		slog.Error("resource version content: s3 get failed", msgError, err) //nolint:gosec // structured slog
		if IsObjectNotFound(err) {
			writeError(w, http.StatusNotFound, msgContentMissing)
			return
		}
		writeError(w, http.StatusInternalServerError, msgContentUnavailable)
		return
	}

	h.recordRead(r.Context(), res, claims, SurfaceDownload, version)
	// The served name carries the version: every revision shares the resource's
	// filename, so downloading two of them would otherwise land two different
	// files under one name.
	blobserve.Serve(w, r, blobserve.Options{
		Name:        fmt.Sprintf("v%d-%s", version, res.Filename),
		ContentType: cmp.Or(contentType, v.MIMEType),
		ModTime:     v.CreatedAt,
		Data:        body,
	})
}

// resolveVersion parses the version path parameter and loads that revision,
// reporting the storage and not-found conditions.
func (h *Handler) resolveVersion(w http.ResponseWriter, r *http.Request, res *Resource) (int, *Version, bool) {
	if h.deps.Versions == nil || h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, msgVersioningUnavailable)
		return 0, nil, false
	}
	version, err := strconv.Atoi(r.PathValue(pathParamVersion))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "invalid version")
		return 0, nil, false
	}
	v, err := h.deps.Versions.GetVersion(r.Context(), res.ID, version)
	if err != nil {
		if IsNotFound(err) {
			writeError(w, http.StatusNotFound, msgNotFound)
			return 0, nil, false
		}
		slog.Error("resource version: read failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "reading version")
		return 0, nil, false
	}
	return version, v, true
}

// handleRestoreVersion handles POST /api/v1/resources/{id}/versions/{version}/restore.
//
// @Summary      Restore a resource version
// @Description  Re-promotes a prior revision's content as a new head revision. The resource ID, URI, and filename are unchanged, and the trail records which version was restored.
// @Tags         Resources
// @Produce      json
// @Param        id       path  string  true  "Resource ID"
// @Param        version  path  int     true  "Version number to restore"
// @Success      200  {object}  resource.Resource
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/versions/{version}/restore [post]
func (h *Handler) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.resolveRevisable(w, r)
	if !ok {
		return
	}
	version, v, ok := h.resolveVersion(w, r, res)
	if !ok {
		return
	}

	body, _, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, v.S3Key)
	if err != nil {
		slog.Error("resource restore: s3 get failed", msgError, err) //nolint:gosec // structured slog
		writeError(w, http.StatusInternalServerError, "retrieving version content")
		return
	}

	// The restore writes the old bytes forward as a new revision rather than
	// rewinding the head, so the trail stays append-only and the restored
	// content is itself restorable.
	updated, err := h.storeRevision(r.Context(), res, claims,
		RevisionUpload{Data: body, MIMEType: v.MIMEType, RestoredFrom: &version})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
	h.notifyCreate(updated)
}

// --- Read recording ---

// recordRead reports a served read to the bound recorder. No-op without one
// (audit disabled, or no database), and never fails the read.
func (h *Handler) recordRead(ctx context.Context, res *Resource, claims *Claims, surface string, version int) {
	if h.deps.ReadRecorder == nil {
		return
	}
	h.deps.ReadRecorder.RecordRead(ctx, ReadEvent{
		ResourceID: res.ID,
		URI:        res.URI,
		Surface:    surface,
		Version:    version,
		UserID:     claims.Sub,
		UserEmail:  claims.Email,
	})
}

// applyUsage fills the resource's audit-derived usage from the bound reader.
// No-op without one; a read failure logs and leaves usage absent rather than
// failing the caller's read, because usage is decoration on a detail view and
// the metadata is what was asked for.
func (h *Handler) applyUsage(ctx context.Context, res *Resource) {
	if h.deps.Usage == nil || res == nil {
		return
	}
	usage, err := h.deps.Usage.ResourceUsage(ctx, []string{res.ID})
	if err != nil {
		slog.Warn("resource usage: read failed", msgError, err)
		return
	}
	if u, ok := usage[res.ID]; ok {
		res.Usage = &u
	}
}

// --- Delete support ---

// deleteAllBlobs removes the head blob and every recorded version's blob.
//
// The head is the one that must succeed: leaving it behind is a live object no
// row points at, and the caller reports the failure. Prior versions are
// best-effort — an already-superseded blob that resists deletion must not make
// the resource undeletable — and a failure is logged for reclamation.
func (h *Handler) deleteAllBlobs(ctx context.Context, res *Resource) error {
	if h.deps.S3Client == nil {
		return nil
	}
	if err := h.deps.S3Client.DeleteObject(ctx, h.deps.S3Bucket, res.S3Key); err != nil {
		return fmt.Errorf("deleting resource blob: %w", err)
	}
	if h.deps.Versions == nil {
		return nil
	}
	versions, err := h.deps.Versions.ListVersions(ctx, res.ID)
	if err != nil {
		slog.Warn("resource delete: version list failed, prior version blobs left behind",
			msgError, err, logKeyResourceID, res.ID) // #nosec G706 -- server-generated ID
		return nil
	}
	for _, v := range versions {
		if v.S3Key == res.S3Key {
			continue // already deleted above
		}
		if err := h.deps.S3Client.DeleteObject(ctx, h.deps.S3Bucket, v.S3Key); err != nil {
			slog.Warn("resource delete: version blob not deleted", msgError, err,
				logKeyResourceID, res.ID, // #nosec G706 -- server-generated ID
				pathParamVersion, v.Version)
		}
	}
	return nil
}
