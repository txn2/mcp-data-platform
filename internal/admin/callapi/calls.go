package callapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
)

// callListResponse wraps a paginated list of call records. Its shape matches
// the portal surface's response so one client type serves both lists.
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

// listCalls handles GET /api/v1/admin/calls.
//
// @Summary      List recorded calls
// @Description  Returns every data-access call the platform recorded, newest first: each query and API invocation with the purpose stated for it, the outcome derived from what was built from it, and how many later sessions re-ran it. Set queue=promotable for the review queue, which keeps the records that answered something and orders them by reuse first.
// @Tags         Calls
// @Produce      json
// @Param        user_id     query  string   false  "Filter to one caller's calls"
// @Param        kind        query  string   false  "Filter by call kind (sql, api)"
// @Param        connection  query  string   false  "Filter by connection name"
// @Param        outcome     query  string   false  "Filter by outcome (failed, satisfied, superseded, ran)"
// @Param        target      query  string   false  "Filter by a dataset URN or endpoint the call addressed"
// @Param        session_id  query  string   false  "Filter to one session's calls"
// @Param        q           query  string   false  "Match the purpose or the statement"
// @Param        queue       query  string   false  "Set to 'promotable' for the review queue"
// @Param        page        query  integer  false  "Page number, 1-based (default: 1)"
// @Param        per_page    query  integer  false  "Results per page (default: 25, max: 200)"
// @Success      200  {object}  callListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/calls [get]
func (h *handler) listCalls(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := callrecord.FilterFromQuery(q)
	// The operator's user facet is read here rather than in the shared
	// parser: it is the one parameter the two surfaces cannot share, since
	// the portal overwrites it with the authenticated caller.
	filter.UserID = q.Get("user_id")

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

// getCall handles GET /api/v1/admin/calls/{id}.
//
// @Summary      Get one recorded call
// @Description  Returns one call record whoever made it: the statement or request line, the purpose stated for it, its outcome and how it was reached, the assets and captures built from it, and how many later sessions re-ran it.
// @Tags         Calls
// @Produce      json
// @Param        id  path  string  true  "Call record ID"
// @Success      200  {object}  callrecord.Record
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/calls/{id} [get]
func (h *handler) getCall(w http.ResponseWriter, r *http.Request) {
	rec, err := h.cfg.Calls.Get(r.Context(), callrecord.Scope{ID: r.PathValue("id")})
	if writeReadError(w, err) {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, rec)
}

// promoteCall handles POST /api/v1/admin/calls/{id}/promote.
//
// @Summary      Promote a recorded call
// @Description  Publishes a satisfied record: a query becomes a Query entity in the data catalog, associated with every dataset it reads; an API call becomes a saved example on its endpoint. The record then carries what it became and who promoted it.
// @Tags         Calls
// @Produce      json
// @Param        id  path  string  true  "Call record ID"
// @Success      200  {object}  callrecord.Record
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/calls/{id}/promote [post]
func (h *handler) promoteCall(w http.ResponseWriter, r *http.Request) {
	rec, err := h.cfg.Promoter.Promote(r.Context(), callrecord.Scope{ID: r.PathValue("id")}, h.actor(r))
	if writeActionError(w, err) {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, rec)
}

// rejectCall handles POST /api/v1/admin/calls/{id}/reject.
//
// @Summary      Decline a recorded call
// @Description  Records that a reviewer decided this record is not worth publishing, with an optional note, so the review queue stops offering it.
// @Tags         Calls
// @Accept       json
// @Produce      json
// @Param        id       path  string         true   "Call record ID"
// @Param        request  body  rejectRequest  false  "Why the record was declined"
// @Success      200  {object}  callrecord.Record
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/calls/{id}/reject [post]
func (h *handler) rejectCall(w http.ResponseWriter, r *http.Request) {
	var body rejectRequest
	// A missing or malformed body is a rejection with no note: the note is
	// optional, and refusing the action over it would be refusing the
	// decision the reviewer already made.
	_ = json.NewDecoder(r.Body).Decode(&body)

	rec, err := h.cfg.Promoter.Reject(r.Context(), callrecord.Scope{ID: r.PathValue("id")}, h.actor(r), body.Note)
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
	case errors.Is(err, callrecord.ErrNotPromotable), errors.Is(err, callrecord.ErrNoPromotionTarget):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to update call record")
	}
	return true
}
