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
	"strings"
	"time"

	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
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
			SpecName:      s.SpecName,
			Content:       s.Content,
			SourceKind:    s.SourceKind,
			SourceURL:     s.SourceURL,
			ETag:          s.ETag,
			BasePath:      s.BasePath,
			LastFetchedAt: s.LastFetchedAt,
		}
		if upErr := h.deps.APICatalogStore.UpsertSpec(r.Context(), dstID, clone); upErr != nil {
			writeError(w, http.StatusInternalServerError,
				"failed to copy spec "+s.SpecName+": "+upErr.Error())
			return false
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
	SpecName      string `json:"spec_name"`
	Content       string `json:"content,omitempty"`
	SourceKind    string `json:"source_kind"`
	SourceURL     string `json:"source_url,omitempty"`
	ETag          string `json:"etag,omitempty"`
	BasePath      string `json:"base_path,omitempty"`
	LastFetchedAt string `json:"last_fetched_at,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

// specListResponse wraps the spec list so we have a stable shape
// the portal can extend later (e.g. with paging) without breaking
// existing JSON consumers.
type specListResponse struct {
	Specs []specResponse `json:"specs"`
}

func (h *Handler) listCatalogSpecs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specs, err := h.deps.APICatalogStore.ListSpecs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list specs")
		return
	}
	out := specListResponse{Specs: make([]specResponse, 0, len(specs))}
	for _, s := range specs {
		out.Specs = append(out.Specs, specToResponse(s, false))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	spec, err := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "spec not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to get spec")
		return
	}
	writeJSON(w, http.StatusOK, specToResponse(*spec, true))
}

// upsertCatalogSpecRequest is the body for the inline / URL save path.
//
// BasePath sets the operator-supplied per-spec URL prefix. Optional;
// empty leaves it unset (the toolkit derives the prefix from the
// spec's servers[0].url at registration time). Normalized via
// catalog.NormalizeBasePath at write time: must start with "/",
// must not contain CR/LF/NUL/?/#, trailing slash is stripped.
type upsertCatalogSpecRequest struct {
	SourceKind string `json:"source_kind"`
	Content    string `json:"content,omitempty"`
	SourceURL  string `json:"source_url,omitempty"`
	BasePath   string `json:"base_path,omitempty"`
}

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
	if err := h.deps.APICatalogStore.UpsertSpec(r.Context(), id, entry); err != nil {
		writeError(w, h.specErrorStatus(err), "failed to save spec: "+err.Error())
		return
	}
	h.reloadConnectionsForCatalog(id)
	saved, _ := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	if saved != nil {
		writeJSON(w, http.StatusOK, specToResponse(*saved, false))
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
			SpecName:   specName,
			Content:    req.Content,
			SourceKind: apicatalog.SourceInline,
			BasePath:   req.BasePath,
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
	// Base path precedence on upload:
	//   1. Explicit ?base_path=... on the upload URL (operator sets
	//      it during a new upload or changes it mid-stream)
	//   2. The previously-stored value on an existing spec row (so
	//      a routine re-upload of refreshed content does not blow
	//      away the operator's override)
	//   3. Empty (the migration default)
	if explicit := r.URL.Query().Get("base_path"); explicit != "" {
		entry.BasePath = explicit
	} else if existing, err := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName); err == nil && existing != nil {
		entry.BasePath = existing.BasePath
	} else if err != nil && !errors.Is(err, apicatalog.ErrNotFound) {
		// Log the swallowed lookup error so an operator chasing a
		// vanished BasePath has a breadcrumb. The upload still
		// proceeds with BasePath="" so a transient lookup failure
		// does not block the operator from saving the new content.
		slog.Warn("apigateway: catalog spec base_path preserve lookup failed",
			"catalog_id", id, "spec_name", specName, logKeyError, err)
	}
	if err := apicatalog.ValidateContent(entry.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.deps.APICatalogStore.UpsertSpec(r.Context(), id, entry); err != nil {
		writeError(w, h.specErrorStatus(err), "failed to save spec: "+err.Error())
		return
	}
	h.reloadConnectionsForCatalog(id)
	saved, _ := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	if saved != nil {
		writeJSON(w, http.StatusOK, specToResponse(*saved, false))
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

func (h *Handler) refreshCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	existing, err := h.deps.APICatalogStore.GetSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "spec not found")
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
		SpecName:      specName,
		Content:       res.Content,
		SourceKind:    apicatalog.SourceURL,
		SourceURL:     existing.SourceURL,
		ETag:          res.ETag,
		BasePath:      existing.BasePath,
		LastFetchedAt: res.FetchedAt,
	}
	if err := h.deps.APICatalogStore.UpsertSpec(r.Context(), id, entry); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save refreshed spec: "+err.Error())
		return
	}
	h.reloadConnectionsForCatalog(id)
	writeJSON(w, http.StatusOK, specToResponse(entry, false))
}

func (h *Handler) deleteCatalogSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(catalogPathID)
	specName := r.PathValue(catalogPathSpec)
	err := h.deps.APICatalogStore.DeleteSpec(r.Context(), id, specName)
	switch {
	case errors.Is(err, apicatalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "spec not found")
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
		LastFetchedAt: formatTime(s.LastFetchedAt),
		CreatedAt:     formatTime(s.CreatedAt),
		UpdatedAt:     formatTime(s.UpdatedAt),
	}
	if includeContent {
		resp.Content = s.Content
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
