package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The portal surface is the read half of managed scripts, for the people who
// own the automations rather than the administrators who approve them. Every
// other script route is admin-only, and a script's owner is frequently not an
// administrator: without these routes the humans the feature is for can read
// their own automations only by asking an agent to call a tool.
//
// It grants nothing. There is no mutation here, and approval stays where it
// was — on the admin surface, behind admin authentication — so a wider audience
// can read what already exists and still cannot change what executes.
//
// Two visibility rules apply, and they are different rules:
//
//   - What a script IS (the contract: name, owner, parameters, approval state,
//     cadence) follows script.VisibleTo, so anyone entitled to see the script
//     sees it.
//   - What a script DID and what it is made of (its runs, their logs, its
//     source, the capability grant bound to it) is the owner's and the
//     administrator's. A log is the script's own account of a working system,
//     and the grant names the connections it reaches; neither is implied by
//     being allowed to know the script exists.

// portalRunListLimit caps a portal run listing that names no limit. The store
// clamps to its own ceiling above this, so a caller cannot widen it.
const portalRunListLimit = 25

// PortalIdentity is the portal caller, resolved by the accessor the
// composition root injects. IsAdmin carries the administrator's unrestricted
// reach into this surface: an admin sees every script and every run, which is
// the same authority the admin API already gives them.
type PortalIdentity struct {
	UserID  string
	Email   string
	Persona string
	IsAdmin bool
}

// owner is the identity a script's owner is compared against: the caller's
// email, falling back to their user id when the credential carries no email
// (an OIDC token without an email claim). It is the same fallback the script
// tool applies when it records an owner, so a script authored through the tool
// is recognized here as the same person's.
//
// It is empty only for a caller the platform cannot name at all, and an empty
// identity matches no owner.
func (p *PortalIdentity) owner() string {
	if p.Email != "" {
		return p.Email
	}
	return p.UserID
}

// ContractReader composes one script's contract document: the record, the
// approved version's parameter contract and approval stamp, the cadence, and
// the last successful run. It is the same document a reference to a script
// resolves to (#1302), so the portal page and an agent's fetch describe a
// script identically.
type ContractReader interface {
	Contract(ctx context.Context, id string) (*script.Contract, error)
}

// LatestRunReader returns the most recent run of each named script, keyed by
// script id and omitting the scripts that have never run. It is a listing
// capability rather than a history one: the alternative is one query per row.
type LatestRunReader interface {
	LatestRuns(ctx context.Context, scriptIDs []string) (map[string]script.Run, error)
}

// RegisterPortal mounts the portal read routes, wrapped in the portal
// authentication middleware. Every handler goes through portalHandler, which
// resolves the caller and answers 401 once for all of them.
//
// The run routes are mounted only where the deployment keeps runs, and the
// detail route only where a contract can be composed; a deployment missing
// either serves the rest rather than failing per request.
func (h *Handler) RegisterPortal(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h.deps.PortalUser == nil {
		return
	}
	mux.Handle("GET /api/v1/portal/scripts", wrap(h.portalHandler(h.portalListScripts)))
	if h.deps.Contracts != nil {
		mux.Handle("GET /api/v1/portal/scripts/{id}", wrap(h.portalHandler(h.portalGetScript)))
	}
	mux.Handle("GET /api/v1/portal/scripts/{id}/versions", wrap(h.portalHandler(h.portalListVersions)))
	if h.deps.Runs == nil {
		return
	}
	mux.Handle("GET /api/v1/portal/scripts/{id}/runs", wrap(h.portalHandler(h.portalListRuns)))
	mux.Handle("GET /api/v1/portal/scripts/{id}/runs/{runID}", wrap(h.portalHandler(h.portalGetRun)))
}

// portalHandler adapts a portal handler by resolving the caller first,
// answering 401 when the request carries no user.
func (h *Handler) portalHandler(fn func(w http.ResponseWriter, r *http.Request, user *PortalIdentity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := h.deps.PortalUser(r)
		if user == nil {
			httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		fn(w, r, user)
	})
}

// portalRun is one run as a listing reports it: what it was, how it ended, and
// what it produced. The log is deliberately absent — it is read one run at a
// time, and a history of fifty logs is a payload nobody asked for.
type portalRun struct {
	ID         string     `json:"id" example:"run_a1b2c3d4"`
	Status     string     `json:"status" example:"succeeded"`
	Trigger    string     `json:"trigger" example:"schedule"`
	Version    int        `json:"version" example:"3"`
	FireTime   time.Time  `json:"fire_time"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMS int64      `json:"duration_ms" example:"1840"`
	// Error is why a failed run failed, carried into the listing because a
	// history whose failures say only "failed" sends every reader to the detail
	// view to learn the same thing.
	Error string `json:"error,omitempty"`
	// OutputCount counts what the run persisted. It is deliberately not called
	// "outputs": the run detail carries the outputs themselves under that name,
	// and one field meaning a count in one payload and a list in another is how
	// a client ends up rendering "3" where a link belongs.
	OutputCount int `json:"output_count" example:"1"`
	// RequestedBy is who asked for the run, empty for a scheduled one, which
	// nobody requested.
	RequestedBy string `json:"requested_by,omitempty" example:"jane@example.com"`
}

// summarizeRun projects a run for a listing.
func summarizeRun(r *script.Run) portalRun {
	return portalRun{
		ID:          r.ID,
		Status:      r.Status,
		Trigger:     r.Trigger,
		Version:     r.Version,
		FireTime:    r.FireTime,
		StartedAt:   r.StartedAt,
		FinishedAt:  r.FinishedAt,
		DurationMS:  r.Metrics.DurationMS,
		Error:       r.Error,
		OutputCount: len(r.Outputs),
		RequestedBy: r.RequestedBy,
	}
}

// portalScriptRow is one script in the portal listing: the record, the cadence
// it fires on, and the state of its most recent run.
type portalScriptRow struct {
	Script script.Script `json:"script"`
	// Schedule is the script's cadence, absent when it has none.
	Schedule *script.Schedule `json:"schedule,omitempty"`
	// LastRun is the most recent run of this script, absent when it has never
	// run and when the caller does not own it — a run is owner-and-admin
	// reading, and so is the fact that one failed.
	LastRun *portalRun `json:"last_run,omitempty"`
	// Owned reports whether this caller may read the script's runs, source, and
	// grant, so the page offers those surfaces rather than linking a reader to
	// a refusal.
	Owned bool `json:"owned" example:"true"`
}

// portalScriptListResponse is the portal listing payload.
type portalScriptListResponse struct {
	Data  []portalScriptRow `json:"data"`
	Total int               `json:"total" example:"3"`
}

// portalListScripts returns the scripts this caller may see.
//
// @Summary      List scripts visible to the portal caller
// @Description  Returns every managed script the caller is entitled to see, each with its cadence and, for the scripts they own, the state of its most recent run. Administrators see every script.
// @Tags         Scripts
// @Produce      json
// @Success      200  {object}  portalScriptListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts [get]
func (h *Handler) portalListScripts(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	scripts, err := h.deps.Scripts.List(r.Context(), portalListFilter(user))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list scripts")
		return
	}
	rows := make([]portalScriptRow, 0, len(scripts))
	for i := range scripts {
		rows = append(rows, portalScriptRow{Script: scripts[i], Owned: ownsScript(&scripts[i], user)})
	}
	h.attachSchedules(r.Context(), rows)
	h.attachLastRuns(r.Context(), rows)
	httpjson.WriteJSON(w, http.StatusOK, portalScriptListResponse{Data: rows, Total: len(rows)})
}

// portalListFilter applies the caller's visibility as a query predicate. An
// administrator carries no predicate, which is the unfiltered admin view.
func portalListFilter(user *PortalIdentity) script.ListFilter {
	if user.IsAdmin {
		return script.ListFilter{}
	}
	return script.ListFilter{VisibleTo: user.owner(), VisiblePersona: user.Persona}
}

// attachSchedules fills in each row's cadence in one query, leaving every row
// scheduleless when the deployment keeps no schedules or the read fails: a
// listing that cannot report a cadence is still a listing, and failing the
// whole page over it would be worse.
func (h *Handler) attachSchedules(ctx context.Context, rows []portalScriptRow) {
	if h.deps.Schedules == nil || len(rows) == 0 {
		return
	}
	// The limit is the number of scripts asked about rather than the store's
	// default, so a page of scripts can never outrun the schedule read: a
	// truncated answer here would render a scheduled script as "on demand",
	// which is a wrong statement rather than a missing decoration.
	ids := rowIDs(rows, false)
	schedules, err := h.deps.Schedules.ListSchedules(ctx, script.ScheduleFilter{ScriptIDs: ids, Limit: len(ids)})
	if err != nil {
		return
	}
	byScript := make(map[string]script.Schedule, len(schedules))
	for i := range schedules {
		byScript[schedules[i].ScriptID] = schedules[i]
	}
	for i := range rows {
		if s, ok := byScript[rows[i].Script.ID]; ok {
			rows[i].Schedule = &s
		}
	}
}

// attachLastRuns fills in the most recent run of each OWNED row in one query.
// The unowned rows are not asked about, so the query cannot return a run this
// caller may not read.
func (h *Handler) attachLastRuns(ctx context.Context, rows []portalScriptRow) {
	if h.deps.LatestRuns == nil {
		return
	}
	ids := rowIDs(rows, true)
	if len(ids) == 0 {
		return
	}
	latest, err := h.deps.LatestRuns.LatestRuns(ctx, ids)
	if err != nil {
		return
	}
	for i := range rows {
		run, ok := latest[rows[i].Script.ID]
		if !ok || !rows[i].Owned {
			continue
		}
		summary := summarizeRun(&run)
		rows[i].LastRun = &summary
	}
}

// rowIDs collects the listing's script ids, restricted to the owned rows when
// ownedOnly is set.
func rowIDs(rows []portalScriptRow, ownedOnly bool) []string {
	ids := make([]string, 0, len(rows))
	for i := range rows {
		if ownedOnly && !rows[i].Owned {
			continue
		}
		ids = append(ids, rows[i].Script.ID)
	}
	return ids
}

// portalScriptResponse is one script's detail payload.
type portalScriptResponse struct {
	Contract script.Contract `json:"contract"`
	Owned    bool            `json:"owned" example:"true"`
}

// portalGetScript returns one script's contract.
//
// @Summary      Get a script's contract
// @Description  Returns what the script is, what it takes, whether anything will execute it, on what cadence, and what it last produced. It is the same contract document a reference to the script resolves to.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  portalScriptResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id} [get]
func (h *Handler) portalGetScript(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	contract, err := h.deps.Contracts.Contract(r.Context(), r.PathValue(pathID))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get script")
		return
	}
	// A script the caller may not see is answered exactly as a script that does
	// not exist, so the difference cannot be used to learn that one exists.
	if contract == nil || (!user.IsAdmin && !contract.VisibleToAny(user.owner(), []string{user.Persona})) {
		httpjson.WriteError(w, http.StatusNotFound, errScriptNot)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, portalScriptResponse{
		Contract: *contract,
		Owned:    user.IsAdmin || ownsEmail(contract.OwnerEmail, user.owner()),
	})
}

// portalListVersions returns an owned script's version history.
//
// @Summary      List a script's versions
// @Description  Returns every version of a script the caller owns, with its source, its author, and the approval stamp and capability grant bound to it. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  versionListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/versions [get]
func (h *Handler) portalListVersions(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	versions, err := h.deps.Versions.ListVersions(r.Context(), sc.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errListVersions)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, versionListResponse{Data: versions, Total: len(versions)})
}

// portalRunListResponse is the run history payload.
type portalRunListResponse struct {
	Data  []portalRun `json:"data"`
	Total int         `json:"total" example:"12"`
}

// portalRunDetail is one run in full, as a reader needs it: what it was given,
// what it cost, what it wrote, and what it printed.
//
// It is a projection rather than the stored row. A run row is also a queue
// entry, and the lease fields on it — which worker holds it, until when — are
// how the platform recovers from a crash, not something a reader of a report's
// history has any use for. Returning the row whole would put worker hostnames
// on a page that is otherwise about a script.
type portalRunDetail struct {
	portalRun
	ScriptID     string         `json:"script_id"`
	ScheduledFor time.Time      `json:"scheduled_for"`
	Attempt      int            `json:"attempt"`
	Params       map[string]any `json:"params,omitempty"`
	// Log is the run's own account of itself, bounded at capture time, so
	// returning it whole is bounded too.
	Log          string             `json:"log,omitempty"`
	LogTruncated bool               `json:"log_truncated,omitempty"`
	Metrics      script.RunMetrics  `json:"metrics"`
	Outputs      []script.RunOutput `json:"outputs,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
}

// detailRun projects one run for the detail route.
func detailRun(r *script.Run) portalRunDetail {
	return portalRunDetail{
		portalRun:    summarizeRun(r),
		ScriptID:     r.ScriptID,
		ScheduledFor: r.ScheduledFor,
		Attempt:      r.Attempt,
		Params:       r.Params,
		Log:          r.Log,
		LogTruncated: r.LogTruncated,
		Metrics:      r.Metrics,
		Outputs:      r.Outputs,
		CreatedAt:    r.CreatedAt,
	}
}

// portalListRuns returns an owned script's run history, newest first.
//
// @Summary      List a script's runs
// @Description  Returns the run history of a script the caller owns, newest first: what each run was triggered by, how it ended, how long it took, and how many outputs it produced. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id        path   string  true   "Script ID"
// @Param        status    query  string  false  "Filter by run status"
// @Param        per_page  query  int     false  "Maximum rows to return"
// @Success      200  {object}  portalRunListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs [get]
func (h *Handler) portalListRuns(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, ok := h.ownedScript(w, r, user)
	if !ok {
		return
	}
	limit := httpjson.ParseLimit(r.URL.Query())
	if limit <= 0 {
		limit = portalRunListLimit
	}
	runs, err := h.deps.Runs.ListRuns(r.Context(), script.RunFilter{
		ScriptID: sc.ID,
		Status:   r.URL.Query().Get("status"),
		Limit:    limit,
	})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	out := make([]portalRun, 0, len(runs))
	for i := range runs {
		out = append(out, summarizeRun(&runs[i]))
	}
	httpjson.WriteJSON(w, http.StatusOK, portalRunListResponse{Data: out, Total: len(out)})
}

// portalGetRun returns one run in full, including the log it captured.
//
// @Summary      Get one script run
// @Description  Returns one run with its parameters, metrics, outputs, and the bounded log the run captured. Restricted to the script's owner, to administrators, and to whoever requested that run.
// @Tags         Scripts
// @Produce      json
// @Param        id     path  string  true  "Script ID"
// @Param        runID  path  string  true  "Run ID"
// @Success      200  {object}  portalRunDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/runs/{runID} [get]
func (h *Handler) portalGetRun(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	sc, err := h.deps.Scripts.GetByID(r.Context(), r.PathValue(pathID))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get script")
		return
	}
	if sc == nil {
		httpjson.WriteError(w, http.StatusNotFound, errScriptNot)
		return
	}
	run, err := h.deps.Runs.GetRun(r.Context(), r.PathValue(pathRunID))
	if errors.Is(err, script.ErrRunNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, errRunNot)
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get run")
		return
	}
	// A run id is unguessable, but unguessable is not an authorization rule.
	// The run must belong to the script in the path, and the caller must be
	// entitled to it: the script's owner, an administrator, or whoever asked
	// for this particular run — the result was handed to them when they
	// requested it, so a run they cannot re-read is an id they cannot follow.
	if run.ScriptID != sc.ID || (!ownsScript(sc, user) && !ownsEmail(run.RequestedBy, user.owner())) {
		httpjson.WriteError(w, http.StatusNotFound, errRunNot)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, detailRun(run))
}

// ownedScript resolves the script named by the path and refuses a caller who
// does not own it, writing the error response either way.
//
// Not-yours and does-not-exist are the same answer, for the same reason the
// tool surface gives them the same one: the difference would tell a caller that
// a script they may not read exists.
func (h *Handler) ownedScript(w http.ResponseWriter, r *http.Request, user *PortalIdentity) (*script.Script, bool) {
	sc, err := h.deps.Scripts.GetByID(r.Context(), r.PathValue(pathID))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to get script")
		return nil, false
	}
	if sc == nil || !ownsScript(sc, user) {
		httpjson.WriteError(w, http.StatusNotFound, errScriptNot)
		return nil, false
	}
	return sc, true
}

// ownsScript reports whether the caller may read a script's runs, source, and
// grant: its owner, or an administrator.
func ownsScript(sc *script.Script, user *PortalIdentity) bool {
	return user.IsAdmin || ownsEmail(sc.OwnerEmail, user.owner())
}

// ownsEmail compares an owner with a caller, requiring both to be identified.
// A script whose owner could not be established would otherwise be owned by
// every caller the portal cannot name either, which is the same
// empty-matches-empty hole the scope rule closes.
func ownsEmail(ownerEmail, caller string) bool {
	return ownerEmail != "" && ownerEmail == caller
}
