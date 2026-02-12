package admin

import (
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

const (
	paramStartTime = "start_time"
	paramEndTime   = "end_time"
)

// registerAuditMetricsRoutes registers audit metrics endpoints.
func (h *Handler) registerAuditMetricsRoutes() {
	if h.deps.AuditMetricsQuerier == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/audit/metrics/timeseries", h.getAuditTimeseries)
	h.mux.HandleFunc("GET /api/v1/admin/audit/metrics/breakdown", h.getAuditBreakdown)
	h.mux.HandleFunc("GET /api/v1/admin/audit/metrics/overview", h.getAuditOverview)
	h.mux.HandleFunc("GET /api/v1/admin/audit/metrics/performance", h.getAuditPerformance)
}

// getAuditTimeseries handles GET /api/v1/admin/audit/metrics/timeseries.
//
// @Summary      Get audit timeseries
// @Description  Returns audit event counts bucketed by time resolution.
// @Tags         Audit Metrics
// @Produce      json
// @Param        resolution  query  string  false  "Time bucket resolution: minute, hour, day (default: hour)"
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Success      200  {array}   audit.TimeseriesBucket
// @Failure      400  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/metrics/timeseries [get]
func (h *Handler) getAuditTimeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	resolution := audit.Resolution(q.Get("resolution"))
	if resolution == "" {
		resolution = audit.ResolutionHour
	}
	if !audit.ValidResolutions[resolution] {
		writeError(w, http.StatusBadRequest, "invalid resolution: must be minute, hour, or day")
		return
	}

	filter := audit.TimeseriesFilter{
		Resolution: resolution,
		StartTime:  parseTimeParam(q, paramStartTime),
		EndTime:    parseTimeParam(q, paramEndTime),
	}

	buckets, err := h.deps.AuditMetricsQuerier.Timeseries(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query timeseries")
		return
	}

	writeJSON(w, http.StatusOK, buckets)
}

// getAuditBreakdown handles GET /api/v1/admin/audit/metrics/breakdown.
//
// @Summary      Get audit breakdown
// @Description  Returns audit event counts grouped by a dimension.
// @Tags         Audit Metrics
// @Produce      json
// @Param        group_by    query  string  true   "Dimension: tool_name, user_id, persona, toolkit_kind, connection"
// @Param        limit       query  integer false  "Max entries (default: 10, max: 100)"
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Success      200  {array}   audit.BreakdownEntry
// @Failure      400  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/metrics/breakdown [get]
func (h *Handler) getAuditBreakdown(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	groupBy := audit.BreakdownDimension(q.Get("group_by"))
	if !audit.ValidBreakdownDimensions[groupBy] {
		writeError(w, http.StatusBadRequest,
			"invalid group_by: must be tool_name, user_id, persona, toolkit_kind, or connection")
		return
	}

	var limit int
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	filter := audit.BreakdownFilter{
		GroupBy:   groupBy,
		Limit:     limit,
		StartTime: parseTimeParam(q, paramStartTime),
		EndTime:   parseTimeParam(q, paramEndTime),
	}

	entries, err := h.deps.AuditMetricsQuerier.Breakdown(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query breakdown")
		return
	}

	writeJSON(w, http.StatusOK, entries)
}

// getAuditOverview handles GET /api/v1/admin/audit/metrics/overview.
//
// @Summary      Get audit overview
// @Description  Returns aggregate audit statistics for the given time range.
// @Tags         Audit Metrics
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Success      200  {object}  audit.Overview
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/metrics/overview [get]
func (h *Handler) getAuditOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	overview, err := h.deps.AuditMetricsQuerier.Overview(
		r.Context(),
		parseTimeParam(q, paramStartTime),
		parseTimeParam(q, paramEndTime),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query overview")
		return
	}

	writeJSON(w, http.StatusOK, overview)
}

// getAuditPerformance handles GET /api/v1/admin/audit/metrics/performance.
//
// @Summary      Get audit performance
// @Description  Returns latency percentile statistics for the given time range.
// @Tags         Audit Metrics
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Success      200  {object}  audit.PerformanceStats
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /audit/metrics/performance [get]
func (h *Handler) getAuditPerformance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	perf, err := h.deps.AuditMetricsQuerier.Performance(
		r.Context(),
		parseTimeParam(q, paramStartTime),
		parseTimeParam(q, paramEndTime),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query performance")
		return
	}

	writeJSON(w, http.StatusOK, perf)
}
