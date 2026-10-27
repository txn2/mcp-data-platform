// Package eventshttp serves the webhook source a managed script runs for
// (#2332): each event the source stores enqueues one run of the script, with
// trigger event, carrying the event as run.event. The owner and an
// administrator set it, as they set a schedule; anyone else is refused by the
// edit funnel. Naming a source is reading it, so only an administrator may
// name any source, and anyone else the source of a form they own.
//
// The composition root mounts it beside scripthttp (ForPortal), whose edit
// funnel and owner check it uses.
package eventshttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxBodyBytes bounds the one-field body of a change.
const maxBodyBytes = 1 << 10

// skippedWindow is how far back the summary counts skipped events.
const skippedWindow = 7 * 24 * time.Hour

// Config is the rule a named source is held to and the store event runs are
// read from.
type Config struct {
	// Check is sourcerule.Rule.Check.
	Check func(ctx context.Context, source, callerEmail string, admin bool) error
	// Summary is how the script's event runs stand (scriptevents.PostgresStore).
	Summary func(ctx context.Context, scriptID string, since time.Time) (scriptevents.EventSummary, error)
	// MaxQueued is scripts.events.max_queued; zero is the default.
	MaxQueued int
}

// ConfigFor is the Config over the platform's database: the stored webhook
// sources, the scripts a form source's owner is read from, and the event runs.
func ConfigFor(db *sql.DB, enc whsource.Encryptor, maxQueued int) Config {
	rule := sourcerule.Rule{Sources: whsource.NewStore(db, enc), FormOwner: sourcerule.OwnerOf(scriptstore.New(db).GetByID)}
	return Config{Check: rule.Check, Summary: scriptevents.NewStore(db).EventSummary, MaxQueued: maxQueued}
}

// Deps carries what the routes need.
type Deps struct {
	// Config is the source rule and the event run store.
	Config Config
	// Edit applies mutate to the script in the path for a caller who owns
	// it, through the edit funnel, writing any refusal itself.
	Edit func(w http.ResponseWriter, r *http.Request, mutate func(*script.Script)) (*script.Script, bool)
	// Owned resolves the script in the path for its owner or an
	// administrator, writing any refusal itself.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
	// Caller is the request's caller: email and whether an administrator.
	Caller func(r *http.Request) (email string, admin bool)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// ForPortal is the handler over scripthttp's edit funnel and owner check
// and the caller deps.PortalUser resolves.
func ForPortal(scripts *scripthttp.Handler, deps scripthttp.Deps, cfg Config) *Handler {
	return New(Deps{
		Config: cfg, Edit: scripts.EditOwned, Owned: scripts.OwnedByCaller,
		Caller: func(r *http.Request) (string, bool) {
			if u := deps.PortalUser(r); u != nil {
				return u.Email, u.IsAdmin
			}
			return "", false
		},
	})
}

// Handler serves the routes.
type Handler struct{ deps Deps }

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the routes, wrapped in the portal authentication
// middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("PUT /api/v1/portal/scripts/{id}/event-source", wrap(http.HandlerFunc(h.setSource)))
	mux.Handle("GET /api/v1/portal/scripts/{id}/events", wrap(http.HandlerFunc(h.summary)))
}

// sourceRequest names the webhook source a script runs for.
type sourceRequest struct {
	// Source is the webhook source's name; empty runs the script for none.
	Source *string `json:"source" example:"form-contact"`
}

// sourceResponse reports the saved source.
type sourceResponse struct {
	Source  string `json:"source" example:"form-contact"`
	Message string `json:"message" example:"Saved. Each event source form-contact stores runs this script."`
}

// setSource sets the webhook source a script runs for.
//
// @Summary      Set the webhook source a script runs for
// @Description  Each event the named source stores enqueues one run of the script with trigger event, carrying the event as run.event (id, type, key, received_at, source, payload); the run executes through the run queue under the script's saved author roles, as a scheduled run does. An empty source runs it for none. An administrator may name any source; the owner only the source of a form they own (403 otherwise). Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id      path  string         true  "Script ID"
// @Param        source  body  sourceRequest  true  "The source"
// @Success      200  {object}  sourceResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/event-source [put]
func (h *Handler) setSource(w http.ResponseWriter, r *http.Request) {
	var req sourceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil || req.Source == nil {
		httpjson.WriteError(w, http.StatusBadRequest, `the body must be {"source": "<webhook source>"}, or {"source": ""} to run for none`)
		return
	}
	email, admin := h.deps.Caller(r)
	if err := h.deps.Config.Check(r.Context(), *req.Source, email, admin); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sourcerule.ErrSourceRefused) {
			status = http.StatusForbidden
		}
		httpjson.WriteError(w, status, err.Error())
		return
	}
	saved, ok := h.deps.Edit(w, r, func(sc *script.Script) { sc.EventSource = *req.Source })
	if !ok {
		return
	}
	msg := "Saved. This script runs for no webhook source."
	if saved.EventSource != "" {
		msg = "Saved. Each event source " + saved.EventSource + " stores runs this script."
	}
	httpjson.WriteJSON(w, http.StatusOK, sourceResponse{Source: saved.EventSource, Message: msg})
}

// summaryResponse is how a script's event runs stand.
type summaryResponse struct {
	scriptevents.EventSummary
	// SkippedWindowDays is how far back skipped_since counts.
	SkippedWindowDays int `json:"skipped_window_days" example:"7"`
	// MaxQueued is how many pending event runs the script may hold.
	MaxQueued int `json:"max_queued" example:"100"`
}

// summary reports how a script's event runs stand.
//
// @Summary      How a script's event runs stand
// @Description  The script's pending event runs, the events skipped in the last seven days because max_queued event runs were already waiting (each recorded as a run with status skipped_backlog), the newest of each, and the bound (scripts.events.max_queued). Restricted to the script's owner and to administrators.
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
