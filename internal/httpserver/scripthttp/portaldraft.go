package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptbehavior"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/draftview"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Validating and dry-running an edit before saving it (#1364).
//
// Editing a script in the portal was a blind save: the author wrote a change,
// could not check that it parsed, could not see what capabilities and
// connections it now reached, and could not execute it once before saving the
// version that runs.
//
// Both mechanisms already existed and were reachable only from an agent
// session. These routes are the same two, on the page the author is already
// looking at:
//
//   - validate parses the source and reports what it would reach. It executes
//     nothing and touches no record.
//   - dry-run executes the edit as the caller, under their own identity and
//     persona, with tighter limits, persisting nothing unless the author asks
//     for the writes.
//
// Neither introduces authority. A dry run reaches exactly what the person
// asking for it reaches: it is their session, their persona, their audit
// trail. What it changes is that a saved version can have been run at least
// once, by the person who wrote it.

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
	// AllowWrites lets a dry run persist through platform.call (#1664). A dry
	// run refuses write-class calls by default, which is what makes it a
	// rehearsal; the author asks for the writes when the pipeline's next step
	// reads what the last one created. Ignored by validate, which executes
	// nothing.
	AllowWrites bool `json:"allow_writes,omitempty"`
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
	// Destinations are where this source's OUTPUTS go, which is not every byte
	// it can move: a write made through platform.call is read in Tools.
	Destinations []string `json:"destinations"`
	// Tools are the tool names the edit passes to platform.call literally, so
	// the author sees the reach of the open half of the surface before a run
	// exercises it (#1419).
	Tools []string `json:"tools"`
	// RefreshTargets are the output names platform.publish_data refreshes, so
	// the author sees which asset's data region the edit rewrites.
	RefreshTargets []string `json:"refresh_targets"`
	// DynamicConnections, DynamicDestinations and DynamicRefreshTargets report
	// that a list above is known to be incomplete because a call computes its
	// target instead of naming one. Reporting the gap is the point: a list that
	// silently omitted a computed name would be a false statement.
	DynamicConnections    bool `json:"dynamic_connections"`
	DynamicDestinations   bool `json:"dynamic_destinations"`
	DynamicRefreshTargets bool `json:"dynamic_refresh_targets"`
	DynamicTools          bool `json:"dynamic_tools"`
	// Library is true when the source is a library (#1941), and Libraries is
	// the library versions it loads.
	Library   bool            `json:"library"`
	Libraries []scriptlib.Ref `json:"libraries"`
	// Note states any such gap in the author's terms.
	Note string `json:"note,omitempty"`
	// SaveRefusal is why a save of the source would be refused, Tests the
	// report of its tests (#1939, #1940), and Differences what it does
	// differently from the saved version (#1942).
	SaveRefusal string                      `json:"save_refusal,omitempty"`
	Tests       *scripttest.Report          `json:"tests,omitempty"`
	Differences []scriptbehavior.Difference `json:"differences"`
}

// portalValidateSource parses an edit and reports what it would reach.
//
// @Summary      Validate a script's source
// @Description  Parses Starlark for a script the caller owns and reports the capabilities, connections and destinations it would reach and the output assets whose data region it refreshes, plus any findings with the correction for each. Nothing is executed and nothing is stored. An empty source validates the script's saved code.
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
	source := script.DraftSource(req.Source, sc)
	// The gate as a save of this source would apply it (#1913): the lint, the
	// tests, and what the new version does differently (#1939, #1942).
	gated := h.gate().Check(r.Context(), scriptsave.Request{
		Existing: sc, Name: sc.Name, Source: source,
		Caller: scriptsave.Caller{Email: user.owner(), Admin: user.IsAdmin},
	})
	report := scriptlint.Merge(scriptlint.WithDestinationCheck(scriptrun.Validate(source), h.deps.Destinations), gated.Lint)
	httpjson.WriteJSON(w, http.StatusOK, validateResponse{
		OK:                    report.OK && !gated.Refused(),
		SaveRefusal:           gated.Refusal,
		Tests:                 gated.Tests,
		Differences:           gated.Differences,
		Findings:              report.Findings,
		Capabilities:          report.Capabilities,
		Connections:           report.Connections,
		Destinations:          report.Destinations,
		Tools:                 report.Tools,
		RefreshTargets:        report.RefreshTargets,
		DynamicConnections:    report.DynamicConnections,
		DynamicDestinations:   report.DynamicDestinations,
		DynamicRefreshTargets: report.DynamicRefreshTargets,
		DynamicTools:          report.DynamicTools,
		Library:               report.Library,
		Libraries:             report.Libraries,
		Note:                  draftview.IncompleteNote(report),
	})
}

// portalDryRunSource executes an edit as the caller and reports what it did.
//
// @Summary      Dry-run a script's source
// @Description  Executes Starlark for a script the caller owns, under the caller's own identity and persona and with tighter limits. It persists nothing by default: platform.export reports the shape of each output instead of writing it, and a platform.call that would persist is refused and named in refused_write. Send allow_writes to let the run write for real, and every write it makes is listed under writes. An empty source runs the script's saved code. The account of the run is kept, so a later reader can see that this exact source was executed, and by whom.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id     path  string        true   "Script ID"
// @Param        draft  body  draftRequest  false  "Source and parameter values"
// @Success      200  {object}  draftview.Response
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
	source := script.DraftSource(req.Source, sc)
	if detail := scriptlint.DraftRefusal(source, h.deps.Destinations); detail != "" {
		httpjson.WriteError(w, http.StatusBadRequest, detail)
		return
	}
	// Values bind against the LIVE record's contract, which is the contract the
	// source being run was written against.
	params, err := script.BindParams(sc.Params, req.Params)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	outcome, err := h.deps.Drafts.Run(r.Context(), scriptdraft.Request{
		Source: source, Name: sc.Name, Script: sc, Params: params,
		// The live state, so the draft reads what a platform run created now
		// would read; what it would have saved is reported, never written.
		State: h.liveState(r.Context(), sc),
		Identity: scriptdraft.Identity{
			UserID: user.UserID, Email: user.Email, Roles: user.Roles,
			AuthType: user.AuthType,
		},
		AllowWrites: req.AllowWrites,
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
	rendered := draftview.Of(outcome)
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
		StateWritten: ran.result.State,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to record a script dry run", "script", ran.script.Name, "error", err)
	}
}

// executedDraft is one draft run that happened: which script, which source,
// who ran it, and what came back.
type executedDraft struct {
	script *script.Script
	source string
	user   *PortalIdentity
	runID  string
	result draftview.Response
}
