package flowhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow/flowrun"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxRunCalls bounds the audited calls one run's overlay reads. A run that made
// more is drawn from the first ones, and says so.
const maxRunCalls = 10000

// maxArgumentBytes bounds the arguments one call carries in the overlay.
const maxArgumentBytes = 2048

// sourceScript is the audit source a managed script's own calls carry.
const sourceScript = "script"

// AuditQuerier reads audit events; it is the read half of the audit store.
type AuditQuerier interface {
	Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error)
}

// runFlowResponse is one run drawn on its version's diagram (#1907).
type runFlowResponse struct {
	ScriptID string `json:"script_id" example:"script_a1b2c3"`
	RunID    string `json:"run_id" example:"dpx_a1b2c3d4"`
	// Version is the version the run executed, whose diagram Graph is.
	Version int    `json:"version" example:"3"`
	Status  string `json:"status" example:"failed"`
	// Cause is why a failed run failed (script, upstream, memory, worker_lost,
	// platform, state_conflict), and Error its message.
	Cause string           `json:"cause,omitempty" example:"upstream"`
	Error string           `json:"error,omitempty"`
	Graph scriptflow.Graph `json:"graph"`
	flowrun.Overlay
	// CallsTruncated is true when the run made more calls than one overlay
	// reads; the first ones are drawn.
	CallsTruncated bool `json:"calls_truncated"`
}

// registerRunFlow mounts the portal route that draws one run, wrapped in the
// portal authentication middleware.
func (h *Handler) registerRunFlow(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/runs/{runID}/flow", wrap(http.HandlerFunc(h.runFlow)))
}

// runFlow draws one run on the diagram of the version it executed.
//
// @Summary      Draw a script run on its flow diagram
// @Description  Returns the diagram of the version a run executed, with each step's part in that run: how many of the run's audited tool calls it made and how long they took, the outputs it wrote, whether the run reached it, and the step the run failed at. Each call on the timeline carries the arguments its audit row recorded, cut at 2048 bytes, for the script's owner and administrators; whoever requested the run sees the calls without them. Calls are attributed by where in the script they were made; a call no step made (a computed tool, a call made before call sites were recorded) is listed in other_calls, so the steps' calls and other_calls always add up to calls. Readable by the script's owner, an administrator, and whoever requested the run.
// @Tags         Scripts
// @Produce      json
// @Param        id     path  string  true  "Script ID"
// @Param        runID  path  string  true  "Run ID"
// @Success      200  {object}  runFlowResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs/{runID}/flow [get]
func (h *Handler) runFlow(w http.ResponseWriter, r *http.Request) {
	if h.deps.SignedIn == nil || !h.deps.SignedIn(r) {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	run, ok := h.deps.Run(w, r)
	if !ok {
		return
	}
	v, err := h.deps.Version(r.Context(), run.ScriptID, run.Version)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the version the run executed")
		return
	}
	if v == nil {
		httpjson.WriteError(w, http.StatusNotFound, errVersionNotFound)
		return
	}
	withArgs := h.deps.ActsOn != nil && h.deps.ActsOn(r, run.ScriptID)
	calls, truncated, err := h.runCalls(r.Context(), run, withArgs)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the run's calls")
		return
	}
	g := scriptflow.Derive(v.Source)
	httpjson.WriteJSON(w, http.StatusOK, runFlowResponse{
		ScriptID: run.ScriptID, RunID: run.ID, Version: run.Version, Status: run.Status,
		Cause: run.Cause, Error: run.Error, Graph: g,
		Overlay:        flowrun.Draw(g, calls, runFacts(run)),
		CallsTruncated: truncated,
	})
}

// runCalls reads the run's audited calls, with the arguments each was sent
// when withArgs: a script's calls are audited under
// the run id as their session (#1284). The window starts where the run was
// created, which keeps the read to the partitions the run can be in.
func (h *Handler) runCalls(ctx context.Context, run *script.Run, withArgs bool) ([]flowrun.Call, bool, error) {
	if h.deps.Audit == nil {
		return []flowrun.Call{}, false, nil
	}
	start := run.CreatedAt.Add(-time.Minute)
	events, err := h.deps.Audit.Query(ctx, audit.QueryFilter{
		SessionID: run.ID, Source: sourceScript, StartTime: &start,
		SortBy: "timestamp", SortOrder: audit.SortAsc, Limit: maxRunCalls + 1,
	})
	if err != nil {
		return nil, false, err //nolint:wrapcheck // answered as a 500 without its text
	}
	truncated := len(events) > maxRunCalls
	if truncated {
		events = events[:maxRunCalls]
	}
	calls := make([]flowrun.Call, 0, len(events))
	for _, e := range events {
		// The run's own lifecycle event shares its session; it records that the
		// run happened, not a call it made.
		if e.EventKind == audit.EventTypeScriptRun {
			continue
		}
		var (
			args string
			cut  bool
		)
		if withArgs {
			args, cut = argumentsText(e.Parameters)
		}
		calls = append(calls, flowrun.Call{
			CallSite: e.CallSite, Tool: e.ToolName, DurationMS: e.DurationMS,
			Success: e.Success, Error: e.ErrorMessage, ResponseChars: e.ResponseChars, At: e.Timestamp,
			Arguments: args, ArgumentsTruncated: cut,
		})
	}
	return calls, truncated, nil
}

// argumentsText is a call's audited arguments as JSON, cut at
// maxArgumentBytes on a character boundary, and whether it was cut. A run
// reads up to maxRunCalls calls, so one call's arguments are bounded rather
// than each carrying a whole document it was sent.
func argumentsText(params map[string]any) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	b, err := json.Marshal(params)
	if err != nil {
		return "", false
	}
	if len(b) <= maxArgumentBytes {
		return string(b), false
	}
	cut := maxArgumentBytes
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return string(b[:cut]), true
}

// runFacts is what the run record says the overlay needs.
func runFacts(run *script.Run) flowrun.RunFacts {
	return flowrun.RunFacts{
		Status: run.Status, Cause: run.Cause, Error: run.Error, Outputs: run.Outputs,
		StateSaved: run.StateWritten != nil, HasResult: len(run.Result) > 0,
		StartedAt: deref(run.StartedAt), FinishedAt: deref(run.FinishedAt),
	}
}

// deref is a time the run record may not have yet, as the zero time.
func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
