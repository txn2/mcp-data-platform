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
	"context"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow/flowcompare"
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
	// Version reads another version of the same script, for ?compare=
	// (#1908) and for the version a run executed (#1907). Nil when the store
	// is absent; the comparison is then 404.
	Version func(ctx context.Context, scriptID string, version int) (*script.Version, error)
	// Run reads the run in the path for a signed-in caller entitled to it, or
	// writes the refusal (scripthttp.Handler.ReadableRun). Nil leaves the run
	// route unmounted.
	Run func(w http.ResponseWriter, r *http.Request) (*script.Run, bool)
	// Audit reads a run's audited calls. Nil draws a run with no calls.
	Audit AuditQuerier
	// Tiles reads a script's tile (#1909). Nil leaves the tile route
	// unmounted.
	Tiles TileReader
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
	if h.deps.Run != nil && h.deps.Version != nil {
		h.registerRunFlow(mux, wrap)
	}
	if h.deps.Tiles != nil {
		mux.Handle("GET /api/v1/portal/scripts/{id}/thumbnail", wrap(http.HandlerFunc(h.scriptTile)))
	}
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
// @Param        compare  query integer  false "An older version to compare with: the graph then marks each node added or changed (with what it said before), and carries the older version's removed nodes and their edges"
// @Success      200  {object}  graphResponse
// @Failure      400  {object}  httpjson.ProblemDetail
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
// @Param        compare  query integer  false "An older version to compare with: the graph then marks each node added or changed (with what it said before), and carries the older version's removed nodes and their edges"
// @Success      200  {object}  graphResponse
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/scripts/{id}/versions/{version}/graph [get]
func (h *Handler) adminGraph(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r)
}

// serve answers the graph of the version the path names, compared with an
// older version when ?compare= names one.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	_, v, ok := h.deps.Load(w, r)
	if !ok {
		return
	}
	g := scriptflow.Derive(v.Source)
	if raw := r.URL.Query().Get("compare"); raw != "" {
		older, ok := h.olderVersion(w, r, v.ScriptID, raw)
		if !ok {
			return
		}
		g = flowcompare.Compare(scriptflow.Derive(older.Source), g, older.Version)
	}
	httpjson.WriteJSON(w, http.StatusOK, graphResponse{ScriptID: v.ScriptID, Version: v.Version, Graph: g})
}

// olderVersion reads the version ?compare= names, writing the refusal when it
// cannot: 400 for a value that is not a version number, 404 for a version the
// script does not have.
func (h *Handler) olderVersion(w http.ResponseWriter, r *http.Request, scriptID, raw string) (*script.Version, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		httpjson.WriteError(w, http.StatusBadRequest, "compare must be a version number")
		return nil, false
	}
	if h.deps.Version == nil {
		httpjson.WriteError(w, http.StatusNotFound, errVersionNotFound)
		return nil, false
	}
	older, err := h.deps.Version(r.Context(), scriptID, n)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the version to compare with")
		return nil, false
	}
	if older == nil {
		httpjson.WriteError(w, http.StatusNotFound, errVersionNotFound)
		return nil, false
	}
	return older, true
}

// errVersionNotFound is the answer for a version the script does not have.
const errVersionNotFound = "version not found"
