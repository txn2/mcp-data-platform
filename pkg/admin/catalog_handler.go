package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

const (
	// logKeySpecName is the structured-log key used by the catalog
	// embedding compute path. Centralized so the field name stays
	// consistent across compute / persist / fail-warning sites.
	logKeySpecName = "spec_name"

	// errSpecNotFound is the 404 message returned when a catalog
	// spec lookup misses. Centralized so revive's add-constant
	// rule stays satisfied across the four handler functions that
	// emit the same response on the same Not-Found condition.
	errSpecNotFound = "spec not found"
)

// API catalog admin REST routes. Catalogs are global (one set of
// OpenAPI specs may back many connections); the api-kind connection
// editor references one catalog by id. Mutations fan out to every
// live api-gateway toolkit via ReloadConnectionsByCatalog so model-
// facing surfaces (api_list_endpoints, api_get_endpoint_schema)
// reflect the new content without a restart.

const (
	// catalogSpecMaxUploadBytes caps multipart spec uploads. Smaller
	// than pkg/resource's MaxUploadBytes (100MB) because OpenAPI
	// specs realistically top out in single-digit MB even for
	// large enterprise APIs; capping aggressively protects the
	// process from a runaway upload.
	catalogSpecMaxUploadBytes int64 = 10 << 20 // 10 MiB

	// multipartMemoryLimit is the in-memory buffer for
	// http.Request.ParseMultipartForm before spillover to disk.
	multipartMemoryLimit int64 = 2 << 20 // 2 MiB

	// errInvalidRequestBody is the 400 message returned when the
	// request payload doesn't unmarshal. Centralized so revive's
	// add-constant rule stays happy.
	errInvalidRequestBody = "invalid request body"

	// catalogPathID is the {id} path placeholder for catalog routes.
	catalogPathID = "id"
	// catalogPathSpec is the {spec} path placeholder for catalog-spec routes.
	catalogPathSpec = "spec"
	// embeddingJobListDefaultLimit caps the default page size for
	// /api-catalogs/{id}/embedding-jobs. Generous enough to cover
	// a normal-size catalog's recent history; small enough that
	// a misbehaving query does not flood the admin response.
	embeddingJobListDefaultLimit = 100
)

// allowedSpecMIMETypes is the allowlist for the upload route's
// Content-Type. OpenAPI docs are YAML or JSON; everything else is
// either operator error or someone trying to use the route as a
// generic file dropper. application/octet-stream is allowed because
// browsers default unknown extensions to it; the content is still
// validated by catalog.ValidateContent before being stored.
var allowedSpecMIMETypes = map[string]bool{
	"application/json":         true,
	"application/yaml":         true,
	"application/x-yaml":       true,
	"application/octet-stream": true,
	"text/yaml":                true,
	"text/x-yaml":              true,
	"text/plain":               true,
}

func (h *Handler) registerCatalogRoutes() {
	if h.deps.APICatalogStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/api-catalogs", h.listCatalogs)
	h.mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}", h.getCatalog)
	h.mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/specs", h.listCatalogSpecs)
	if !h.isMutable() {
		return
	}
	h.mux.HandleFunc("POST /api/v1/admin/api-catalogs", h.createCatalog)
	h.mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}", h.updateCatalog)
	h.mux.HandleFunc("DELETE /api/v1/admin/api-catalogs/{id}", h.deleteCatalog)
	h.mux.HandleFunc("POST /api/v1/admin/api-catalogs/{id}/clone", h.cloneCatalog)
	h.mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/specs/{spec}", h.getCatalogSpec)
	h.mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}", h.upsertCatalogSpec)
	h.mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}/upload", h.uploadCatalogSpec)
	h.mux.HandleFunc("POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/refresh", h.refreshCatalogSpec)
	h.mux.HandleFunc("DELETE /api/v1/admin/api-catalogs/{id}/specs/{spec}", h.deleteCatalogSpec)
	// Embedding-job admin surface. The /reembed endpoint that
	// earlier revisions of this handler shipped is gone: spec
	// writes now enqueue a job automatically, the reconciler fills
	// in any gap, and the operator never needs a button. The
	// remaining endpoints are read-only visibility plus a manual
	// retry escape hatch for operators who need to force a re-embed
	// after an external model swap.
	if h.deps.EmbedJobs != nil {
		h.mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-status", h.listCatalogEmbeddingStatuses)
		h.mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-health", h.getCatalogEmbeddingHealth)
		h.mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-jobs", h.listCatalogEmbeddingJobs)
		h.mux.HandleFunc("POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/reembed", h.manualRetryEmbedding)
	}
}

// catalogResponse is the JSON wire shape for a catalog listing or
// detail response. Lifted out of apicatalog.Catalog so we can carry
// the derived spec_count / ref_count fields the portal renders
// without bloating the storage struct.
type catalogResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	SpecCount   int    `json:"spec_count"`
	RefCount    int    `json:"ref_count"`
}

// listCatalogs handles GET /api/v1/admin/api-catalogs.
//
// @Summary      List API catalogs
// @Description  Returns all API catalogs with derived spec_count and ref_count fields.
// @Tags         API Catalogs
// @Produce      json
// @Success      200  {array}   catalogResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs [get]
func (h *Handler) listCatalogs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.deps.APICatalogStore.ListCatalogs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list catalogs")
		slog.Warn("listCatalogs", logKeyError, err)
		return
	}
	out := make([]catalogResponse, 0, len(cs))
	for _, c := range cs {
		out = append(out, h.catalogToResponse(r.Context(), c))
	}
	writeJSON(w, http.StatusOK, out)
}

// getCatalog handles GET /api/v1/admin/api-catalogs/{id}.
//
// @Summary      Get API catalog
// @Description  Returns a single API catalog by ID with derived spec_count and ref_count fields.
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  catalogResponse
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id} [get]
func (h *Handler) getCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	c, err := h.deps.APICatalogStore.GetCatalog(r.Context(), id)
	if errors.Is(err, apicatalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "catalog not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get catalog")
		return
	}
	writeJSON(w, http.StatusOK, h.catalogToResponse(r.Context(), *c))
}

// createCatalogRequest is the body for POST /api/v1/admin/api-catalogs.
type createCatalogRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
}

// createCatalog handles POST /api/v1/admin/api-catalogs.
//
// @Summary      Create API catalog
// @Description  Creates a new API catalog. Only available in database config mode.
// @Tags         API Catalogs
// @Accept       json
// @Produce      json
// @Param        body  body  createCatalogRequest  true  "Catalog definition"
// @Success      201  {object}  catalogResponse
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs [post]
func (h *Handler) createCatalog(w http.ResponseWriter, r *http.Request) {
	var req createCatalogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	c := apicatalog.Catalog{
		ID:          req.ID,
		Name:        req.Name,
		Version:     req.Version,
		DisplayName: req.DisplayName,
		Description: req.Description,
		CreatedBy:   userIDForAudit(r),
	}
	err := h.deps.APICatalogStore.CreateCatalog(r.Context(), c)
	if errors.Is(err, apicatalog.ErrInvalidID) {
		writeError(w, http.StatusBadRequest,
			"id must be lowercase alphanumeric with hyphens, 1-100 chars, no leading/trailing hyphen")
		return
	}
	if errors.Is(err, apicatalog.ErrConflict) {
		writeError(w, http.StatusConflict, "catalog id or (name, version) already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create catalog")
		slog.Warn("createCatalog", logKeyError, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.catalogToResponse(r.Context(), c))
}

// updateCatalogRequest carries the partial-edit payload for PUT
// /api/v1/admin/api-catalogs/{id}. Pointer fields let the handler
// distinguish "operator omitted this field" from "operator
// explicitly cleared this field".
type updateCatalogRequest struct {
	Name        *string `json:"name,omitempty"`
	Version     *string `json:"version,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// updateCatalog handles PUT /api/v1/admin/api-catalogs/{id}.
//
// @Summary      Update API catalog
// @Description  Applies a partial edit to a catalog and reloads dependent api-gateway connections.
// @Tags         API Catalogs
// @Accept       json
// @Produce      json
// @Param        id    path  string                true  "Catalog ID"
// @Param        body  body  updateCatalogRequest  true  "Catalog fields to update"
// @Success      200  {object}  catalogResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id} [put]
func (h *Handler) updateCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	var req updateCatalogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	err := h.deps.APICatalogStore.UpdateCatalog(r.Context(), id, apicatalog.Update{
		Name:        req.Name,
		Version:     req.Version,
		DisplayName: req.DisplayName,
		Description: req.Description,
	})
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "catalog not found")
		return
	case errors.Is(err, apicatalog.ErrConflict):
		writeError(w, http.StatusConflict, "edit would collide with an existing (name, version)")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to update catalog")
		return
	}
	h.reloadConnectionsForCatalog(id)
	updated, _ := h.deps.APICatalogStore.GetCatalog(r.Context(), id)
	if updated != nil {
		writeJSON(w, http.StatusOK, h.catalogToResponse(r.Context(), *updated))
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// deleteCatalog handles DELETE /api/v1/admin/api-catalogs/{id}.
//
// @Summary      Delete API catalog
// @Description  Deletes a catalog. Rejected with 409 if any connection still references it.
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id} [delete]
func (h *Handler) deleteCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	refs, err := h.deps.APICatalogStore.ReferencingConnections(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check catalog references")
		return
	}
	if len(refs) > 0 {
		names := make([]string, 0, len(refs))
		for _, ref := range refs {
			names = append(names, ref.Kind+"/"+ref.Name)
		}
		writeError(w, http.StatusConflict,
			"catalog still referenced by: "+strings.Join(names, ", "))
		return
	}
	err = h.deps.APICatalogStore.DeleteCatalog(r.Context(), id)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "catalog not found")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to delete catalog")
	default:
		writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
	}
}

// cloneCatalogRequest is the body for POST /api/v1/admin/api-catalogs/{id}/clone.
type cloneCatalogRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// cloneCatalog handles POST /api/v1/admin/api-catalogs/{id}/clone.
//
// @Summary      Clone API catalog
// @Description  Creates a new catalog by copying the source catalog's metadata, specs, and embeddings.
// @Tags         API Catalogs
// @Accept       json
// @Produce      json
// @Param        id    path  string               true  "Source catalog ID"
// @Param        body  body  cloneCatalogRequest  true  "Destination catalog definition"
// @Success      201  {object}  catalogResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/clone [post]
func (h *Handler) cloneCatalog(w http.ResponseWriter, r *http.Request) {
	srcID := r.PathValue(catalogPathID)
	var req cloneCatalogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	src, err := h.deps.APICatalogStore.GetCatalog(r.Context(), srcID)
	if errors.Is(err, apicatalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "source catalog not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get source catalog")
		return
	}
	dst := apicatalog.Catalog{
		ID:          req.ID,
		Name:        firstNonEmpty(req.Name, src.Name),
		Version:     req.Version,
		DisplayName: firstNonEmpty(req.DisplayName, src.DisplayName),
		Description: src.Description,
		CreatedBy:   userIDForAudit(r),
	}
	if !h.createClonedCatalog(w, r, dst) {
		return
	}
	if !h.copyCatalogSpecs(w, r, srcID, dst.ID) {
		return
	}
	writeJSON(w, http.StatusCreated, h.catalogToResponse(r.Context(), dst))
}

// createClonedCatalog wraps CreateCatalog with the error-mapping
// shared with createCatalog. Returns false when the response was
// already written and the caller must abort.
func (h *Handler) createClonedCatalog(w http.ResponseWriter, r *http.Request, dst apicatalog.Catalog) bool {
	err := h.deps.APICatalogStore.CreateCatalog(r.Context(), dst)
	switch {
	case errors.Is(err, apicatalog.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "destination id is invalid")
		return false
	case errors.Is(err, apicatalog.ErrConflict):
		writeError(w, http.StatusConflict, "destination id or (name, version) already exists")
		return false
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to create destination catalog")
		return false
	}
	return true
}

// copyCatalogSpecs duplicates every spec from src into dst. Returns
// false when the response was already written and the caller must
// abort.
func (h *Handler) copyCatalogSpecs(w http.ResponseWriter, r *http.Request, srcID, dstID string) bool {
	specs, err := h.deps.APICatalogStore.ListSpecs(r.Context(), srcID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list source specs")
		return false
	}
	for _, s := range specs {
		clone := apicatalog.SpecEntry{
			SpecName:       s.SpecName,
			Content:        s.Content,
			SourceKind:     s.SourceKind,
			SourceURL:      s.SourceURL,
			ETag:           s.ETag,
			BasePath:       s.BasePath,
			Title:          s.Title,
			Description:    s.Description,
			LastFetchedAt:  s.LastFetchedAt,
			OperationCount: s.OperationCount,
		}
		if upErr := h.deps.APICatalogStore.UpsertSpec(r.Context(), dstID, clone); upErr != nil {
			writeError(w, http.StatusInternalServerError,
				"failed to copy spec "+s.SpecName+": "+upErr.Error())
			return false
		}
		// Clone the persisted vectors too so the new catalog
		// answers semantic ranking on the first call without
		// recomputing. Best-effort: a missing source-side vector
		// set just means the destination spec starts un-indexed
		// and the reconciler enqueues a job on the next sweep.
		if rows, err := h.deps.APICatalogStore.ListOperationEmbeddings(r.Context(), srcID, s.SpecName); err == nil && len(rows) > 0 {
			if upErr := h.deps.APICatalogStore.UpsertOperationEmbeddings(r.Context(), dstID, s.SpecName, rows); upErr != nil {
				slog.Warn("apigateway: clone embeddings copy failed",
					logKeyCatalogID, dstID, logKeySpecName, s.SpecName, logKeyError, upErr)
			}
		} else {
			// Vectors were missing on the source side too;
			// enqueue a job so the worker fills them in
			// asynchronously. Without this the cloned spec
			// would sit at "not indexed" until the periodic
			// reconciler picked it up.
			h.enqueueEmbedJob(r.Context(), dstID, s.SpecName)
		}
	}
	return true
}

// specResponse is the JSON wire shape returned by spec routes.
// Carries the operator-visible metadata; content is included only
// on the explicit GET /specs/{spec} detail endpoint.
//
// BasePath is the operator-set override prefix applied to every
// operation in this spec at api_list_endpoints and
// api_invoke_endpoint time. Empty means "no override"; the toolkit
// falls back to deriving the prefix from the spec's servers[0].url.
// See catalog.NormalizeBasePath for the leading-slash / trailing-
// slash / control-character rules enforced on write.
type specResponse struct {
	SpecName   string `json:"spec_name"`
	Content    string `json:"content,omitempty"`
	SourceKind string `json:"source_kind"`
	SourceURL  string `json:"source_url,omitempty"`
	ETag       string `json:"etag,omitempty"`
	BasePath   string `json:"base_path,omitempty"`
	// Title and Description are the operator-set per-spec summary
	// overrides. Empty means "derive from the spec's info.title /
	// info.description". See catalog.NormalizeSpecTitle /
	// NormalizeSpecDescription for the validation rules.
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	LastFetchedAt string `json:"last_fetched_at,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	// OperationCount is the number of operations the spec content
	// parses to. Stored alongside the spec on every write so the
	// portal can render "N/M indexed" without re-parsing the
	// content on the client.
	OperationCount int `json:"operation_count"`
	// EmbeddingCount is the count of persisted operation embedding
	// rows for this (catalog, spec). Equal to OperationCount when
	// the queue has fully drained for the spec; less while a job
	// is in flight or after a partial failure.
	EmbeddingCount int `json:"embedding_count"`
	// EmbeddingStatus reflects the most recent embedding job's
	// terminal or in-flight state: "" when no job has ever run
	// for the spec, "pending" while queued, "running" while a
	// worker is processing it, "succeeded" when current, "failed"
	// when retries are exhausted. The portal uses this for the
	// per-spec badge text and color.
	EmbeddingStatus string `json:"embedding_status,omitempty"`
	// EmbeddingAttempts is the most recent job's attempt count.
	// Rendered as "running (attempt N)" while in flight, useful
	// for operators trying to gauge whether a slow provider is
	// just slow or stuck retrying.
	EmbeddingAttempts int `json:"embedding_attempts,omitempty"`
	// EmbeddingLastError is the most recent job's last_error
	// column. Non-empty only when the most recent job failed or
	// is on a retry; rendered in a tooltip / detail row so the
	// operator can see "provider returned 502" without grepping
	// pod logs.
	EmbeddingLastError string `json:"embedding_last_error,omitempty"`
}

// specListResponse wraps the spec list so we have a stable shape
// the portal can extend later (e.g. with paging) without breaking
// existing JSON consumers.
type specListResponse struct {
	Specs []specResponse `json:"specs"`
}

// listCatalogSpecs handles GET /api/v1/admin/api-catalogs/{id}/specs.
//
// @Summary      List catalog specs
// @Description  Returns the specs in a catalog with embedding metadata. Spec content is omitted.
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  specListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs [get]
func (h *Handler) listCatalogSpecs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specs, err := h.deps.APICatalogStore.ListSpecs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list specs")
		return
	}
	out := specListResponse{Specs: make([]specResponse, 0, len(specs))}
	for _, s := range specs {
		out.Specs = append(out.Specs, h.specToResponseWithEmbedding(r.Context(), id, s, false))
	}
	writeJSON(w, http.StatusOK, out)
}

// getCatalogSpec handles GET /api/v1/admin/api-catalogs/{id}/specs/{spec}.
//
// @Summary      Get catalog spec
// @Description  Returns a single catalog spec including its content and embedding metadata.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  specResponse
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec} [get]
func (h *Handler) getCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	spec, err := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, errSpecNotFound)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	writeJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, *spec, true))
}

// upsertCatalogSpecRequest is the body for the inline / URL save path.
//
// BasePath sets the operator-supplied per-spec URL prefix. Optional;
// empty leaves it unset (the toolkit derives the prefix from the
// spec's servers[0].url at registration time). Normalized via
// catalog.NormalizeBasePath at write time: must start with "/",
// must not contain CR/LF/NUL/?/#, trailing slash is stripped.
// Title and Description set the operator-supplied per-spec summary
// overrides surfaced by api_list_specs and the multi-spec gate.
// Optional; empty leaves them unset (the toolkit derives the values
// from the spec content's info.title / info.description). Normalized
// via catalog.NormalizeSpecTitle / NormalizeSpecDescription at write
// time: trimmed, no CR/LF/NUL, capped at 200 / 2000 chars.
type upsertCatalogSpecRequest struct {
	SourceKind  string `json:"source_kind"`
	Content     string `json:"content,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	BasePath    string `json:"base_path,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// upsertCatalogSpec handles PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}.
//
// @Summary      Upsert catalog spec
// @Description  Creates or updates a catalog spec from inline content or a fetched URL, then enqueues an embedding job.
// @Tags         API Catalogs
// @Accept       json
// @Produce      json
// @Param        id    path  string                    true  "Catalog ID"
// @Param        spec  path  string                    true  "Spec name"
// @Param        body  body  upsertCatalogSpecRequest  true  "Spec source and metadata"
// @Success      200  {object}  specResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      502  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec} [put]
func (h *Handler) upsertCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	var req upsertCatalogSpecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	entry, err := h.materializeSpec(r.Context(), specName, req)
	if err != nil {
		// materializeSpec errors are either user-input mismatches
		// (missing content for inline, missing URL for url, invalid
		// kind, upload-on-wrong-route) or fetch-time SSRF/upstream
		// failures. Route the SSRF/fetch ones through
		// specErrorStatus so 400/413/502 stay accurate, and surface
		// everything else as 400.
		status := http.StatusBadRequest
		if isFetchError(err) {
			status = h.specErrorStatus(err)
		}
		writeError(w, status, err.Error())
		return
	}
	if err := apicatalog.ValidateContent(entry.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry.OperationCount = apicatalog.CountOperations(entry.Content)
	if err := h.deps.APICatalogStore.UpsertSpec(r.Context(), id, entry); err != nil {
		writeError(w, h.specErrorStatus(err), "failed to save spec: "+err.Error())
		return
	}
	h.enqueueEmbedJob(r.Context(), id, entry.SpecName)
	h.reloadConnectionsForCatalog(id)
	saved, _ := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	if saved != nil {
		writeJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, *saved, false))
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// materializeSpec converts the upsert request into a SpecEntry,
// fetching the URL when source_kind=url. Validation of the resulting
// content (it must parse as OpenAPI) is the caller's responsibility
// — we centralize the fetch and the shape construction here so the
// admin handler's body stays focused on HTTP plumbing.
func (*Handler) materializeSpec(ctx context.Context, specName string, req upsertCatalogSpecRequest) (apicatalog.SpecEntry, error) {
	switch req.SourceKind {
	case apicatalog.SourceInline:
		if req.Content == "" {
			return apicatalog.SpecEntry{}, errors.New("content is required for source_kind=inline")
		}
		return apicatalog.SpecEntry{
			SpecName:    specName,
			Content:     req.Content,
			SourceKind:  apicatalog.SourceInline,
			BasePath:    req.BasePath,
			Title:       req.Title,
			Description: req.Description,
		}, nil
	case apicatalog.SourceURL:
		if req.SourceURL == "" {
			return apicatalog.SpecEntry{}, errors.New("source_url is required for source_kind=url")
		}
		res, err := apicatalog.FetchFromURL(ctx, req.SourceURL, apicatalog.FetchOptions{})
		if err != nil {
			return apicatalog.SpecEntry{}, fmt.Errorf("fetch failed: %w", err)
		}
		return apicatalog.SpecEntry{
			SpecName:      specName,
			Content:       res.Content,
			SourceKind:    apicatalog.SourceURL,
			SourceURL:     req.SourceURL,
			ETag:          res.ETag,
			BasePath:      req.BasePath,
			Title:         req.Title,
			Description:   req.Description,
			LastFetchedAt: res.FetchedAt,
		}, nil
	case apicatalog.SourceUpload:
		return apicatalog.SpecEntry{}, errors.New("source_kind=upload must use the /upload endpoint")
	default:
		return apicatalog.SpecEntry{}, fmt.Errorf("invalid source_kind %q", req.SourceKind)
	}
}

// isFetchError reports whether err originates in the catalog URL
// fetcher (SSRF guard, upstream non-2xx, body-size cap). Used by
// upsertCatalogSpec to route fetch failures through specErrorStatus
// while leaving simple user-input mismatches as a 400.
func isFetchError(err error) bool {
	return errors.Is(err, apicatalog.ErrSSRFBlocked) ||
		errors.Is(err, apicatalog.ErrUpstream) ||
		errors.Is(err, apicatalog.ErrTooLarge) ||
		errors.Is(err, apicatalog.ErrInvalidContent)
}

// specErrorStatus picks the right HTTP status for a spec-write error.
// Centralized so each route doesn't duplicate the same switch.
func (*Handler) specErrorStatus(err error) int {
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, apicatalog.ErrInvalidSpecName):
		return http.StatusBadRequest
	case errors.Is(err, apicatalog.ErrInvalidBasePath):
		return http.StatusBadRequest
	case errors.Is(err, apicatalog.ErrInvalidSpecMetadata):
		return http.StatusBadRequest
	case errors.Is(err, apicatalog.ErrSSRFBlocked):
		return http.StatusBadRequest
	case errors.Is(err, apicatalog.ErrUpstream):
		return http.StatusBadGateway
	case errors.Is(err, apicatalog.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

// readSpecUpload parses the multipart upload, enforces the size cap
// and the MIME allowlist, and returns the raw body. Returns ok=false
// (with the response already written) on any rejection so the
// caller can early-return without re-checking each step.
func readSpecUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, catalogSpecMaxUploadBytes+1024)
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' form field")
		return nil, false
	}
	defer func() { _ = file.Close() }()
	if header.Size > catalogSpecMaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("file exceeds %d-byte limit", catalogSpecMaxUploadBytes))
		return nil, false
	}
	if ct := header.Header.Get("Content-Type"); ct != "" {
		mediaType, _, mtErr := mime.ParseMediaType(ct)
		if mtErr != nil || !allowedSpecMIMETypes[strings.ToLower(mediaType)] {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported content-type: "+ct)
			return nil, false
		}
	}
	body, err := io.ReadAll(io.LimitReader(file, catalogSpecMaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read upload: "+err.Error())
		return nil, false
	}
	if int64(len(body)) > catalogSpecMaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds size cap")
		return nil, false
	}
	return body, true
}

// applyUploadSpecMetadata sets the operator-overridable per-spec
// metadata (base_path, title, description) on entry from the upload
// request, applied per field with this precedence:
//  1. Explicit ?base_path / ?title / ?description on the upload URL.
//  2. The previously-stored value on the existing spec row (so a
//     routine re-upload of refreshed content does not blow away an
//     operator override).
//  3. Empty (the migration default).
//
// existing may be nil (new spec or a swallowed lookup failure), in
// which case only the explicit query values apply.
func applyUploadSpecMetadata(entry *apicatalog.SpecEntry, q url.Values, existing *apicatalog.SpecEntry) {
	entry.BasePath = q.Get("base_path")
	entry.Title = q.Get("title")
	entry.Description = q.Get("description")
	if existing == nil {
		return
	}
	if entry.BasePath == "" {
		entry.BasePath = existing.BasePath
	}
	if entry.Title == "" {
		entry.Title = existing.Title
	}
	if entry.Description == "" {
		entry.Description = existing.Description
	}
}

// uploadCatalogSpec handles PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}/upload.
//
// @Summary      Upload catalog spec
// @Description  Stores a catalog spec from a multipart file upload, then enqueues an embedding job.
// @Tags         API Catalogs
// @Accept       multipart/form-data
// @Produce      json
// @Param        id           path      string  true   "Catalog ID"
// @Param        spec         path      string  true   "Spec name"
// @Param        base_path    query     string  false  "Operator base_path override"
// @Param        title        query     string  false  "Operator title override"
// @Param        description  query     string  false  "Operator description override"
// @Param        file         formData  file    true   "OpenAPI spec file (YAML or JSON)"
// @Success      200  {object}  specResponse
// @Failure      400  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      415  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/upload [put]
func (h *Handler) uploadCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	body, ok := readSpecUpload(w, r)
	if !ok {
		return
	}
	entry := apicatalog.SpecEntry{
		SpecName:   specName,
		Content:    string(body),
		SourceKind: apicatalog.SourceUpload,
	}
	existing, lookupErr := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	if lookupErr != nil && !errors.Is(lookupErr, apicatalog.ErrNotFound) {
		// Log the swallowed lookup error so an operator chasing a
		// vanished override has a breadcrumb. The upload still
		// proceeds with empty overrides so a transient lookup failure
		// does not block the operator from saving the new content.
		slog.Warn("apigateway: catalog spec metadata preserve lookup failed",
			"catalog_id", id, "spec_name", specName, logKeyError, lookupErr)
		existing = nil
	}
	applyUploadSpecMetadata(&entry, r.URL.Query(), existing)
	if err := apicatalog.ValidateContent(entry.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry.OperationCount = apicatalog.CountOperations(entry.Content)
	if err := h.deps.APICatalogStore.UpsertSpec(r.Context(), id, entry); err != nil {
		writeError(w, h.specErrorStatus(err), "failed to save spec: "+err.Error())
		return
	}
	h.enqueueEmbedJob(r.Context(), id, entry.SpecName)
	h.reloadConnectionsForCatalog(id)
	saved, _ := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	if saved != nil {
		writeJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, *saved, false))
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// refreshCatalogSpec handles POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/refresh.
//
// @Summary      Refresh catalog spec
// @Description  Re-fetches a URL-sourced spec from its source_url, stores the new content, and enqueues an embedding job.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  specResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      502  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/refresh [post]
func (h *Handler) refreshCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	existing, err := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, errSpecNotFound)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	if existing.SourceKind != apicatalog.SourceURL {
		writeError(w, http.StatusBadRequest, "spec was not URL-sourced; refresh is only valid for source_kind=url")
		return
	}
	res, err := apicatalog.FetchFromURL(r.Context(), existing.SourceURL, apicatalog.FetchOptions{})
	if err != nil {
		writeError(w, h.specErrorStatus(err), "fetch failed: "+err.Error())
		return
	}
	entry := apicatalog.SpecEntry{
		SpecName:       specName,
		Content:        res.Content,
		SourceKind:     apicatalog.SourceURL,
		SourceURL:      existing.SourceURL,
		ETag:           res.ETag,
		BasePath:       existing.BasePath,
		Title:          existing.Title,
		Description:    existing.Description,
		LastFetchedAt:  res.FetchedAt,
		OperationCount: apicatalog.CountOperations(res.Content),
	}
	if err := h.deps.APICatalogStore.UpsertSpec(r.Context(), id, entry); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save refreshed spec: "+err.Error())
		return
	}
	h.enqueueEmbedJob(r.Context(), id, entry.SpecName)
	h.reloadConnectionsForCatalog(id)
	writeJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, entry, false))
}

// deleteCatalogSpec handles DELETE /api/v1/admin/api-catalogs/{id}/specs/{spec}.
//
// @Summary      Delete catalog spec
// @Description  Removes a spec from a catalog and reloads dependent api-gateway connections.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec} [delete]
func (h *Handler) deleteCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	err := h.deps.APICatalogStore.DeleteSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, errSpecNotFound)
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to delete spec")
	default:
		h.reloadConnectionsForCatalog(id)
		writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
	}
}

// catalogToResponse decorates a Catalog with spec_count and
// ref_count by reading the store. Errors are swallowed (the counts
// just become zero) — the catalog listing should still render even
// if a transient DB hiccup happens during the lookup.
func (h *Handler) catalogToResponse(ctx context.Context, c apicatalog.Catalog) catalogResponse {
	resp := catalogResponse{
		ID:          c.ID,
		Name:        c.Name,
		Version:     c.Version,
		DisplayName: c.DisplayName,
		Description: c.Description,
		CreatedBy:   c.CreatedBy,
		CreatedAt:   formatTime(c.CreatedAt),
		UpdatedAt:   formatTime(c.UpdatedAt),
	}
	if specs, err := h.deps.APICatalogStore.ListSpecs(ctx, c.ID); err == nil {
		resp.SpecCount = len(specs)
	}
	if refs, err := h.deps.APICatalogStore.ReferencingConnections(ctx, c.ID); err == nil {
		resp.RefCount = len(refs)
	}
	return resp
}

// specToResponse maps a SpecEntry to the wire shape. includeContent
// controls whether the (potentially large) content is returned —
// list/upsert paths omit it to keep response sizes predictable.
func specToResponse(s apicatalog.SpecEntry, includeContent bool) specResponse {
	resp := specResponse{
		SpecName:      s.SpecName,
		SourceKind:    s.SourceKind,
		SourceURL:     s.SourceURL,
		ETag:          s.ETag,
		BasePath:      s.BasePath,
		Title:         s.Title,
		Description:   s.Description,
		LastFetchedAt: formatTime(s.LastFetchedAt),
		CreatedAt:     formatTime(s.CreatedAt),
		UpdatedAt:     formatTime(s.UpdatedAt),
	}
	if includeContent {
		resp.Content = s.Content
	}
	return resp
}

// specToResponseWithEmbedding behaves like specToResponse but also
// populates EmbeddingCount / OperationCount / EmbeddingStatus
// from the catalog spec row and the embedding job queue. Single
// callers exist on the list / detail / write paths; queue access
// is best-effort (a missed read leaves the embedding fields at
// zero rather than failing the response, which is the same
// degradation mode the UI accepts).
func (h *Handler) specToResponseWithEmbedding(ctx context.Context, catalogID string, s apicatalog.SpecEntry, includeContent bool) specResponse {
	resp := specToResponse(s, includeContent)
	resp.OperationCount = s.OperationCount
	if h.deps.APICatalogStore != nil {
		if rows, err := h.deps.APICatalogStore.ListOperationEmbeddings(ctx, catalogID, s.SpecName); err == nil {
			resp.EmbeddingCount = len(rows)
		}
	}
	if h.deps.EmbedJobs != nil {
		jobs, err := h.deps.EmbedJobs.List(ctx, catalogindex.ListFilter{
			CatalogID: catalogID,
			SpecName:  s.SpecName,
			Limit:     1,
		})
		if err == nil && len(jobs) > 0 {
			j := jobs[0]
			resp.EmbeddingStatus = string(j.Status)
			resp.EmbeddingAttempts = j.Attempts
			resp.EmbeddingLastError = j.LastError
		}
	}
	return resp
}

// enqueueEmbedJob is the producer-side hook every spec write
// path calls after the spec row commits. It records the job
// row alongside (or just after) the spec write and lets the
// worker / reconciler / reaper drive the actual embedding pass
// off the request path. Failures are logged but do not block
// the spec write: the reconciler will pick up any spec whose
// embedding-row count is below operation_count on its next
// sweep, so a missed enqueue still converges.
func (h *Handler) enqueueEmbedJob(ctx context.Context, catalogID, specName string) {
	if h.deps.EmbedJobs == nil {
		// No queue (file mode / no DB). The data path falls
		// back to lexical and the operator gets no embeddings;
		// this is the documented degraded mode.
		return
	}
	if _, err := h.deps.EmbedJobs.Enqueue(ctx, catalogindex.SpecKey{
		CatalogID: catalogID, SpecName: specName,
	}, catalogindex.KindSpecWrite); err != nil {
		slog.Warn("apigateway: enqueue embedding job failed",
			logKeyCatalogID, catalogID, logKeySpecName, specName, logKeyError, err)
	}
}

// logKeyCatalogID is the structured-log key for catalog ids in the
// admin handler. Kept local to this file so other admin handlers
// don't accidentally drift the spelling.
const logKeyCatalogID = "catalog_id"

// listCatalogEmbeddingStatuses returns one row per spec in the
// catalog with operation_count, embedding_count, and last job
// state. The portal renders this list as per-spec badges in the
// CatalogsPanel so the operator can see, at a glance, which
// specs are fully indexed and which are queued / failed.
//
// @Summary      List catalog embedding statuses
// @Description  Returns one row per spec with operation_count, embedding_count, and latest job state.
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/embedding-status [get]
func (h *Handler) listCatalogEmbeddingStatuses(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	rows, err := h.deps.EmbedJobs.SpecStatuses(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list embedding statuses")
		slog.Warn("apigateway: list embedding statuses",
			logKeyCatalogID, id, logKeyError, err)
		return
	}
	out := make([]embeddingStatusResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, embeddingStatusResponseFromRow(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"specs": out})
}

// getCatalogEmbeddingHealth returns the per-catalog roll-up the
// portal renders at the top of the catalog editor.
//
// @Summary      Get catalog embedding health
// @Description  Returns the per-catalog embedding roll-up (specs total/indexed/pending/running/failed).
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  embeddingHealthResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/embedding-health [get]
func (h *Handler) getCatalogEmbeddingHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	h2, err := h.deps.EmbedJobs.Health(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute embedding health")
		slog.Warn("apigateway: embedding health",
			logKeyCatalogID, id, logKeyError, err)
		return
	}
	writeJSON(w, http.StatusOK, embeddingHealthResponse{
		CatalogID:    h2.CatalogID,
		SpecsTotal:   h2.SpecsTotal,
		SpecsIndexed: h2.SpecsIndexed,
		SpecsPending: h2.SpecsPending,
		SpecsRunning: h2.SpecsRunning,
		SpecsFailed:  h2.SpecsFailed,
	})
}

// listCatalogEmbeddingJobs returns recent job rows for the
// catalog. Used by the admin "Embedding history" view and for
// debugging "why did this spec fail to index" questions.
//
// @Summary      List catalog embedding jobs
// @Description  Returns recent embedding job rows for a catalog, optionally filtered by status and spec_name.
// @Tags         API Catalogs
// @Produce      json
// @Param        id         path   string  true   "Catalog ID"
// @Param        status     query  string  false  "Filter by job status"
// @Param        spec_name  query  string  false  "Filter by spec name"
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/embedding-jobs [get]
func (h *Handler) listCatalogEmbeddingJobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	filter := catalogindex.ListFilter{CatalogID: id, Limit: embeddingJobListDefaultLimit}
	if s := r.URL.Query().Get("status"); s != "" {
		filter.Status = catalogindex.Status(s)
	}
	if s := r.URL.Query().Get("spec_name"); s != "" {
		filter.SpecName = s
	}
	jobs, err := h.deps.EmbedJobs.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list embedding jobs")
		slog.Warn("apigateway: list embedding jobs",
			logKeyCatalogID, id, logKeyError, err)
		return
	}
	out := make([]embeddingJobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, embeddingJobResponseFromJob(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// manualRetryEmbedding is the operator escape hatch for forcing
// a re-embed when the automatic path's dedup says "no work" but
// the operator knows otherwise (model swapped externally,
// upstream embedding model version drifted behind the same
// name, debugging). It enqueues a manual_retry job, which the
// worker treats identically to a spec_write job except for the
// audit kind. The worker's compute path skips the text-hash
// dedup for manual_retry kind, so vectors are recomputed fresh.
//
// @Summary      Retry catalog spec embedding
// @Description  Enqueues a manual_retry embedding job for a spec, recomputing vectors fresh without the text-hash dedup.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      202  {object}  map[string]interface{}
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/reembed [post]
func (h *Handler) manualRetryEmbedding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	if _, err := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName); err != nil {
		if errors.Is(err, apicatalog.ErrNotFound) {
			writeError(w, http.StatusNotFound, errSpecNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	created, err := h.deps.EmbedJobs.Enqueue(r.Context(), catalogindex.SpecKey{
		CatalogID: id, SpecName: specName,
	}, catalogindex.KindManualRetry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue embedding job: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":  "queued",
		"created": created,
	})
}

// embeddingStatusResponse / embeddingHealthResponse /
// embeddingJobResponse are the JSON shapes the admin endpoints
// return. Mirroring the catalogindex types as a separate set keeps
// the wire format insulated from internal refactors.
type embeddingStatusResponse struct {
	SpecName       string `json:"spec_name"`
	OperationCount int    `json:"operation_count"`
	EmbeddingCount int    `json:"embedding_count"`
	// EmbeddedSoFar is the worker's in-flight chunk-progress counter.
	// While JobStatus == "running" the portal renders this against
	// OperationCount so a long embed pass shows incremental progress
	// instead of staying at 0/N until the final atomic upsert commits
	// EmbeddingCount in one tick (#430). Reset to 0 only when Claim
	// picks the job up; terminal succeeded / failed rows and pending
	// rows recovered from a lease expiry may still carry a prior
	// attempt's value, which is why the portal gates its rendering
	// on JobStatus == running.
	EmbeddedSoFar int    `json:"embedded_so_far,omitempty"`
	JobStatus     string `json:"job_status,omitempty"`
	JobAttempts   int    `json:"job_attempts,omitempty"`
	JobLastError  string `json:"job_last_error,omitempty"`
	JobUpdatedAt  string `json:"job_updated_at,omitempty"`
}

func embeddingStatusResponseFromRow(row catalogindex.SpecStatusRow) embeddingStatusResponse {
	resp := embeddingStatusResponse{
		SpecName:       row.SpecName,
		OperationCount: row.OperationCount,
		EmbeddingCount: row.EmbeddingCount,
		EmbeddedSoFar:  row.EmbeddedSoFar,
		JobStatus:      string(row.JobStatus),
		JobAttempts:    row.JobAttempts,
		JobLastError:   row.JobLastError,
	}
	if row.JobUpdatedAt != nil {
		resp.JobUpdatedAt = formatTime(*row.JobUpdatedAt)
	}
	return resp
}

type embeddingHealthResponse struct {
	CatalogID    string `json:"catalog_id"`
	SpecsTotal   int    `json:"specs_total"`
	SpecsIndexed int    `json:"specs_indexed"`
	SpecsPending int    `json:"specs_pending"`
	SpecsRunning int    `json:"specs_running"`
	SpecsFailed  int    `json:"specs_failed"`
}

type embeddingJobResponse struct {
	ID             int64  `json:"id"`
	CatalogID      string `json:"catalog_id"`
	SpecName       string `json:"spec_name"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	LastError      string `json:"last_error,omitempty"`
	WorkerID       string `json:"worker_id,omitempty"`
	NextRunAt      string `json:"next_run_at,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	CompletedAt    string `json:"completed_at,omitempty"`
}

func embeddingJobResponseFromJob(j catalogindex.Job) embeddingJobResponse {
	resp := embeddingJobResponse{
		ID:        j.ID,
		CatalogID: j.CatalogID,
		SpecName:  j.SpecName,
		Kind:      string(j.Kind),
		Status:    string(j.Status),
		Attempts:  j.Attempts,
		LastError: j.LastError,
		WorkerID:  j.WorkerID,
		NextRunAt: formatTime(j.NextRunAt),
		CreatedAt: formatTime(j.CreatedAt),
	}
	if j.LeaseExpiresAt != nil {
		resp.LeaseExpiresAt = formatTime(*j.LeaseExpiresAt)
	}
	if j.StartedAt != nil {
		resp.StartedAt = formatTime(*j.StartedAt)
	}
	if j.CompletedAt != nil {
		resp.CompletedAt = formatTime(*j.CompletedAt)
	}
	return resp
}

// reloadConnectionsForCatalog iterates registered api-gateway
// toolkits and asks each to rebuild every connection pointing at
// the given catalog. Triggered on any mutation that changes the
// catalog's effective content so model-facing tool output reflects
// the new specs without a process restart.
func (h *Handler) reloadConnectionsForCatalog(catalogID string) {
	if h.deps.ToolkitRegistry == nil {
		return
	}
	for _, tk := range h.deps.ToolkitRegistry.All() {
		api, ok := tk.(*apigatewaykit.Toolkit)
		if !ok {
			continue
		}
		api.ReloadConnectionsByCatalog(catalogID)
	}
	// Broadcast to peer replicas so they rebuild their own in-memory
	// connections from this catalog (issue #501). The loop above only
	// reloads this replica.
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishCatalogReload(catalogID)
	}
}

// firstNonEmpty returns a when non-empty, otherwise b. Used by the
// clone path so the operator only has to specify what differs.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// formatTime renders the audit-visible timestamp shape we use across
// the admin API. Zero time → empty string so the JSON wire shape
// omits the field cleanly.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// validateConnectionCatalog rejects an api-kind connection whose
// config.catalog_id names a catalog that doesn't exist. Called from
// setConnectionInstance before the connection is persisted so the
// operator gets a clean 400 instead of a connection that registers
// with zero ops and confuses the model. When no catalog store is
// wired the check is skipped — the toolkit's runtime path already
// warns and proceeds, which is the right behavior for catalog-less
// deployments.
func (h *Handler) validateConnectionCatalog(ctx context.Context, kind string, config map[string]any) (string, bool) {
	if kind != connectionKindAPI {
		return "", true
	}
	if h.deps.APICatalogStore == nil {
		return "", true
	}
	raw, ok := config["catalog_id"]
	if !ok {
		return "", true
	}
	id, ok := raw.(string)
	if !ok || id == "" {
		return "", true
	}
	_, err := h.deps.APICatalogStore.GetCatalog(ctx, id)
	if errors.Is(err, apicatalog.ErrNotFound) {
		return "catalog_id references a catalog that does not exist: " + id, false
	}
	if err != nil {
		slog.Warn("validateConnectionCatalog: lookup failed",
			"catalog_id", id, logKeyError, err)
		return "failed to validate catalog_id", false
	}
	return "", true
}

// userIDForAudit returns the operator email/id for the audit trail
// on catalog mutations. Empty when no auth context is attached (CLI
// tests, dev mode).
func userIDForAudit(r *http.Request) string {
	if u := GetUser(r.Context()); u != nil {
		if u.Email != "" {
			return u.Email
		}
		return u.UserID
	}
	return ""
}
