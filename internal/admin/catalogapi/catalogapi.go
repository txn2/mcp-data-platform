// Package catalogapi serves the /api/v1/admin/api-catalogs surface: the
// OpenAPI spec bundles that api-kind connections reference by catalog_id, and
// the per-spec embedding job visibility and retry endpoints. It is a
// decomposition seam of pkg/admin (which sat at the package size budget); the
// parent registers it on the admin mux and injects the request-scoped helpers
// it shares with the other admin routes.
//
// Catalogs are global — one set of specs may back many connections — so every
// mutation fans out to the live api-gateway toolkits via
// ReloadConnectionsByCatalog, and to peer replicas via the reloader, so that
// model-facing surfaces (api_list_endpoints, api_get_endpoint_schema) reflect
// new content without a restart.
package catalogapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

const (
	// logKeyError is the structured-log key for an error value, matching
	// the key the parent admin package uses so the two emit one field name.
	logKeyError = "error"

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

// CatalogStore is the subset of apigateway/catalog.Store these routes need.
// Declared here rather than against the concrete store so the apigateway
// toolkit's other dependencies (auth-events writer, embedding provider) do not
// become this seam's — or its parent's — transitive concern. pkg/admin aliases
// this as its exported APICatalogStore.
type CatalogStore interface {
	CreateCatalog(ctx context.Context, c apicatalog.Catalog) error
	GetCatalog(ctx context.Context, id string) (*apicatalog.Catalog, error)
	ListCatalogs(ctx context.Context) ([]apicatalog.Catalog, error)
	UpdateCatalog(ctx context.Context, id string, u apicatalog.Update) error
	DeleteCatalog(ctx context.Context, id string) error
	UpsertSpec(ctx context.Context, catalogID string, spec apicatalog.SpecEntry) error
	GetSpec(ctx context.Context, catalogID, specName string) (*apicatalog.SpecEntry, error)
	ListSpecs(ctx context.Context, catalogID string) ([]apicatalog.SpecEntry, error)
	DeleteSpec(ctx context.Context, catalogID, specName string) error
	ReferencingConnections(ctx context.Context, catalogID string) ([]apicatalog.ConnectionRef, error)
	UpsertOperationEmbeddings(ctx context.Context, catalogID, specName string, rows []apicatalog.OperationEmbedding) error
	ListOperationEmbeddings(ctx context.Context, catalogID, specName string) ([]apicatalog.OperationEmbedding, error)
	DeleteOperationEmbeddings(ctx context.Context, catalogID, specName string) error
	SetOperationCount(ctx context.Context, catalogID, specName string, count int) error
}

// CatalogReloader fans a catalog mutation out to peer replicas so their live
// api-gateway toolkits pick up the new content. Implemented by
// *platform.Platform; nil on single-replica and test setups, where the local
// reload below is the whole story.
type CatalogReloader interface {
	PublishCatalogReload(catalogID string)
}

// ToolkitReloader is the live-registry side of the same fan-out: the local
// replica's api-gateway toolkits are reloaded in-process.
type ToolkitReloader interface {
	All() []registry.Toolkit
}

// Config carries the stores and parent-owned helpers these routes need.
type Config struct {
	// Catalogs persists OpenAPI spec bundles. nil disables every route in
	// this package.
	Catalogs CatalogStore
	// EmbedJobs is the Postgres-backed embedding job queue. nil (no
	// database) disables the embedding visibility and retry routes; spec
	// writes still succeed without persisting embeddings.
	EmbedJobs catalogindex.Store
	// Reload broadcasts a catalog change to peer replicas. nil skips the
	// broadcast.
	Reload CatalogReloader
	// Toolkits is the live toolkit registry used to reload this replica's
	// api-gateway instances after a catalog mutation.
	Toolkits ToolkitReloader
	// Mutable reports database config mode; false registers the read
	// routes only, matching the other admin configuration surfaces.
	Mutable bool
	// Author resolves the acting admin for audit columns.
	Author func(*http.Request) string
	// Decode is the parent's strict JSON body decoder (unknown fields
	// rejected, size-capped); its error text is safe to return as the
	// problem detail.
	Decode func(w http.ResponseWriter, r *http.Request, dst any) error
	// DecodeLimit is Decode with a caller-supplied body cap, used by the
	// inline-spec write path whose `content` field legitimately carries
	// the same payload the multipart route allows up to 10 MiB.
	DecodeLimit func(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the API-catalog routes on mux. Reads need only the store;
// writes additionally need database config mode, matching the gating the
// parent applies to its other configuration surfaces.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Catalogs == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/api-catalogs", h.listCatalogs)
	mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}", h.getCatalog)
	mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/specs", h.listCatalogSpecs)
	if !cfg.Mutable {
		return
	}
	mux.HandleFunc("POST /api/v1/admin/api-catalogs", h.createCatalog)
	mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}", h.updateCatalog)
	mux.HandleFunc("DELETE /api/v1/admin/api-catalogs/{id}", h.deleteCatalog)
	mux.HandleFunc("POST /api/v1/admin/api-catalogs/{id}/clone", h.cloneCatalog)
	mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/specs/{spec}", h.getCatalogSpec)
	mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}", h.upsertCatalogSpec)
	mux.HandleFunc("PUT /api/v1/admin/api-catalogs/{id}/specs/{spec}/upload", h.uploadCatalogSpec)
	mux.HandleFunc("POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/refresh", h.refreshCatalogSpec)
	mux.HandleFunc("DELETE /api/v1/admin/api-catalogs/{id}/specs/{spec}", h.deleteCatalogSpec)
	// Embedding-job admin surface. The /reembed endpoint that
	// earlier revisions of this handler shipped is gone: spec
	// writes now enqueue a job automatically, the reconciler fills
	// in any gap, and the operator never needs a button. The
	// remaining endpoints are read-only visibility plus a manual
	// retry escape hatch for operators who need to force a re-embed
	// after an external model swap.
	if cfg.EmbedJobs != nil {
		mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-status", h.listCatalogEmbeddingStatuses)
		mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-health", h.getCatalogEmbeddingHealth)
		mux.HandleFunc("GET /api/v1/admin/api-catalogs/{id}/embedding-jobs", h.listCatalogEmbeddingJobs)
		mux.HandleFunc("POST /api/v1/admin/api-catalogs/{id}/specs/{spec}/reembed", h.manualRetryEmbedding)
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
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs [get]
func (h *handler) listCatalogs(w http.ResponseWriter, r *http.Request) {
	cs, err := h.cfg.Catalogs.ListCatalogs(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list catalogs")
		slog.Warn("listCatalogs", logKeyError, err)
		return
	}
	out := make([]catalogResponse, 0, len(cs))
	for _, c := range cs {
		out = append(out, h.catalogToResponse(r.Context(), c))
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// getCatalog handles GET /api/v1/admin/api-catalogs/{id}.
//
// @Summary      Get API catalog
// @Description  Returns a single API catalog by ID with derived spec_count and ref_count fields.
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  catalogResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id} [get]
func (h *handler) getCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	c, err := h.cfg.Catalogs.GetCatalog(r.Context(), id)
	if errors.Is(err, apicatalog.ErrNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, "catalog not found")
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get catalog")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, h.catalogToResponse(r.Context(), *c))
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
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs [post]
func (h *handler) createCatalog(w http.ResponseWriter, r *http.Request) {
	var req createCatalogRequest
	if err := h.cfg.Decode(w, r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DisplayName == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	if req.Name == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	c := apicatalog.Catalog{
		ID:          req.ID,
		Name:        req.Name,
		Version:     req.Version,
		DisplayName: req.DisplayName,
		Description: req.Description,
		CreatedBy:   h.cfg.Author(r),
	}
	err := h.cfg.Catalogs.CreateCatalog(r.Context(), c)
	if errors.Is(err, apicatalog.ErrInvalidID) {
		httpjson.WriteError(w, http.StatusBadRequest,
			"id must be lowercase alphanumeric with hyphens, 1-100 chars, no leading/trailing hyphen")
		return
	}
	if errors.Is(err, apicatalog.ErrConflict) {
		httpjson.WriteError(w, http.StatusConflict, "catalog id or (name, version) already exists")
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to create catalog")
		slog.Warn("createCatalog", logKeyError, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, h.catalogToResponse(r.Context(), c))
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
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id} [put]
func (h *handler) updateCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	var req updateCatalogRequest
	if err := h.cfg.Decode(w, r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := h.cfg.Catalogs.UpdateCatalog(r.Context(), id, apicatalog.Update{
		Name:        req.Name,
		Version:     req.Version,
		DisplayName: req.DisplayName,
		Description: req.Description,
	})
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "catalog not found")
		return
	case errors.Is(err, apicatalog.ErrConflict):
		httpjson.WriteError(w, http.StatusConflict, "edit would collide with an existing (name, version)")
		return
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to update catalog")
		return
	}
	h.reloadConnectionsForCatalog(id)
	updated, _ := h.cfg.Catalogs.GetCatalog(r.Context(), id)
	if updated != nil {
		httpjson.WriteJSON(w, http.StatusOK, h.catalogToResponse(r.Context(), *updated))
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, httpjson.StatusResponse{Status: "ok"})
}

// deleteCatalog handles DELETE /api/v1/admin/api-catalogs/{id}.
//
// @Summary      Delete API catalog
// @Description  Deletes a catalog. Rejected with 409 if any connection still references it.
// @Tags         API Catalogs
// @Produce      json
// @Param        id  path  string  true  "Catalog ID"
// @Success      200  {object}  httpjson.StatusResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id} [delete]
func (h *handler) deleteCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	refs, err := h.cfg.Catalogs.ReferencingConnections(r.Context(), id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to check catalog references")
		return
	}
	if len(refs) > 0 {
		names := make([]string, 0, len(refs))
		for _, ref := range refs {
			names = append(names, ref.Kind+"/"+ref.Name)
		}
		httpjson.WriteError(w, http.StatusConflict,
			"catalog still referenced by: "+strings.Join(names, ", "))
		return
	}
	err = h.cfg.Catalogs.DeleteCatalog(r.Context(), id)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "catalog not found")
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to delete catalog")
	default:
		// WithoutCancel: the delete has committed, so the queue cleanup
		// must not die with the request (a client disconnect here would
		// otherwise leave an open failure nothing else can resolve).
		catalogindex.CancelCatalogBestEffort(context.WithoutCancel(r.Context()), h.cfg.EmbedJobs, id)
		httpjson.WriteJSON(w, http.StatusOK, httpjson.StatusResponse{Status: "deleted"})
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
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/clone [post]
func (h *handler) cloneCatalog(w http.ResponseWriter, r *http.Request) {
	srcID := r.PathValue(catalogPathID)
	var req cloneCatalogRequest
	if err := h.cfg.Decode(w, r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	src, err := h.cfg.Catalogs.GetCatalog(r.Context(), srcID)
	if errors.Is(err, apicatalog.ErrNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, "source catalog not found")
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get source catalog")
		return
	}
	dst := apicatalog.Catalog{
		ID:          req.ID,
		Name:        firstNonEmpty(req.Name, src.Name),
		Version:     req.Version,
		DisplayName: firstNonEmpty(req.DisplayName, src.DisplayName),
		Description: src.Description,
		CreatedBy:   h.cfg.Author(r),
	}
	if !h.createClonedCatalog(w, r, dst) {
		return
	}
	if !h.copyCatalogSpecs(w, r, srcID, dst.ID) {
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, h.catalogToResponse(r.Context(), dst))
}

// createClonedCatalog wraps CreateCatalog with the error-mapping
// shared with createCatalog. Returns false when the response was
// already written and the caller must abort.
func (h *handler) createClonedCatalog(w http.ResponseWriter, r *http.Request, dst apicatalog.Catalog) bool {
	err := h.cfg.Catalogs.CreateCatalog(r.Context(), dst)
	switch {
	case errors.Is(err, apicatalog.ErrInvalidID):
		httpjson.WriteError(w, http.StatusBadRequest, "destination id is invalid")
		return false
	case errors.Is(err, apicatalog.ErrConflict):
		httpjson.WriteError(w, http.StatusConflict, "destination id or (name, version) already exists")
		return false
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to create destination catalog")
		return false
	}
	return true
}

// copyCatalogSpecs duplicates every spec from src into dst. Returns
// false when the response was already written and the caller must
// abort.
func (h *handler) copyCatalogSpecs(w http.ResponseWriter, r *http.Request, srcID, dstID string) bool {
	specs, err := h.cfg.Catalogs.ListSpecs(r.Context(), srcID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list source specs")
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
		if upErr := h.cfg.Catalogs.UpsertSpec(r.Context(), dstID, clone); upErr != nil {
			httpjson.WriteError(w, http.StatusInternalServerError,
				"failed to copy spec "+s.SpecName+": "+upErr.Error())
			return false
		}
		// Clone the persisted vectors too so the new catalog
		// answers semantic ranking on the first call without
		// recomputing. Best-effort: a missing source-side vector
		// set just means the destination spec starts un-indexed
		// and the reconciler enqueues a job on the next sweep.
		if rows, err := h.cfg.Catalogs.ListOperationEmbeddings(r.Context(), srcID, s.SpecName); err == nil && len(rows) > 0 {
			if upErr := h.cfg.Catalogs.UpsertOperationEmbeddings(r.Context(), dstID, s.SpecName, rows); upErr != nil {
				slog.Warn("apigateway: clone embeddings copy failed",
					logKeyCatalogID, logsan.SanitizeForLog(dstID), logKeySpecName, logsan.SanitizeForLog(s.SpecName), logKeyError, upErr)
			}
		} else {
			// Vectors were missing on the source side too;
			// enqueue a job so the worker fills them in
			// asynchronously. Without this the cloned spec
			// would sit at "not indexed" until the periodic
			// reconciler picked it up.
			catalogindex.EnqueueBestEffort(r.Context(), h.cfg.EmbedJobs, dstID, s.SpecName)
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
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs [get]
func (h *handler) listCatalogSpecs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specs, err := h.cfg.Catalogs.ListSpecs(r.Context(), id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list specs")
		return
	}
	out := specListResponse{Specs: make([]specResponse, 0, len(specs))}
	for _, s := range specs {
		out.Specs = append(out.Specs, h.specToResponseWithEmbedding(r.Context(), id, s, false))
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
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
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec} [get]
func (h *handler) getCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	spec, err := h.cfg.Catalogs.GetSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, errSpecNotFound)
		return
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, *spec, true))
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
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      502  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec} [put]
func (h *handler) upsertCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	var req upsertCatalogSpecRequest
	// The inline `content` field carries a full OpenAPI spec, which routinely
	// exceeds the small default admin body cap; allow the same 10 MiB bound the
	// multipart upload path uses (plus JSON framing overhead) so large specs
	// still save. Unknown-field rejection still applies.
	if err := h.cfg.DecodeLimit(w, r, &req, catalogSpecMaxUploadBytes+1024); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
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
		httpjson.WriteError(w, status, err.Error())
		return
	}
	if err := apicatalog.ValidateContent(entry.Content); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry.OperationCount = apicatalog.CountOperations(entry.Content)
	if err := h.cfg.Catalogs.UpsertSpec(r.Context(), id, entry); err != nil {
		httpjson.WriteError(w, h.specErrorStatus(err), "failed to save spec: "+err.Error())
		return
	}
	catalogindex.EnqueueBestEffort(r.Context(), h.cfg.EmbedJobs, id, entry.SpecName)
	h.reloadConnectionsForCatalog(id)
	saved, _ := h.cfg.Catalogs.GetSpec(r.Context(), id, specName)
	if saved != nil {
		httpjson.WriteJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, *saved, false))
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, httpjson.StatusResponse{Status: "ok"})
}

// materializeSpec converts the upsert request into a SpecEntry,
// fetching the URL when source_kind=url. Validation of the resulting
// content (it must parse as OpenAPI) is the caller's responsibility
// — we centralize the fetch and the shape construction here so the
// admin handler's body stays focused on HTTP plumbing.
func (*handler) materializeSpec(ctx context.Context, specName string, req upsertCatalogSpecRequest) (apicatalog.SpecEntry, error) {
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
func (*handler) specErrorStatus(err error) int {
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
	// #nosec G120 -- r.Body is bounded by http.MaxBytesReader above, so the
	// multipart parse cannot exhaust memory; gosec's pattern match misses it.
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "missing 'file' form field")
		return nil, false
	}
	defer func() { _ = file.Close() }()
	if header.Size > catalogSpecMaxUploadBytes {
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("file exceeds %d-byte limit", catalogSpecMaxUploadBytes))
		return nil, false
	}
	if ct := header.Header.Get("Content-Type"); ct != "" {
		mediaType, _, mtErr := mime.ParseMediaType(ct)
		if mtErr != nil || !allowedSpecMIMETypes[strings.ToLower(mediaType)] {
			httpjson.WriteError(w, http.StatusUnsupportedMediaType, "unsupported content-type: "+ct)
			return nil, false
		}
	}
	body, err := io.ReadAll(io.LimitReader(file, catalogSpecMaxUploadBytes+1))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "failed to read upload: "+err.Error())
		return nil, false
	}
	if int64(len(body)) > catalogSpecMaxUploadBytes {
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, "upload exceeds size cap")
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
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      415  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/upload [put]
func (h *handler) uploadCatalogSpec(w http.ResponseWriter, r *http.Request) {
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
	existing, lookupErr := h.cfg.Catalogs.GetSpec(r.Context(), id, specName)
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
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry.OperationCount = apicatalog.CountOperations(entry.Content)
	if err := h.cfg.Catalogs.UpsertSpec(r.Context(), id, entry); err != nil {
		httpjson.WriteError(w, h.specErrorStatus(err), "failed to save spec: "+err.Error())
		return
	}
	catalogindex.EnqueueBestEffort(r.Context(), h.cfg.EmbedJobs, id, entry.SpecName)
	h.reloadConnectionsForCatalog(id)
	saved, _ := h.cfg.Catalogs.GetSpec(r.Context(), id, specName)
	if saved != nil {
		httpjson.WriteJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, *saved, false))
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, httpjson.StatusResponse{Status: "ok"})
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
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      413  {object}  httpjson.ProblemDetail
// @Failure      502  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/refresh [post]
func (h *handler) refreshCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	existing, err := h.cfg.Catalogs.GetSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, errSpecNotFound)
		return
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	if existing.SourceKind != apicatalog.SourceURL {
		httpjson.WriteError(w, http.StatusBadRequest, "spec was not URL-sourced; refresh is only valid for source_kind=url")
		return
	}
	res, err := apicatalog.FetchFromURL(r.Context(), existing.SourceURL, apicatalog.FetchOptions{})
	if err != nil {
		httpjson.WriteError(w, h.specErrorStatus(err), "fetch failed: "+err.Error())
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
	if err := h.cfg.Catalogs.UpsertSpec(r.Context(), id, entry); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to save refreshed spec: "+err.Error())
		return
	}
	catalogindex.EnqueueBestEffort(r.Context(), h.cfg.EmbedJobs, id, entry.SpecName)
	h.reloadConnectionsForCatalog(id)
	httpjson.WriteJSON(w, http.StatusOK, h.specToResponseWithEmbedding(r.Context(), id, entry, false))
}

// deleteCatalogSpec handles DELETE /api/v1/admin/api-catalogs/{id}/specs/{spec}.
//
// @Summary      Delete catalog spec
// @Description  Removes a spec from a catalog and reloads dependent api-gateway connections.
// @Tags         API Catalogs
// @Produce      json
// @Param        id    path  string  true  "Catalog ID"
// @Param        spec  path  string  true  "Spec name"
// @Success      200  {object}  httpjson.StatusResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec} [delete]
func (h *handler) deleteCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	err := h.cfg.Catalogs.DeleteSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, errSpecNotFound)
	case err != nil:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to delete spec")
	default:
		// WithoutCancel: same rationale as deleteCatalog — the spec row
		// is gone, so the cleanup outlives the request.
		catalogindex.CancelBestEffort(context.WithoutCancel(r.Context()), h.cfg.EmbedJobs, id, specName)
		h.reloadConnectionsForCatalog(id)
		httpjson.WriteJSON(w, http.StatusOK, httpjson.StatusResponse{Status: "deleted"})
	}
}

// catalogToResponse decorates a Catalog with spec_count and
// ref_count by reading the store. Errors are swallowed (the counts
// just become zero) — the catalog listing should still render even
// if a transient DB hiccup happens during the lookup.
func (h *handler) catalogToResponse(ctx context.Context, c apicatalog.Catalog) catalogResponse {
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
	if specs, err := h.cfg.Catalogs.ListSpecs(ctx, c.ID); err == nil {
		resp.SpecCount = len(specs)
	}
	if refs, err := h.cfg.Catalogs.ReferencingConnections(ctx, c.ID); err == nil {
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
func (h *handler) specToResponseWithEmbedding(ctx context.Context, catalogID string, s apicatalog.SpecEntry, includeContent bool) specResponse {
	resp := specToResponse(s, includeContent)
	resp.OperationCount = s.OperationCount
	if h.cfg.Catalogs != nil {
		if rows, err := h.cfg.Catalogs.ListOperationEmbeddings(ctx, catalogID, s.SpecName); err == nil {
			resp.EmbeddingCount = len(rows)
		}
	}
	if h.cfg.EmbedJobs != nil {
		jobs, err := h.cfg.EmbedJobs.List(ctx, catalogindex.ListFilter{
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
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/embedding-status [get]
func (h *handler) listCatalogEmbeddingStatuses(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	rows, err := h.cfg.EmbedJobs.SpecStatuses(r.Context(), id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list embedding statuses")
		slog.Warn("apigateway: list embedding statuses",
			logKeyCatalogID, id, logKeyError, err)
		return
	}
	out := make([]embeddingStatusResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, embeddingStatusResponseFromRow(r))
	}
	httpjson.WriteJSON(w, http.StatusOK, map[string]any{"specs": out})
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
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/embedding-health [get]
func (h *handler) getCatalogEmbeddingHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	h2, err := h.cfg.EmbedJobs.Health(r.Context(), id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to compute embedding health")
		slog.Warn("apigateway: embedding health",
			logKeyCatalogID, id, logKeyError, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, embeddingHealthResponse{
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
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/embedding-jobs [get]
func (h *handler) listCatalogEmbeddingJobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	filter := catalogindex.ListFilter{CatalogID: id, Limit: embeddingJobListDefaultLimit}
	if s := r.URL.Query().Get("status"); s != "" {
		filter.Status = catalogindex.Status(s)
	}
	if s := r.URL.Query().Get("spec_name"); s != "" {
		filter.SpecName = s
	}
	jobs, err := h.cfg.EmbedJobs.List(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list embedding jobs")
		slog.Warn("apigateway: list embedding jobs",
			logKeyCatalogID, id, logKeyError, err)
		return
	}
	out := make([]embeddingJobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, embeddingJobResponseFromJob(j))
	}
	httpjson.WriteJSON(w, http.StatusOK, map[string]any{"jobs": out})
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
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/reembed [post]
func (h *handler) manualRetryEmbedding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	if _, err := h.cfg.Catalogs.GetSpec(r.Context(), id, specName); err != nil {
		if errors.Is(err, apicatalog.ErrNotFound) {
			httpjson.WriteError(w, http.StatusNotFound, errSpecNotFound)
			return
		}
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	created, err := h.cfg.EmbedJobs.Enqueue(r.Context(), catalogindex.SpecKey{
		CatalogID: id, SpecName: specName,
	}, catalogindex.KindManualRetry)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to enqueue embedding job: "+err.Error())
		return
	}
	httpjson.WriteJSON(w, http.StatusAccepted, map[string]any{
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
func (h *handler) reloadConnectionsForCatalog(catalogID string) {
	if h.cfg.Toolkits == nil {
		return
	}
	for _, tk := range h.cfg.Toolkits.All() {
		api, ok := tk.(*apigatewaykit.Toolkit)
		if !ok {
			continue
		}
		api.ReloadConnectionsByCatalog(catalogID)
	}
	// Broadcast to peer replicas so they rebuild their own in-memory
	// connections from this catalog (issue #501). The loop above only
	// reloads this replica.
	if h.cfg.Reload != nil {
		h.cfg.Reload.PublishCatalogReload(catalogID)
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
