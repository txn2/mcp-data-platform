package resource

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// MaxMultipartMemory is the max memory for multipart form parsing (10 MB).
const MaxMultipartMemory = 10 << 20

// Common response message constants.
const (
	msgError          = "error"
	msgUnauthorized   = "unauthorized"
	msgNotFound       = "not found"
	pathParamID       = "id"
	headerContentType = "Content-Type"
	// mimeTypeOctetStream is the MIME type used as a fallback when an
	// upload omits Content-Type and as a sentinel in tests.
	mimeTypeOctetStream = "application/octet-stream"
)

// Deps holds the dependencies for the resource HTTP handler.
type Deps struct {
	Store     Store
	S3Client  S3Client
	S3Bucket  string
	URIScheme string          // defaults to "mcp" if empty
	OnCreate  func(*Resource) // called after successful create to register with MCP
	OnDelete  func(string)    // called after successful delete with URI to unregister
	// OnDeleteID is called after a successful delete with the resource's ID,
	// so a consumer keyed on the record rather than on its MCP URI can clean
	// up after it -- the tables registered over its file (#1327). Separate
	// from OnDelete because that one exists to unregister an MCP resource and
	// is keyed on the URI it was registered under.
	OnDeleteID func(context.Context, string)
	// OnRevised is called after a revision moves the resource's head -- a
	// replacement or a restore -- with the version number it was recorded as,
	// so the tables registered over the file follow it (#1536). It returns
	// what happened to each table, which the response carries; the revision
	// is never failed by it. Nil on a deployment that cannot register tables.
	OnRevised func(ctx context.Context, id string, version int) []string

	// Versions records content revisions. Absent on a deployment whose store
	// does not implement VersionStore, which disables the revision and version
	// routes (503) and leaves create, metadata edits, and reads unaffected.
	Versions VersionStore
	// MaxVersions is the retention cap; non-positive selects DefaultMaxVersions.
	MaxVersions int
	// ReadRecorder audits served content reads. Absent when audit is disabled,
	// which silences read events without affecting the reads themselves.
	ReadRecorder ReadRecorder
	// Usage supplies audit-derived read counts for the detail read. Absent when
	// audit is disabled, which leaves the usage field off the response.
	Usage UsageReader
	// MoveRecorder audits a resource refiled into another library. Absent when
	// audit is disabled, which silences the event without affecting the move.
	MoveRecorder MoveRecorder
}

// ClaimsExtractor extracts resource Claims from an HTTP request.
// Provided by the platform auth middleware.
type ClaimsExtractor func(r *http.Request) (*Claims, error)

// ErrForbidden signals that authentication succeeded at the credential level
// but the request is refused for a policy reason the client can recover from
// without re-authenticating — specifically a CSRF-token failure on a
// cookie-authenticated mutation. A ClaimsExtractor returns it so the handler
// responds 403 (not 401), matching the admin/portal surfaces and preventing
// the SPA from force-logging-out the user on a recoverable CSRF error.
var ErrForbidden = errors.New("forbidden")

// Handler provides HTTP endpoints for resource CRUD.
type Handler struct {
	mux       *http.ServeMux
	deps      Deps
	extractFn ClaimsExtractor
}

// authenticate resolves the caller's claims, writing the appropriate HTTP
// error and returning ok=false when authentication fails. A CSRF rejection
// (ErrForbidden) maps to 403 so the SPA surfaces a recoverable error rather
// than force-logging-out the user; every other failure maps to 401.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (*Claims, bool) {
	claims, err := h.extractFn(r)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusForbidden, "invalid or missing CSRF token")
			return nil, false
		}
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
		return nil, false
	}
	return claims, true
}

// notifyCreate registers a newly created resource with MCP clients.
func (h *Handler) notifyCreate(res *Resource) {
	if h.deps.OnCreate != nil {
		slog.Debug("resource handler: notifying create", "resource_id", res.ID) // #nosec G706 -- ID is server-generated, not user input
		h.deps.OnCreate(res)
	}
}

// notifyDelete unregisters a deleted resource from MCP clients.
func (h *Handler) notifyDelete(uri string) {
	if h.deps.OnDelete != nil {
		slog.Debug("resource handler: notifying delete") //nolint:gosec // removed URI from log to satisfy taint analysis
		h.deps.OnDelete(uri)
	}
}

// notifyDeleteID reports a deleted resource to the consumers keyed on its ID.
func (h *Handler) notifyDeleteID(ctx context.Context, id string) {
	if h.deps.OnDeleteID != nil {
		h.deps.OnDeleteID(ctx, id)
	}
}

// NewHandler creates a resource handler with auth middleware.
func NewHandler(deps Deps, extractFn ClaimsExtractor, authMiddle func(http.Handler) http.Handler) *Handler {
	inner := http.NewServeMux()
	h := &Handler{
		mux:       inner,
		deps:      deps,
		extractFn: extractFn,
	}
	h.registerRoutesOn(inner)
	if authMiddle != nil {
		outer := http.NewServeMux()
		outer.Handle("/", authMiddle(inner))
		h.mux = outer
	}
	return h
}

func (h *Handler) registerRoutesOn(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/resources", h.handleCreate)
	mux.HandleFunc("GET /api/v1/resources/facets", h.handleFacets)
	mux.HandleFunc("GET /api/v1/resources/thumbnails/pending", h.handlePendingThumbnails)
	mux.HandleFunc("PUT /api/v1/resources/{id}/thumbnail", h.handleUploadThumbnail)
	mux.HandleFunc("GET /api/v1/resources/{id}/thumbnail", h.handleGetThumbnail)
	mux.HandleFunc("DELETE /api/v1/resources/{id}/thumbnail", h.handleClearThumbnail)
	mux.HandleFunc("POST /api/v1/resources/folders/move", h.handleFolderMove)
	mux.HandleFunc("GET /api/v1/resources", h.handleList)
	mux.HandleFunc("GET /api/v1/resources/{id}", h.handleGet)
	mux.HandleFunc("GET /api/v1/resources/{id}/content", h.handleGetContent)
	mux.HandleFunc("PATCH /api/v1/resources/{id}", h.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/resources/{id}", h.handleDelete)
	h.registerContentRoutes(mux)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// createInput holds the validated fields from a resource creation request.
type createInput struct {
	scope       Scope
	scopeID     string
	path        string
	displayName string
	description string
	tags        []string
}

// validateCreateInput parses and validates the form fields for resource creation.
func validateCreateInput(r *http.Request) (*createInput, error) {
	scope := Scope(r.FormValue("scope"))
	scopeID := r.FormValue("scope_id")
	path := r.FormValue("path")
	displayName := r.FormValue("display_name")
	description := r.FormValue("description")
	tags := r.Form["tags"]

	if err := ValidateScope(scope, scopeID); err != nil {
		return nil, err
	}
	if err := ValidatePath(path); err != nil {
		return nil, err
	}
	if err := ValidateDisplayName(displayName); err != nil {
		return nil, err
	}
	if err := ValidateDescription(description); err != nil {
		return nil, err
	}
	if err := ValidateTags(tags); err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}

	return &createInput{
		scope:       scope,
		scopeID:     scopeID,
		path:        path,
		displayName: displayName,
		description: description,
		tags:        tags,
	}, nil
}

// uploadedFile holds the contents and metadata of an uploaded file.
type uploadedFile struct {
	data     []byte
	filename string
	// mimeType is the type the resource is stored under: the multipart part's
	// declaration when it was specific, otherwise the type detected from the
	// bytes.
	mimeType string
	// declaredMIMEType is the multipart part's own declaration, kept so the
	// caller can tell whether detection replaced it.
	declaredMIMEType string
}

// readUploadedFile reads and validates the uploaded file from the request.
func readUploadedFile(r *http.Request) (*uploadedFile, error) {
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, errors.New("file is required")
	}
	defer func() { _ = file.Close() }()

	if header.Size > MaxUploadBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxUploadBytes/(1<<20))
	}

	declared := header.Header.Get(headerContentType)
	// The declaration is checked against the deny list before the body is read
	// so a rejected type costs nothing, and the detected type is checked again
	// below: detection must not be able to route around the deny list.
	if err := ValidateMIMEType(declared); err != nil {
		return nil, err
	}

	filename, err := SanitizeFilename(header.Filename)
	if err != nil {
		return nil, fmt.Errorf("invalid filename: %w", err)
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	if int64(len(data)) > MaxUploadBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxUploadBytes/(1<<20))
	}

	// Browsers send application/octet-stream for any extension they do not
	// recognize, and non-browser clients often send nothing at all, so the
	// declaration alone would leave most uploads without a usable preview.
	//
	// The filename goes in with the bytes because a declaration can also be
	// specific and wrong: a machine with Excel installed sends
	// application/vnd.ms-excel for a .csv, and a resource stored under that
	// type is not a CSV to the portal's table panel, to a thumbnail, or to
	// manage_table. Detection prefers the name only where the content agrees
	// with it (#1438).
	mimeType := contenttype.DetectFileBytes(declared, filename, data)
	if err := ValidateMIMEType(mimeType); err != nil {
		return nil, err
	}

	return &uploadedFile{
		data:             data,
		filename:         filename,
		mimeType:         mimeType,
		declaredMIMEType: declared,
	}, nil
}

// errorResponse is the JSON error envelope returned by all error responses.
// Used by swagger annotations only.
type errorResponse struct { //nolint:unused // swagger model
	Error string `json:"error" example:"descriptive error message"`
}

// listResponse is the JSON envelope returned by the list endpoint.
// Used by swagger annotations only.
type listResponse struct { //nolint:unused // swagger model
	Resources []Resource `json:"resources"`
	Total     int        `json:"total" example:"42"`
}

// facetsResponse is the JSON envelope returned by the facets endpoint: what a
// library holds, for the controls that narrow it.
// Used by swagger annotations only.
type facetsResponse struct { //nolint:unused // swagger model
	Folders []Folder `json:"folders"`
	Tags    []string `json:"tags"`
}

// --- Create ---

// handleCreate handles POST /api/v1/resources.
//
// @Summary      Create resource
// @Description  Upload a new managed resource with metadata and file content.
// @Tags         Resources
// @Accept       multipart/form-data
// @Produce      json
// @Param        file         formData  file    true   "File to upload (max 100 MB)"
// @Param        display_name formData  string  true   "Human-readable display name"
// @Param        scope        formData  string  true   "Visibility scope"  Enums(global, persona, user)
// @Param        scope_id     formData  string  false  "Persona name or user sub (required for persona/user scopes)"
// @Param        path         formData  string  true   "Folder path inside the library (e.g. runbooks, datasets/media-manager)"
// @Param        description  formData  string  false  "Optional description"
// @Param        tags         formData  []string false  "Optional tags" collectionFormat(multi)
// @Success      201  {object}  resource.Resource
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      409  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources [post]
func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(MaxMultipartMemory); err != nil { // #nosec G120 -- body bounded by MaxBytesReader above
		slog.Warn("resource upload: multipart parse failed", msgError, err) //nolint:gosec // structured slog, no injection
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	input, err := validateCreateInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !CanWriteScope(*claims, input.scope, input.scopeID) {
		writeError(w, http.StatusForbidden, "insufficient permissions for scope")
		return
	}

	uf, err := readUploadedFile(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := CreateResource(r.Context(), h.deps, claims, NewResource{
		Scope: input.scope, ScopeID: input.scopeID,
		Path: input.path, Filename: uf.filename,
		DisplayName: input.displayName, Description: input.description,
		Tags: input.tags,
		Data: uf.data, MIMEType: uf.mimeType, DeclaredMIMEType: uf.declaredMIMEType,
	})
	if err != nil {
		var ce *conflictError
		if errors.As(err, &ce) {
			writeError(w, http.StatusConflict, ce.Error())
			return
		}
		var se *storageError
		if errors.As(err, &se) {
			writeError(w, http.StatusServiceUnavailable, se.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
	h.notifyCreate(res)
}

// --- List ---

// handleList handles GET /api/v1/resources.
//
// @Summary      List resources
// @Description  List managed resources the caller may read: their own, their personas', and global. A platform administrator's unfiltered listing spans every library, and a scope they may write to is listable whether or not they belong to it.
// @Tags         Resources
// @Produce      json
// @Param        scope    query  string  false  "Filter by scope"  Enums(global, persona, user)
// @Param        scope_id query  string  false  "Filter by scope ID (persona name or user sub)"
// @Param        path     query  string  false  "Filter by folder path; returns that folder and everything beneath it"
// @Param        tag      query  string  false  "Filter by tag"
// @Param        q        query  string  false  "Search display_name and description"
// @Param        sort     query  string  false  "Ordering (default updated)"  Enums(updated, last_read)
// @Param        limit    query  int     false  "Max results to return (default 100, max 200)"
// @Param        offset   query  int     false  "Pagination offset (default 0)"
// @Success      200  {object}  resource.listResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources [get]
func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	scopes, allScopes := ListScopes(*claims, r.URL.Query().Get("scope"), r.URL.Query().Get("scope_id"))

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	// Honor a client-supplied page size (the portal's infinite scroll requests a
	// fixed window per page); the store clamps it to MaxListLimit. An absent or
	// non-positive value falls back to the default.
	limit := DefaultListLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	filter := Filter{
		Scopes:    scopes,
		AllScopes: allScopes,
		Path:      r.URL.Query().Get("path"),
		Tag:       r.URL.Query().Get("tag"),
		Query:     r.URL.Query().Get("q"),
		Sort:      Sort(r.URL.Query().Get("sort")),
		Limit:     limit,
		Offset:    offset,
	}

	resources, total, err := h.deps.Store.List(r.Context(), filter)
	if err != nil {
		slog.Error("resource list failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "listing resources")
		return
	}
	if resources == nil {
		resources = []Resource{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"resources": resources,
		"total":     total,
	})
}

// handleFacets handles GET /api/v1/resources/facets.
//
// @Summary      List a library's facets
// @Description  The folders of the libraries the caller may read, each with the exact number of resources filed under it at every depth, and the distinct tags those resources carry. Both are derived from the rows rather than stored, so this is what a tree and a tag filter are drawn from; deriving them from a page of the listing could only ever report what had arrived.
// @Tags         Resources
// @Produce      json
// @Param        scope    query  string  false  "Filter by scope"  Enums(global, persona, user)
// @Param        scope_id query  string  false  "Filter by scope ID (persona name or user sub)"
// @Success      200  {object}  resource.facetsResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/facets [get]
func (h *Handler) handleFacets(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	scopes, allScopes := ListScopes(*claims, r.URL.Query().Get("scope"), r.URL.Query().Get("scope_id"))

	filter := Filter{Scopes: scopes, AllScopes: allScopes}

	folders, err := h.deps.Store.Folders(r.Context(), filter)
	if err != nil {
		slog.Error("resource folder list failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "listing folders")
		return
	}
	if folders == nil {
		folders = []Folder{}
	}

	tags, err := h.deps.Store.Tags(r.Context(), filter)
	if err != nil {
		slog.Error("resource tag list failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "listing tags")
		return
	}
	if tags == nil {
		tags = []string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "tags": tags})
}

// --- Get ---

// handleGet handles GET /api/v1/resources/{id}.
//
// @Summary      Get resource
// @Description  Retrieve metadata for a single managed resource by ID.
// @Tags         Resources
// @Produce      json
// @Param        id   path  string  true  "Resource ID"
// @Success      200  {object}  resource.Resource
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id} [get]
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	res, _, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}
	// The detail read is the one place usage is worth a rollup query: the list
	// path serves the admin table, which sorts on the stored last_read_at
	// instead.
	h.applyUsage(r.Context(), res)
	writeJSON(w, http.StatusOK, res)
}

// --- Get Content ---

// contentSurface reads which door a content request is coming through.
//
// Everything is a download unless the caller says it is the portal drawing the
// library, which is what `preview=1` declares (resource.SurfacePreview). The
// declaration only names the reason for the read: the audit row is written
// either way, under the caller's own identity.
func contentSurface(r *http.Request) string {
	if r.URL.Query().Get("preview") == "1" {
		return SurfacePreview
	}
	return SurfaceDownload
}

// handleGetContent handles GET /api/v1/resources/{id}/content.
//
// @Summary      Download resource content
// @Description  Download the binary content of a managed resource.
// @Tags         Resources
// @Produce      octet-stream
// @Param        id       path   string  true   "Resource ID"
// @Param        preview  query  string  false  "Set to 1 when the portal is rendering the library's own image tile: the read is audited as portal_preview and does not stamp the resource's last-read time"
// @Success      200  {file}  binary
// @Failure      401  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Failure      503  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id}/content [get]
func (h *Handler) handleGetContent(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "blob storage not configured")
		return
	}

	body, contentType, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, res.S3Key)
	if err != nil {
		slog.Error("resource content: s3 get failed", msgError, err) //nolint:gosec // structured slog
		if IsObjectNotFound(err) {
			writeError(w, http.StatusNotFound, msgContentMissing)
			return
		}
		writeError(w, http.StatusInternalServerError, msgContentUnavailable)
		return
	}

	// Recorded before serving so the audit row exists whether or not the client
	// finishes the download. The bytes are already in memory at this point, so
	// the record sits between the object read and the response rather than in
	// front of the whole request.
	h.recordRead(r.Context(), res, claims, contentSurface(r), 0)

	blobserve.Serve(w, r, blobserve.Options{
		Name:        res.Filename,
		ContentType: cmp.Or(contentType, res.MIMEType),
		ModTime:     res.UpdatedAt,
		Data:        body,
	})
}

// --- Update ---

// validateUpdate checks that all provided fields in the update are valid.
func validateUpdate(u Update) error {
	if u.DisplayName != nil {
		if err := ValidateDisplayName(*u.DisplayName); err != nil {
			return err
		}
	}
	if u.Description != nil {
		if err := ValidateDescription(*u.Description); err != nil {
			return err
		}
	}
	if u.Tags != nil {
		if err := ValidateTags(u.Tags); err != nil {
			return err
		}
	}
	if u.Path != nil {
		if err := ValidatePath(*u.Path); err != nil {
			return err
		}
	}
	return nil
}

// handleUpdate handles PATCH /api/v1/resources/{id}.
//
// @Summary      Update resource
// @Description  Update mutable metadata fields of a managed resource, and/or refile it by naming a target scope, a target folder path, or both.
// @Tags         Resources
// @Accept       json
// @Produce      json
// @Param        id   path  string           true  "Resource ID"
// @Param        body body  resource.Update   true  "Fields to update"
// @Success      200  {object}  resource.Resource
// @Failure      400  {object}  resource.errorResponse
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      409  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id} [patch]
func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	// The same resolve-and-authorize prologue the delete route runs: the caller
	// must be able to see the resource before they are told whether they may
	// change it, or a refusal tells a stranger the id exists.
	res, claims, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	id := res.ID

	var u Update
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateUpdate(u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The move runs first and its old URI is captured before it does. A metadata
	// edit applied first would be committed and then possibly stranded by a
	// refused move, and the caller would have to guess which half took.
	vacated, ok := h.applyMove(w, r, claims, res, u)
	if !ok {
		return
	}
	if !h.applyFields(w, r, id, u) {
		return
	}

	updated, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading updated resource")
		return
	}
	writeJSON(w, http.StatusOK, updated)
	// A moved resource is registered with MCP under a new URI, so the address it
	// left has to be withdrawn or clients keep listing a resource that is no
	// longer there -- the registry is keyed on the URI, not on the id.
	if vacated != "" {
		h.notifyDelete(vacated)
	}
	h.notifyCreate(updated)
}

// applyFields writes the metadata half of a PATCH. A request that carries only
// a move has no fields, and writing an empty Update would still bump updated_at
// and drop the stored embedding for nothing.
func (h *Handler) applyFields(w http.ResponseWriter, r *http.Request, id string, u Update) bool {
	if !u.Fields() {
		return true
	}
	if err := h.deps.Store.Update(r.Context(), id, u); err != nil {
		slog.Error("resource update failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "updating resource")
		return false
	}
	return true
}

// applyMove performs the relocation half of a PATCH, if the body carried one,
// and returns the URI the resource vacated ("" when it did not move). It writes
// the error response and returns ok=false on failure, so the caller stops.
//
// A library and a folder named in the same request are one relocation, not two:
// they are the two halves of one URI, so the resource takes one new address,
// records one alias for the one it left, and produces one audit event (#1528).
// Whichever half the request does not name keeps the value the row already has.
func (h *Handler) applyMove(w http.ResponseWriter, r *http.Request, claims *Claims, res *Resource, u Update) (string, bool) {
	if !u.Relocates() {
		return "", true
	}
	to := Destination{Scope: res.Scope, ScopeID: res.ScopeID, Path: res.Path}
	if u.Scope != nil {
		to.Scope = *u.Scope
		// A scope id on its own names no library, so it is read only alongside a
		// scope -- and an omitted one under a named scope is the global library,
		// which has none.
		to.ScopeID = ""
		if u.ScopeID != nil {
			to.ScopeID = *u.ScopeID
		}
	}
	if u.Path != nil {
		to.Path = *u.Path
	}
	// Read before the move for the reason MoveResource snapshots its own: the
	// resource this holds may be the row the store rewrites.
	vacated := res.URI
	uri, err := MoveResource(r.Context(), h.deps, claims, res, to)
	switch {
	case err == nil:
	case errors.Is(err, ErrMoveForbidden):
		writeError(w, http.StatusForbidden, err.Error())
		return "", false
	case IsMoveConflict(err):
		writeError(w, http.StatusConflict, err.Error())
		return "", false
	case IsInvalidScope(err), IsInvalidPath(err):
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	default:
		slog.Error("resource move failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "moving resource")
		return "", false
	}
	if uri == "" {
		return "", true
	}
	return vacated, true
}

// --- Delete ---

// handleDelete handles DELETE /api/v1/resources/{id}.
//
// @Summary      Delete resource
// @Description  Delete a managed resource, removing both the S3 blob and database metadata.
// @Tags         Resources
// @Param        id   path  string  true  "Resource ID"
// @Success      204  "No Content"
// @Failure      401  {object}  resource.errorResponse
// @Failure      403  {object}  resource.errorResponse
// @Failure      404  {object}  resource.errorResponse
// @Failure      500  {object}  resource.errorResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /resources/{id} [delete]
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	res, claims, ok := h.resolveReadable(w, r)
	if !ok {
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	id := res.ID

	// Delete the blobs first — the head's failure fails the request, to avoid
	// leaving a live object no row points at.
	if err := h.deleteAllBlobs(r.Context(), res); err != nil {
		slog.Error("resource delete: s3 delete failed", msgError, err) //nolint:gosec // structured slog
		writeError(w, http.StatusInternalServerError, "deleting resource blob")
		return
	}

	// The version rows go with the resource row (ON DELETE CASCADE).
	if err := h.deps.Store.Delete(r.Context(), id); err != nil {
		slog.Error("resource delete failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "deleting resource")
		return
	}

	w.WriteHeader(http.StatusNoContent)
	h.notifyDelete(res.URI)
	h.notifyDeleteID(r.Context(), id)
}

// conflictError signals a 409 Conflict (e.g. duplicate URI).
type conflictError struct{ msg string }

func (e *conflictError) Error() string { return e.msg }

// storageError is a write the blob store would not take.
//
// It is separated from the generic 500 because the cause is outside the
// platform and the outcome is specific: nothing was created, so retrying is
// the right response rather than first checking whether a half-made resource
// exists. It answers 503 for the same reason.
//
// The message carries no colon on purpose. writeError truncates a 5xx body at
// the first one so an internal chain cannot leak, which is what reduced this
// failure to the bare fragment "storing file" — a storage outage that read, to
// the person who hit it, as having been refused permission.
type storageError struct{ msg string }

func (e *storageError) Error() string { return e.msg }

// msgStorageRefused is what a caller is told when blob storage rejects a write.
// It states the outcome (nothing saved) rather than the mechanism, which is in
// the log beside it.
const msgStorageRefused = "The storage backend did not accept the file. Nothing was saved. " +
	"Try again, and tell an administrator if it keeps failing."

// --- HTTP helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", msgError, err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	// Sanitize: never leak internal details.
	if status >= http.StatusInternalServerError {
		msg = strings.SplitN(msg, ":", 2)[0]
	}
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{msgError: msg})
}
