// Package bindinghttp serves a managed script's endpoint binding on its
// portal page (#2171): the binding and its recent failed requests, which the
// script's owner and an administrator read, and the binding's setting, turning
// on and off, which only an administrator may do. The rules are manage_script's
// own: every route here carries its request out through the same
// internal/endpoint/epcmd handler the tool's endpoint commands do.
//
// It is mounted by scripthttp, which resolves the caller and the script and
// answers everybody but the script's owner and an administrator before any
// handler here runs.
package bindinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxBodyBytes bounds a binding write.
const maxBodyBytes = 16 << 10

// Deps carries the commands, the script store and the caller's gate.
type Deps struct {
	Commands *epcmd.Handler
	Scripts  interface {
		GetByID(ctx context.Context, id string) (*script.Script, error)
	}
	// Owned resolves the script in the path for its owner or an
	// administrator, or writes the refusal and reports false. actor is who
	// the caller is.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
	// Admin reports whether the caller is an administrator.
	Admin func(r *http.Request) bool
}

// Handler serves the binding routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the routes, each wrapped in the portal authentication.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/endpoint", wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/v1/portal/scripts/{id}/endpoint", wrap(http.HandlerFunc(h.put)))
	mux.Handle("POST /api/v1/portal/scripts/{id}/endpoint/enable", wrap(http.HandlerFunc(h.enable)))
	mux.Handle("POST /api/v1/portal/scripts/{id}/endpoint/disable", wrap(http.HandlerFunc(h.disable)))
}

// bindingRequest is a binding set on a script: epcmd.Input's fields and the
// time one request may run.
type bindingRequest struct {
	epcmd.Input
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// get returns the script's binding.
//
// @Summary      Get a script's endpoint
// @Description  Returns whether the script is bound to an endpoint and, when it is, the binding: its name, address, methods, path template, access mode (authenticated, public or bot_check), the callers an authenticated endpoint admits, the bot check a bot_check endpoint names and the methods it checks, limits and allowed origins, with the requests that failed in the last seven days (status, error and how long each took), newest first. A form (is_form) reports where its submissions are stored (form: source, table, connection, decrypt_personas) or, before its source is made, the connections it could be made on (form_connections). Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/endpoint [get]
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, epcmd.CmdList, nil)
}

// put binds the script to an endpoint, or changes its binding.
//
// @Summary      Bind a script to an endpoint
// @Description  Binds the script to /endpoints/{endpoint}, or changes its binding. A field left out keeps what the binding had, or takes the default on a new one. The script must define handle(req), or form_spec(req) and form_submit(req, values) (a form); binding a form makes the webhook source its accepted submissions are stored in, on connection (the Trino connection whose scratch catalog holds webhook_form_<endpoint>, defaulting to the only one, named once), with decrypt_personas the personas a script may decrypt its sensitive fields as. auth_mode is authenticated (a platform API key or a browser session among callers: "*", "persona:<name>" or "api_key:<name>", empty admitting no one but the administrator, who is always admitted), public (anyone, with no check) or bot_check (anyone who passes the bot check bot_check names, one configured under Admin > Settings > Bot checks, on the methods in checked_methods, empty for every method); a binding naming a check that does not exist is refused, and so is signed_in. Restricted to administrators: the script's owner is refused.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id       path  string          true  "Script ID"
// @Param        binding  body  bindingRequest  true  "The binding"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/endpoint [put]
func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	var req bindingRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	h.run(w, r, epcmd.CmdSet, &req)
}

// enable turns the script's endpoint on.
//
// @Summary      Turn a script's endpoint on
// @Description  Turns the endpoint on: it answers requests again. Restricted to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  map[string]any
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/endpoint/enable [post]
func (h *Handler) enable(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, epcmd.CmdEnable, nil)
}

// disable turns the script's endpoint off.
//
// @Summary      Turn a script's endpoint off
// @Description  Turns the endpoint off: it answers 404 until it is turned on, and its binding is kept. Restricted to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  map[string]any
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/endpoint/disable [post]
func (h *Handler) disable(w http.ResponseWriter, r *http.Request) {
	h.run(w, r, epcmd.CmdDisable, nil)
}

// run resolves the script for its owner or an administrator and carries the
// command out as manage_script would.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, cmd string, body *bindingRequest) {
	scriptID, actor, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	sc, err := h.deps.Scripts.GetByID(r.Context(), scriptID)
	if err != nil || sc == nil {
		httpjson.WriteError(w, http.StatusNotFound, "script not found")
		return
	}
	req := epcmd.Request{Command: cmd, Script: sc, Admin: h.deps.Admin(r), Actor: actor}
	if body != nil {
		req.Input, req.TimeoutSeconds = body.Input, body.TimeoutSeconds
	}
	out, err := h.deps.Commands.Run(r.Context(), req)
	switch {
	case errors.Is(err, epbinding.ErrNotAllowed):
		httpjson.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, epcmd.ErrStore):
		slog.ErrorContext(r.Context(), "endpoint binding: the store failed", "script_id", scriptID, "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the endpoint binding could not be read or written")
	case err != nil:
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteJSON(w, http.StatusOK, out)
	}
}
