// Package apprhttp serves the Approvals queue on the portal (#2437, #2494):
// a person's own requests, every request for an administrator, filing one and
// deciding one, through the service manage_approval uses and under its
// rules. Every write is audited as a manage_approval call from the portal.
package apprhttp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/approval/apprtool"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Prefix opens every route.
const Prefix = "/api/v1/portal/approvals"

// maxBody bounds a request body.
const maxBody = 64 << 10

// sourcePortal is the audit source of a portal write.
const sourcePortal = "portal"

// Config is what the routes use.
type Config struct {
	Service *approval.Service
	// Actor is who made a request, nil for none.
	Actor func(r *http.Request) *approval.Actor
	Audit middleware.AuditLogger
}

type handler struct{ cfg Config }

// Register mounts the routes behind wrap, the portal's authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Service == nil || cfg.Actor == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+Prefix, wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST "+Prefix, wrap(http.HandlerFunc(h.submit)))
	mux.Handle("GET "+Prefix+"/{id}", wrap(http.HandlerFunc(h.get)))
	mux.Handle("POST "+Prefix+"/{id}/decide", wrap(http.HandlerFunc(h.decide)))
}

// submitRequest and decideRequest are the documented request bodies.
type (
	submitRequest struct {
		Script  string `json:"script" example:"order-intake"`
		Version int    `json:"version" example:"2"`
		Purpose string `json:"purpose,omitempty" example:"form_public"`
		Reason  string `json:"reason,omitempty"`
	}
	decideRequest struct {
		Decision string `json:"decision" example:"approve"`
		Comment  string `json:"comment,omitempty"`
	}
	listResponse struct {
		Requests []approval.Request `json:"requests"`
		Count    int                `json:"count"`
	}
)

// list answers the requests the caller may read.
//
// @Summary      List approval requests
// @Description  The caller's own requests, or for an administrator every request, newest first. status filters by pending, approved or denied; script_id by script.
// @Tags         Approvals
// @Produce      json
// @Param        status     query  string  false  "pending, approved or denied"
// @Param        script_id  query  string  false  "One script's requests"
// @Success      200  {object}  listResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/approvals [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	rs, err := h.cfg.Service.List(r.Context(), actor, approval.Filter{Status: q.Get("status"), ScriptID: q.Get("script_id")})
	if err != nil {
		answer(w, nil, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, listResponse{Requests: rs, Count: len(rs)})
}

// get answers one request.
//
// @Summary      Read an approval request
// @Description  One request with what changed since the version approved before. The requester and administrators read it.
// @Tags         Approvals
// @Produce      json
// @Param        id  path  string  true  "Request id"
// @Success      200  {object}  approval.Request
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/approvals/{id} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	req, err := h.cfg.Service.Get(r.Context(), actor, r.PathValue("id"))
	answer(w, req, err)
}

// submit files a request.
//
// @Summary      Request approval of a version
// @Description  Asks the administrators to approve a version of the caller's form to be served to callers with no session (purpose form_public). Each administrator is notified once.
// @Tags         Approvals
// @Accept       json
// @Produce      json
// @Param        body  body  submitRequest  true  "The request"
// @Success      200  {object}  approval.Request
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/approvals [post]
func (h *handler) submit(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	var in submitRequest
	if !ok || !decode(w, r, &in) {
		return
	}
	req, err := h.cfg.Service.Submit(r.Context(), actor, approval.Submit{Script: in.Script, Version: in.Version, Purpose: in.Purpose, Reason: in.Reason})
	h.record(r, actor, apprtool.CmdSubmit, map[string]any{"script": in.Script, "version": in.Version}, err)
	answer(w, req, err)
}

// decide records an administrator's decision.
//
// @Summary      Decide an approval request
// @Description  Approves or denies a pending request. Administrators decide every request, their own included (recorded as self-approved); anyone else is refused.
// @Tags         Approvals
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "Request id"
// @Param        body  body  decideRequest  true  "The decision"
// @Success      200  {object}  approval.Request
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      422  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/approvals/{id}/decide [post]
func (h *handler) decide(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	var in decideRequest
	if !ok || !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	req, err := h.cfg.Service.Decide(r.Context(), actor, id, in.Decision, in.Comment)
	h.record(r, actor, apprtool.CmdDecide, map[string]any{"id": id, "decision": in.Decision}, err)
	answer(w, req, err)
}

func (h *handler) actor(w http.ResponseWriter, r *http.Request) (approval.Actor, bool) {
	a := h.cfg.Actor(r)
	if a == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to use approvals")
		return approval.Actor{}, false
	}
	return *a, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "the request body is not the JSON this route takes")
		return false
	}
	return true
}

func status(err error) int {
	var refused *approval.RefusedError
	switch {
	case errors.Is(err, approval.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, approval.ErrNotAllowed):
		return http.StatusForbidden
	case errors.Is(err, approval.ErrDecided), errors.Is(err, approval.ErrPending):
		return http.StatusConflict
	case errors.As(err, &refused):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

func answer(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := status(err)
		msg := err.Error()
		if code == http.StatusInternalServerError {
			slog.Warn("approval request failed", "error", logsan.SanitizeForLog(msg))
			msg = "the approval could not be read or written"
		}
		httpjson.WriteError(w, code, msg)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, v)
}

// record audits one write as a manage_approval call from the portal.
func (h *handler) record(r *http.Request, actor approval.Actor, command string, params map[string]any, opErr error) {
	if h.cfg.Audit == nil {
		return
	}
	params["command"] = command
	ev := middleware.AuditEvent{
		ID: audit.NewEventID(), Timestamp: time.Now().UTC(), ToolName: apprtool.ToolName,
		Source: sourcePortal, Transport: "http", Authorized: true, Success: opErr == nil, Parameters: params,
		UserID: actor.Email, UserEmail: actor.Email, Persona: actor.Persona,
	}
	if opErr != nil {
		ev.ErrorMessage = opErr.Error()
	}
	if err := h.cfg.Audit.Log(r.Context(), ev); err != nil {
		slog.Warn("approval audit failed", "command", command, "error", logsan.SanitizeForLog(err.Error()))
	}
}
