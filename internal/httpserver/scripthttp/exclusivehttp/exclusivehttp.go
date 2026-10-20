// Package exclusivehttp serves the owner's setting for whether a managed
// script's runs may overlap (#1986, #2209): off, refuse (no run starts while
// another is pending or running, whatever started it) or queue (another run
// waits until none is running).
//
// It is mounted by scripthttp, which resolves the caller and the script,
// refuses everybody but the script's owner and an administrator, and lands
// the change through the edit funnel every portal edit crosses.
package exclusivehttp

import (
	"encoding/json"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/runstate"

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
	mux.Handle("PUT /api/v1/portal/scripts/{id}/exclusive", wrap(http.HandlerFunc(h.setExclusive)))
}

// exclusiveRequest sets whether a script's runs may overlap.
type exclusiveRequest struct {
	// Exclusive is off, refuse or queue; true and false are read as refuse
	// and off.
	Exclusive *runstate.ExclusiveMode `json:"exclusive" swaggertype:"string" enums:"off,refuse,queue"`
}

// exclusiveResponse reports the saved setting.
type exclusiveResponse struct {
	Exclusive runstate.ExclusiveMode `json:"exclusive" swaggertype:"string" enums:"off,refuse,queue" example:"queue"`
	Message   string                 `json:"message" example:"Saved. A run started while another run of this script is running waits its turn."`
}

// savedMessages say what each mode does, as the route reports it saved.
var savedMessages = map[runstate.ExclusiveMode]string{
	runstate.ExclusiveOff:    "Saved. Runs of this script may overlap.",
	runstate.ExclusiveRefuse: "Saved. A run cannot start while another run of this script is pending or running.",
	runstate.ExclusiveQueue:  "Saved. A run started while another run of this script is running waits its turn.",
}

// setExclusive sets whether a script's runs may overlap.
//
// @Summary      Set whether a script's runs may overlap
// @Description  exclusive is off, refuse or queue (true and false are read as refuse and off). Under refuse a script has at most one pending or running run, whatever started it: a scheduled fire while a run is open is recorded as skipped_overlap, and a run_script or portal run is refused with 409 naming the open run. Under queue another run is accepted and stays pending until no other run of the script is running. Turning refuse on while more than one run is open, or queue on while more than one is running, is refused with 409. Restricted to the script's owner and to administrators.
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
		httpjson.WriteError(w, http.StatusBadRequest, `the body must be {"exclusive": "off"}, {"exclusive": "refuse"} or {"exclusive": "queue"}`)
		return
	}
	saved, ok := h.deps.Edit(w, r, func(sc *script.Script) { sc.Exclusive = *req.Exclusive })
	if !ok {
		return
	}
	mode := saved.Exclusive.Or()
	httpjson.WriteJSON(w, http.StatusOK, exclusiveResponse{Exclusive: mode, Message: savedMessages[mode]})
}
