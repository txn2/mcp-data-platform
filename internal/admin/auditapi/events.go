package auditapi

import (
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// auditEventResponse wraps a paginated list of audit events.
type auditEventResponse struct {
	Data    []audit.Event `json:"data"`
	Total   int           `json:"total" example:"196"`
	Page    int           `json:"page" example:"1"`
	PerPage int           `json:"per_page" example:"50"`
}

// auditFiltersResponse holds unique values for dropdown filters.
type auditFiltersResponse struct {
	Users        []string          `json:"users" example:"marcus.johnson@example.com,lisa.chang@example.com"`
	Tools        []string          `json:"tools" example:"trino_query,datahub_search,s3_list_objects"`
	ToolkitKinds []string          `json:"toolkit_kinds" example:"api,datahub,trino,s3,memory"`
	Sources      []string          `json:"sources" example:"mcp,rest,admin"`
	EventKinds   []string          `json:"event_kinds" example:"mcp_tool_call,apigateway_invoke"`
	UserLabels   map[string]string `json:"user_labels,omitempty"`
}

// auditStatsResponse holds aggregate audit statistics.
type auditStatsResponse struct {
	Total    int `json:"total" example:"1500"`
	Success  int `json:"success" example:"1423"`
	Failures int `json:"failures" example:"77"`
}

const (
	defaultAuditLimit = 50
	colUserID         = "user_id"
	colToolName       = "tool_name"
	colToolkitKind    = "toolkit_kind"
	colSource         = "source"
	colEventKind      = "event_kind"
)

// listAuditEvents handles GET /api/v1/admin/audit/events.
//
// @Summary      List audit events
// @Description  Returns paginated audit events with optional filtering.
// @Tags         Audit
// @Produce      json
// @Param        user_id       query  string  false  "Filter by user ID"
// @Param        tool_name     query  string  false  "Filter by tool name"
// @Param        toolkit_kind  query  string  false  "Filter by toolkit kind (e.g. api, trino, datahub, s3, memory)"
// @Param        source        query  string  false  "Filter by event source (e.g. mcp)"
// @Param        event_kind    query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Param        session_id    query  string  false  "Filter by MCP session ID"
// @Param        success       query  boolean false  "Filter by success/failure"
// @Param        start_time    query  string  false  "Events after this time (RFC 3339)"
// @Param        end_time      query  string  false  "Events before this time (RFC 3339)"
// @Param        sort_by       query  string  false  "Sort column (default: timestamp)"
// @Param        sort_order    query  string  false  "Sort direction: asc, desc (default: desc)"
// @Param        page          query  integer false  "Page number, 1-based (default: 1)"
// @Param        per_page      query  integer false  "Results per page (default: 50)"
// @Success      200  {object}  auditEventResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/events [get]
func (h *handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := audit.QueryFilter{
		UserID:      q.Get(colUserID),
		ToolName:    q.Get(colToolName),
		ToolkitKind: q.Get(colToolkitKind),
		Source:      q.Get(colSource),
		EventKind:   q.Get(colEventKind),
		SessionID:   q.Get("session_id"),
		Search:      q.Get("search"),
		SortBy:      q.Get("sort_by"),
		StartTime:   httpjson.ParseTimeParam(q, "start_time"),
		EndTime:     httpjson.ParseTimeParam(q, "end_time"),
	}

	if order := audit.SortOrder(q.Get("sort_order")); order == audit.SortAsc || order == audit.SortDesc {
		filter.SortOrder = order
	}

	if v := q.Get("success"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			filter.Success = &b
		}
	}

	filter.Limit = httpjson.ParseLimit(q)
	if filter.Limit <= 0 {
		filter.Limit = defaultAuditLimit
	}
	effectiveLimit := filter.Limit
	filter.Offset = httpjson.ParsePageOffset(q, effectiveLimit)

	events, err := h.cfg.Events.Query(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query audit events")
		return
	}

	// Count without limit/offset for total
	countFilter := filter
	countFilter.Limit = 0
	countFilter.Offset = 0
	total, err := h.cfg.Events.Count(r.Context(), countFilter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count audit events")
		return
	}

	if events == nil {
		events = []audit.Event{}
	}

	page := filter.Offset/effectiveLimit + 1
	httpjson.WriteJSON(w, http.StatusOK, auditEventResponse{
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
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/events/filters [get]
func (h *handler) listAuditEventFilters(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	startTime := httpjson.ParseTimeParam(q, "start_time")
	endTime := httpjson.ParseTimeParam(q, "end_time")

	// One distinct query per filter dropdown, in this order: the first
	// failure decides the response, so the messages stay tied to their
	// column.
	var resp auditFiltersResponse
	columns := []struct {
		column string
		errMsg string
		dest   *[]string
	}{
		{colUserID, "failed to query distinct users", &resp.Users},
		{colToolName, "failed to query distinct tools", &resp.Tools},
		{colToolkitKind, "failed to query distinct toolkit kinds", &resp.ToolkitKinds},
		{colSource, "failed to query distinct sources", &resp.Sources},
		{colEventKind, "failed to query distinct event kinds", &resp.EventKinds},
	}
	for _, c := range columns {
		values, err := h.cfg.Events.Distinct(r.Context(), c.column, startTime, endTime)
		if err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, c.errMsg)
			return
		}
		// A nil slice marshals to null; the dropdowns expect an empty array.
		if values == nil {
			values = []string{}
		}
		*c.dest = values
	}

	// Fetch user_id → user_email mapping for display labels. Non-fatal:
	// labels are optional.
	if labels, err := h.cfg.Events.DistinctPairs(r.Context(), colUserID, "user_email", startTime, endTime); err == nil {
		resp.UserLabels = labels
	}

	httpjson.WriteJSON(w, http.StatusOK, resp)
}

// getAuditEvent handles GET /api/v1/admin/audit/events/{id}.
//
// @Summary      Get audit event
// @Description  Returns a single audit event by ID.
// @Tags         Audit
// @Produce      json
// @Param        id  path  string  true  "Audit event ID"
// @Success      200  {object}  audit.Event
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/events/{id} [get]
func (h *handler) getAuditEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathParamID)
	filter := audit.QueryFilter{ID: id, Limit: 1}
	events, err := h.cfg.Events.Query(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query audit event")
		return
	}
	if len(events) == 0 {
		httpjson.WriteError(w, http.StatusNotFound, "audit event not found")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, events[0])
}

// getAuditStats handles GET /api/v1/admin/audit/stats.
//
// @Summary      Get audit stats
// @Description  Returns aggregate counts for total, successful, and failed events. Supports time and filter parameters.
// @Tags         Audit
// @Produce      json
// @Param        user_id       query  string  false  "Filter by user ID"
// @Param        tool_name     query  string  false  "Filter by tool name"
// @Param        toolkit_kind  query  string  false  "Filter by toolkit kind"
// @Param        source        query  string  false  "Filter by event source"
// @Param        event_kind    query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Param        start_time    query  string  false  "Events after this time (RFC 3339)"
// @Param        end_time      query  string  false  "Events before this time (RFC 3339)"
// @Success      200  {object}  auditStatsResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/stats [get]
func (h *handler) getAuditStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	baseFilter := audit.QueryFilter{
		UserID:      q.Get(colUserID),
		ToolName:    q.Get(colToolName),
		ToolkitKind: q.Get(colToolkitKind),
		Source:      q.Get(colSource),
		EventKind:   q.Get(colEventKind),
		StartTime:   httpjson.ParseTimeParam(q, "start_time"),
		EndTime:     httpjson.ParseTimeParam(q, "end_time"),
	}

	total, err := h.cfg.Events.Count(r.Context(), baseFilter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count audit events")
		return
	}

	successVal := true
	successFilter := baseFilter
	successFilter.Success = &successVal
	successCount, err := h.cfg.Events.Count(r.Context(), successFilter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count successful events")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, auditStatsResponse{
		Total:    total,
		Success:  successCount,
		Failures: total - successCount,
	})
}
