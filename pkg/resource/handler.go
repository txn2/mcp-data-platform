package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
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
)

// Deps holds the dependencies for the resource HTTP handler.
type Deps struct {
	Store     Store
	S3Client  S3Client
	S3Bucket  string
	URIScheme string          // defaults to "mcp" if empty
	OnCreate  func(*Resource) // called after successful create to register with MCP
	OnDelete  func(string)    // called after successful delete with URI to unregister
}

// ClaimsExtractor extracts resource Claims from an HTTP request.
// Provided by the platform auth middleware.
type ClaimsExtractor func(r *http.Request) (*Claims, error)

// Handler provides HTTP endpoints for resource CRUD.
type Handler struct {
	mux       *http.ServeMux
	deps      Deps
	extractFn ClaimsExtractor
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
	mux.HandleFunc("GET /api/v1/resources", h.handleList)
	mux.HandleFunc("GET /api/v1/resources/{id}", h.handleGet)
	mux.HandleFunc("GET /api/v1/resources/{id}/content", h.handleGetContent)
	mux.HandleFunc("PATCH /api/v1/resources/{id}", h.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/resources/{id}", h.handleDelete)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) uriScheme() string {
	if h.deps.URIScheme != "" {
		return h.deps.URIScheme
	}
	return DefaultURIScheme
}

// createInput holds the validated fields from a resource creation request.
type createInput struct {
	scope       Scope
	scopeID     string
	category    string
	displayName string
	description string
	tags        []string
}

// validateCreateInput parses and validates the form fields for resource creation.
func validateCreateInput(r *http.Request) (*createInput, error) {
	scope := Scope(r.FormValue("scope"))
	scopeID := r.FormValue("scope_id")
	category := r.FormValue("category")
	displayName := r.FormValue("display_name")
	description := r.FormValue("description")
	tags := r.Form["tags"]

	if err := ValidateScope(scope, scopeID); err != nil {
		return nil, err
	}
	if err := ValidateCategory(category); err != nil {
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
		category:    category,
		displayName: displayName,
		description: description,
		tags:        tags,
	}, nil
}

// uploadedFile holds the contents and metadata of an uploaded file.
type uploadedFile struct {
	data     []byte
	filename string
	mimeType string
}

// readUploadedFile reads and validates the uploaded file from the request.
func readUploadedFile(r *http.Request) (*uploadedFile, error) {
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("file is required")
	}
	defer func() { _ = file.Close() }()

	if header.Size > MaxUploadBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxUploadBytes/(1<<20))
	}

	mimeType := header.Header.Get(headerContentType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if err := ValidateMIMEType(mimeType); err != nil {
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

	return &uploadedFile{
		data:     data,
		filename: filename,
		mimeType: mimeType,
	}, nil
}

// --- Create ---

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
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

	res, err := h.persistResource(r, claims, input, uf)
	if err != nil {
		var ce *conflictError
		if errors.As(err, &ce) {
			writeError(w, http.StatusConflict, ce.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
	h.notifyCreate(res)
}

// persistResource generates an ID, uploads to S3, inserts metadata, and returns the saved resource.
func (h *Handler) persistResource(r *http.Request, claims *Claims, input *createInput, uf *uploadedFile) (*Resource, error) {
	id, err := GenerateID()
	if err != nil {
		return nil, fmt.Errorf("generating ID: %w", err)
	}

	uri := BuildURI(h.uriScheme(), input.scope, input.scopeID, input.category, uf.filename)
	s3Key := BuildS3Key(input.scope, input.scopeID, id, uf.filename)

	res := Resource{
		ID: id, Scope: input.scope, ScopeID: input.scopeID,
		Category: input.category, Filename: uf.filename,
		DisplayName: input.displayName, Description: input.description,
		MIMEType: uf.mimeType, SizeBytes: int64(len(uf.data)),
		S3Key: s3Key, URI: uri, Tags: input.tags,
		UploaderSub: claims.Sub, UploaderEmail: claims.Email,
	}

	if h.deps.S3Client != nil {
		if err := h.deps.S3Client.PutObject(r.Context(), h.deps.S3Bucket, s3Key, uf.data, uf.mimeType); err != nil {
			slog.Error("resource upload: s3 put failed", msgError, err)
			return nil, fmt.Errorf("storing file: %w", err)
		}
	}

	if err := h.deps.Store.Insert(r.Context(), res); err != nil {
		// Clean up orphaned S3 blob.
		if h.deps.S3Client != nil {
			_ = h.deps.S3Client.DeleteObject(r.Context(), h.deps.S3Bucket, s3Key)
		}
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, &conflictError{msg: "a resource with this scope, category, and filename already exists"}
		}
		slog.Error("resource upload: db insert failed", msgError, err)
		return nil, fmt.Errorf("saving resource metadata: %w", err)
	}

	saved, getErr := h.deps.Store.Get(r.Context(), id)
	if getErr != nil {
		now := time.Now().UTC()
		res.CreatedAt = now
		res.UpdatedAt = now
		return &res, nil //nolint:nilerr // fallback to pre-read version if re-fetch fails
	}
	return saved, nil
}

// --- List ---

// narrowScopes filters visible scopes to match the caller-requested scope
// and optional scope ID. If no matches are found the original list is returned.
func narrowScopes(visible []ScopeFilter, scopeParam, scopeIDParam string) []ScopeFilter {
	var narrowed []ScopeFilter
	for _, sf := range visible {
		if string(sf.Scope) == scopeParam {
			if scopeIDParam == "" || sf.ScopeID == scopeIDParam {
				narrowed = append(narrowed, sf)
			}
		}
	}
	return narrowed
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}

	scopes := VisibleScopes(*claims)
	if scopeParam := r.URL.Query().Get("scope"); scopeParam != "" {
		scopes = narrowScopes(scopes, scopeParam, r.URL.Query().Get("scope_id"))
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	filter := Filter{
		Scopes:   scopes,
		Category: r.URL.Query().Get("category"),
		Tag:      r.URL.Query().Get("tag"),
		Query:    r.URL.Query().Get("q"),
		Limit:    DefaultListLimit,
		Offset:   offset,
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

// --- Get ---

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}

	id := r.PathValue(pathParamID)
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}

	writeJSON(w, http.StatusOK, res)
}

// --- Get Content ---

// sanitizeContentType extracts the base media type, discarding parameters.
// Falls back to application/octet-stream for unparseable values.
func sanitizeContentType(ct string) string {
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil || mediaType == "" {
		return "application/octet-stream"
	}
	return mediaType
}

func (h *Handler) handleGetContent(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}

	id := r.PathValue(pathParamID)
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "blob storage not configured")
		return
	}

	body, contentType, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, res.S3Key)
	if err != nil {
		slog.Error("resource content: s3 get failed", msgError, err) //nolint:gosec // structured slog
		writeError(w, http.StatusInternalServerError, "retrieving content")
		return
	}

	if contentType == "" {
		contentType = res.MIMEType
	}
	safeType := sanitizeContentType(contentType)

	disposition := "attachment"
	if strings.HasPrefix(safeType, "text/") {
		disposition = "inline"
	}

	w.Header().Set(headerContentType, safeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, res.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body) // #nosec G705 -- Content-Type is sanitized via mime.ParseMediaType above
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
	if u.Category != nil {
		if err := ValidateCategory(*u.Category); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}

	id := r.PathValue(pathParamID)
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	var u Update
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateUpdate(u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.deps.Store.Update(r.Context(), id, u); err != nil {
		slog.Error("resource update failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "updating resource")
		return
	}

	updated, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading updated resource")
		return
	}
	writeJSON(w, http.StatusOK, updated)
	h.notifyCreate(updated)
}

// --- Delete ---

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, msgUnauthorized)
		return
	}

	id := r.PathValue(pathParamID)
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	// Delete S3 object — fail the request if blob deletion fails to avoid orphaned DB rows.
	if h.deps.S3Client != nil {
		if err := h.deps.S3Client.DeleteObject(r.Context(), h.deps.S3Bucket, res.S3Key); err != nil {
			slog.Error("resource delete: s3 delete failed", msgError, err) //nolint:gosec // structured slog
			writeError(w, http.StatusInternalServerError, "deleting resource blob")
			return
		}
	}

	if err := h.deps.Store.Delete(r.Context(), id); err != nil {
		slog.Error("resource delete failed", msgError, err)
		writeError(w, http.StatusInternalServerError, "deleting resource")
		return
	}

	w.WriteHeader(http.StatusNoContent)
	h.notifyDelete(res.URI)
}

// conflictError signals a 409 Conflict (e.g. duplicate URI).
type conflictError struct{ msg string }

func (e *conflictError) Error() string { return e.msg }

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
