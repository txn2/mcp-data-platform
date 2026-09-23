// Package statehttp serves a managed script's state on its portal page
// (#1537): reading the one JSON object a script carries from run to run, and
// the owner's two resets of it.
//
// It is mounted by scripthttp, which resolves the caller and the script and
// refuses everybody but the script's owner and an administrator before any
// handler here runs.
package statehttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Deps carries the state store and the ownership gate.
type Deps struct {
	States script.StateStore
	// Owned resolves the script in the path for a caller who owns it, or
	// writes the refusal and reports false. actor is who the caller is, as a
	// reset records it.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
}

// Handler serves the state routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// A script's state on its page (#1537).
//
// A script carries one JSON object from run to run: a run reads it as
// run.state and saves it with platform.save_state, and the platform applies
// the save when the run succeeds. What the page adds is reading it, and the
// two resets — replace it, or clear it so the next run starts over — because a
// wrong watermark is otherwise stuck. Both are the owner's and an
// administrator's, the same rule every other write on this page applies, and
// a reset is recorded with who did it.

// maxStateBodyBytes bounds a state request: the object itself is bounded at
// script.MaxStateBytes, and the envelope around it is small.
const maxStateBodyBytes = script.MaxStateBytes + (4 << 10)

// stateRequest is the whole object a replace sets.
type stateRequest struct {
	State map[string]any `json:"state"`
}

// stateResponse is a script's state as the page reads it.
type stateResponse struct {
	// State is the object itself, {} when nothing has been saved.
	State map[string]any `json:"state"`
	// Revision counts writes; 0 means nothing was ever saved or reset.
	Revision int64 `json:"revision" example:"3"`
	// UpdatedAt is when this revision was written, absent at revision 0.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// RunID names the run that wrote this revision, and UpdatedBy the person
	// who set or cleared it; one of the two is set past revision 0.
	RunID     string `json:"run_id,omitempty" example:"dpx_a1b2c3d4"`
	UpdatedBy string `json:"updated_by,omitempty" example:"jane@example.com"`
	// Message states what a write means for the next run.
	Message string `json:"message,omitempty"`
}

// renderState projects the stored state for the page.
func renderState(st *script.State, message string) stateResponse {
	out := stateResponse{State: st.Value, Revision: st.Revision, RunID: st.RunID, UpdatedBy: st.UpdatedBy, Message: message}
	if out.State == nil {
		out.State = map[string]any{}
	}
	if st.Revision > 0 {
		at := st.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}

// Register mounts the state routes, each wrapped in the portal authentication
// middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/state", wrap(http.HandlerFunc(h.getState)))
	mux.Handle("PUT /api/v1/portal/scripts/{id}/state", wrap(http.HandlerFunc(h.setState)))
	mux.Handle("DELETE /api/v1/portal/scripts/{id}/state", wrap(http.HandlerFunc(h.clearState)))
}

// getState returns a script's state.
//
// @Summary      Get a script's state
// @Description  Returns the one JSON object a script carries from run to run, with the revision the platform holds it at and who wrote it: the run that saved it, or the person who set or cleared it. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  stateResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/state [get]
func (h *Handler) getState(w http.ResponseWriter, r *http.Request) {
	scriptID, _, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	st, err := h.deps.States.GetState(r.Context(), scriptID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the script's state")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, renderState(st, ""))
}

// setState replaces a script's state with the object sent.
//
// @Summary      Replace a script's state
// @Description  Replaces the whole state object of a script the caller owns and moves its revision forward, recording who did it. The next run reads this object; a run already in flight that read the previous revision fails at its write, because the reset was after its premise. Refused when a value is not JSON-representable or the object is over the size bound.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id     path  string        true  "Script ID"
// @Param        state  body  stateRequest  true  "The whole state object"
// @Success      200  {object}  stateResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/state [put]
func (h *Handler) setState(w http.ResponseWriter, r *http.Request) {
	scriptID, actor, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	var req stateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStateBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.State == nil {
		httpjson.WriteError(w, http.StatusBadRequest, "state is required: send the whole object the next run should read")
		return
	}
	h.writeState(w, r, stateReset{
		scriptID: scriptID, actor: actor, value: req.State, message: script.StateResetMessage(false),
	})
}

// clearState resets a script's state to {}.
//
// @Summary      Clear a script's state
// @Description  Resets the state of a script the caller owns to an empty object and moves its revision forward, recording who did it, so the next run starts over. A run already in flight that read the previous revision fails at its write.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  stateResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/state [delete]
func (h *Handler) clearState(w http.ResponseWriter, r *http.Request) {
	scriptID, actor, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	h.writeState(w, r, stateReset{
		scriptID: scriptID, actor: actor, value: map[string]any{}, message: script.StateResetMessage(true),
	})
}

// stateReset is one reset as a route resolved it: which script, who did it, the object
// the state becomes, and what the answer says it means for the next run.
type stateReset struct {
	scriptID string
	actor    string
	value    map[string]any
	message  string
}

// writeState applies one reset as the caller and answers with the state as
// it now stands.
func (h *Handler) writeState(w http.ResponseWriter, r *http.Request, reset stateReset) {
	if err := script.ValidateState(reset.value); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := h.deps.States.SetState(r.Context(), reset.scriptID, reset.value, reset.actor)
	if err != nil {
		slog.Error("failed to write script state", "script_id", reset.scriptID, "error", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to write the script's state")
		return
	}
	slog.Info("script state reset", "script_id", reset.scriptID, "revision", st.Revision, "by", reset.actor)
	httpjson.WriteJSON(w, http.StatusOK, renderState(st, reset.message))
}
