package sessionapi

import (
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
)

// errAuthRequired is the message an unauthenticated request gets. Spelled here
// rather than imported from pkg/portal because that package imports this one to
// register the routes and so cannot be imported back; the wording is what a
// client sees and must stay identical on both sides.
const errAuthRequired = "authentication required"

// sessionListResponse wraps a paginated list of the caller's session
// summaries. Its shape matches the operator surface's response so one client
// type serves both lists.
type sessionListResponse struct {
	Data    []sessionview.Summary `json:"data"`
	Total   int                   `json:"total" example:"7"`
	Page    int                   `json:"page" example:"1"`
	PerPage int                   `json:"per_page" example:"25"`
}

// listSessions handles GET /api/v1/portal/sessions.
//
// @Summary      List my sessions
// @Description  Returns the calling user's own sessions, most recently active first. A session is not a stored row: it is every audit event sharing one session id, so this list reaches as far back as audit retention. There is no user parameter — the listing is always the caller's own.
// @Tags         Sessions
// @Produce      json
// @Param        kind          query  string   false  "Filter by session id origin (agent, portal, script, transport)"
// @Param        start_time    query  string   false  "Sessions with activity after this time (RFC 3339)"
// @Param        end_time      query  string   false  "Sessions with activity before this time (RFC 3339)"
// @Param        has_assets    query  boolean  false  "Only sessions that saved at least one asset"
// @Param        has_failures  query  boolean  false  "Only sessions with at least one failed call"
// @Param        page          query  integer  false  "Page number, 1-based (default: 1)"
// @Param        per_page      query  integer  false  "Results per page (default: 25, max: 200)"
// @Success      200  {object}  sessionListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sessions [get]
func (h *handler) listSessions(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	// The caller is assigned after the query string is read, so a
	// hand-written user_id parameter cannot widen the listing.
	filter := sessionview.FilterFromQuery(r.URL.Query())
	filter.UserID = user.UserID

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

// getSession handles GET /api/v1/portal/sessions/{id}.
//
// @Summary      Get one of my sessions
// @Description  Returns one of the caller's own sessions: its summary, the assets and insights it produced, and a page of its call timeline with the purpose stated for each call. A session id belonging to another user is not found, the same answer an id that was never used gets.
// @Tags         Sessions
// @Produce      json
// @Param        id        path   string   true   "Session ID"
// @Param        page      query  integer  false  "Timeline page number, 1-based (default: 1)"
// @Param        per_page  query  integer  false  "Timeline entries per page (default: 25, max: 200)"
// @Success      200  {object}  sessionview.Detail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/sessions/{id} [get]
func (h *handler) getSession(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	limit := sessionview.ClampPerPage(httpjson.ParseLimit(q))

	detail, err := sessionview.Load(r.Context(), h.cfg.Sessions, sessionview.Scope{
		SessionID: r.PathValue("id"),
		UserID:    user.UserID,
		Limit:     limit,
		Offset:    httpjson.ParsePageOffset(q, limit),
	})
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
