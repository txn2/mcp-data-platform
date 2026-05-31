package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// indexJobsListDefaultLimit is the job-list page size when the caller
// supplies none. indexJobsListMaxLimit caps an explicit limit so a
// drill-down cannot pull an unbounded result set (mirrors the queue
// store's own clamp).
const (
	indexJobsListDefaultLimit = 50
	indexJobsListMaxLimit     = 500
)

// indexJobsSummaryResponse is the cross-kind health payload the
// Indexing dashboard renders on load: the embedding-provider banner
// plus one row per registered kind.
type indexJobsSummaryResponse struct {
	// Provider is the embedding-provider health (configured / model /
	// dimension / status). A degraded provider makes every index
	// meaningless, so the dashboard shows it as a banner.
	Provider embeddingProviderStatusResponse `json:"provider"`
	// Kinds is one summary row per registered index_jobs consumer,
	// sorted by kind. Empty when no queue is wired (no database or no
	// configured provider) or no consumer registered.
	Kinds []indexKindSummary `json:"kinds"`
}

// indexKindSummary is one registered kind's health verdict, job-state
// rollup, last activity, and optional coverage.
type indexKindSummary struct {
	Kind string `json:"kind"`
	// Verdict is the plain-language health state the dashboard leads
	// with: "healthy", "indexing", or "degraded". Computed server-side
	// from counts + coverage so the UI renders one word instead of
	// reconciling three independent metric families. "healthy" is the
	// single resting state for any fully-indexed, quiescent, failure-free
	// kind regardless of job history; recency is carried by LastActivity.
	Verdict string `json:"verdict"`
	Pending int    `json:"pending"`
	Running int    `json:"running"`
	// Succeeded / Failed are per-unit latest-status counts ("N units
	// whose last run was X"), NOT job counts. The UI labels them as
	// such so "1 succeeded" no longer reads as a one-job history.
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	// UnresolvedFailures is the number of distinct units with an open
	// failed job. It is the verdict's "degraded" signal and the count
	// the triage panel badge shows, distinct from Failed (which still
	// counts units whose latest row is a dismissed/superseded failure).
	UnresolvedFailures int `json:"unresolved_failures"`
	// LastActivity is the most recent job's activity timestamp
	// (completed, else started, else created), RFC3339, omitted when
	// the kind has no jobs yet (e.g. vectors seeded outside the queue).
	// The UI renders recency from this; its absence means "no job has
	// run", which on a fully-indexed kind reads as up to date, not
	// "never".
	LastActivity *string `json:"last_activity,omitempty"`
	// Coverage is the indexed-vs-expected rollup, omitted for kinds
	// whose Sink reports none.
	Coverage *indexCoverageResponse `json:"coverage,omitempty"`
}

// failedUnitResponse is one unit on the failure-triage surface: the
// latest open failure plus the timestamps and last-success context an
// operator needs to tell a live incident from a stale tombstone.
type failedUnitResponse struct {
	SourceKind string `json:"source_kind"`
	SourceID   string `json:"source_id"`
	// LatestJobID is the row the UI drills into for the un-redacted
	// error and the job's timeline.
	LatestJobID int64  `json:"latest_job_id"`
	LastError   string `json:"last_error,omitempty"`
	Attempts    int    `json:"attempts"`
	// Occurrences is how many open failed rows the unit has (>1 means it
	// failed, was retried, and failed again without an intervening
	// success).
	Occurrences   int    `json:"occurrences"`
	FirstFailedAt string `json:"first_failed_at,omitempty"`
	LastFailedAt  string `json:"last_failed_at,omitempty"`
	// LastSucceededAt is the unit's most recent success, omitted when it
	// has never succeeded, so the UI can show "last succeeded Xm ago".
	LastSucceededAt *string `json:"last_succeeded_at,omitempty"`
}

// dismissRequest is the POST /index-jobs/dismiss body: resolve every
// open failure for one unit.
type dismissRequest struct {
	Kind     string `json:"kind"`
	SourceID string `json:"source_id"`
}

// indexCoverageResponse is a kind's indexed-vs-expected vector totals.
// ExpectedKnown distinguishes a real ratio (api-catalog) from an
// indexed-only count (tools, which re-syncs continuously).
type indexCoverageResponse struct {
	Indexed       int  `json:"indexed"`
	Expected      int  `json:"expected"`
	ExpectedKnown bool `json:"expected_known"`
}

// indexJobResponse is one index_jobs row in the drill-down list. All
// timestamps are RFC3339; nullable ones are omitted when unset.
type indexJobResponse struct {
	ID             int64   `json:"id"`
	SourceKind     string  `json:"source_kind"`
	SourceID       string  `json:"source_id"`
	Trigger        string  `json:"trigger"`
	Status         string  `json:"status"`
	Attempts       int     `json:"attempts"`
	LastError      string  `json:"last_error,omitempty"`
	NextRunAt      string  `json:"next_run_at,omitempty"`
	WorkerID       string  `json:"worker_id,omitempty"`
	LeaseExpiresAt *string `json:"lease_expires_at,omitempty"`
	CreatedAt      string  `json:"created_at,omitempty"`
	StartedAt      *string `json:"started_at,omitempty"`
	CompletedAt    *string `json:"completed_at,omitempty"`
	ItemsDone      int     `json:"items_done"`
}

// reindexRequest is the POST /index-jobs/reindex body. SourceID is
// optional: present targets one unit, absent re-enqueues every
// out-of-sync unit of the kind.
type reindexRequest struct {
	Kind     string `json:"kind"`
	SourceID string `json:"source_id,omitempty"`
}

// registerIndexJobsRoutes registers the cross-kind Indexing dashboard
// endpoints. The read endpoints register unconditionally and degrade
// gracefully when no queue is wired (deps.IndexJobs nil) so the
// dashboard can render an informative empty state; the re-index write
// is only meaningful with a live queue.
func (h *Handler) registerIndexJobsRoutes() {
	h.mux.HandleFunc("GET /api/v1/admin/index-jobs", h.getIndexJobsSummary)
	h.mux.HandleFunc("GET /api/v1/admin/index-jobs/jobs", h.listIndexJobs)
	h.mux.HandleFunc("GET /api/v1/admin/index-jobs/failures", h.listIndexJobsFailures)
	h.mux.HandleFunc("POST /api/v1/admin/index-jobs/reindex", h.reindexIndexJobs)
	h.mux.HandleFunc("POST /api/v1/admin/index-jobs/dismiss", h.dismissIndexJobsFailure)
}

// getIndexJobsSummary handles GET /api/v1/admin/index-jobs.
//
// @Summary      Cross-kind index-jobs health summary
// @Description  Returns embedding-provider health plus a per-kind rollup (job-state counts, last activity, coverage) for every registered index_jobs consumer. Renders an empty kinds list when no queue is wired.
// @Tags         System
// @Produce      json
// @Success      200  {object}  indexJobsSummaryResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/index-jobs [get]
func (h *Handler) getIndexJobsSummary(w http.ResponseWriter, r *http.Request) {
	resp := indexJobsSummaryResponse{
		Provider: h.embeddingProviderStatus(),
		Kinds:    []indexKindSummary{},
	}
	svc := h.deps.IndexJobs
	if svc == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	for _, kind := range svc.Kinds() {
		summary, err := kindSummary(r.Context(), svc, kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load index-jobs summary")
			slog.Warn("admin: index-jobs summary", logKeyKind, sanitizeLogValue(kind), logKeyError, err)
			return
		}
		resp.Kinds = append(resp.Kinds, summary)
	}
	writeJSON(w, http.StatusOK, resp)
}

// kindSummary assembles one kind's rollup: job-state counts, optional
// coverage, and last-activity timestamp from the newest job row.
func kindSummary(ctx context.Context, svc IndexJobsService, kind string) (indexKindSummary, error) {
	counts, err := svc.Counts(ctx, kind)
	if err != nil {
		return indexKindSummary{}, fmt.Errorf("counts: %w", err)
	}
	out := indexKindSummary{
		Kind:               kind,
		Pending:            counts.Pending,
		Running:            counts.Running,
		Succeeded:          counts.Succeeded,
		Failed:             counts.Failed,
		UnresolvedFailures: counts.UnresolvedFailures,
	}
	if counts.LastActivity != nil && !counts.LastActivity.IsZero() {
		s := counts.LastActivity.UTC().Format(time.RFC3339)
		out.LastActivity = &s
	}
	cov, err := svc.Coverage(ctx, kind)
	if err != nil {
		return indexKindSummary{}, fmt.Errorf("coverage: %w", err)
	}
	if cov != nil {
		out.Coverage = &indexCoverageResponse{
			Indexed:       cov.Indexed,
			Expected:      cov.Expected,
			ExpectedKnown: cov.ExpectedKnown,
		}
	}
	// Verdict is derived from the same counts + coverage the UI receives,
	// so the lead health word and the detail metrics can never disagree.
	out.Verdict = string(indexjobs.DeriveVerdict(counts, cov))
	return out, nil
}

// listIndexJobs handles GET /api/v1/admin/index-jobs/jobs.
//
// @Summary      Index-jobs drill-down list
// @Description  Returns index_jobs rows newest first, filterable by kind, status, and source_id. Used by the dashboard's in-flight, retry/backoff, and failure-triage views.
// @Tags         System
// @Produce      json
// @Param        kind       query  string  false  "Filter by source kind"
// @Param        status     query  string  false  "Filter by status (pending|running|succeeded|failed)"
// @Param        source_id  query  string  false  "Filter by exact source id"
// @Param        limit      query  int     false  "Max rows (default 50, max 500)"
// @Success      200  {object}  map[string][]indexJobResponse
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/index-jobs/jobs [get]
func (h *Handler) listIndexJobs(w http.ResponseWriter, r *http.Request) {
	svc := h.deps.IndexJobs
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []indexJobResponse{}})
		return
	}
	filter := indexjobs.ListFilter{
		SourceKind: r.URL.Query().Get("kind"),
		SourceID:   r.URL.Query().Get("source_id"),
		Limit:      parseIndexJobsLimit(r.URL.Query().Get("limit")),
	}
	if s := r.URL.Query().Get("status"); s != "" {
		if !validJobStatus(s) {
			writeError(w, http.StatusBadRequest, "invalid status: must be pending, running, succeeded, or failed")
			return
		}
		filter.Status = indexjobs.Status(s)
	}
	jobs, err := svc.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list index jobs")
		slog.Warn("admin: list index jobs", logKeyError, err)
		return
	}
	out := make([]indexJobResponse, 0, len(jobs))
	for i := range jobs {
		out = append(out, indexJobResponseFromJob(jobs[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// listIndexJobsFailures handles GET /api/v1/admin/index-jobs/failures.
//
// @Summary      Active failure-triage units
// @Description  Returns the units with open (unresolved) failures, one entry per unit, most-recently-failed first, with first/last-seen timestamps and last-success context. A failure leaves this set automatically once a later job for the same unit succeeds, or when an operator dismisses it.
// @Tags         System
// @Produce      json
// @Param        kind   query  string  false  "Filter by source kind"
// @Param        limit  query  int     false  "Max units (default 50, max 500)"
// @Success      200  {object}  map[string][]failedUnitResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/index-jobs/failures [get]
func (h *Handler) listIndexJobsFailures(w http.ResponseWriter, r *http.Request) {
	svc := h.deps.IndexJobs
	if svc == nil {
		writeJSON(w, http.StatusOK, map[string]any{"failures": []failedUnitResponse{}})
		return
	}
	units, err := svc.ActiveFailures(r.Context(), r.URL.Query().Get("kind"), parseIndexJobsLimit(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list index-job failures")
		slog.Warn("admin: list index-job failures", logKeyError, err)
		return
	}
	out := make([]failedUnitResponse, 0, len(units))
	for i := range units {
		out = append(out, failedUnitResponseFromUnit(units[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"failures": out})
}

// dismissIndexJobsFailure handles POST /api/v1/admin/index-jobs/dismiss.
//
// @Summary      Dismiss a unit's open failures
// @Description  Resolves every open failed job for one unit, clearing it from the triage surface. The explicit fallback for a failure that will never be superseded (e.g. a removed consumer's leftover rows). Idempotent: dismissing an already-clean unit returns 200 with resolved=0.
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        request  body      dismissRequest  true  "Unit to dismiss"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/index-jobs/dismiss [post]
func (h *Handler) dismissIndexJobsFailure(w http.ResponseWriter, r *http.Request) {
	svc := h.deps.IndexJobs
	if svc == nil {
		writeError(w, http.StatusConflict, "index jobs are not available without a database and an embedding provider")
		return
	}
	var req dismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" || req.SourceID == "" {
		writeError(w, http.StatusBadRequest, "kind and source_id are required")
		return
	}
	resolved, err := svc.Resolve(r.Context(), req.Kind, req.SourceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to dismiss failure")
		slog.Warn("admin: dismiss failure", logKeyKind, sanitizeLogValue(req.Kind), logKeyError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "resolved",
		"resolved": resolved,
	})
}

// reindexIndexJobs handles POST /api/v1/admin/index-jobs/reindex.
//
// @Summary      Re-index (manual retry / force re-embed)
// @Description  Enqueues manual-retry jobs for a kind. With source_id it targets one unit; without, every out-of-sync unit. Mirrors the api-catalog manual-retry action across all kinds.
// @Tags         System
// @Accept       json
// @Produce      json
// @Param        request  body      reindexRequest  true  "Re-index target"
// @Success      202  {object}  map[string]any
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/index-jobs/reindex [post]
func (h *Handler) reindexIndexJobs(w http.ResponseWriter, r *http.Request) {
	svc := h.deps.IndexJobs
	if svc == nil {
		writeError(w, http.StatusConflict, "index jobs are not available without a database and an embedding provider")
		return
	}
	var req reindexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}
	enqueued, err := svc.Reindex(r.Context(), req.Kind, req.SourceID)
	if err != nil {
		if errors.Is(err, indexjobs.ErrUnknownKind) {
			writeError(w, http.StatusNotFound, "unknown kind: "+sanitizeLogValue(req.Kind))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to enqueue re-index")
		slog.Warn("admin: reindex", logKeyKind, sanitizeLogValue(req.Kind), logKeyError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":   "queued",
		"enqueued": enqueued,
		"count":    len(enqueued),
	})
}

// parseIndexJobsLimit parses the limit query param, defaulting and
// clamping to the [1, indexJobsListMaxLimit] window.
func parseIndexJobsLimit(raw string) int {
	if raw == "" {
		return indexJobsListDefaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return indexJobsListDefaultLimit
	}
	if n > indexJobsListMaxLimit {
		return indexJobsListMaxLimit
	}
	return n
}

// validJobStatus reports whether s is one of the queue's job states.
func validJobStatus(s string) bool {
	switch indexjobs.Status(s) {
	case indexjobs.StatusPending, indexjobs.StatusRunning,
		indexjobs.StatusSucceeded, indexjobs.StatusFailed:
		return true
	default:
		return false
	}
}

// indexJobResponseFromJob maps a queue job row to its JSON shape,
// formatting nullable timestamps as omitted-when-nil RFC3339 strings.
func indexJobResponseFromJob(j indexjobs.Job) indexJobResponse {
	out := indexJobResponse{
		ID:         j.ID,
		SourceKind: j.SourceKind,
		SourceID:   j.SourceID,
		Trigger:    string(j.Trigger),
		Status:     string(j.Status),
		Attempts:   j.Attempts,
		LastError:  j.LastError,
		WorkerID:   j.WorkerID,
		ItemsDone:  j.ItemsDone,
	}
	if !j.NextRunAt.IsZero() {
		out.NextRunAt = j.NextRunAt.UTC().Format(time.RFC3339)
	}
	if !j.CreatedAt.IsZero() {
		out.CreatedAt = j.CreatedAt.UTC().Format(time.RFC3339)
	}
	out.LeaseExpiresAt = formatNullableTime(j.LeaseExpiresAt)
	out.StartedAt = formatNullableTime(j.StartedAt)
	out.CompletedAt = formatNullableTime(j.CompletedAt)
	return out
}

// failedUnitResponseFromUnit maps a triage unit to its JSON shape. The
// first/last-failed timestamps are always present (the store COALESCEs
// created_at); last_succeeded is the genuinely-nullable one.
func failedUnitResponseFromUnit(u indexjobs.FailedUnit) failedUnitResponse {
	out := failedUnitResponse{
		SourceKind:  u.SourceKind,
		SourceID:    u.SourceID,
		LatestJobID: u.LatestJobID,
		LastError:   u.LastError,
		Attempts:    u.Attempts,
		Occurrences: u.Occurrences,
	}
	// formatTime (catalog_handler.go) renders zero -> "" so the omitempty
	// tags drop the field; UTC-normalize first to match the other
	// timestamps on this surface.
	out.FirstFailedAt = formatTime(u.FirstFailedAt.UTC())
	out.LastFailedAt = formatTime(u.LastFailedAt.UTC())
	out.LastSucceededAt = formatNullableTime(u.LastSucceededAt)
	return out
}

// formatNullableTime renders a nullable timestamp as an
// omitted-when-nil RFC3339 pointer.
func formatNullableTime(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
