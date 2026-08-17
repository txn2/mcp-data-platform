package callapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
)

// errAuthRequired is the message an unauthenticated request gets. Spelled here
// rather than imported from pkg/portal because that package imports this one to
// register the routes and so cannot be imported back; the wording is what a
// client sees and must stay identical on both sides.
const errAuthRequired = "authentication required"

// callListResponse wraps a paginated list of the caller's call records. Its
// shape matches the operator surface's response so one client type serves both
// lists.
type callListResponse struct {
	Data    []callrecord.Record `json:"data"`
	Total   int                 `json:"total" example:"12"`
	Page    int                 `json:"page" example:"1"`
	PerPage int                 `json:"per_page" example:"25"`
}

// rejectRequest is the body of a rejection: why the record is not worth
// publishing, so the same record is not proposed again with no explanation.
type rejectRequest struct {
	Note string `json:"note,omitempty" example:"Superseded by the revenue_by_region view."`
}

// listCalls handles GET /api/v1/portal/calls.
//
// @Summary      List my calls
// @Description  Returns the calling user's own data-access calls, newest first: every query and API invocation the platform recorded, with the purpose stated for it and the outcome derived from what was built from it. There is no user parameter; the listing is always the caller's own.
// @Tags         Calls
// @Produce      json
// @Param        kind        query  string   false  "Filter by call kind (sql, api)"
// @Param        connection  query  string   false  "Filter by connection name"
// @Param        outcome     query  string   false  "Filter by outcome (failed, satisfied, superseded, ran)"
// @Param        target      query  string   false  "Filter by a dataset URN or endpoint the call addressed"
// @Param        session_id  query  string   false  "Filter to one session's calls"
// @Param        q           query  string   false  "Match the purpose or the statement"
// @Param        queue       query  string   false  "Set to 'promotable' for the records awaiting promotion, most reused first"
// @Param        page        query  integer  false  "Page number, 1-based (default: 1)"
// @Param        per_page    query  integer  false  "Results per page (default: 25, max: 200)"
// @Success      200  {object}  callListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/calls [get]
func (h *handler) listCalls(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	// The caller is assigned after the query string is read, so a
	// hand-written user_id parameter cannot widen the listing.
	filter := callrecord.FilterFromQuery(r.URL.Query())
	filter.UserID = user.UserID

	records, err := h.cfg.Calls.List(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list calls")
		return
	}
	total, err := h.cfg.Calls.Count(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count calls")
		return
	}

	httpjson.WriteJSON(w, http.StatusOK, callListResponse{
		Data:    records,
		Total:   total,
		Page:    filter.Offset/filter.Limit + 1,
		PerPage: filter.Limit,
	})
}

// getCall handles GET /api/v1/portal/calls/{id}.
//
// @Summary      Get one of my calls
// @Description  Returns one of the caller's own call records: the statement or request line, the purpose stated for it, its outcome and how it was reached, the assets and captures built from it, and how many later sessions re-ran it. A record id belonging to another user is not found, the same answer an id that was never used gets.
// @Tags         Calls
// @Produce      json
// @Param        id  path  string  true  "Call record ID"
// @Success      200  {object}  callrecord.Record
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/calls/{id} [get]
func (h *handler) getCall(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	rec, err := h.cfg.Calls.Get(r.Context(), callrecord.Scope{
		ID:     r.PathValue("id"),
		UserID: user.UserID,
	})
	if writeReadError(w, err) {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, rec)
}

// promoteCall handles POST /api/v1/portal/calls/{id}/promote.
//
// @Summary      Promote one of my calls
// @Description  Publishes a satisfied record: a query becomes a Query entity in the data catalog, associated with every dataset it reads; an API call becomes a saved example on its endpoint. The record then carries what it became. Only the caller's own records can be promoted here, and only a record that answered something and has not already been promoted or declined.
// @Tags         Calls
// @Produce      json
// @Param        id  path  string  true  "Call record ID"
// @Success      200  {object}  callrecord.Record
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/calls/{id}/promote [post]
func (h *handler) promoteCall(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	scope := callrecord.Scope{ID: r.PathValue("id"), UserID: user.UserID}
	rec, err := h.cfg.Promoter.Promote(r.Context(), scope, user.Email)
	if writeActionError(w, err) {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, rec)
}

// rejectCall handles POST /api/v1/portal/calls/{id}/reject.
//
// @Summary      Decline one of my calls
// @Description  Records that the caller decided this record is not worth publishing, with an optional note, so it stops being offered for promotion.
// @Tags         Calls
// @Accept       json
// @Produce      json
// @Param        id       path  string         true   "Call record ID"
// @Param        request  body  rejectRequest  false  "Why the record was declined"
// @Success      200  {object}  callrecord.Record
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/calls/{id}/reject [post]
func (h *handler) rejectCall(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	var body rejectRequest
	// A missing or malformed body is a rejection with no note: the note is
	// optional, and refusing the action over it would be refusing the
	// decision the caller already made.
	_ = json.NewDecoder(r.Body).Decode(&body)

	scope := callrecord.Scope{ID: r.PathValue("id"), UserID: user.UserID}
	rec, err := h.cfg.Promoter.Reject(r.Context(), scope, user.Email, body.Note)
	if writeActionError(w, err) {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, rec)
}

// writeReadError maps a read failure onto its response and reports whether it
// wrote one.
func writeReadError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, callrecord.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "call record not found")
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read call record")
	}
	return true
}

// writeActionError maps a promotion or rejection failure onto its response. A
// record that is not in a promotable state is a conflict rather than a bad
// request: nothing about the request was wrong, the record moved.
func writeActionError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, callrecord.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "call record not found")
	case errors.Is(err, callrecord.ErrNotPromotable):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, callrecord.ErrNoPromotionTarget):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to update call record")
	}
	return true
}
