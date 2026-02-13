package admin

import (
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// auditEventResponse wraps a paginated list of audit events.
type auditEventResponse struct {
	Data    []audit.Event `json:"data"`
	Total   int           `json:"total"`
	Page    int           `json:"page"`
	PerPage int           `json:"per_page"`
}

// auditFiltersResponse holds unique values for dropdown filters.
type auditFiltersResponse struct {
	Users []string `json:"users"`
	Tools []string `json:"tools"`
}

// auditStatsResponse holds aggregate audit statistics.
type auditStatsResponse struct {
	Total    int `json:"total"`
	Success  int `json:"success"`
	Failures int `json:"failures"`
}

const defaultAuditLimit = 50

// listAuditEvents handles GET /api/v1/admin/audit/events.
//
// @Summary      List audit events
// @Description  Returns paginated audit events with optional filtering.
// @Tags         Audit
// @Produce      json
// @Param        user_id     query  string  false  "Filter by user ID"
// @Param        tool_name   query  string  false  "Filter by tool name"
// @Param        session_id  query  string  false  "Filter by MCP session ID"
// @Param        success     query  boolean false  "Filter by success/failure"
// @Param        start_time  query  string  false  "Events after this time (RFC 3339)"
// @Param        end_time    query  string  false  "Events before this time (RFC 3339)"
// @Param        sort_by     query  string  false  "Sort column (default: timestamp)"
// @Param        sort_order  query  string  false  "Sort direction: asc, desc (default: desc)"
// @Param        page        query  integer false  "Page number, 1-based (default: 1)"
// @Param        per_page    query  integer false  "Results per page (default: 50)"
// @Success      200  {object}  auditEventResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/events [get]
func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := audit.QueryFilter{
		UserID:    q.Get("user_id"),
		ToolName:  q.Get("tool_name"),
		SessionID: q.Get("session_id"),
		Search:    q.Get("search"),
		SortBy:    q.Get("sort_by"),
		StartTime: parseTimeParam(q, "start_time"),
		EndTime:   parseTimeParam(q, "end_time"),
	}

	if order := audit.SortOrder(q.Get("sort_order")); order == audit.SortAsc || order == audit.SortDesc {
		filter.SortOrder = order
	}

	if v := q.Get("success"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			filter.Success = &b
		}
	}

	filter.Limit = parseLimit(q)
	if filter.Limit <= 0 {
		filter.Limit = defaultAuditLimit
	}
	effectiveLimit := filter.Limit
	filter.Offset = parsePageOffset(q, effectiveLimit)

	events, err := h.deps.AuditQuerier.Query(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query audit events")
		return
	}

	// Count without limit/offset for total
	countFilter := filter
	countFilter.Limit = 0
	countFilter.Offset = 0
	total, err := h.deps.AuditQuerier.Count(r.Context(), countFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count audit events")
		return
	}

	if events == nil {
		events = []audit.Event{}
	}

	page := filter.Offset/effectiveLimit + 1
	writeJSON(w, http.StatusOK, auditEventResponse{
		Data:    events,
		Total:   total,
		Page:    page,
		PerPage: effectiveLimit,
	})
}

// listAuditEventFilters handles GET /api/v1/admin/audit/events/filters.
//
// @Summary      Get audit event filter values
// @Description  Returns unique user IDs and tool names seen in the audit log, sorted alphabetically.
// @Tags         Audit
// @Produce      json
// @Param        start_time  query  string  false  "Events after this time (RFC 3339)"
// @Param        end_time    query  string  false  "Events before this time (RFC 3339)"
// @Success      200  {object}  auditFiltersResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/events/filters [get]
func (h *Handler) listAuditEventFilters(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	startTime := parseTimeParam(q, "start_time")
	endTime := parseTimeParam(q, "end_time")

	users, err := h.deps.AuditQuerier.Distinct(r.Context(), "user_id", startTime, endTime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query distinct users")
		return
	}

	tools, err := h.deps.AuditQuerier.Distinct(r.Context(), "tool_name", startTime, endTime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query distinct tools")
		return
	}

	if users == nil {
		users = []string{}
	}
	if tools == nil {
		tools = []string{}
	}

	writeJSON(w, http.StatusOK, auditFiltersResponse{
		Users: users,
		Tools: tools,
	})
}

// getAuditEvent handles GET /api/v1/admin/audit/events/{id}.
//
// @Summary      Get audit event
// @Description  Returns a single audit event by ID.
// @Tags         Audit
// @Produce      json
// @Param        id  path  string  true  "Audit event ID"
// @Success      200  {object}  audit.Event
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/events/{id} [get]
func (h *Handler) getAuditEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)
	filter := audit.QueryFilter{ID: id, Limit: 1}
	events, err := h.deps.AuditQuerier.Query(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query audit event")
		return
	}
	if len(events) == 0 {
		writeError(w, http.StatusNotFound, "audit event not found")
		return
	}
	writeJSON(w, http.StatusOK, events[0])
}

// getAuditStats handles GET /api/v1/admin/audit/stats.
//
// @Summary      Get audit stats
// @Description  Returns aggregate counts for total, successful, and failed events. Supports time and filter parameters.
// @Tags         Audit
// @Produce      json
// @Param        user_id     query  string  false  "Filter by user ID"
// @Param        tool_name   query  string  false  "Filter by tool name"
// @Param        start_time  query  string  false  "Events after this time (RFC 3339)"
// @Param        end_time    query  string  false  "Events before this time (RFC 3339)"
// @Success      200  {object}  auditStatsResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/stats [get]
func (h *Handler) getAuditStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	baseFilter := audit.QueryFilter{
		UserID:    q.Get("user_id"),
		ToolName:  q.Get("tool_name"),
		StartTime: parseTimeParam(q, "start_time"),
		EndTime:   parseTimeParam(q, "end_time"),
	}

	total, err := h.deps.AuditQuerier.Count(r.Context(), baseFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count audit events")
		return
	}

	successVal := true
	successFilter := baseFilter
	successFilter.Success = &successVal
	successCount, err := h.deps.AuditQuerier.Count(r.Context(), successFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count successful events")
		return
	}

	writeJSON(w, http.StatusOK, auditStatsResponse{
		Total:    total,
		Success:  successCount,
		Failures: total - successCount,
	})
}
