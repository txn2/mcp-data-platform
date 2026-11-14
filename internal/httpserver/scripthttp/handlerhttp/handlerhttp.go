// Package handlerhttp serves a managed script's handlers on the portal
// (#2441, #2434, #2435): what starts the script (its endpoints, schedules and
// webhook sources), each added, changed, turned on and off and deleted by its
// id, and Admin > Handlers, every handler of every script. Every route carries
// its request out through internal/platform/scripthandler, the service the
// manage_script handler commands carry theirs out through, so the rules are
// the same on both surfaces.
package handlerhttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxBodyBytes bounds a handler write.
const maxBodyBytes = 16 << 10

// pathID is the path value a handler route names its handler by.
const pathID = "id"

// Deps is what the routes need.
type Deps struct {
	Handlers *scripthandler.Service
	Scripts  interface {
		GetByID(ctx context.Context, id string) (*script.Script, error)
	}
	// Owned resolves the script in the path for its owner or an
	// administrator, writing any refusal itself.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
	// Caller is the request's caller, ok false when it carries none.
	Caller func(r *http.Request) (c scripthandler.Caller, ok bool)
	// Tester answers the test panel (#2436); nil answers that the
	// deployment cannot test calls.
	Tester Tester
}

// Handler serves the routes.
type Handler struct{ deps Deps }

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the portal routes, each wrapped in the portal
// authentication.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/handlers", wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/portal/scripts/{id}/handlers", wrap(http.HandlerFunc(h.add)))
	mux.Handle("GET /api/v1/portal/handlers/{id}", wrap(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/v1/portal/handlers/{id}", wrap(http.HandlerFunc(h.change)))
	mux.Handle("DELETE /api/v1/portal/handlers/{id}", wrap(http.HandlerFunc(h.remove)))
	mux.Handle("POST /api/v1/portal/handlers/{id}/enable", wrap(http.HandlerFunc(h.enable)))
	mux.Handle("POST /api/v1/portal/handlers/{id}/disable", wrap(http.HandlerFunc(h.disable)))
	h.registerCalls(mux, wrap)
}

// RegisterAdmin mounts Admin > Handlers under prefix, wrapped in the admin
// authentication.
func (h *Handler) RegisterAdmin(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/handlers", wrap(http.HandlerFunc(h.adminList)))
}

// SetRequest is a handler added or changed: its kind, and that kind's
// settings, named as manage_script names them.
type SetRequest struct {
	// Kind is endpoint, schedule or webhook; a change reads it from the
	// handler and may leave it out.
	Kind string `json:"kind,omitempty" example:"schedule"`
	epcmd.Input
	// TimeoutSeconds is how long one request to an endpoint may run.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Cron, Timezone and Args are a schedule's.
	Cron     string         `json:"cron,omitempty" example:"0 7 * * 1-5"`
	Timezone string         `json:"timezone,omitempty" example:"America/Los_Angeles"`
	Args     map[string]any `json:"args,omitempty"`
	// EventSource is a webhook handler's source.
	EventSource string `json:"event_source,omitempty" example:"form-contact"`
	// The tool-call kinds' settings (policy, transform, watch; #2436): each
	// left out keeps its value on a change. Connection is the connection
	// setting endpoints share.
	Operations *[]string                `json:"operations,omitempty"`
	Personas   *[]string                `json:"personas,omitempty"`
	Users      *[]string                `json:"users,omitempty"`
	Conditions *[]callhandler.Condition `json:"conditions,omitempty"`
	TimeoutMS  *int                     `json:"timeout_ms,omitempty" example:"2000"`
	OnFailure  *string                  `json:"on_failure,omitempty" example:"hold"`
	ItemsPath  *string                  `json:"items_path,omitempty" example:"subscribers"`
	Writes     *map[string]bool         `json:"writes,omitempty"`
	Protected  *[]string                `json:"protected,omitempty"`
	Order      *int                     `json:"order,omitempty"`
	// Approve approves the script's latest version for handlers as the
	// administrator setting the handler.
	Approve bool `json:"approve,omitempty"`
}

// ScriptHandlers is a script's handlers and what adding one offers.
type ScriptHandlers struct {
	// Handlers are the script's handlers, each as its kind describes it,
	// with its id, kind and type (the kind, or form).
	Handlers []map[string]any `json:"handlers"`
	// Add is each kind as adding one offers it to the caller.
	Add []scripthandler.Offer `json:"add"`
	// Endpoint is what adding an endpoint offers: where its page would
	// answer and, for a form, the connections its submissions could be
	// stored on.
	Endpoint map[string]any `json:"endpoint,omitempty"`
	// Sources are the webhook sources the caller may run the script for.
	Sources []string `json:"sources"`
	// MaxPerKind is how many handlers of one kind a script may hold.
	MaxPerKind int `json:"max_per_kind" example:"20"`
}

// HandlerList is one page of handlers.
type HandlerList struct {
	Data  []scripthandler.Handler `json:"data"`
	Total int                     `json:"total" example:"3"`
}

// write answers out, or the refusal err is.
func write(w http.ResponseWriter, r *http.Request, status int, out any, err error) {
	switch {
	case err == nil:
		httpjson.WriteJSON(w, status, out)
	case scripthandler.Refused(err):
		httpjson.WriteError(w, http.StatusForbidden, err.Error())
	case scripthandler.Missing(err):
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
	case scripthandler.Failed(err):
		slog.ErrorContext(r.Context(), "handlers: the store failed", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the handler could not be read or written")
	default:
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	}
}

func (h *Handler) caller(w http.ResponseWriter, r *http.Request) (scripthandler.Caller, bool) {
	c, ok := h.deps.Caller(r)
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
	}
	return c, ok
}

// script is the script in the path, for its owner or an administrator.
func (h *Handler) script(w http.ResponseWriter, r *http.Request) (*script.Script, scripthandler.Caller, bool) {
	c, ok := h.caller(w, r)
	if !ok {
		return nil, c, false
	}
	id, _, ok := h.deps.Owned(w, r)
	if !ok {
		return nil, c, false
	}
	sc, err := h.deps.Scripts.GetByID(r.Context(), id)
	if err != nil || sc == nil {
		httpjson.WriteError(w, http.StatusNotFound, "script not found")
		return nil, c, false
	}
	return sc, c, true
}

// list returns a script's handlers.
//
// @Summary      List a script's handlers
// @Description  Returns what starts the script: each of its handlers (an endpoint, a form, a schedule or a webhook source) as its kind describes it, with its id, kind, type, settings and whether it is on; an endpoint's recent failed requests and, for a form, where its submissions are stored; a schedule's next fire and missed fires. add says, for each kind, whether the caller may add one and why not; endpoint says where a page would answer and, on a form, the connections its submissions could be stored on; sources are the webhook sources the caller may run the script for. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  ScriptHandlers
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/handlers [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	sc, c, ok := h.script(w, r)
	if !ok {
		return
	}
	out, err := h.scriptHandlers(r.Context(), sc, c)
	write(w, r, http.StatusOK, out, err)
}

func (h *Handler) scriptHandlers(ctx context.Context, sc *script.Script, c scripthandler.Caller) (*ScriptHandlers, error) {
	svc := h.deps.Handlers
	rows, _, err := svc.List(ctx, scripthandler.Filter{ScriptIDs: []string{sc.ID}}, scripthandler.Caller{Admin: true})
	if err != nil {
		return nil, err //nolint:wrapcheck // the service's error is read by identity
	}
	out := &ScriptHandlers{Handlers: []map[string]any{}, Add: svc.Offers(sc, c), MaxPerKind: scripthandler.MaxPerKind}
	for _, row := range rows {
		one, err := svc.Get(ctx, row.ID, c)
		if err != nil {
			return nil, err //nolint:wrapcheck // the service's error is read by identity
		}
		one["type"] = row.Type
		out.Handlers = append(out.Handlers, one)
	}
	if svc.Endpoints != nil {
		if out.Endpoint, err = svc.Endpoints.Choices(ctx, sc); err != nil {
			return nil, err //nolint:wrapcheck // the endpoint kind names what it could not read
		}
	}
	if out.Sources, err = svc.SourceNames(ctx, c); err != nil {
		return nil, err //nolint:wrapcheck // the service's error is read by identity
	}
	return out, nil
}

// decode reads a SetRequest.
func decode(w http.ResponseWriter, r *http.Request) (SetRequest, bool) {
	var body SetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body must be a handler: kind and its settings")
		return body, false
	}
	return body, true
}

func setRequest(c scripthandler.Caller, sc *script.Script, body SetRequest) scripthandler.SetRequest {
	req := scripthandler.SetRequest{
		Caller: c, Script: sc, Kind: body.Kind, Endpoint: body.Input, TimeoutSeconds: body.TimeoutSeconds,
		Cron: body.Cron, Timezone: body.Timezone, Args: body.Args, EventSource: body.EventSource, Approve: body.Approve,
		Call: callhandler.Patch{
			Operations: body.Operations, Personas: body.Personas, Users: body.Users, Conditions: body.Conditions,
			TimeoutMS: body.TimeoutMS, OnFailure: body.OnFailure, ItemsPath: body.ItemsPath, Writes: body.Writes,
			Protected: body.Protected, Order: body.Order,
		},
	}
	if body.Connection != "" {
		conn := body.Connection
		req.Call.Connection = &conn
	}
	return req
}

// add adds a handler to a script.
//
// @Summary      Add a handler to a script
// @Description  Adds what starts the script: kind endpoint (the endpoint settings manage_script's handler_set takes: endpoint, methods, path, auth_mode, callers, bot_check, limits, origins, response_mode; a form adds connection and decrypt_personas), schedule (cron, timezone, args) or webhook (event_source). An endpoint named like one the script already has changes that one; a name another script holds is refused, naming its handler. A schedule with the cadence and args of one the script already has is refused, naming it. A script holds at most max_per_kind handlers of a kind. The script's owner adds schedules and webhook handlers (a webhook only for the source of a form they own) and the endpoint of their own form; an administrator adds any. Answers the handler as GET /portal/handlers/{id} does.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id    path  string      true  "Script ID"
// @Param        body  body  SetRequest  true  "The handler"
// @Success      201  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/handlers [post]
func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	sc, c, ok := h.script(w, r)
	if !ok {
		return
	}
	body, ok := decode(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Handlers.Set(r.Context(), setRequest(c, sc, body))
	write(w, r, http.StatusCreated, out, err)
}

// get returns one handler.
//
// @Summary      Get a handler
// @Description  Returns one handler as its kind describes it: an endpoint's settings, address, recent failed requests and, for a form, where its submissions are stored; a schedule's cadence, args, next fire and missed fires; a webhook handler's source. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Handler ID"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/handlers/{id} [get]
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Handlers.Get(r.Context(), r.PathValue(pathID), c)
	write(w, r, http.StatusOK, out, err)
}

// change changes one handler's settings.
//
// @Summary      Change a handler
// @Description  Changes one handler's settings, by the rules adding one follows. An endpoint's field left out keeps its value; a schedule takes its whole cadence (cron, timezone, args); a webhook handler's source does not change, and saving one leaves it on or off as it was (enable turns it on). Answers the handler as GET /portal/handlers/{id} does.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id    path  string      true  "Handler ID"
// @Param        body  body  SetRequest  true  "The handler's settings"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/handlers/{id} [put]
func (h *Handler) change(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	body, ok := decode(w, r)
	if !ok {
		return
	}
	row, sc, err := h.deps.Handlers.Resolve(r.Context(), r.PathValue(pathID), c)
	if err != nil {
		write(w, r, http.StatusOK, nil, err)
		return
	}
	req := setRequest(c, sc, body)
	req.Kind, req.Handler = row.Kind, row.ID
	if row.Kind == scripthandler.KindWebhook && req.EventSource == "" {
		req.EventSource = row.Target
	}
	out, err := h.deps.Handlers.Set(r.Context(), req)
	write(w, r, http.StatusOK, out, err)
}

// remove deletes one handler.
//
// @Summary      Delete a handler
// @Description  Deletes one handler. An endpoint answers nothing at its address from then on, and its failed and refused requests go with it; a form's webhook source and the submissions stored in it stay. A schedule that produced runs is refused (403): its runs name it, so it is disabled instead. Restricted to the script's owner and to administrators, by the rules adding one follows.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Handler ID"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/handlers/{id} [delete]
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Handlers.Delete(r.Context(), r.PathValue(pathID), c)
	write(w, r, http.StatusOK, out, err)
}

// enable turns one handler on.
//
// @Summary      Turn a handler on
// @Description  Turns one handler on. A schedule turned on is due for the fire it was parked on. A form served to callers with no session needs an approved version before anyone but an administrator turns it on. Answers the handler as GET /portal/handlers/{id} does.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Handler ID"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/handlers/{id}/enable [post]
func (h *Handler) enable(w http.ResponseWriter, r *http.Request) { h.setEnabled(w, r, true) }

// disable turns one handler off.
//
// @Summary      Turn a handler off
// @Description  Turns one handler off: an endpoint answers nothing, a schedule fires nothing, a webhook source's events start no run. Answers the handler as GET /portal/handlers/{id} does.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Handler ID"
// @Success      200  {object}  map[string]any
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/handlers/{id}/disable [post]
func (h *Handler) disable(w http.ResponseWriter, r *http.Request) { h.setEnabled(w, r, false) }

func (h *Handler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	out, err := h.deps.Handlers.Enable(r.Context(), r.PathValue(pathID), enabled, c)
	write(w, r, http.StatusOK, out, err)
}

// adminList returns every handler of every script.
//
// @Summary      List every handler
// @Description  Returns one page of every script's handlers (Admin > Handlers): each one's id, kind, type (endpoint, form, schedule or webhook), script, owner, target (an endpoint's name, a schedule's cadence, a webhook handler's source), whether it is on, who created it and when. type narrows to handlers of any of the types named and may be repeated; search narrows to handlers whose script name or target contains it; owner to one person's scripts; enabled to handlers that are on or off. total counts every match.
// @Tags         Scripts
// @Produce      json
// @Param        type     query  []string  false  "Narrow to these handler types"  collectionFormat(multi)
// @Param        search   query  string    false  "Narrow to handlers whose script name or target contains this"
// @Param        owner    query  string    false  "Narrow to one owner's scripts, by email"
// @Param        enabled  query  boolean   false  "Narrow to handlers that are on or off"
// @Param        limit    query  int       false  "Page size (default and most 500)"
// @Param        offset   query  int       false  "Rows to skip"
// @Success      200  {object}  HandlerList
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/handlers [get]
func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := scripthandler.Filter{Types: q["type"], Search: strings.TrimSpace(q.Get("search")), OwnerEmail: strings.TrimSpace(q.Get("owner"))}
	if v := q.Get("enabled"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "enabled must be true or false")
			return
		}
		f.Enabled = &on
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	rows, total, err := h.deps.Handlers.List(r.Context(), f, scripthandler.Caller{Admin: true})
	write(w, r, http.StatusOK, HandlerList{Data: rows, Total: total}, err)
}
