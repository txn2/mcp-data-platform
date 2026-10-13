// Package resindexapi is the admin REST surface for how managed resources are
// indexed for search (#2111): how many are under each policy, and a
// reclassification that applies the operator's resources.index rules to every
// resource no writer chose a policy for.
package resindexapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/resourceindex"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcereclass"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// indexPath is the route the counts are read from.
const indexPath = "/api/v1/admin/resources/index"

// Store reads the counts. *resourceindex.Store satisfies it.
type Store interface {
	Counts(ctx context.Context) (resourceindex.PolicyCounts, error)
}

// Reclassifier applies the rules. *resourceindex.Reclassifier satisfies it.
type Reclassifier interface {
	Run(ctx context.Context) (resourcereclass.ReclassifyResult, error)
}

// Config carries the store, the reclassifier and where its runs are kept.
type Config struct {
	// Store reads the counts. nil mounts nothing.
	Store        Store
	Reclassifier Reclassifier
	// Runs keeps the last reclassification for every replica. Required with
	// a Reclassifier.
	Runs Runs
}

// Run is a reclassification, as every replica reports it.
type Run struct {
	StartedAt  time.Time                         `json:"started_at"`
	FinishedAt *time.Time                        `json:"finished_at,omitempty"`
	Result     *resourcereclass.ReclassifyResult `json:"result,omitempty"`
	Error      string                            `json:"error,omitempty"`
}

// IndexState is what GET answers: the counts, and the last reclassification
// any replica ran, absent when none has run.
type IndexState struct {
	Counts  resourceindex.PolicyCounts `json:"counts"`
	LastRun *Run                       `json:"last_run,omitempty"`
}

type handler struct {
	cfg Config
	// run is the background context a reclassification outlives its request
	// on.
	run context.Context //nolint:containedctx // the lifetime of a reclassification started by a request
}

// Mount registers the routes over the platform's database: the counts read
// from it, and a reclassification under the resolver whose owed jobs are
// enqueued on the platform's own queue table, which wakes the workers of
// whichever replica claims them.
func Mount(mux *http.ServeMux, wrap func(http.Handler) http.Handler, db *sql.DB, resolver *indexpolicy.Resolver) {
	queue := indexjobs.NewPostgresStore(db)
	resources := indexjobs.NewProducer(resourceindex.SourceKind)
	resources.Bind(queue)
	folders := indexjobs.NewProducer(resourceindex.FolderKind)
	folders.Bind(queue)
	Register(mux, wrap, Config{
		Store:        resourceindex.NewStore(db),
		Reclassifier: resourcereclass.NewReclassifier(db, resolver, resources, folders),
		Runs:         PostgresRuns{DB: db},
	})
	slog.Info("Resource index admin API enabled on " + indexPath)
}

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Store == nil {
		return
	}
	h := &handler{cfg: cfg, run: context.Background()}
	mux.Handle("GET "+indexPath, wrap(http.HandlerFunc(h.get)))
	if cfg.Reclassifier != nil && cfg.Runs != nil {
		mux.Handle("POST "+indexPath+"/reclassify", wrap(http.HandlerFunc(h.reclassify)))
	}
}

// get handles GET /api/v1/admin/resources/index.
//
// @Summary      Read how managed resources are indexed
// @Description  Returns how many managed resources search indexes in full (metadata and a prefix of their contents), on their metadata alone, and not at all; how many of those a writer chose rather than the resources.index rules; the bytes of extracted text kept for search; how many folders are searchable as one entry; and the last reclassification, whichever replica ran it.
// @Tags         Resources
// @Produce      json
// @Success      200  {object}  IndexState
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/resources/index [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	counts, err := h.cfg.Store.Counts(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the index counts failed")
		return
	}
	state := IndexState{Counts: counts}
	if h.cfg.Runs != nil {
		if state.LastRun, err = h.cfg.Runs.Last(r.Context()); err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "reading the last reclassification failed")
			return
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, state)
}

// reclassify handles POST /api/v1/admin/resources/index/reclassify.
//
// @Summary      Reclassify managed resources by the index rules
// @Description  Starts a walk over every managed resource that applies the resources.index rules again to each one no writer chose a policy for, in batches of 500 with a pause between them. A resource whose policy changes drops its vector and its kept text, and is re-indexed for what its new policy reads; a resource under a policy that reads no content keeps no extracted text. A policy a writer chose is never changed. Answers 202 at once; GET reports the result when the walk ends. Answers 409 while one is running.
// @Tags         Resources
// @Produce      json
// @Success      202  {object}  Run
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/resources/index/reclassify [post]
func (h *handler) reclassify(w http.ResponseWriter, r *http.Request) {
	started, ok, err := h.cfg.Runs.Begin(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "starting the reclassification failed")
		return
	}
	if !ok {
		httpjson.WriteError(w, http.StatusConflict, resourcereclass.ErrReclassifyRunning.Error())
		return
	}
	go func() {
		res, err := h.cfg.Reclassifier.Run(h.run)
		h.finish(res, err)
	}()
	httpjson.WriteJSON(w, http.StatusAccepted, Run{StartedAt: started})
}

// finish records a reclassification's end.
func (h *handler) finish(res resourcereclass.ReclassifyResult, err error) {
	failure := ""
	if err != nil {
		failure = err.Error()
		if !errors.Is(err, resourcereclass.ErrReclassifyRunning) {
			slog.Error("resource index: reclassification failed", "error", err) //nolint:gosec // structured slog of a store error
		}
	} else {
		slog.Info("resource index: reclassification finished",
			"examined", res.Examined, "changed", res.Changed, "cleared", res.Cleared)
	}
	if err := h.cfg.Runs.Finish(h.run, res, failure); err != nil {
		slog.Error("resource index: recording the reclassification's end failed", "error", err) //nolint:gosec // structured slog of a store error
	}
}
