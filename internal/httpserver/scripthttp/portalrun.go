package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/runcontrol"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptgrant"
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

// runResponse identifies a queued run: the answer to a request that did not
// wait, or whose wait ran out before the run finished. A run that finished
// inside the wait is answered with the run itself (#1845).
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
// @Description  Queues one run of the latest saved version of a script the caller owns or was granted, binding the supplied parameters against its contract. A parameter bound to caller.<claim> takes the caller's claim (an API key's attribute): a value for it in the body is refused with 400, and a caller without the claim is refused with 403. The run is executed by a worker under the script's own identity, exactly as a scheduled fire is, and appears in the script's run history. A disabled or retired script is refused, in the run gate's own words. With wait, the request holds for up to that many seconds (at most 300): a run that finishes in time is answered 200 with the run itself, including the value it returned with platform.result, its outputs and its error; otherwise, and without wait, 202 with the run id to follow.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id    path   string      true   "Script ID"
// @Param        wait  query  int         false  "Seconds to wait for the run to finish (0-300)"
// @Param        run   body   runRequest  false  "Parameter values"
// @Success      200  {object}  portalRunDetail
// @Success      202  {object}  runResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs [post]
func (h *Handler) portalRunScript(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.runnableScript(w, r, user)
	if !ok {
		return
	}
	wait, ok := waitFor(w, r)
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
	params, err := scriptgrant.BindCaller(version.Params, req.Params, user.Claims)
	if errors.Is(err, scriptgrant.ErrClaimMissing) {
		httpjson.WriteError(w, http.StatusForbidden, err.Error())
		return
	}
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
	if wait > 0 {
		finished, done, err := runcontrol.AwaitRun(r.Context(), h.deps.Runs, run, wait, runcontrol.PollEvery)
		if err != nil {
			slog.Warn("scripts: reading a run while waiting on it failed", "run_id", run.ID, "error", logsan.SanitizeForLog(err.Error()))
		}
		if done {
			httpjson.WriteJSON(w, http.StatusOK, detailRun(finished))
			return
		}
	}
	httpjson.WriteJSON(w, http.StatusAccepted, runResponse{
		RunID: run.ID, Status: run.Status, Version: run.Version,
		Message: "Queued. It appears in this script's run history and updates as it progresses.",
	})
}

// waitFor reads the wait query parameter: absent is no wait, and a value past
// runcontrol.MaxWaitSeconds is held to it rather than refused, which is what
// run_script does with wait_seconds.
func waitFor(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	raw := r.URL.Query().Get("wait")
	if raw == "" {
		return 0, true
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "wait must be a whole number of seconds, 0 to "+
			strconv.Itoa(runcontrol.MaxWaitSeconds))
		return 0, false
	}
	return time.Duration(min(seconds, runcontrol.MaxWaitSeconds)) * time.Second, true
}

// cancelResponse is what a cancel did.
type cancelResponse struct {
	RunID string `json:"run_id" example:"run_a1b2c3d4"`
	// Status is the run's status after the request: canceled when it ended
	// here, running while its worker is still to stop it.
	Status string `json:"status" example:"running"`
	// Outcome is canceled (it had not started and will not), requested (it
	// is running and ends canceled within seconds), canceled_orphaned (its
	// worker had stopped reporting, so it was ended directly, #1860) or
	// already_finished.
	Outcome string `json:"outcome" example:"requested"`
	Message string `json:"message"`
}

// portalCancelRun stops a run (#1847).
//
// @Summary      Cancel a script run
// @Description  Stops a run. A queued run is canceled and never starts; a running one ends canceled within seconds, keeping the outputs it already wrote; a finished one is left as it is, which the answer says. Restricted to the script's owner, to administrators, and to whoever requested that run.
// @Tags         Scripts
// @Produce      json
// @Param        id     path  string  true  "Script ID"
// @Param        runID  path  string  true  "Run ID"
// @Success      200  {object}  cancelResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs/{runID}/cancel [post]
func (h *Handler) portalCancelRun(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	run, ok := h.readableRun(w, r, user)
	if !ok {
		return
	}
	prior, now, err := h.deps.Runs.CancelRun(r.Context(), run.ID, user.owner())
	if errors.Is(err, script.ErrRunNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, errRunNot)
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to cancel the run")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, cancelResponse{
		RunID: run.ID, Status: now,
		Outcome: string(runcontrol.OutcomeOf(prior, now)), Message: runcontrol.CancelMessage(prior, now),
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
