// Package integrationapi is the admin REST surface for integrations (#2659):
// every operation with the script chosen for it and its items, the scripts the
// picker offers, choosing and clearing an operation's script, and a retry.
// Choosing, changing, clearing and retrying are audited.
package integrationapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// basePath is the collection route.
const basePath = "/api/v1/admin/integrations"

// maxBodyBytes bounds a request body.
const maxBodyBytes = 16 << 10

// paramOperation is the path parameter and audit parameter naming the
// operation.
const paramOperation = "operation"

// The tool names an action is audited under.
const (
	toolBind  = "integration_bind"
	toolClear = "integration_clear"
	toolRetry = "integration_retry"
)

// Service is what the routes act through. *integration.Service satisfies it.
type Service interface {
	Overview(ctx context.Context) ([]integration.Overview, error)
	Candidates(ctx context.Context, a integration.Actor, op, search string) ([]integration.Candidate, error)
	Bind(ctx context.Context, a integration.Actor, req integration.BindRequest) (*integration.Binding, error)
	Clear(ctx context.Context, a integration.Actor, op string) error
	Retry(ctx context.Context, op string) error
}

// Config carries the service and the parent-owned helpers.
type Config struct {
	// Service carries out the actions; nil mounts nothing.
	Service Service
	// Actor is the administrator making a request. Every request the routes
	// see passed the admin API's authentication.
	Actor func(*http.Request) integration.Actor
	// Audit records every change; nil records none.
	Audit middleware.AuditLogger
}

type handler struct{ cfg Config }

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Service == nil || cfg.Actor == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+basePath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("GET "+basePath+"/{operation}/scripts", wrap(http.HandlerFunc(h.scripts)))
	mux.Handle("PUT "+basePath+"/{operation}", wrap(http.HandlerFunc(h.bind)))
	mux.Handle("DELETE "+basePath+"/{operation}", wrap(http.HandlerFunc(h.clear)))
	mux.Handle("POST "+basePath+"/{operation}/retry", wrap(http.HandlerFunc(h.retry)))
}

// list handles GET /api/v1/admin/integrations.
//
// @Summary      List integrations
// @Description  Returns every operation the platform hands to a script (Site routing first): its contract, the script chosen for it with the version approved for handlers, how its last run went (with the run's error, which only administrators read), each item's status and the counts by status. An operation with no script chosen has no binding and no items.
// @Tags         Integrations
// @Produce      json
// @Success      200  {object}  IntegrationList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/integrations [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.cfg.Service.Overview(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading the integrations failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, IntegrationList{Integrations: out})
}

// scripts handles GET /api/v1/admin/integrations/{operation}/scripts.
//
// @Summary      List the scripts an integration may call
// @Description  Returns the scripts, not libraries, whose latest version defines the operation's function (route(req) for site_routing), with the version approved for handlers and whether that version defines it too.
// @Tags         Integrations
// @Produce      json
// @Param        operation  path   string  true   "Operation, such as site_routing"
// @Param        search     query  string  false  "Narrows by name or description"
// @Success      200  {object}  CandidateList
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/integrations/{operation}/scripts [get]
func (h *handler) scripts(w http.ResponseWriter, r *http.Request) {
	out, err := h.cfg.Service.Candidates(r.Context(), h.cfg.Actor(r), r.PathValue(paramOperation), r.URL.Query().Get("search"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, CandidateList{Scripts: out})
}

// bind handles PUT /api/v1/admin/integrations/{operation}.
//
// @Summary      Choose an integration's script
// @Description  Chooses the script an operation calls, or changes it or its interval, and calls it with every item at once. Refused when the version that would run does not define the operation's function, naming the contract, and when no version is approved for handlers and approve is not set. Audited.
// @Tags         Integrations
// @Accept       json
// @Produce      json
// @Param        operation  path  string     true  "Operation, such as site_routing"
// @Param        body       body  BindInput  true  "The script, the interval and whether to approve its latest version"
// @Success      200  {object}  integration.Binding
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/integrations/{operation} [put]
func (h *handler) bind(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue(paramOperation)
	var in BindInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the body is not a script choice: "+err.Error())
		return
	}
	b, err := h.cfg.Service.Bind(r.Context(), h.cfg.Actor(r), integration.BindRequest{
		Operation: op, ScriptID: strings.TrimSpace(in.ScriptID), IntervalSeconds: in.IntervalSeconds, Approve: in.Approve,
	})
	h.record(r, toolBind, map[string]any{paramOperation: op, "script_id": in.ScriptID, "interval_seconds": in.IntervalSeconds, "approve": in.Approve}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, b)
}

// clear handles DELETE /api/v1/admin/integrations/{operation}.
//
// @Summary      Clear an integration's script
// @Description  Stops the operation's calls and drops every status it kept, so the deployment behaves as it does with no script. Audited.
// @Tags         Integrations
// @Produce      json
// @Param        operation  path  string  true  "Operation, such as site_routing"
// @Success      200  {object}  Done
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/integrations/{operation} [delete]
func (h *handler) clear(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue(paramOperation)
	err := h.cfg.Service.Clear(r.Context(), h.cfg.Actor(r), op)
	h.record(r, toolClear, map[string]any{paramOperation: op}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, Done{Operation: op, Done: true})
}

// retry handles POST /api/v1/admin/integrations/{operation}/retry.
//
// @Summary      Retry an integration now
// @Description  Calls the operation's script with every item at once, when the run in flight, if any, ends. Audited.
// @Tags         Integrations
// @Produce      json
// @Param        operation  path  string  true  "Operation, such as site_routing"
// @Success      202  {object}  Done
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/integrations/{operation}/retry [post]
func (h *handler) retry(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue(paramOperation)
	err := h.cfg.Service.Retry(r.Context(), op)
	h.record(r, toolRetry, map[string]any{paramOperation: op}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusAccepted, Done{Operation: op, Done: true})
}

// writeError answers an error by what it is.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, integration.ErrUnknown), errors.Is(err, integration.ErrNotBound):
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, integration.ErrNotAllowed):
		httpjson.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, integration.ErrInvalid):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, "the integration request failed")
	}
}

// record audits one action. Best-effort: a failed write is warned and the
// request stands.
func (h *handler) record(r *http.Request, tool string, params map[string]any, opErr error) {
	if h.cfg.Audit == nil {
		return
	}
	a := h.cfg.Actor(r)
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: time.Now().UTC(), ToolName: tool, ToolkitKind: "integration",
		Source: "admin", Transport: "http", EventKind: string(audit.EventTypeAdmin),
		Authorized: true, Success: opErr == nil, Parameters: params, UserID: a.Email, UserEmail: a.Email,
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.cfg.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("integration action audit failed", "tool", tool, "error", logsan.SanitizeForLog(err.Error()))
	}
}
