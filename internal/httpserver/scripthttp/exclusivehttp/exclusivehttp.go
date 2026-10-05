// Package exclusivehttp serves the owner's setting that a managed script runs
// one at a time (#1986): no run starts while another run of it is pending or
// running, whatever started it.
//
// It is mounted by scripthttp, which resolves the caller and the script,
// refuses everybody but the script's owner and an administrator, and lands
// the change through the edit funnel every portal edit crosses.
package exclusivehttp

import (
	"encoding/json"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxBodyBytes bounds the one-boolean body of a change.
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
	mux.Handle("PUT /api/v1/portal/scripts/{id}/exclusive", wrap(http.HandlerFunc(h.setExclusive)))
}

// exclusiveRequest sets whether a script runs one at a time.
type exclusiveRequest struct {
	// Exclusive true holds the script to one pending or running run, whatever
	// started it.
	Exclusive *bool `json:"exclusive"`
}

// exclusiveResponse reports the saved setting.
type exclusiveResponse struct {
	Exclusive bool   `json:"exclusive" example:"true"`
	Message   string `json:"message" example:"Saved. A run cannot start while another run of this script is pending or running."`
}

// setExclusive sets whether a script runs one at a time.
//
// @Summary      Set whether a script runs one at a time
// @Description  With exclusive true, a script has at most one pending or running run, whatever started it: a scheduled fire while a run is open is recorded as skipped_overlap, and a run_script or portal run is refused with 409 naming the open run. Turning it on while more than one run is open is refused with 409. Restricted to the script's owner and to administrators.
// @Tags         Scripts
// @Accept       json
// @Produce      json
// @Param        id         path  string            true  "Script ID"
// @Param        exclusive  body  exclusiveRequest  true  "The setting"
// @Success      200  {object}  exclusiveResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/exclusive [put]
func (h *Handler) setExclusive(w http.ResponseWriter, r *http.Request) {
	var req exclusiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil || req.Exclusive == nil {
		httpjson.WriteError(w, http.StatusBadRequest, `the body must be {"exclusive": true} or {"exclusive": false}`)
		return
	}
	saved, ok := h.deps.Edit(w, r, func(sc *script.Script) { sc.Exclusive = *req.Exclusive })
	if !ok {
		return
	}
	msg := "Saved. Runs of this script may overlap."
	if saved.Exclusive {
		msg = "Saved. A run cannot start while another run of this script is pending or running."
	}
	httpjson.WriteJSON(w, http.StatusOK, exclusiveResponse{Exclusive: saved.Exclusive, Message: msg})
}
