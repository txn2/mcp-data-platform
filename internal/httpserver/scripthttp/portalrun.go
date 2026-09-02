package scripthttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// Running a script from the portal (#1363).
//
// The page already showed the contract, the parameters, and the run history,
// and offered no way to run any of it: an owner who wanted fresh output before
// the next scheduled fire had to leave and ask an agent to call run_script.
// This route is that action, and it is deliberately the same action — it
// queues a run of the latest saved version and returns its id, so the worker
// executes it under the script principal exactly as a scheduled fire is
// executed. There is one path into execution, and this is not a second one.
//
// It grants nothing. Whether a run is admitted at all is script.RefuseRun's
// answer, the same one the contract document reports and run_script obeys, so
// this route cannot run something those two call unrunnable.

// maxRunBodyBytes bounds a run request. The body is a set of scalars against a
// declared parameter contract, so this is generous for anything legitimate and
// small enough that an authenticated non-administrator cannot make the decoder
// materialize a payload of consequence.
const maxRunBodyBytes = 64 << 10

// runRequest is a request for one run: the values to bind, and nothing else.
type runRequest struct {
	Params map[string]any `json:"params,omitempty"`
}

// runResponse identifies the queued run. It carries no result: a run is
// executed by a worker, and the page follows it through the run history rather
// than holding a request open for the ten minutes a run may take.
type runResponse struct {
	RunID   string `json:"run_id" example:"run_a1b2c3d4"`
	Status  string `json:"status" example:"pending"`
	Version int    `json:"version" example:"3"`
	// Message states what was queued, in the owner's terms.
	Message string `json:"message"`
}

// portalRunScript queues one run of a script's latest saved version.
//
// @Summary      Run a script
// @Description  Queues one run of the latest saved version of a script the caller owns, binding the supplied parameters against its contract. The run is executed by a worker under the script's own identity, exactly as a scheduled fire is, and appears in the script's run history. A disabled or retired script is refused, in the run gate's own words.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id   path  string      true   "Script ID"
// @Param        run  body  runRequest  false  "Parameter values"
// @Success      202  {object}  runResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs [post]
func (h *Handler) portalRunScript(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	req, ok := decodeRunRequest(w, r)
	if !ok {
		return
	}
	version, ok := h.admittedVersion(w, r, sc)
	if !ok {
		return
	}
	params, err := script.BindParams(version.Params, req.Params)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, ok := h.enqueueRun(w, r, queuedRun{
		script: sc, version: version, params: params, user: user,
	})
	if !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusAccepted, runResponse{
		RunID: run.ID, Status: run.Status, Version: run.Version,
		Message: "Queued. It appears in this script's run history and updates as it progresses.",
	})
}

// decodeRunRequest reads the parameter values, treating an absent body as no
// values: a script whose parameters are all optional is run by asking for a
// run, with nothing to say about it.
func decodeRunRequest(w http.ResponseWriter, r *http.Request) (runRequest, bool) {
	var req runRequest
	if r.ContentLength == 0 {
		return req, true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, true
}

// admittedVersion is the version a run requested right now would execute —
// the script's latest saved version — or a refusal written to w.
//
// The refusal is script.RefuseRun's, verbatim. It is the gate's own reading of
// the same question — disabled, superseded, deprecated — so this route and the
// contract document the page is rendered from cannot disagree about whether a
// run would happen.
func (h *Handler) admittedVersion(w http.ResponseWriter, r *http.Request, sc *script.Script) (*script.Version, bool) {
	if err := script.RefuseRun(sc); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	v, err := h.deps.Versions.GetVersion(r.Context(), sc.ID, sc.Version)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the script's current version")
		return nil, false
	}
	if v == nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the script's current version is missing from its history")
		return nil, false
	}
	return v, true
}

// queuedRun is what one run request resolved to: which code, with which
// values, for whom.
type queuedRun struct {
	script  *script.Script
	version *script.Version
	params  map[string]any
	user    *PortalIdentity
}

// enqueueRun mints the run identity and puts the run on the queue.
//
// The id is minted here because it is also the run's session id: the worker
// threads it onto the session the run drives, so every audit row the run
// produces carries it and the id handed back identifies the whole run rather
// than a queue entry. Enqueuing wakes a worker; nothing is executed here.
func (h *Handler) enqueueRun(
	w http.ResponseWriter,
	r *http.Request,
	req queuedRun,
) (*script.Run, bool) {
	runID, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to queue the run")
		return nil, false
	}
	run := &script.Run{
		ID: runID, ScriptID: req.script.ID,
		VersionID: req.version.ID, Version: req.version.Version,
		Trigger: script.TriggerPortal, Params: req.params, RequestedBy: req.user.owner(),
	}
	if err := h.deps.Runs.Enqueue(r.Context(), run); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to queue the run")
		return nil, false
	}
	return run, true
}

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

// registerPortalState mounts the state routes.
func (h *Handler) registerPortalState(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/state", wrap(h.portalHandler(h.portalGetState)))
	mux.Handle("PUT /api/v1/portal/scripts/{id}/state", wrap(h.portalHandler(h.portalSetState)))
	mux.Handle("DELETE /api/v1/portal/scripts/{id}/state", wrap(h.portalHandler(h.portalClearState)))
}

// portalGetState returns a script's state.
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
func (h *Handler) portalGetState(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	st, err := h.deps.States.GetState(r.Context(), sc.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the script's state")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, renderState(st, ""))
}

// portalSetState replaces a script's state with the object sent.
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
func (h *Handler) portalSetState(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
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
	h.writeState(w, r, user, stateReset{
		script: sc, value: req.State, message: script.StateResetMessage(false),
	})
}

// portalClearState resets a script's state to {}.
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
func (h *Handler) portalClearState(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	h.writeState(w, r, user, stateReset{
		script: sc, value: map[string]any{}, message: script.StateResetMessage(true),
	})
}

// stateReset is one reset as a route resolved it: which script, the object
// the state becomes, and what the answer says it means for the next run.
type stateReset struct {
	script  *script.Script
	value   map[string]any
	message string
}

// writeState applies one reset as the caller and answers with the state as
// it now stands.
func (h *Handler) writeState(w http.ResponseWriter, r *http.Request, user *PortalIdentity, reset stateReset) {
	if err := script.ValidateState(reset.value); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := h.deps.States.SetState(r.Context(), reset.script.ID, reset.value, user.owner())
	if err != nil {
		slog.Error("failed to write script state", "script_id", reset.script.ID, "error", err)
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to write the script's state")
		return
	}
	slog.Info("script state reset", "script_id", reset.script.ID, "revision", st.Revision, "by", user.owner())
	httpjson.WriteJSON(w, http.StatusOK, renderState(st, reset.message))
}

// liveState reads a script's state for a draft, or nil where this deployment
// keeps none; nil reads as {} inside the run.
func (h *Handler) liveState(ctx context.Context, sc *script.Script) map[string]any {
	if h.deps.States == nil {
		return nil
	}
	st, err := h.deps.States.GetState(ctx, sc.ID)
	if err != nil {
		slog.Warn("failed to read script state for a draft; the draft reads {}", "script_id", sc.ID, "error", err)
		return nil
	}
	return st.Value
}
