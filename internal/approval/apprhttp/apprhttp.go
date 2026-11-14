// Package apprhttp serves the Approvals queue on the portal (#2437, #2494):
// a person's own requests, every request for an administrator, filing one and
// deciding one, through the service manage_approval uses and under its
// rules. Every write is audited as a manage_approval call from the portal.
package apprhttp

import (
	"context"
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
	// Recent reads a call request's requester's recent calls of its
	// operation (#2437); nil answers none.
	Recent func(ctx context.Context, r *approval.Request) ([]RecentCall, error)
}

// RecentCall is one of a requester's calls of a held call's operation in the
// last day: when, how many items, and what came of it, never its arguments.
type RecentCall struct {
	At      time.Time `json:"at"`
	Items   int       `json:"items" example:"5"`
	Outcome string    `json:"outcome" example:"held"`
}

// RecentCallList is the requester's recent calls, newest first.
type RecentCallList struct {
	Data []RecentCall `json:"data"`
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
	mux.Handle("GET "+Prefix+"/{id}/calls", wrap(http.HandlerFunc(h.calls)))
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
		// Calls and Until are what approving a call request grants: that
		// many calls (at most what was asked; 0 is what was asked) until a
		// deadline (24 hours from now when omitted, at most 90 days).
		Calls int        `json:"calls,omitempty" example:"3"`
		Until *time.Time `json:"until,omitempty"`
	}
	listResponse struct {
		Requests []approval.Request `json:"requests"`
		Count    int                `json:"count"`
	}
)

// list answers the requests the caller may read.
//
// @Summary      List approval requests
// @Description  The caller's own requests and the held calls whose policies name them, or for an administrator every request, newest first. status filters by pending, approved or denied; script_id by script; kind by call, handler_version, form or site.
// @Tags         Approvals
// @Produce      json
// @Param        status     query  string  false  "pending, approved or denied"
// @Param        script_id  query  string  false  "One script's requests"
// @Param        kind       query  string  false  "call, handler_version, form or site"
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
	rs, err := h.cfg.Service.List(r.Context(), actor, approval.Filter{Status: q.Get("status"), ScriptID: q.Get("script_id"), Kind: q.Get("kind")})
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

// calls lists the requester's recent calls of a held call's operation.
//
// @Summary      A held call's recent calls
// @Description  The requester's calls of a call request's operation on its connection in the last 24 hours, newest first, from the audit record: when each was made, how many items it carried, and whether it ran, ran on an allowance, failed, was held or was refused. Never a call's arguments. Readable by whoever may read the request: an administrator, its requester, or an approver its policies name. Any other request answers an empty list.
// @Tags         Approvals
// @Produce      json
// @Param        id  path  string  true  "Request id"
// @Success      200  {object}  RecentCallList
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/approvals/{id}/calls [get]
func (h *handler) calls(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	req, err := h.cfg.Service.Get(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		answer(w, nil, err)
		return
	}
	list := RecentCallList{Data: []RecentCall{}}
	if h.cfg.Recent != nil {
		got, err := h.cfg.Recent(r.Context(), req)
		if err != nil {
			answer(w, nil, err)
			return
		}
		list.Data = append(list.Data, got...)
	}
	answer(w, list, nil)
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
// @Description  Approves or denies a pending request. A call request a policy held is decided by the approvers its policies named or an administrator, never by its requester unless an administrator, and approving it grants calls until a deadline. Administrators decide every other request, their own included (recorded as self-approved); anyone else is refused.
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
	req, err := h.cfg.Service.DecideAllowance(r.Context(), actor, id, approval.Ruling{
		Decision: in.Decision, Comment: in.Comment, Allowance: approval.Allowance{Calls: in.Calls, Until: in.Until},
	})
	h.record(r, actor, apprtool.CmdDecide, map[string]any{"id": id, "decision": in.Decision, "calls": in.Calls}, err)
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
