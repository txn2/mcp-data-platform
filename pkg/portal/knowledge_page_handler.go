package portal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// Knowledge page validation bounds and error messages.
const (
	maxKnowledgePageTitleLen   = 200
	maxKnowledgePageSummaryLen = 2000
	maxKnowledgePageBodyLen    = 1 << 20 // 1 MiB of markdown
	maxKnowledgePageSlugLen    = 120
	maxKnowledgePageTags       = 20
	maxKnowledgePageTagLen     = 50

	errKnowledgePageNotFoundMsg = "knowledge page not found"
	errKnowledgePageForbidden   = "editing knowledge requires apply_knowledge access"

	// kpIDParam is the path parameter name for a knowledge page id.
	kpIDParam = "id"
)

// registerKnowledgePageRoutes wires the knowledge-page REST endpoints. Reads are
// open to every authenticated user (pages are org-shared canonical knowledge);
// writes are gated to personas with apply_knowledge access (admin roles), the
// same authorization that lets a persona apply everyone's captured insights.
func (h *Handler) registerKnowledgePageRoutes() {
	if h.deps.KnowledgePageStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages", h.listKnowledgePages)
	if _, ok := h.deps.KnowledgePageStore.(knowledgepage.Searcher); ok {
		h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/search", h.searchKnowledgePages)
	}
	h.mux.HandleFunc("POST /api/v1/portal/knowledge-pages", h.createKnowledgePage)
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/{id}", h.getKnowledgePage)
	h.mux.HandleFunc("PUT /api/v1/portal/knowledge-pages/{id}", h.updateKnowledgePage)
	h.mux.HandleFunc("DELETE /api/v1/portal/knowledge-pages/{id}", h.deleteKnowledgePage)
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/{id}/versions", h.listKnowledgePageVersions)
	// Entity references (#664): the entities a page provides knowledge about.
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/{id}/refs", h.listKnowledgePageRefs)
	h.mux.HandleFunc("PUT /api/v1/portal/knowledge-pages/{id}/refs", h.setKnowledgePageRefs)
	h.mux.HandleFunc("POST /api/v1/portal/knowledge-pages/refs/resolve", h.resolveKnowledgePageRefs)
}

// knowledgePageRequest is the create/update payload.
type knowledgePageRequest struct {
	Slug          string   `json:"slug,omitempty"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary,omitempty"`
	Body          string   `json:"body,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	ChangeSummary string   `json:"change_summary,omitempty"`
}

// knowledgePageListResponse is the paginated list envelope.
type knowledgePageListResponse struct {
	Pages []knowledgepage.Page `json:"pages"`
	Total int                  `json:"total"`
}

// createKnowledgePage handles POST /api/v1/portal/knowledge-pages (apply_knowledge access).
func (h *Handler) createKnowledgePage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	if !h.userHasApplyKnowledge(user) {
		writeError(w, http.StatusForbidden, errKnowledgePageForbidden)
		return
	}

	var req knowledgePageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if errMsg := validateKnowledgePage(req); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	page := knowledgepage.Page{
		ID:           knowledgepage.NewID(),
		Slug:         strings.TrimSpace(req.Slug),
		Title:        strings.TrimSpace(req.Title),
		Summary:      req.Summary,
		Body:         req.Body,
		Tags:         normalizeTags(req.Tags),
		CreatedBy:    user.Email,
		CreatedEmail: user.Email,
	}
	if err := h.deps.KnowledgePageStore.Insert(r.Context(), page); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create knowledge page")
		return
	}
	h.reconcileInlineRefs(r.Context(), page.ID, page.Body)
	created, err := h.deps.KnowledgePageStore.Get(r.Context(), page.ID)
	if err != nil {
		writeJSON(w, http.StatusCreated, page)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// listKnowledgePages handles GET /api/v1/portal/knowledge-pages (any user).
func (h *Handler) listKnowledgePages(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	filter := knowledgepage.Filter{
		Tag:    strings.TrimSpace(r.URL.Query().Get("tag")),
		Query:  strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:  intParam(r, paramLimit, defaultLimit),
		Offset: intParam(r, paramOffset, 0),
	}
	pages, total, err := h.deps.KnowledgePageStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge pages")
		return
	}
	if pages == nil {
		pages = []knowledgepage.Page{}
	}
	writeJSON(w, http.StatusOK, knowledgePageListResponse{Pages: pages, Total: total})
}

// searchKnowledgePages handles GET /api/v1/portal/knowledge-pages/search?q= (any
// user). It ranks page CONTENT; the embedding is computed server-side when a
// provider is configured, else it degrades to lexical-only.
func (h *Handler) searchKnowledgePages(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	searcher, ok := h.deps.KnowledgePageStore.(knowledgepage.Searcher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "knowledge page search not available")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}
	results, err := searcher.Search(r.Context(), knowledgepage.SearchQuery{
		QueryText: query,
		// Reuse the shared embed-for-search decision so the noop/zero-vector
		// guard applies and the portal cannot drift from the agent surfaces.
		Embedding: h.embedSearchQuery(r.Context(), query),
		Limit:     intParam(r, paramLimit, defaultLimit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "knowledge page search failed")
		return
	}
	if results == nil {
		results = []knowledgepage.ScoredPage{}
	}
	writeJSON(w, http.StatusOK, results)
}

// getKnowledgePage handles GET /api/v1/portal/knowledge-pages/{id} (any user).
func (h *Handler) getKnowledgePage(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	page, err := h.deps.KnowledgePageStore.Get(r.Context(), r.PathValue(kpIDParam))
	if errors.Is(err, knowledgepage.ErrNotFound) {
		writeError(w, http.StatusNotFound, errKnowledgePageNotFoundMsg)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get knowledge page")
		return
	}
	if page.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errKnowledgePageNotFoundMsg)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// updateKnowledgePage handles PUT /api/v1/portal/knowledge-pages/{id} (apply_knowledge access).
func (h *Handler) updateKnowledgePage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	if !h.userHasApplyKnowledge(user) {
		writeError(w, http.StatusForbidden, errKnowledgePageForbidden)
		return
	}

	var req knowledgePageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if errMsg := validateKnowledgePage(req); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	title := strings.TrimSpace(req.Title)
	tags := normalizeTags(req.Tags)
	update := knowledgepage.Update{
		Title:         &title,
		Summary:       &req.Summary,
		Body:          &req.Body,
		Tags:          &tags,
		UpdatedBy:     user.Email,
		ChangeSummary: req.ChangeSummary,
	}
	if strings.TrimSpace(req.Slug) != "" {
		slug := strings.TrimSpace(req.Slug)
		update.Slug = &slug
	}
	id := r.PathValue(kpIDParam)
	if err := h.deps.KnowledgePageStore.Update(r.Context(), id, update); err != nil {
		if errors.Is(err, knowledgepage.ErrNotFound) {
			writeError(w, http.StatusNotFound, errKnowledgePageNotFoundMsg)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update knowledge page")
		return
	}
	h.reconcileInlineRefs(r.Context(), id, req.Body)
	updated, err := h.deps.KnowledgePageStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read updated knowledge page")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteKnowledgePage handles DELETE /api/v1/portal/knowledge-pages/{id} (apply_knowledge access).
func (h *Handler) deleteKnowledgePage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	if !h.userHasApplyKnowledge(user) {
		writeError(w, http.StatusForbidden, errKnowledgePageForbidden)
		return
	}
	if err := h.deps.KnowledgePageStore.SoftDelete(r.Context(), r.PathValue(kpIDParam)); err != nil {
		if errors.Is(err, knowledgepage.ErrNotFound) {
			writeError(w, http.StatusNotFound, errKnowledgePageNotFoundMsg)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete knowledge page")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listKnowledgePageVersions handles GET /api/v1/portal/knowledge-pages/{id}/versions (any user).
func (h *Handler) listKnowledgePageVersions(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	versions, total, err := h.deps.KnowledgePageStore.ListVersions(
		r.Context(), r.PathValue(kpIDParam), intParam(r, paramLimit, defaultLimit), intParam(r, paramOffset, 0))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	if versions == nil {
		versions = []knowledgepage.Version{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "total": total})
}

// validateKnowledgePage checks the create/update payload, returning an empty
// string when valid or a user-facing message otherwise.
func validateKnowledgePage(req knowledgePageRequest) string {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return "title is required"
	}
	if utf8.RuneCountInString(title) > maxKnowledgePageTitleLen {
		return fmt.Sprintf("title exceeds %d characters", maxKnowledgePageTitleLen)
	}
	if utf8.RuneCountInString(req.Summary) > maxKnowledgePageSummaryLen {
		return fmt.Sprintf("summary exceeds %d characters", maxKnowledgePageSummaryLen)
	}
	if len(req.Body) > maxKnowledgePageBodyLen {
		return fmt.Sprintf("body exceeds %d bytes", maxKnowledgePageBodyLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Slug)) > maxKnowledgePageSlugLen {
		return fmt.Sprintf("slug exceeds %d characters", maxKnowledgePageSlugLen)
	}
	if len(req.Tags) > maxKnowledgePageTags {
		return fmt.Sprintf("too many tags (max %d)", maxKnowledgePageTags)
	}
	for _, t := range req.Tags {
		if utf8.RuneCountInString(t) > maxKnowledgePageTagLen {
			return fmt.Sprintf("tag %q exceeds %d characters", t, maxKnowledgePageTagLen)
		}
	}
	return ""
}

// normalizeTags trims, drops blanks, and de-duplicates tags, returning a non-nil
// slice so the JSON surface never emits null.
func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
