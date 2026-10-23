// Package mailactionshttp serves the owner's setting for whether a managed
// script may act on mail it reads (#2247). Off, a run that has called a mail_*
// tool is refused every later write and output (internal/mailbarrier); on,
// the script's contract shows that it reads mail and acts on it.
//
// It is mounted by scripthttp, which resolves the caller and the script,
// refuses everybody but the script's owner and an administrator, and lands
// the change through the edit funnel every portal edit crosses, which is what
// audits it.
package mailactionshttp

import (
	"encoding/json"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxBodyBytes bounds the one-field body of a change.
const maxBodyBytes = 1 << 10

// Deps carries the edit the route makes.
type Deps struct {
	// Edit applies mutate to the script in the path for a caller who owns
	// it, through the edit funnel, writing any refusal itself; it reports the
	// script as saved.
	Edit func(w http.ResponseWriter, r *http.Request, mutate func(*script.Script)) (*script.Script, bool)
}

// Handler serves the route.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Register mounts the route, wrapped in the portal authentication middleware.
func (h *Handler) Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("PUT /api/v1/portal/scripts/{id}/mail-actions", wrap(http.HandlerFunc(h.setMailActions)))
}

// mailActionsRequest sets whether a script may act on mail.
type mailActionsRequest struct {
	MailActions *bool `json:"mail_actions" example:"true"`
}

// mailActionsResponse reports the saved setting.
type mailActionsResponse struct {
	MailActions bool   `json:"mail_actions" example:"true"`
	Message     string `json:"message" example:"Saved. This script reads mail and acts on it."`
}

// setMailActions sets whether a script may act on mail it reads.
//
// @Summary      Set whether a script may act on mail it reads
// @Description  With mail_actions false (the default), a run that has called a mail_* tool is refused every later write-class tool call and every output, because a sender can steer what a message says. Turning it on lets the script act on mail; its contract then says so. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id            path  string              true  "Script ID"
// @Param        mail_actions  body  mailActionsRequest  true  "The setting"
// @Success      200  {object}  mailActionsResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/mail-actions [put]
func (h *Handler) setMailActions(w http.ResponseWriter, r *http.Request) {
	var req mailActionsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil || req.MailActions == nil {
		httpjson.WriteError(w, http.StatusBadRequest, `the body must be {"mail_actions": true} or {"mail_actions": false}`)
		return
	}
	saved, ok := h.deps.Edit(w, r, func(sc *script.Script) { sc.MailActions = *req.MailActions })
	if !ok {
		return
	}
	msg := "Saved. A run that reads mail cannot write anything afterwards."
	if saved.MailActions {
		msg = "Saved. This script reads mail and acts on it."
	}
	httpjson.WriteJSON(w, http.StatusOK, mailActionsResponse{MailActions: saved.MailActions, Message: msg})
}
