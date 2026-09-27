// Package flowhttp serves a managed script version's flow graph (#1906): the
// diagram the script page's Flow tab draws, derived from the version's source
// by internal/platform/scriptflow.
//
// It is REST only. The graph is for the people who own, review and depend on a
// script; an agent reads the code, so no MCP tool response carries it.
//
// It is mounted by the composition root beside scripthttp, and reads a version
// through scripthttp's own lookup, so a version is found, and not found, the
// way every other script route finds it. The read rule is the source's: on the
// portal everyone signed in (#1866), on the admin surface the admin
// authentication middleware.
package flowhttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Deps carries the version lookup and, for the portal, who is signed in.
type Deps struct {
	// Load resolves the script and version named by the path, or writes the
	// refusal and reports false (scripthttp.Handler.LoadScriptVersion).
	Load func(w http.ResponseWriter, r *http.Request) (*script.Script, *script.Version, bool)
	// SignedIn reports whether a portal request carries a user. The portal
	// route answers 401 without one.
	SignedIn func(r *http.Request) bool
}

// Handler serves the graph routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// graphResponse is one version's flow graph.
type graphResponse struct {
	ScriptID string `json:"script_id" example:"script_a1b2c3"`
	Version  int    `json:"version" example:"4"`
	scriptflow.Graph
}

// RegisterPortal mounts the portal route, wrapped in the portal authentication
// middleware.
func (h *Handler) RegisterPortal(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/scripts/{id}/versions/{version}/graph", wrap(http.HandlerFunc(h.portalGraph)))
}

// RegisterAdmin mounts the admin route under prefix, wrapped in the admin
// authentication middleware.
func (h *Handler) RegisterAdmin(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/scripts/{id}/versions/{version}/graph", wrap(http.HandlerFunc(h.adminGraph)))
}

// portalGraph returns one version's flow graph.
//
// @Summary      Get a script version's flow graph
// @Description  Returns the diagram of one version of a script, derived from its source: every platform call as a step, the values passed between them, the function boxes they are drawn in, and the run parameters with the steps each one reaches. A source that does not parse returns ok false with its findings and an empty diagram. Readable by everyone signed in, as the source is.
// @Tags         Scripts
// @Produce      json
// @Param        id       path  string   true  "Script ID"
// @Param        version  path  integer  true  "Version number"
// @Success      200  {object}  graphResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/scripts/{id}/versions/{version}/graph [get]
func (h *Handler) portalGraph(w http.ResponseWriter, r *http.Request) {
	if h.deps.SignedIn == nil || !h.deps.SignedIn(r) {
		httpjson.WriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	h.serve(w, r)
}

// adminGraph returns one version's flow graph.
//
// @Summary      Get a script version's flow graph
// @Description  Returns the diagram of one version of a script, derived from its source: every platform call as a step, the values passed between them, the function boxes they are drawn in, and the run parameters with the steps each one reaches. A source that does not parse returns ok false with its findings and an empty diagram.
// @Tags         Scripts
// @Produce      json
// @Param        id       path  string   true  "Script ID"
// @Param        version  path  integer  true  "Version number"
// @Success      200  {object}  graphResponse
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/versions/{version}/graph [get]
func (h *Handler) adminGraph(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r)
}

// serve answers the graph of the version the path names.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	_, v, ok := h.deps.Load(w, r)
	if !ok {
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, graphResponse{
		ScriptID: v.ScriptID, Version: v.Version, Graph: scriptflow.Derive(v.Source),
	})
}
