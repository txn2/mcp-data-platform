package feedbackapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// respondValidationRequest is the body for POST /threads/{id}/validation.
type respondValidationRequest struct {
	Result string `json:"result"` // validated | disputed
	Reason string `json:"reason,omitempty"`
}

// respondValidation handles POST /api/v1/portal/threads/{id}/validation
// (Phase 3 / #603): the original feedback author records a validation outcome.
// Disputing re-opens the thread. This is the human counterpart of the
// manage_feedback respond_validation action.
//
// @Summary      Respond to a validation request
// @Description  The feedback author marks a thread validated or disputed (with an optional reason). Disputing re-opens the thread.
// @Tags         Feedback
// @Accept       json
// @Produce      json
// @Param        id    path  string                    true  "Thread ID"
// @Param        body  body  respondValidationRequest  true  "Validation outcome"
// @Success      200  {object}  threads.Thread
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/threads/{id}/validation [post]
func (h *Handler) respondValidation(w http.ResponseWriter, r *http.Request) {
	user, thread := h.loadThreadForValidationResponse(w, r)
	if thread == nil {
		return
	}
	var req respondValidationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Result != threads.ValidationStateValidated && req.Result != threads.ValidationStateDisputed {
		httpjson.WriteError(w, http.StatusBadRequest, "result must be 'validated' or 'disputed'")
		return
	}
	if err := h.cfg.Threads.RespondValidation(r.Context(), thread.ID,
		threads.ValidationResponse(req), user.UserID, user.Email); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to record validation")
		return
	}
	updated, err := h.cfg.Threads.GetThread(r.Context(), thread.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to load updated thread")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, updated)
}

// loadThreadForValidationResponse fetches the thread and verifies the caller is
// its author (or an admin) — the only party allowed to answer a validation
// request. Writes the error and returns nil on failure.
func (h *Handler) loadThreadForValidationResponse(w http.ResponseWriter, r *http.Request) (*access.User, *threads.Thread) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, nil
	}
	thread, err := h.cfg.Threads.GetThread(r.Context(), r.PathValue("id"))
	if err != nil || thread == nil {
		httpjson.WriteError(w, http.StatusNotFound, "thread not found")
		return nil, nil
	}
	if !h.userIsAdmin(user) && !isThreadAuthor(user, thread) {
		httpjson.WriteError(w, http.StatusForbidden, "only the feedback author can respond to a validation request")
		return nil, nil
	}
	return user, thread
}

// isThreadAuthor reports whether the user authored the thread, matching by user
// id or case-insensitive email (the same predicate as the manage_feedback
// respond_validation action, so the two surfaces agree on who the author is).
func isThreadAuthor(user *access.User, thread *threads.Thread) bool {
	if thread.AuthorID == user.UserID {
		return true
	}
	return user.Email != "" && strings.EqualFold(thread.AuthorEmail, user.Email)
}
