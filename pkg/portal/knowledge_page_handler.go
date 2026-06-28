package portal

import (
	"context"
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
	// Source-insight lineage (#678): the insights a page was synthesized from.
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/{id}/lineage", h.knowledgePageLineage)
	// Entity references (#664): the entities a page provides knowledge about.
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/{id}/refs", h.listKnowledgePageRefs)
	h.mux.HandleFunc("PUT /api/v1/portal/knowledge-pages/{id}/refs", h.setKnowledgePageRefs)
	h.mux.HandleFunc("POST /api/v1/portal/knowledge-pages/refs/resolve", h.resolveKnowledgePageRefs)
	h.mux.HandleFunc("GET /api/v1/portal/knowledge-pages/backlinks", h.knowledgePageBacklinks)
}

// knowledgePageRequest is the create/update payload.
type knowledgePageRequest struct {
	Slug          string   `json:"slug,omitempty"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary,omitempty"`
	Body          string   `json:"body,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	ChangeSummary string   `json:"change_summary,omitempty"`
	// ForceNew overrides the create-time duplicate gate (#705): when set, a page
	// whose content is highly similar to an existing one is created anyway instead
	// of being rejected with the candidate pages. Ignored on update.
	ForceNew bool `json:"force_new,omitempty"`
}

// knowledgePageDuplicateResponse is the 409 body the create path returns when the
// dedup gate (#705) blocks a near-duplicate: the candidate pages to consolidate
// against, so the client can re-submit a PUT to a candidate's id (an update) or
// re-POST with force_new.
type knowledgePageDuplicateResponse struct {
	DuplicateBlocked bool                           `json:"duplicate_blocked"`
	Candidates       []knowledgepage.DedupCandidate `json:"candidates"`
	Message          string                         `json:"message"`
}

// knowledgePageListResponse is the paginated list envelope.
type knowledgePageListResponse struct {
	Pages []knowledgepage.Page `json:"pages"`
	Total int                  `json:"total"`
}

// knowledgePageVersionsResponse is the paginated version-history envelope.
type knowledgePageVersionsResponse struct {
	Versions []knowledgepage.Version `json:"versions"`
	Total    int                     `json:"total"`
}

// createKnowledgePage handles POST /api/v1/portal/knowledge-pages (apply_knowledge access).
//
// @Summary      Create a knowledge page
// @Description  Creates a canonical, org-shared knowledge page. Requires apply_knowledge access (the same authorization that applies captured insights).
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        page  body  knowledgePageRequest  true  "Knowledge page content"
// @Success      201  {object}  knowledgepage.Page
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages [post]
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

	// Exact-slug collision: a POST with a slug that already names a live page would
	// otherwise hit the unique-slug index and surface as an opaque 500. Return a
	// clear 409 pointing at the existing page so the caller updates it instead (the
	// MCP apply path consolidates by slug; force_new cannot override a hard slug
	// collision). #705.
	if slug := strings.TrimSpace(req.Slug); slug != "" {
		if existing, err := h.deps.KnowledgePageStore.GetBySlug(r.Context(), slug); err == nil && existing != nil && existing.DeletedAt == nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("a knowledge page with slug %q already exists; update it instead", slug))
			return
		}
	}

	// Create-time duplicate gate (#705), shared with the MCP apply path: block a
	// near-duplicate create and return the candidate pages, unless force_new is set.
	if !req.ForceNew {
		dup, err := h.knowledgePageDuplicates(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check for duplicate knowledge pages")
			return
		}
		if len(dup) > 0 {
			writeJSON(w, http.StatusConflict, knowledgePageDuplicateResponse{
				DuplicateBlocked: true,
				Candidates:       dup,
				Message: "A similar knowledge page already exists. Update an existing page instead of creating a duplicate: " +
					"edit a candidate page, or resubmit with force_new to create a separate page anyway.",
			})
			return
		}
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

// knowledgePageDuplicates runs the create-time dedup probe (#705): it ranks
// existing pages against the candidate's content by pure cosine and returns those
// at or above the configured similarity threshold. It is a no-op (no candidates)
// when the gate is disabled (threshold <= 0), the store cannot rank by cosine (no
// DuplicateProber), or no real embedding provider is configured (the score is not
// thresholdable), so the create proceeds. The shared knowledgepage.NearDuplicatePages
// enforces the same rule the MCP apply path uses, embedding over the same IndexText
// composition, so the two surfaces cannot drift.
func (h *Handler) knowledgePageDuplicates(ctx context.Context, req knowledgePageRequest) ([]knowledgepage.DedupCandidate, error) {
	if h.deps.KnowledgePageDedupThreshold <= 0 {
		return nil, nil
	}
	prober, ok := h.deps.KnowledgePageStore.(knowledgepage.DuplicateProber)
	if !ok {
		return nil, nil
	}
	emb := h.embedSearchQuery(ctx, knowledgepage.IndexText(strings.TrimSpace(req.Title), req.Body, normalizeTags(req.Tags)))
	candidates, err := knowledgepage.NearDuplicatePages(ctx, prober, emb, h.deps.KnowledgePageDedupThreshold)
	if err != nil {
		return nil, fmt.Errorf("ranking knowledge pages for dedup: %w", err)
	}
	return candidates, nil
}

// listKnowledgePages handles GET /api/v1/portal/knowledge-pages (any user).
//
// @Summary      List knowledge pages
// @Description  Returns canonical, org-shared knowledge pages. Open to every authenticated user.
// @Tags         Knowledge
// @Produce      json
// @Param        tag     query  string   false  "Filter by tag"
// @Param        q       query  string   false  "Filter by title/summary text"
// @Param        limit   query  integer  false  "Results per page (default: 20)"
// @Param        offset  query  integer  false  "Pagination offset"
// @Success      200  {object}  knowledgePageListResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages [get]
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
//
// @Summary      Search knowledge pages
// @Description  Ranks knowledge-page content by relevance to the query. Uses semantic ranking when an embedding provider is configured, else degrades to lexical-only. Open to every authenticated user.
// @Tags         Knowledge
// @Produce      json
// @Param        q      query  string   true   "Search query"
// @Param        limit  query  integer  false  "Maximum results (default: 20)"
// @Success      200  {array}   knowledgepage.ScoredPage
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      501  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/search [get]
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
//
// @Summary      Get a knowledge page
// @Description  Returns a single canonical knowledge page by id. Open to every authenticated user.
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Knowledge page id"
// @Success      200  {object}  knowledgepage.Page
// @Failure      401  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/{id} [get]
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
//
// @Summary      Update a knowledge page
// @Description  Replaces a knowledge page's content and records a new version. Requires apply_knowledge access.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        id    path  string                true  "Knowledge page id"
// @Param        page  body  knowledgePageRequest  true  "Knowledge page content"
// @Success      200  {object}  knowledgepage.Page
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/{id} [put]
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
//
// @Summary      Delete a knowledge page
// @Description  Soft-deletes a knowledge page. Requires apply_knowledge access.
// @Tags         Knowledge
// @Param        id  path  string  true  "Knowledge page id"
// @Success      204  "No Content"
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/{id} [delete]
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
//
// @Summary      List a knowledge page's version history
// @Description  Returns the saved versions of a knowledge page, newest first. Open to every authenticated user.
// @Tags         Knowledge
// @Produce      json
// @Param        id      path   string   true   "Knowledge page id"
// @Param        limit   query  integer  false  "Results per page (default: 20)"
// @Param        offset  query  integer  false  "Pagination offset"
// @Success      200  {object}  knowledgePageVersionsResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge-pages/{id}/versions [get]
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
	writeJSON(w, http.StatusOK, knowledgePageVersionsResponse{Versions: versions, Total: total})
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
