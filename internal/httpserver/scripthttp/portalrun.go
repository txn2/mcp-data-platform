package scripthttp

import (
	"encoding/json"
	"net/http"

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
