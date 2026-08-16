package sessionapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
)

const (
	// defaultPerPage is the page size when the caller states none.
	defaultPerPage = 25
	// maxPerPage caps a caller-stated page size. A session list row is an
	// aggregate over an unbounded number of audit rows, so an uncapped
	// page is an uncapped query.
	maxPerPage = 200
)

// sessionListResponse wraps a paginated list of session summaries.
type sessionListResponse struct {
	Data    []sessionview.Summary `json:"data"`
	Total   int                   `json:"total" example:"42"`
	Page    int                   `json:"page" example:"1"`
	PerPage int                   `json:"per_page" example:"25"`
}

// parseFilter reads the query string into a session filter. An unparseable
// value is treated as absent rather than as an error: the failure mode of a
// bad filter is an unfiltered or empty page, never a 400 the UI has to model.
func parseFilter(r *http.Request) sessionview.Filter {
	q := r.URL.Query()
	filter := sessionview.Filter{
		UserID:      q.Get("user_id"),
		Kind:        sessionview.Kind(q.Get("kind")),
		StartTime:   httpjson.ParseTimeParam(q, "start_time"),
		EndTime:     httpjson.ParseTimeParam(q, "end_time"),
		HasAssets:   parseBool(q.Get("has_assets")),
		HasFailures: parseBool(q.Get("has_failures")),
	}
	filter.Limit = clampPerPage(httpjson.ParseLimit(q))
	filter.Offset = httpjson.ParsePageOffset(q, filter.Limit)
	return filter
}

// parseBool reads a boolean flag, treating anything unparseable as unset.
func parseBool(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// clampPerPage bounds a requested page size into [1, maxPerPage].
func clampPerPage(limit int) int {
	switch {
	case limit <= 0:
		return defaultPerPage
	case limit > maxPerPage:
		return maxPerPage
	default:
		return limit
	}
}

// listSessions handles GET /api/v1/admin/sessions.
//
// @Summary      List sessions
// @Description  Returns sessions derived from the audit log, most recently active first. A session is not a stored row: it is every audit event sharing one session id, so the list reaches as far back as audit retention.
// @Tags         Sessions
// @Produce      json
// @Param        user_id       query  string   false  "Filter by user ID"
// @Param        kind          query  string   false  "Filter by session id origin (agent, portal, script, transport)"
// @Param        start_time    query  string   false  "Sessions with activity after this time (RFC 3339)"
// @Param        end_time      query  string   false  "Sessions with activity before this time (RFC 3339)"
// @Param        has_assets    query  boolean  false  "Only sessions that saved at least one asset"
// @Param        has_failures  query  boolean  false  "Only sessions with at least one failed call"
// @Param        page          query  integer  false  "Page number, 1-based (default: 1)"
// @Param        per_page      query  integer  false  "Results per page (default: 25, max: 200)"
// @Success      200  {object}  sessionListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/sessions [get]
func (h *handler) listSessions(w http.ResponseWriter, r *http.Request) {
	filter := parseFilter(r)

	sessions, err := h.cfg.Sessions.List(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	total, err := h.cfg.Sessions.Count(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count sessions")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, sessionListResponse{
		Data:    sessions,
		Total:   total,
		Page:    filter.Offset/filter.Limit + 1,
		PerPage: filter.Limit,
	})
}

// getSession handles GET /api/v1/admin/sessions/{id}.
//
// @Summary      Get session
// @Description  Returns one session: its summary, the assets and insights it produced, and a page of its call timeline with the purpose stated for each call.
// @Tags         Sessions
// @Produce      json
// @Param        id        path   string   true   "Session ID"
// @Param        page      query  integer  false  "Timeline page number, 1-based (default: 1)"
// @Param        per_page  query  integer  false  "Timeline entries per page (default: 25, max: 200)"
// @Success      200  {object}  sessionview.Detail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/sessions/{id} [get]
func (h *handler) getSession(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampPerPage(httpjson.ParseLimit(q))
	offset := httpjson.ParsePageOffset(q, limit)

	detail, err := sessionview.Load(r.Context(), h.cfg.Sessions, r.PathValue("id"), limit, offset)
	if errors.Is(err, sessionview.ErrNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read session")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, detail)
}
