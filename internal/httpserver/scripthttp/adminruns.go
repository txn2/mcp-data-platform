package scripthttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The operator's view of what the platform has been running unattended (#1307).
//
// Every other run listing is scoped to one script, which answers "how is this
// report going". An administrator has the other question — how are the
// automations going, all of them — and until now the only way to ask it was to
// query the run table. The answer is one listing across every script, beside
// the metrics series the same runs emit (script_runs_total and friends), which
// is what the charts on that page read.
//
// It is admin-only by mounting: this file registers on the admin mux, behind
// the admin authentication the composition root wraps it in.

// adminRunListLimit caps a listing that names no limit. It is the store's own
// ceiling (scriptstore.defaultRunListLimit) rather than a larger number: the
// store clamps anything above it, so asking for more would report a listing as
// complete while silently returning half of it.
const adminRunListLimit = 50

// adminRun is one run in the operator's listing. It carries the script id the
// run belongs to, because a listing across scripts is unreadable without it;
// the page resolves the id to a name from the script listing it already holds,
// rather than this route joining a name onto every row.
type adminRun struct {
	portalRun
	ScriptID string `json:"script_id"`
}

// adminRunListResponse is the operator run-listing payload.
type adminRunListResponse struct {
	Data  []adminRun `json:"data"`
	Total int        `json:"total" example:"42"`
	// Limit is the cap this answer was read under. A page that filled it has
	// older runs behind it, and saying so is the difference between a listing
	// that ended and one that was truncated.
	Limit int `json:"limit" example:"50"`
}

// listRuns returns recent runs across every script.
//
// @Summary      List managed-script runs
// @Description  Returns recent runs across every script, newest first, with what triggered each one, how it ended, how long it took, and how many outputs it produced. The operator view of what the platform has been running unattended.
// @Tags         Scripts
// @Produce      json
// @Param        status    query  string  false  "Filter by run status"
// @Param        per_page  query  int     false  "Maximum rows to return"
// @Success      200  {object}  adminRunListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/runs [get]
func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := httpjson.ParseLimit(r.URL.Query())
	if limit <= 0 || limit > adminRunListLimit {
		limit = adminRunListLimit
	}
	// An empty ScriptID is the store's own "across every script", which is the
	// whole point of this listing.
	runs, err := h.deps.Runs.ListRuns(r.Context(), script.RunFilter{
		Status: r.URL.Query().Get("status"),
		Limit:  limit,
	})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	out := make([]adminRun, 0, len(runs))
	for i := range runs {
		out = append(out, adminRun{portalRun: summarizeRun(&runs[i]), ScriptID: runs[i].ScriptID})
	}
	// Limit is echoed so a caller can tell a listing that ended from one that
	// was cut off: len(out) == Limit means there is more history behind it.
	httpjson.WriteJSON(w, http.StatusOK, adminRunListResponse{
		Data: out, Total: len(out), Limit: limit,
	})
}
