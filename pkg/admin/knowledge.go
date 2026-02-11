package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const pathParamID = "id"

// KnowledgeHandler provides admin REST endpoints for knowledge management.
type KnowledgeHandler struct {
	insightStore   knowledge.InsightStore
	changesetStore knowledge.ChangesetStore
	datahubWriter  knowledge.DataHubWriter
}

// NewKnowledgeHandler creates a new knowledge admin handler.
func NewKnowledgeHandler(
	insightStore knowledge.InsightStore,
	changesetStore knowledge.ChangesetStore,
	writer knowledge.DataHubWriter,
) *KnowledgeHandler {
	return &KnowledgeHandler{
		insightStore:   insightStore,
		changesetStore: changesetStore,
		datahubWriter:  writer,
	}
}

// insightListResponse wraps a paginated list of insights.
type insightListResponse struct {
	Data    []knowledge.Insight `json:"data"`
	Total   int                 `json:"total"`
	Page    int                 `json:"page"`
	PerPage int                 `json:"per_page"`
}

// ListInsights handles GET /api/v1/admin/knowledge/insights.
//
// @Summary      List insights
// @Description  Returns paginated insights with optional filtering.
// @Tags         Knowledge
// @Produce      json
// @Param        status       query  string  false  "Filter by status"
// @Param        category     query  string  false  "Filter by category"
// @Param        entity_urn   query  string  false  "Filter by entity URN"
// @Param        captured_by  query  string  false  "Filter by capturer"
// @Param        confidence   query  string  false  "Filter by confidence level"
// @Param        since        query  string  false  "Insights after this time (RFC 3339)"
// @Param        until        query  string  false  "Insights before this time (RFC 3339)"
// @Param        page         query  integer false  "Page number, 1-based (default: 1)"
// @Param        per_page     query  integer false  "Results per page (default: 20)"
// @Success      200  {object}  insightListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/insights [get]
func (h *KnowledgeHandler) ListInsights(w http.ResponseWriter, r *http.Request) {
	filter := parseInsightFilter(r)
	insights, total, err := h.insightStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if insights == nil {
		insights = []knowledge.Insight{}
	}

	writeJSON(w, http.StatusOK, insightListResponse{
		Data:    insights,
		Total:   total,
		Page:    filter.Offset/filter.EffectiveLimit() + 1,
		PerPage: filter.EffectiveLimit(),
	})
}

// GetInsight handles GET /api/v1/admin/knowledge/insights/{id}.
//
// @Summary      Get insight
// @Description  Returns a single insight by ID.
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Insight ID"
// @Success      200  {object}  knowledge.Insight
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/insights/{id} [get]
func (h *KnowledgeHandler) GetInsight(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)
	insight, err := h.insightStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "insight not found")
		return
	}
	writeJSON(w, http.StatusOK, insight)
}

// statusUpdateRequest represents the body of PUT /insights/:id/status.
type statusUpdateRequest struct {
	Status      string `json:"status"`
	ReviewNotes string `json:"review_notes"`
}

// UpdateInsightStatus handles PUT /api/v1/admin/knowledge/insights/{id}/status.
//
// @Summary      Update insight status
// @Description  Approve or reject an insight. Status must be 'approved' or 'rejected'.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        id    path  string               true  "Insight ID"
// @Param        body  body  statusUpdateRequest   true  "Status update"
// @Success      200  {object}  statusResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/insights/{id}/status [put]
func (h *KnowledgeHandler) UpdateInsightStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)

	var req statusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate target status
	if req.Status != knowledge.StatusApproved && req.Status != knowledge.StatusRejected {
		writeError(w, http.StatusBadRequest, "status must be 'approved' or 'rejected'")
		return
	}

	// Get current insight to validate transition
	insight, err := h.insightStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "insight not found")
		return
	}

	if err := knowledge.ValidateStatusTransition(insight.Status, req.Status); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	reviewedBy := ""
	if user := GetUser(r.Context()); user != nil {
		reviewedBy = user.UserID
	}

	if err := h.insightStore.UpdateStatus(r.Context(), id, req.Status, reviewedBy, req.ReviewNotes); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// insightUpdateRequest represents the body of PUT /insights/:id.
type insightUpdateRequest struct {
	InsightText string `json:"insight_text,omitempty"`
	Category    string `json:"category,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// UpdateInsight handles PUT /api/v1/admin/knowledge/insights/{id}.
//
// @Summary      Update insight
// @Description  Update insight text, category, or confidence. Cannot edit an applied insight.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        id    path  string                true  "Insight ID"
// @Param        body  body  insightUpdateRequest  true  "Fields to update"
// @Success      200  {object}  statusResponse
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/insights/{id} [put]
func (h *KnowledgeHandler) UpdateInsight(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)

	var req insightUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Check if insight is already applied
	insight, err := h.insightStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "insight not found")
		return
	}
	if insight.Status == knowledge.StatusApplied {
		writeError(w, http.StatusConflict, "cannot edit an applied insight")
		return
	}

	updates := knowledge.InsightUpdate{
		InsightText: req.InsightText,
		Category:    req.Category,
		Confidence:  req.Confidence,
	}
	if err := h.insightStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// GetStats handles GET /api/v1/admin/knowledge/insights/stats.
//
// @Summary      Get insight stats
// @Description  Returns aggregated insight statistics by entity, category, confidence, and status.
// @Tags         Knowledge
// @Produce      json
// @Param        status       query  string  false  "Filter by status"
// @Param        category     query  string  false  "Filter by category"
// @Param        entity_urn   query  string  false  "Filter by entity URN"
// @Param        captured_by  query  string  false  "Filter by capturer"
// @Param        confidence   query  string  false  "Filter by confidence level"
// @Param        since        query  string  false  "Insights after this time (RFC 3339)"
// @Param        until        query  string  false  "Insights before this time (RFC 3339)"
// @Success      200  {object}  knowledge.InsightStats
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/insights/stats [get]
func (h *KnowledgeHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	filter := parseInsightFilter(r)
	stats, err := h.insightStore.Stats(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// changesetListResponse wraps a paginated list of changesets.
type changesetListResponse struct {
	Data    []knowledge.Changeset `json:"data"`
	Total   int                   `json:"total"`
	Page    int                   `json:"page"`
	PerPage int                   `json:"per_page"`
}

// ListChangesets handles GET /api/v1/admin/knowledge/changesets.
//
// @Summary      List changesets
// @Description  Returns paginated changesets with optional filtering.
// @Tags         Knowledge
// @Produce      json
// @Param        entity_urn   query  string  false  "Filter by entity URN"
// @Param        applied_by   query  string  false  "Filter by applier"
// @Param        rolled_back  query  boolean false  "Filter by rollback state"
// @Param        since        query  string  false  "Changesets after this time (RFC 3339)"
// @Param        until        query  string  false  "Changesets before this time (RFC 3339)"
// @Param        page         query  integer false  "Page number, 1-based (default: 1)"
// @Param        per_page     query  integer false  "Results per page (default: 20)"
// @Success      200  {object}  changesetListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/changesets [get]
func (h *KnowledgeHandler) ListChangesets(w http.ResponseWriter, r *http.Request) {
	filter := parseChangesetFilter(r)
	changesets, total, err := h.changesetStore.ListChangesets(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if changesets == nil {
		changesets = []knowledge.Changeset{}
	}

	writeJSON(w, http.StatusOK, changesetListResponse{
		Data:    changesets,
		Total:   total,
		Page:    filter.Offset/filter.EffectiveLimit() + 1,
		PerPage: filter.EffectiveLimit(),
	})
}

// GetChangeset handles GET /api/v1/admin/knowledge/changesets/{id}.
//
// @Summary      Get changeset
// @Description  Returns a single changeset by ID.
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Changeset ID"
// @Success      200  {object}  knowledge.Changeset
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/changesets/{id} [get]
func (h *KnowledgeHandler) GetChangeset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)
	cs, err := h.changesetStore.GetChangeset(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "changeset not found")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// RollbackChangeset handles POST /api/v1/admin/knowledge/changesets/{id}/rollback.
//
// @Summary      Rollback changeset
// @Description  Rolls back a changeset, restoring previous values to DataHub.
// @Tags         Knowledge
// @Produce      json
// @Param        id  path  string  true  "Changeset ID"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /knowledge/changesets/{id}/rollback [post]
func (h *KnowledgeHandler) RollbackChangeset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)

	// Get changeset to check state and get previous values
	cs, err := h.changesetStore.GetChangeset(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "changeset not found")
		return
	}
	if cs.RolledBack {
		writeError(w, http.StatusConflict, "changeset already rolled back")
		return
	}

	// Write previous values back to DataHub
	if h.datahubWriter != nil {
		if desc, ok := cs.PreviousValue["description"].(string); ok && desc != "" {
			if err := h.datahubWriter.UpdateDescription(r.Context(), cs.TargetURN, desc); err != nil {
				writeError(w, http.StatusInternalServerError, "rollback failed: "+err.Error())
				return
			}
		}
	}

	rolledBackBy := ""
	if user := GetUser(r.Context()); user != nil {
		rolledBackBy = user.UserID
	}

	if err := h.changesetStore.RollbackChangeset(r.Context(), id, rolledBackBy); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "rolled_back"})
}

// parseTimeParam parses an RFC3339 time from a query parameter.
func parseTimeParam(q url.Values, key string) *time.Time {
	v := q.Get(key)
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

// parsePageOffset parses the page query parameter and computes offset using the given effective limit.
func parsePageOffset(q url.Values, effectiveLimit int) int {
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return (n - 1) * effectiveLimit
		}
	}
	return 0
}

// parseLimit parses the per_page query parameter into a limit value.
func parseLimit(q url.Values) int {
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// parseInsightFilter parses query parameters into an InsightFilter.
func parseInsightFilter(r *http.Request) knowledge.InsightFilter {
	q := r.URL.Query()
	filter := knowledge.InsightFilter{
		Status:     q.Get("status"),
		Category:   q.Get("category"),
		EntityURN:  q.Get("entity_urn"),
		CapturedBy: q.Get("captured_by"),
		Confidence: q.Get("confidence"),
		Since:      parseTimeParam(q, "since"),
		Until:      parseTimeParam(q, "until"),
		Limit:      parseLimit(q),
	}
	filter.Offset = parsePageOffset(q, filter.EffectiveLimit())

	return filter
}

// parseChangesetFilter parses query parameters into a ChangesetFilter.
func parseChangesetFilter(r *http.Request) knowledge.ChangesetFilter {
	q := r.URL.Query()
	filter := knowledge.ChangesetFilter{
		EntityURN: q.Get("entity_urn"),
		AppliedBy: q.Get("applied_by"),
		Since:     parseTimeParam(q, "since"),
		Until:     parseTimeParam(q, "until"),
		Limit:     parseLimit(q),
	}

	if v := q.Get("rolled_back"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			filter.RolledBack = &b
		}
	}
	filter.Offset = parsePageOffset(q, filter.EffectiveLimit())

	return filter
}
