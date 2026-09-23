// Package outputshttp serves the outputs of the script runs a caller asked for
// (#1848): an application that runs scripts for its users reads back what
// those runs wrote, across scripts, filtered by script, tag and metadata, each
// with a short-lived link that downloads it.
//
// It is mounted by scripthttp, which resolves the caller. A caller reads the
// outputs of the runs it requested, whoever owns the scripts, because the run
// already handed them to it; an administrator reads every run's.
package outputshttp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Route is where the listing is served. A literal segment outranks the {id}
// wildcard, so no script id can shadow it.
const Route = "GET /api/v1/portal/scripts/runs/outputs"

// maxRuns caps how many of the caller's most recent runs are read.
const maxRuns = 50

// RunLister reads run history.
type RunLister interface {
	ListRuns(ctx context.Context, filter script.RunFilter) ([]script.Run, error)
}

// Deps carries the run history, the caller, and the link minter.
type Deps struct {
	Runs RunLister
	// Caller is who is asking, or ok=false after writing the refusal. An
	// administrator reads every run's outputs.
	Caller func(w http.ResponseWriter, r *http.Request) (requester string, isAdmin, ok bool)
	// ContentURL mints a signed link to one asset version, and its expiry.
	// Nil leaves outputs without a link.
	ContentURL func(assetID string, version int) (string, time.Time)
}

// Handler serves the listing.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the route, wrapped in the portal authentication middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle(Route, wrap(http.HandlerFunc(h.list)))
}

// runOutput is one output of one run.
type runOutput struct {
	RunID      string           `json:"run_id" example:"dpx_a1b2c3d4"`
	ScriptID   string           `json:"script_id"`
	Version    int              `json:"version" example:"3"`
	Params     map[string]any   `json:"params,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Output     script.RunOutput `json:"output"`
	// ContentURL downloads this version without a session until
	// ContentURLExpiresAt; absent for an output that is not a portal asset.
	ContentURL          string     `json:"content_url,omitempty"`
	ContentURLExpiresAt *time.Time `json:"content_url_expires_at,omitempty"`
}

// listResponse is the listing.
type listResponse struct {
	Data  []runOutput `json:"data"`
	Total int         `json:"total" example:"4"`
}

// filter is what a listing narrows by.
type filter struct {
	tags     []string
	metadata map[string]string
}

// list returns the outputs of the caller's runs.
//
// @Summary      List the outputs of the caller's script runs
// @Description  Returns the portal outputs of the script runs the caller requested, newest first, across every script, each with the run's parameters and an expiring signed URL that downloads that exact version without a session. A caller need not own the scripts: a grantee reads the outputs of the runs it started. Administrators read every run's. script_id narrows to one script; tag (repeatable) and metadata.<key>=<value> narrow to outputs whose platform.export named them, all combined with AND. Reads the caller's 50 most recent finished runs.
// @Tags         Scripts
// @Produce      json
// @Param        script_id     query  string    false  "Narrow to one script"
// @Param        tag           query  []string  false  "Require these tags"  collectionFormat(multi)
// @Param        metadata.key  query  string    false  "Require this metadata value, as metadata.<key>=<value>"
// @Success      200  {object}  listResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/runs/outputs [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	requester, isAdmin, ok := h.deps.Caller(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	runFilter := script.RunFilter{ScriptID: q.Get("script_id"), Status: script.RunStatusSucceeded, Limit: maxRuns}
	if !isAdmin {
		runFilter.RequestedBy = requester
	}
	if runFilter.RequestedBy == "" && !isAdmin {
		httpjson.WriteJSON(w, http.StatusOK, listResponse{Data: []runOutput{}})
		return
	}
	runs, err := h.deps.Runs.ListRuns(r.Context(), runFilter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	f := filter{tags: nonEmpty(q["tag"]), metadata: metadataParams(q)}
	out := make([]runOutput, 0)
	for i := range runs {
		for _, o := range runs[i].Outputs {
			if o.AssetID == "" || !f.matches(o) {
				continue
			}
			out = append(out, h.project(&runs[i], o))
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, listResponse{Data: out, Total: len(out)})
}

// project is one output as the listing reports it, with its link.
func (h *Handler) project(run *script.Run, o script.RunOutput) runOutput {
	item := runOutput{
		RunID: run.ID, ScriptID: run.ScriptID, Version: run.Version,
		Params: run.Params, FinishedAt: run.FinishedAt, Output: o,
	}
	if h.deps.ContentURL != nil {
		link, expires := h.deps.ContentURL(o.AssetID, o.AssetVersion)
		item.ContentURL, item.ContentURLExpiresAt = link, &expires
	}
	return item
}

// matches reports whether an output carries every tag and metadata value.
func (f filter) matches(o script.RunOutput) bool {
	for _, t := range f.tags {
		if !slices.Contains(o.Tags, t) {
			return false
		}
	}
	for k, want := range f.metadata {
		got, ok := o.Metadata[k]
		if !ok || fmt.Sprint(got) != want {
			return false
		}
	}
	return true
}

// metadataParams reads metadata.<key>=<value>.
func metadataParams(q url.Values) map[string]string {
	out := map[string]string{}
	for param, values := range q {
		if key, ok := strings.CutPrefix(param, "metadata."); ok && key != "" && len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

// nonEmpty drops blank values.
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
