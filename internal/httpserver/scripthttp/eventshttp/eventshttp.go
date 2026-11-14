// Package eventshttp serves how a managed script's event runs stand (#2332):
// each event a webhook source stores enqueues one run of every script with a
// webhook handler for it (#2434), with trigger event. Which sources a script
// runs for is its handlers', set through internal/httpserver/scripthttp/
// handlerhttp.
//
// The composition root mounts it beside scripthttp (ForPortal), whose owner
// check it uses.
package eventshttp

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
)

// skippedWindow is how far back the summary counts skipped events.
const skippedWindow = 7 * 24 * time.Hour

// Config is the store event runs are read from.
type Config struct {
	// Summary is how the script's event runs stand (scriptevents.PostgresStore).
	Summary func(ctx context.Context, scriptID string, since time.Time) (scriptevents.EventSummary, error)
	// MaxQueued is scripts.events.max_queued; zero is the default.
	MaxQueued int
}

// ConfigFor is the Config over the platform's database.
func ConfigFor(db *sql.DB, maxQueued int) Config {
	return Config{Summary: scriptevents.NewStore(db).EventSummary, MaxQueued: maxQueued}
}

// Deps carries what the routes need.
type Deps struct {
	// Config is the event run store.
	Config Config
	// Owned resolves the script in the path for its owner or an
	// administrator, writing any refusal itself.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// ForPortal is the handler over scripthttp's owner check.
func ForPortal(scripts *scripthttp.Handler, cfg Config) *Handler {
	return New(Deps{Config: cfg, Owned: scripts.OwnedByCaller})
}

// Handler serves the routes.
type Handler struct{ deps Deps }

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the routes, wrapped in the portal authentication
// middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/events", wrap(http.HandlerFunc(h.summary)))
}

// summaryResponse is how a script's event runs stand.
type summaryResponse struct {
	scriptevents.EventSummary
	// SkippedWindowDays is how far back skipped_since counts.
	SkippedWindowDays int `json:"skipped_window_days" example:"7"`
	// MaxQueued is how many pending event runs each of the script's webhook
	// handlers may hold.
	MaxQueued int `json:"max_queued" example:"100"`
}

// summary reports how a script's event runs stand.
//
// @Summary      How a script's event runs stand
// @Description  The script's pending event runs, the events skipped in the last seven days because max_queued event runs of the webhook handler were already waiting (each recorded as a run with status skipped_backlog), the newest of each, and the bound per webhook handler (scripts.events.max_queued). Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  summaryResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/events [get]
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	now := time.Now
	if h.deps.Now != nil {
		now = h.deps.Now
	}
	sum, err := h.deps.Config.Summary(r.Context(), id, now().Add(-skippedWindow))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the script's event runs")
		return
	}
	limit := h.deps.Config.MaxQueued
	if limit <= 0 {
		limit = scriptevents.DefaultMaxQueued
	}
	httpjson.WriteJSON(w, http.StatusOK, summaryResponse{EventSummary: sum, SkippedWindowDays: int(skippedWindow / (24 * time.Hour)), MaxQueued: limit})
}
