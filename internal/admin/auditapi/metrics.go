package auditapi

import (
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

const (
	paramStartTime = "start_time"
	paramEndTime   = "end_time"
	paramEventKind = "event_kind"
)

// registerAuditMetricsRoutes registers audit metrics endpoints.
func (h *handler) registerAuditMetricsRoutes(mux *http.ServeMux) {
	if h.cfg.Metrics == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/admin/audit/metrics/timeseries", h.getAuditTimeseries)
	mux.HandleFunc("GET /api/v1/admin/audit/metrics/breakdown", h.getAuditBreakdown)
	mux.HandleFunc("GET /api/v1/admin/audit/metrics/overview", h.getAuditOverview)
	mux.HandleFunc("GET /api/v1/admin/audit/metrics/performance", h.getAuditPerformance)
	mux.HandleFunc("GET /api/v1/admin/audit/metrics/enrichment", h.getAuditEnrichment)
	mux.HandleFunc("GET /api/v1/admin/audit/metrics/discovery", h.getAuditDiscovery)
}

// getAuditTimeseries handles GET /api/v1/admin/audit/metrics/timeseries.
//
// @Summary      Get audit timeseries
// @Description  Returns audit event counts bucketed by time resolution.
// @Tags         Audit
// @Produce      json
// @Param        resolution  query  string  false  "Time bucket resolution: minute, hour, day (default: hour)"
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Param        event_kind  query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Success      200  {array}   audit.TimeseriesBucket
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/metrics/timeseries [get]
func (h *handler) getAuditTimeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	resolution := audit.Resolution(q.Get("resolution"))
	if resolution == "" {
		resolution = audit.ResolutionHour
	}
	if !audit.ValidResolutions[resolution] {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid resolution: must be minute, hour, or day")
		return
	}

	filter := audit.TimeseriesFilter{
		Resolution: resolution,
		StartTime:  httpjson.ParseTimeParam(q, paramStartTime),
		EndTime:    httpjson.ParseTimeParam(q, paramEndTime),
		EventKind:  q.Get(paramEventKind),
	}

	buckets, err := h.cfg.Metrics.Timeseries(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query timeseries")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, buckets)
}

// getAuditBreakdown handles GET /api/v1/admin/audit/metrics/breakdown.
//
// @Summary      Get audit breakdown
// @Description  Returns audit event counts grouped by a dimension.
// @Tags         Audit
// @Produce      json
// @Param        group_by    query  string  true   "Dimension: tool_name, user_id, persona, toolkit_kind, connection"
// @Param        limit       query  integer false  "Max entries (default: 10, max: 100)"
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Param        event_kind  query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Success      200  {array}   audit.BreakdownEntry
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/metrics/breakdown [get]
func (h *handler) getAuditBreakdown(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	groupBy := audit.BreakdownDimension(q.Get("group_by"))
	if !audit.ValidBreakdownDimensions[groupBy] {
		httpjson.WriteError(w, http.StatusBadRequest,
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
		StartTime: httpjson.ParseTimeParam(q, paramStartTime),
		EndTime:   httpjson.ParseTimeParam(q, paramEndTime),
		EventKind: q.Get(paramEventKind),
	}

	entries, err := h.cfg.Metrics.Breakdown(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query breakdown")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, entries)
}

// getAuditOverview handles GET /api/v1/admin/audit/metrics/overview.
//
// @Summary      Get audit overview
// @Description  Returns aggregate audit statistics for the given time range.
// @Tags         Audit
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Param        event_kind  query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Success      200  {object}  audit.Overview
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/metrics/overview [get]
func (h *handler) getAuditOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	overview, err := h.cfg.Metrics.Overview(
		r.Context(),
		audit.MetricsFilter{
			StartTime: httpjson.ParseTimeParam(q, paramStartTime),
			EndTime:   httpjson.ParseTimeParam(q, paramEndTime),
			EventKind: q.Get(paramEventKind),
		},
	)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query overview")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, overview)
}

// getAuditPerformance handles GET /api/v1/admin/audit/metrics/performance.
//
// @Summary      Get audit performance
// @Description  Returns latency percentile statistics for the given time range.
// @Tags         Audit
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Param        event_kind  query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Success      200  {object}  audit.PerformanceStats
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/metrics/performance [get]
func (h *handler) getAuditPerformance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	perf, err := h.cfg.Metrics.Performance(
		r.Context(),
		audit.MetricsFilter{
			StartTime: httpjson.ParseTimeParam(q, paramStartTime),
			EndTime:   httpjson.ParseTimeParam(q, paramEndTime),
			EventKind: q.Get(paramEventKind),
		},
	)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query performance")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, perf)
}

// getAuditEnrichment handles GET /api/v1/admin/audit/metrics/enrichment.
//
// @Summary      Get enrichment metrics
// @Description  Returns aggregate enrichment statistics including mode breakdown and token savings.
// @Tags         Audit
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Param        event_kind  query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Success      200  {object}  audit.EnrichmentStats
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/metrics/enrichment [get]
func (h *handler) getAuditEnrichment(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	stats, err := h.cfg.Metrics.Enrichment(
		r.Context(),
		audit.MetricsFilter{
			StartTime: httpjson.ParseTimeParam(q, paramStartTime),
			EndTime:   httpjson.ParseTimeParam(q, paramEndTime),
			EventKind: q.Get(paramEventKind),
		},
	)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query enrichment metrics")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, stats)
}

// getAuditDiscovery handles GET /api/v1/admin/audit/metrics/discovery.
//
// @Summary      Get discovery pattern metrics
// @Description  Returns discovery-before-query session pattern statistics.
// @Tags         Audit
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Param        event_kind  query  string  false  "Filter by event kind (mcp_tool_call, apigateway_invoke)"
// @Success      200  {object}  audit.DiscoveryStats
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/audit/metrics/discovery [get]
func (h *handler) getAuditDiscovery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	stats, err := h.cfg.Metrics.Discovery(
		r.Context(),
		audit.MetricsFilter{
			StartTime: httpjson.ParseTimeParam(q, paramStartTime),
			EndTime:   httpjson.ParseTimeParam(q, paramEndTime),
			EventKind: q.Get(paramEventKind),
		},
	)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to query discovery metrics")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, stats)
}
