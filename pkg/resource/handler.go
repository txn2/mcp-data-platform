package resource

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// MaxMultipartMemory is the max memory for multipart form parsing (10 MB).
const MaxMultipartMemory = 10 << 20

// Deps holds the dependencies for the resource HTTP handler.
type Deps struct {
	Store     Store
	S3Client  S3Client
	S3Bucket  string
	URIScheme string // defaults to "mcp" if empty
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

// --- Create ---

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := r.ParseMultipartForm(MaxMultipartMemory); err != nil {
		slog.Warn("resource upload: multipart parse failed",
			"error", err,
			"content_type", r.Header.Get("Content-Type"),
		)
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}

	scope := Scope(r.FormValue("scope"))
	scopeID := r.FormValue("scope_id")
	category := r.FormValue("category")
	displayName := r.FormValue("display_name")
	description := r.FormValue("description")
	tagsRaw := r.Form["tags"]

	// Validate all fields.
	if err := ValidateScope(scope, scopeID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateCategory(category); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateDisplayName(displayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateDescription(description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateTags(tagsRaw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Permission check.
	if !CanWriteScope(*claims, scope, scopeID) {
		writeError(w, http.StatusForbidden, "insufficient permissions for scope")
		return
	}

	// Read uploaded file.
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	if header.Size > MaxUploadBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file exceeds %d MB limit", MaxUploadBytes/(1<<20)))
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if err := ValidateMIMEType(mimeType); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	filename, err := SanitizeFilename(header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filename: "+err.Error())
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading file")
		return
	}
	if int64(len(data)) > MaxUploadBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file exceeds %d MB limit", MaxUploadBytes/(1<<20)))
		return
	}

	id, err := GenerateID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generating ID")
		return
	}

	uri := BuildURI(h.uriScheme(), scope, scopeID, category, filename)
	s3Key := BuildS3Key(scope, scopeID, id, filename)

	if tags := tagsRaw; tags == nil {
		tagsRaw = []string{}
	}

	res := Resource{
		ID:            id,
		Scope:         scope,
		ScopeID:       scopeID,
		Category:      category,
		Filename:      filename,
		DisplayName:   displayName,
		Description:   description,
		MIMEType:      mimeType,
		SizeBytes:     int64(len(data)),
		S3Key:         s3Key,
		URI:           uri,
		Tags:          tagsRaw,
		UploaderSub:   claims.Sub,
		UploaderEmail: claims.Email,
	}

	// Upload to S3.
	if h.deps.S3Client != nil {
		if err := h.deps.S3Client.PutObject(r.Context(), h.deps.S3Bucket, s3Key, data, mimeType); err != nil {
			slog.Error("resource upload: s3 put failed", "error", err, "key", s3Key)
			writeError(w, http.StatusInternalServerError, "storing file")
			return
		}
	}

	// Insert metadata.
	if err := h.deps.Store.Insert(r.Context(), res); err != nil {
		slog.Error("resource upload: db insert failed", "error", err)
		writeError(w, http.StatusInternalServerError, "saving resource metadata")
		return
	}

	// Re-read to get timestamps.
	saved, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusCreated, res)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

// --- List ---

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	filter := Filter{
		Scopes:   VisibleScopes(*claims),
		Category: r.URL.Query().Get("category"),
		Tag:      r.URL.Query().Get("tag"),
		Query:    r.URL.Query().Get("q"),
		Limit:    100,
	}

	// Apply caller-requested scope filter only if it narrows visibility.
	if scopeParam := r.URL.Query().Get("scope"); scopeParam != "" {
		scopeIDParam := r.URL.Query().Get("scope_id")
		var narrowed []ScopeFilter
		for _, sf := range filter.Scopes {
			if string(sf.Scope) == scopeParam {
				if scopeIDParam == "" || sf.ScopeID == scopeIDParam {
					narrowed = append(narrowed, sf)
				}
			}
		}
		if len(narrowed) > 0 {
			filter.Scopes = narrowed
		}
	}

	resources, total, err := h.deps.Store.List(r.Context(), filter)
	if err != nil {
		slog.Error("resource list failed", "error", err)
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
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	writeJSON(w, http.StatusOK, res)
}

// --- Get Content ---

func (h *Handler) handleGetContent(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "blob storage not configured")
		return
	}

	body, contentType, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, res.S3Key)
	if err != nil {
		slog.Error("resource content: s3 get failed", "error", err, "key", res.S3Key)
		writeError(w, http.StatusInternalServerError, "retrieving content")
		return
	}

	if contentType == "" {
		contentType = res.MIMEType
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, res.Filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// --- Update ---

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	var u Update
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate provided fields.
	if u.DisplayName != nil {
		if err := ValidateDisplayName(*u.DisplayName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if u.Description != nil {
		if err := ValidateDescription(*u.Description); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if u.Tags != nil {
		if err := ValidateTags(u.Tags); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if u.Category != nil {
		if err := ValidateCategory(*u.Category); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := h.deps.Store.Update(r.Context(), id, u); err != nil {
		slog.Error("resource update failed", "error", err)
		writeError(w, http.StatusInternalServerError, "updating resource")
		return
	}

	updated, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading updated resource")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- Delete ---

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractFn(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	res, err := h.deps.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !CanReadResource(*claims, res) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !CanModifyResource(*claims, res) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	// Delete S3 object.
	if h.deps.S3Client != nil {
		if err := h.deps.S3Client.DeleteObject(r.Context(), h.deps.S3Bucket, res.S3Key); err != nil {
			slog.Error("resource delete: s3 delete failed", "error", err, "key", res.S3Key)
		}
	}

	if err := h.deps.Store.Delete(r.Context(), id); err != nil {
		slog.Error("resource delete failed", "error", err)
		writeError(w, http.StatusInternalServerError, "deleting resource")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- HTTP helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	// Sanitize: never leak internal details.
	if status >= 500 {
		msg = strings.SplitN(msg, ":", 2)[0]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
