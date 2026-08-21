package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The portal surface is managed scripts for the people who own the
// automations. A script's owner is frequently not an administrator: without
// these routes the humans the feature is for can work with their own
// automations only by asking an agent to call a tool.
//
// One visibility rule applies throughout: a script is its owner's. What it is,
// what it did, and what it is made of are all read by the person who owns it
// and by an administrator, and by nobody else.

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
	// Roles is the authority this caller holds. It is recorded on any version
	// they author (#1307): a run of that version presents exactly these roles,
	// which is what keeps a script from holding more access than the person
	// who wrote it.
	Roles []string
	// AuthType is HOW this caller was authenticated. It matters because the
	// dry-run route opens a session on their behalf (#1364): that session must
	// present the authentication the request actually arrived with rather than
	// a kind no authenticator issues.
	AuthType string
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

// ContractReader composes one script's contract document: the record, its
// parameter contract, the cadence, and the last successful run. It is the same
// document a reference to a script resolves to (#1302), so the portal page and
// an agent's fetch describe a script identically.
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
// The run routes are mounted only where the deployment keeps runs, the detail
// route only where a contract can be composed, and the schedule routes only
// where the deployment keeps schedules; a deployment missing any of them serves
// the rest rather than failing per request.
func (h *Handler) RegisterPortal(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h.deps.PortalUser == nil {
		return
	}
	mux.Handle("GET /api/v1/portal/scripts", wrap(h.portalHandler(h.portalListScripts)))
	if h.deps.Contracts != nil {
		mux.Handle("GET /api/v1/portal/scripts/{id}", wrap(h.portalHandler(h.portalGetScript)))
	}
	mux.Handle("GET /api/v1/portal/scripts/{id}/versions", wrap(h.portalHandler(h.portalListVersions)))
	// Editing the code is the owner's, and it crosses the same edit funnel the
	// tool crosses (#1307).
	mux.Handle("PUT /api/v1/portal/scripts/{id}/source", wrap(h.portalHandler(h.portalSetSource)))
	// Documenting the script is the owner's too: what a script SAYS about
	// itself is not what it does (#1369).
	mux.Handle("PUT /api/v1/portal/scripts/{id}/metadata", wrap(h.portalHandler(h.portalSetMetadata)))
	// Moving it to somebody else is an administrator's, and the handler is what
	// refuses everybody else: it is mounted here because it is the same detail
	// page, not because every caller of that page may use it (#1404).
	mux.Handle("PUT /api/v1/portal/scripts/{id}/owner", wrap(h.portalHandler(h.portalTransferOwner)))
	// Checking an edit before saving it (#1364). Validating parses and
	// reports; it executes nothing, needs no collaborator, and is therefore
	// always available where the editor is.
	mux.Handle("POST /api/v1/portal/scripts/{id}/validate", wrap(h.portalHandler(h.portalValidateSource)))
	if h.deps.Drafts != nil {
		mux.Handle("POST /api/v1/portal/scripts/{id}/dry-run", wrap(h.portalHandler(h.portalDryRunSource)))
	}
	if h.deps.Connections != nil {
		mux.Handle("GET /api/v1/portal/scripts/{id}/connections", wrap(h.portalHandler(h.portalScriptConnections)))
	}
	if h.deps.Schedules != nil {
		h.registerPortalSchedules(mux, wrap)
	}
	if h.deps.Runs == nil {
		return
	}
	// A literal segment outranks the {id} wildcard, so a script whose id is
	// "runs" cannot shadow the caller's cross-script run listing (#1405).
	mux.Handle("GET /api/v1/portal/scripts/runs", wrap(h.portalHandler(h.portalListOwnRuns)))
	mux.Handle("GET /api/v1/portal/scripts/{id}/runs", wrap(h.portalHandler(h.portalListRuns)))
	mux.Handle("GET /api/v1/portal/scripts/{id}/runs/{runID}", wrap(h.portalHandler(h.portalGetRun)))
	// Running one now (#1363). It is mounted with the history because it is the
	// same store: what a run IS and what a run DID are one record.
	mux.Handle("POST /api/v1/portal/scripts/{id}/runs", wrap(h.portalHandler(h.portalRunScript)))
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
	// Schedule is the script's cadence, absent when it has none. On a row this
	// caller does not own it carries the cadence alone (see reportableSchedule).
	Schedule *script.Schedule `json:"schedule,omitempty"`
	// LastRun is the most recent run of this script, absent when it has never
	// run and when the caller does not own it — a run is owner-and-admin
	// reading, and so is the fact that one failed.
	LastRun *portalRun `json:"last_run,omitempty"`
	// Owned reports whether this caller may read the script's runs and source,
	// so the page offers those surfaces rather than linking a reader to a
	// refusal.
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
// @Description  Returns every managed script the caller is entitled to see, each with its cadence and, for the scripts they own, the state of its most recent run. Administrators see every script. The category, tag and search parameters narrow the listing; tag may be repeated, and a script matching any of the named tags is returned.
// @Tags         Scripts
// @Produce      json
// @Param        category  query  string    false  "Narrow to one category slug"
// @Param        tag       query  []string  false  "Narrow to the scripts carrying any of these tags"  collectionFormat(multi)
// @Param        search    query  string    false  "Narrow to the scripts whose name, display name or description contains this text"
// @Success      200  {object}  portalScriptListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts [get]
func (h *Handler) portalListScripts(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	scripts, err := h.deps.Scripts.List(r.Context(), portalListFilter(user, r.URL.Query()))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list scripts")
		return
	}
	rows := make([]portalScriptRow, 0, len(scripts))
	for i := range scripts {
		owned := ownsScript(&scripts[i], user)
		rows = append(rows, portalScriptRow{Script: reportableScript(scripts[i], owned), Owned: owned})
	}
	h.attachSchedules(r.Context(), rows)
	h.attachLastRuns(r.Context(), rows)
	httpjson.WriteJSON(w, http.StatusOK, portalScriptListResponse{Data: rows, Total: len(rows)})
}

// reportableScript is a script row as its reader may have it: complete, except
// that a row the reader does not own carries no SOURCE. Reading the code is
// what the version history and the editor are for, and both are the owner's and
// the administrator's.
//
// The predicate already limits a non-admin to their own scripts, so the guard
// bites only where a row could arrive unowned — an administrator's unfiltered
// listing, where owned is true and the source is theirs to read. It is kept as
// the listing's own answer to the question rather than as an inference from the
// predicate: a listing that grew a second population would otherwise hand out
// code silently. The tool listing projects its fields and never carried it.
func reportableScript(sc script.Script, owned bool) script.Script {
	if !owned {
		sc.Source = ""
	}
	return sc
}

// nonEmpty drops the empty values from a repeated query parameter, so a request
// carrying `?tag=` reads as one that named no tag rather than as one asking for
// the scripts tagged with the empty string — a predicate nothing satisfies,
// which would answer a URL that looks unfiltered with an empty listing. It is
// what Query.Get already does for the single-valued category beside it.
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// portalListFilter applies the caller's visibility as a query predicate, plus
// the category and tag axes the listing filters on (#1369) and the free-text
// search the filter bar types into (#1405). The search runs in the store,
// against the name, display name and description, so it covers every script
// the caller owns rather than the page of them a listing happens to hold.
//
// An administrator carries no visibility predicate, which is the unfiltered
// admin view; the three narrowing axes apply to them exactly as they do to
// everybody else, because those narrow what a reader asked for rather than
// what they are entitled to.
//
// A nil query names no axis, which is how a caller asks for the visibility
// predicate alone.
func portalListFilter(user *PortalIdentity, query url.Values) script.ListFilter {
	filter := script.ListFilter{
		Category: query.Get("category"),
		Tags:     nonEmpty(query["tag"]),
		Search:   query.Get("search"),
	}
	if user.IsAdmin {
		return filter
	}
	filter.OwnerEmail = user.owner()
	return filter
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
			rows[i].Schedule = reportableSchedule(s, rows[i].Owned)
		}
	}
}

// reportableSchedule is a cadence as the row's reader may have it. The cadence
// itself is contract-level — it is in the contract document every surface
// serves — but the values every fire BINDS are not: they are what the owner
// configured this automation to ask about, and a caller entitled only to know
// that the script exists is not entitled to them. They are dropped for a row
// this caller does not own, along with the stamps of who changed the cadence.
func reportableSchedule(s script.Schedule, owned bool) *script.Schedule {
	if owned {
		return &s
	}
	return &script.Schedule{
		ID: s.ID, ScriptID: s.ScriptID,
		CronSpec: s.CronSpec, Timezone: s.Timezone,
		Enabled: s.Enabled, NextRunAt: s.NextRunAt,
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
	// Source is the live script's code, present only for the owner and an
	// administrator: it is what the editor on that page opens (#1307). The
	// contract document deliberately does not carry it, because that document
	// is what a reference to the script resolves to.
	Source string `json:"source,omitempty"`
	// DraftParams is the LIVE record's parameter contract, which is not always
	// the contract above only in freshness: it is read with the source, so the
	// dry-run form binds against exactly the contract the code beside it was
	// written against (#1364). It travels with the source for the same
	// audience and for the same reason.
	DraftParams []script.Param `json:"draft_params,omitempty"`
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
	if contract == nil || (!user.IsAdmin && !contract.OwnedBy(user.owner())) {
		httpjson.WriteError(w, http.StatusNotFound, errScriptNot)
		return
	}
	owned := user.IsAdmin || ownsEmail(contract.OwnerEmail, user.owner())
	source, draftParams := h.liveRecord(r, contract.ID, owned)
	httpjson.WriteJSON(w, http.StatusOK, portalScriptResponse{
		Contract:    *contract,
		Owned:       owned,
		Source:      source,
		DraftParams: draftParams,
	})
}

// liveRecord is the script's current code and the parameter contract that
// code was written against, read for the owner alone. A read that fails leaves
// the editor closed rather than failing the whole page: the contract above it
// is still worth showing.
func (h *Handler) liveRecord(r *http.Request, id string, owned bool) (string, []script.Param) {
	if !owned {
		return "", nil
	}
	sc, err := h.deps.Scripts.GetByID(r.Context(), id)
	if err != nil || sc == nil {
		return "", nil
	}
	return sc.Source, sc.Params
}

// portalListVersions returns an owned script's version history.
//
// @Summary      List a script's versions
// @Description  Returns every version of a script the caller owns, with its source, its author, and the roles that author held. Restricted to the script's owner and to administrators.
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

// portalScriptRun is one run in the caller's cross-script listing. It names
// the script it belongs to twice over — the id a link is built from, and the
// name a person reads — because a listing that spans scripts is unreadable
// without the second and unnavigable without the first.
type portalScriptRun struct {
	portalRun
	ScriptID string `json:"script_id" example:"script_a1b2c3d4"`
	// ScriptName is the script's display name, falling back to its name. It is
	// empty only for a run whose script is outside the listing this answer
	// resolved names from, and a client shows the id in that case.
	ScriptName string `json:"script_name,omitempty" example:"Daily Sales Report"`
}

// portalOwnRunsResponse is the cross-script run listing payload.
type portalOwnRunsResponse struct {
	Data  []portalScriptRun `json:"data"`
	Total int               `json:"total" example:"12"`
	// Limit is the cap this answer was read under, so a client that filled it
	// can say there is older history behind it rather than presenting a
	// truncated listing as the whole of it.
	Limit int `json:"limit" example:"50"`
}

// portalOwnRunsLimit caps a cross-script listing that names no limit. It is the
// store's own ceiling rather than a larger number: the store clamps anything
// above it, so asking for more would report a listing as complete while
// returning part of it.
const portalOwnRunsLimit = 50

// portalListOwnRuns returns the caller's runs across every script they own,
// newest first (#1405).
//
// The per-script history answers "how is this report going". This answers the
// other question an owner has — how are my scripts going, all of them — which
// they previously could only ask by opening each script in turn. Naming one
// script narrows it back down (#1407), which is the listing a metric that names
// a script links to.
//
// Visibility is the listing's own: a caller reads the runs of the scripts they
// own, and an administrator reads every run, which is the same reach their
// script listing already has. The owned set is bound into the query rather
// than filtered out of the answer, so a run this caller may not read is never
// fetched.
//
// The owned set is read under the script store's own listing cap, so a caller
// holding more scripts than that cap reads the runs of the most recently
// updated of them. That is far beyond any real personal collection, and the
// alternative — an unbounded id list bound into every run query — is a worse
// answer to a case nobody is in.
//
// @Summary      List the caller's script runs
// @Description  Returns recent runs across every script the caller owns, newest first, with what triggered each one, how it ended, why it failed, and which script it belongs to. Administrators see the runs of every script.
// @Tags         Scripts
// @Produce      json
// @Param        status     query  string  false  "Filter by run status"
// @Param        script_id  query  string  false  "Narrow the listing to one script"
// @Param        per_page   query  int     false  "Maximum rows to return"
// @Success      200  {object}  portalOwnRunsResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/runs [get]
func (h *Handler) portalListOwnRuns(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	// The visibility predicate alone: the facet axes narrow which scripts a
	// reader asked to see, and a run listing is not filtered by them.
	scripts, err := h.deps.Scripts.List(r.Context(), portalListFilter(user, nil))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list scripts")
		return
	}
	limit := httpjson.ParseLimit(r.URL.Query())
	if limit <= 0 || limit > portalOwnRunsLimit {
		limit = portalOwnRunsLimit
	}
	filter := script.RunFilter{
		// ScriptID narrows the listing to one script (#1407), which is what a
		// metric that names a script links to. The store ANDs it with the
		// visibility predicate below rather than replacing it, so naming a
		// script the caller may not read answers with nothing rather than with
		// its runs.
		ScriptID: r.URL.Query().Get("script_id"),
		Status:   r.URL.Query().Get("status"),
		Limit:    limit,
	}
	if !user.IsAdmin {
		// A non-nil, empty set is the answer for a caller who owns nothing: it
		// matches no run, where a nil set would list every run on the platform.
		filter.ScriptIDs = scriptIDs(scripts)
	}
	runs, err := h.deps.Runs.ListRuns(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	names := scriptNames(scripts)
	out := make([]portalScriptRun, 0, len(runs))
	for i := range runs {
		out = append(out, portalScriptRun{
			portalRun:  summarizeRun(&runs[i]),
			ScriptID:   runs[i].ScriptID,
			ScriptName: names[runs[i].ScriptID],
		})
	}
	httpjson.WriteJSON(w, http.StatusOK, portalOwnRunsResponse{
		Data: out, Total: len(out), Limit: limit,
	})
}

// scriptIDs collects a listing's ids, always non-nil so an empty listing binds
// an empty set rather than reading as an unfiltered one.
func scriptIDs(scripts []script.Script) []string {
	ids := make([]string, 0, len(scripts))
	for i := range scripts {
		ids = append(ids, scripts[i].ID)
	}
	return ids
}

// scriptNames maps a listing's ids to what a person calls each script.
func scriptNames(scripts []script.Script) map[string]string {
	names := make(map[string]string, len(scripts))
	for i := range scripts {
		name := scripts[i].DisplayName
		if name == "" {
			name = scripts[i].Name
		}
		names[scripts[i].ID] = name
	}
	return names
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

// ownsScript reports whether the caller may read a script's runs and source:
// its owner, or an administrator.
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
