package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Validating and dry-running an edit before asking for approval (#1364).
//
// Editing a script in the portal was a blind save: the author wrote a change,
// could not check that it parsed, could not see what capabilities and
// connections it now reached, could not execute it once, and sent it to an
// administrator — who then approved code neither of them had run.
//
// Both mechanisms already existed and were reachable only from an agent
// session. These routes are the same two, on the page the author is already
// looking at:
//
//   - validate parses the source and reports what it would reach. It executes
//     nothing and touches no record.
//   - dry-run executes the edit as the caller, under their own identity and
//     persona, with tighter limits, persisting nothing it produced.
//
// Neither can approve anything and neither introduces authority. A dry run
// reaches exactly what the person asking for it reaches: it is their session,
// their persona, their audit trail. What it changes is that a version reaching
// a reviewer has been run at least once, by the person who wrote it.

// DraftRunner executes a draft under the calling person's own identity. The
// composition root supplies it, because building one needs the assembled MCP
// server. Nil leaves the dry-run route unmounted; validate stays, since parsing
// needs nothing but the source.
type DraftRunner interface {
	Run(ctx context.Context, req scriptdraft.Request) (*scriptdraft.Outcome, error)
}

// draftRequest is a source to check, with the values a dry run binds. An empty
// source means the script's stored code, which is what "run what is saved"
// asks for.
type draftRequest struct {
	Source string         `json:"source,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// validateResponse is what the edited source would reach, and everything the
// author should know before a reviewer sees it.
type validateResponse struct {
	OK       bool                `json:"ok" example:"true"`
	Findings []scriptrun.Finding `json:"findings"`
	// Capabilities, Connections and Destinations are what the code plainly
	// reaches. They are the reviewer's diff material, shown to the author first
	// so the capability change is theirs to notice rather than a surprise in
	// somebody else's queue.
	Capabilities []string `json:"capabilities"`
	Connections  []string `json:"connections"`
	Destinations []string `json:"destinations"`
	// DynamicConnections and DynamicDestinations report that a list above is
	// known to be incomplete because a call computes its target instead of
	// naming one. Reporting the gap is the point: a list that silently omitted
	// a computed name would be a false statement.
	DynamicConnections  bool `json:"dynamic_connections"`
	DynamicDestinations bool `json:"dynamic_destinations"`
	// Note states any such gap in the author's terms.
	Note string `json:"note,omitempty"`
}

// portalValidateSource parses an edit and reports what it would reach.
//
// @Summary      Validate a script's source
// @Description  Parses Starlark for a script the caller owns and reports the capabilities, connections and destinations it would reach, plus any findings with the correction for each. Nothing is executed and nothing is stored. An empty source validates the script's saved code.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id     path  string        true   "Script ID"
// @Param        draft  body  draftRequest  false  "Source to validate"
// @Success      200  {object}  validateResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/validate [post]
func (h *Handler) portalValidateSource(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	req, ok := decodeDraftRequest(w, r)
	if !ok {
		return
	}
	report := scriptrun.Validate(sourceOr(req.Source, sc))
	httpjson.WriteJSON(w, http.StatusOK, validateResponse{
		OK:                  report.OK,
		Findings:            report.Findings,
		Capabilities:        report.Capabilities,
		Connections:         report.Connections,
		Destinations:        report.Destinations,
		DynamicConnections:  report.DynamicConnections,
		DynamicDestinations: report.DynamicDestinations,
		Note:                incompleteNote(report),
	})
}

// dryRunResponse is one draft execution as the editor reports it. A failed run
// answers with the same fields a successful one does: the log is the whole
// reason to have run it.
type dryRunResponse struct {
	RunID  string `json:"run_id" example:"run_a1b2c3d4"`
	Status string `json:"status" example:"succeeded"`
	Error  string `json:"error,omitempty"`
	// Log is what the run printed, bounded when it was captured.
	Log          string                `json:"log,omitempty"`
	LogTruncated bool                  `json:"log_truncated,omitempty"`
	Metrics      script.RunMetrics     `json:"metrics"`
	Outputs      []script.DryRunOutput `json:"outputs"`
	// Message states what did and did not happen, because "succeeded" on a run
	// that deliberately wrote nothing is the sentence most likely to be
	// misread.
	Message string `json:"message"`
}

// portalDryRunSource executes an edit as the caller and reports what it did.
//
// @Summary      Dry-run a script's source
// @Description  Executes Starlark for a script the caller owns, under the caller's own identity and persona and with tighter limits, persisting nothing: platform.export reports the shape of each output instead of writing it, and no approval is touched. An empty source runs the script's saved code. The account of the run is kept so a reviewer can see that the version they are approving was executed, and by whom.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id     path  string        true   "Script ID"
// @Param        draft  body  draftRequest  false  "Source and parameter values"
// @Success      200  {object}  dryRunResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/dry-run [post]
func (h *Handler) portalDryRunSource(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	if err := script.RefuseDraftRun(sc); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	req, ok := decodeDraftRequest(w, r)
	if !ok {
		return
	}
	source := sourceOr(req.Source, sc)
	if detail := refuseSource(source); detail != "" {
		httpjson.WriteError(w, http.StatusBadRequest, detail)
		return
	}
	// Values bind against the LIVE record's contract, which is the contract the
	// source being run was written against. The approved version's would be the
	// wrong one: a draft is precisely the code that does not match it yet.
	params, err := script.BindParams(sc.Params, req.Params)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	outcome, err := h.deps.Drafts.Run(r.Context(), scriptdraft.Request{
		Source: source, Name: sc.Name, Params: params,
		Identity: scriptdraft.Identity{
			UserID: user.UserID, Email: user.Email, Roles: user.Roles,
			AuthType: user.AuthType,
		},
	})
	if err != nil {
		// Busy is the platform declining to start another interpreter right now,
		// which is a different answer from the platform being broken: it says to
		// try again, and a client that retries will succeed.
		if errors.Is(err, scriptdraft.ErrBusy) {
			httpjson.WriteError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		httpjson.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rendered := draftOutcome(outcome)
	h.recordDryRun(r.Context(), executedDraft{
		script: sc, source: source, user: user, runID: outcome.RunID, result: rendered,
	})
	httpjson.WriteJSON(w, http.StatusOK, rendered)
}

// decodeDraftRequest reads a source-and-parameters body, treating an absent one
// as "the saved source, with no values": pressing dry-run on an unedited script
// is a legitimate request and needs no body.
func decodeDraftRequest(w http.ResponseWriter, r *http.Request) (draftRequest, bool) {
	var req draftRequest
	if r.ContentLength == 0 {
		return req, true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSourceBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, true
}

// sourceOr resolves the source to act on: the edit when one was sent, and the
// stored code otherwise.
func sourceOr(sent string, sc *script.Script) string {
	if sent != "" {
		return sent
	}
	return sc.Source
}

// incompleteNote states that a validate report's lists are known to be short,
// which is the one thing an author reading them must not miss.
func incompleteNote(report scriptrun.Report) string {
	switch {
	case report.DynamicConnections && report.DynamicDestinations:
		return "At least one call computes its connection and at least one computes its destination, " +
			"so both lists are incomplete."
	case report.DynamicConnections:
		return "At least one platform.query call computes its connection instead of naming one, " +
			"so the connection list is incomplete."
	case report.DynamicDestinations:
		return "At least one platform.export call computes its destination instead of naming one, " +
			"so the destination list is incomplete."
	}
	return ""
}

// draftOutcome renders one executed draft.
func draftOutcome(outcome *scriptdraft.Outcome) dryRunResponse {
	out := dryRunResponse{
		RunID: outcome.RunID, Status: script.RunStatusSucceeded,
		Outputs: draftOutputs(outcome),
		Message: "Nothing was persisted. platform.export reported the shape of each output " +
			"rather than writing it.",
	}
	if outcome.Result != nil {
		out.Log = outcome.Result.Log
		out.LogTruncated = outcome.Result.LogTruncated
		out.Metrics = draftMetrics(outcome.Result)
	}
	if outcome.Failed() {
		out.Status = script.RunStatusFailed
		out.Error = outcome.Err.Error()
		out.Message = "A script failure is deterministic: the same source on the same inputs fails " +
			"the same way, so running it again changes nothing. Fix the script and dry-run it again."
	}
	return out
}

// draftMetrics projects the engine's result into the metrics shape every other
// run surface reports, so a draft's cost is read in the same units as an
// approved run's.
func draftMetrics(result *scriptrun.Result) script.RunMetrics {
	return script.RunMetrics{
		Steps:      result.Steps,
		DurationMS: result.Duration.Milliseconds(),
		Queries:    result.Queries,
		Exports:    len(result.Exports),
	}
}

// draftOutputs is the shape of what the run would have written. Every entry is
// a preview by construction, so the locators a persisted output carries are
// absent rather than empty.
func draftOutputs(outcome *scriptdraft.Outcome) []script.DryRunOutput {
	out := []script.DryRunOutput{}
	if outcome.Result == nil {
		return out
	}
	for _, e := range outcome.Result.Exports {
		out = append(out, script.DryRunOutput{
			Name: e.Name, Destination: e.Destination, Format: e.Format,
			RowCount: e.RowCount, Bytes: e.Bytes,
		})
	}
	return out
}

// recordDryRun keeps the account of what the author ran, so the reviewer of the
// version carrying this exact source can see that somebody executed it.
//
// A failure to record is logged and dropped. The run already happened and its
// result is on its way to the author; failing the response over the bookkeeping
// would discard the thing they asked for to protect a note about it.
func (h *Handler) recordDryRun(ctx context.Context, ran executedDraft) {
	if h.deps.DryRuns == nil {
		return
	}
	err := h.deps.DryRuns.RecordDryRun(ctx, &script.DryRun{
		ID: ran.runID, ScriptID: ran.script.ID, SourceSHA256: script.SourceDigest(ran.source),
		RequestedBy: ran.user.owner(), Status: ran.result.Status, Error: ran.result.Error,
		Log: ran.result.Log, LogTruncated: ran.result.LogTruncated,
		Metrics: ran.result.Metrics, Outputs: ran.result.Outputs,
	})
	if err != nil {
		slog.Error("failed to record a script dry run", "script", ran.script.Name, "error", err)
	}
}

// executedDraft is one draft run that happened: which script, which source,
// who ran it, and what came back.
type executedDraft struct {
	script *script.Script
	source string
	user   *PortalIdentity
	runID  string
	result dryRunResponse
}
