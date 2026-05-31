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

// indexKindSummary is one registered kind's job-state rollup, last
// activity, and optional coverage.
type indexKindSummary struct {
	Kind      string `json:"kind"`
	Pending   int    `json:"pending"`
	Running   int    `json:"running"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	// LastActivity is the most recent job's activity timestamp
	// (completed, else started, else created), RFC3339, omitted when
	// the kind has no jobs yet.
	LastActivity *string `json:"last_activity,omitempty"`
	// Coverage is the indexed-vs-expected rollup, omitted for kinds
	// whose Sink reports none.
	Coverage *indexCoverageResponse `json:"coverage,omitempty"`
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
	h.mux.HandleFunc("POST /api/v1/admin/index-jobs/reindex", h.reindexIndexJobs)
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
			slog.Warn("admin: index-jobs summary", "kind", sanitizeLogValue(kind), logKeyError, err)
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
		Kind:      kind,
		Pending:   counts.Pending,
		Running:   counts.Running,
		Succeeded: counts.Succeeded,
		Failed:    counts.Failed,
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
		slog.Warn("admin: reindex", "kind", sanitizeLogValue(req.Kind), logKeyError, err)
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

// formatNullableTime renders a nullable timestamp as an
// omitted-when-nil RFC3339 pointer.
func formatNullableTime(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
