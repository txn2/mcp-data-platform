// Package granthttp serves a managed script's run grants on its portal page
// (#1846): who other than the owner may run it, and the owner's adding and
// withdrawing of them.
//
// It is mounted by scripthttp, which resolves the caller and the script and
// refuses everybody but the script's owner and an administrator before any
// handler here runs. Every grant and withdrawal is audited.
package granthttp

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptgrant"
)

// Audited act names, the tool name each is filtered by in the audit log.
const (
	auditToolGrant  = "script_grant"
	auditToolRevoke = "script_revoke"
)

// maxGrantBodyBytes bounds a grant request: two short names.
const maxGrantBodyBytes = 4 << 10

// Deps carries the grant store, the ownership gate and the audit writer.
type Deps struct {
	Grants scriptgrant.Store
	// Owned resolves the script in the path for a caller who owns it, or
	// writes the refusal and reports false. actor is who the caller is.
	Owned func(w http.ResponseWriter, r *http.Request) (scriptID, actor string, ok bool)
	// Audit records one act on the script. Nil records nothing.
	Audit func(r *http.Request, tool, scriptID string, params map[string]any, opErr error)
}

// Handler serves the grant routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the grant routes, each wrapped in the portal authentication
// middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/grants", wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/portal/scripts/{id}/grants", wrap(http.HandlerFunc(h.add)))
	mux.Handle("DELETE /api/v1/portal/scripts/{id}/grants/{kind}/{principal}", wrap(http.HandlerFunc(h.remove)))
}

// grantRequest names the principal a grant is for.
type grantRequest struct {
	Kind      string `json:"principal_kind" example:"api_key"`
	Principal string `json:"principal" example:"reporting-app"`
}

// grantListResponse is a script's grants.
type grantListResponse struct {
	Data  []scriptgrant.Grant `json:"data"`
	Total int                 `json:"total" example:"1"`
}

// list returns a script's grants.
//
// @Summary      List a script's run grants
// @Description  Returns who other than the owner may run this script: each persona, role or API key it is granted to, who granted it, and when. A grantee may run the script and read the runs it started, with their outputs; it may not read the source or change the script, and the run executes as the script with its author's roles. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Produce      json
// @Param        id  path  string  true  "Script ID"
// @Success      200  {object}  grantListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/grants [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	scriptID, _, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	h.writeList(w, r, scriptID, http.StatusOK)
}

// add grants a principal the right to run the script.
//
// @Summary      Grant a script's runs
// @Description  Lets a persona, a role or an API key (by name) run this script over HTTP and read the runs it started. Granting what is already granted changes nothing. Answers with the script's grants as they now stand. Restricted to the script's owner and to administrators, and audited.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id     path  string        true  "Script ID"
// @Param        grant  body  grantRequest  true  "The principal to grant"
// @Success      201  {object}  grantListResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/grants [post]
func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	scriptID, actor, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	var req grantRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGrantBodyBytes)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	g := scriptgrant.Grant{ScriptID: scriptID, Kind: req.Kind, Principal: req.Principal, GrantedBy: actor}
	if err := g.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := h.deps.Grants.Add(r.Context(), g)
	h.audit(r, auditToolGrant, g, err)
	if err != nil {
		slog.Error("failed to grant a script", "script_id", scriptID, "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to grant the script")
		return
	}
	h.writeList(w, r, scriptID, http.StatusCreated)
}

// remove withdraws a grant.
//
// @Summary      Withdraw a script run grant
// @Description  Withdraws one grant; the principal can no longer run the script. Runs it already started are unaffected. Answers with the script's grants as they now stand, or 404 when there was no such grant. Restricted to the script's owner and to administrators, and audited.
// @Tags         Scripts
// @Produce      json
// @Param        id         path  string  true  "Script ID"
// @Param        kind       path  string  true  "persona, role or api_key"
// @Param        principal  path  string  true  "The persona, role or API key name"
// @Success      200  {object}  grantListResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/grants/{kind}/{principal} [delete]
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	scriptID, _, ok := h.deps.Owned(w, r)
	if !ok {
		return
	}
	g := scriptgrant.Grant{ScriptID: scriptID, Kind: r.PathValue("kind"), Principal: r.PathValue("principal")}
	removed, err := h.deps.Grants.Remove(r.Context(), g)
	h.audit(r, auditToolRevoke, g, err)
	if err != nil {
		slog.Error("failed to withdraw a script grant", "script_id", scriptID, "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to withdraw the grant")
		return
	}
	if !removed {
		httpjson.WriteError(w, http.StatusNotFound, "this script has no such grant")
		return
	}
	h.writeList(w, r, scriptID, http.StatusOK)
}

// writeList answers with the script's grants as they stand.
func (h *Handler) writeList(w http.ResponseWriter, r *http.Request, scriptID string, status int) {
	grants, err := h.deps.Grants.List(r.Context(), scriptID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list the script's grants")
		return
	}
	httpjson.WriteJSON(w, status, grantListResponse{Data: grants, Total: len(grants)})
}

// audit records one grant or withdrawal, whether or not the store took it.
func (h *Handler) audit(r *http.Request, tool string, g scriptgrant.Grant, opErr error) {
	if h.deps.Audit == nil {
		return
	}
	h.deps.Audit(r, tool, g.ScriptID, map[string]any{"principal_kind": g.Kind, "principal": g.Principal}, opErr)
}
