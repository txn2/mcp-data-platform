package apichangeapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/apichange"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
)

// UsedByView is what uses one operation.
type UsedByView struct {
	Operation string             `json:"operation"`
	UsedBy    []apichange.Caller `json:"used_by"`
}

// operationUsedBy handles GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/operations/{operationId}/used-by.
//
// @Summary      Get what uses a catalog operation
// @Description  Lists the scripts, endpoints and prompts that use the operation through the connections that reference the catalog: scripts whose saved source names it, scripts a run of which called it in the last 90 days, the endpoints bound to those scripts, and prompts whose text names the operation and the connection. operationId is the spec's operationId, or "METHOD path" for an operation with none.
// @Tags         API Catalogs
// @Produce      json
// @Param        id           path  string  true  "Catalog ID"
// @Param        spec         path  string  true  "Spec name"
// @Param        operationId  path  string  true  "Operation ID"
// @Success      200  {object}  UsedByView
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-catalogs/{id}/specs/{spec}/operations/{operationId}/used-by [get]
func (h *handler) operationUsedBy(w http.ResponseWriter, r *http.Request) {
	k := specKey(r)
	spec, err := h.cfg.Catalogs.GetSpec(r.Context(), k.CatalogID, k.SpecName)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	refs, err := h.cfg.Catalogs.ReferencingConnections(r.Context(), k.CatalogID)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	conns := make([]string, 0, len(refs))
	for _, ref := range refs {
		conns = append(conns, ref.Name)
	}
	op := r.PathValue("operationId")
	view := UsedByView{Operation: op, UsedBy: []apichange.Caller{}}
	if h.cfg.Callers != nil {
		callers, err := h.cfg.Callers(r.Context(), conns, apichange.NewOperationIndex(spec.Effective()))
		if err != nil {
			h.readError(r.Context(), w, err)
			return
		}
		if list := callers[op]; list != nil {
			view.UsedBy = list
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// ConnectionUsedByView is what uses each of a connection's operations.
type ConnectionUsedByView struct {
	Connection string       `json:"connection"`
	CatalogID  string       `json:"catalog_id,omitempty"`
	Operations []UsedByView `json:"operations"`
}

// connectionUsedBy handles GET /api/v1/admin/connection-instances/api/{name}/used-by.
//
// @Summary      Get what uses an api connection
// @Description  Lists, for each operation of the connection's catalog that something uses, the scripts, endpoints and prompts that use it through this connection. Operations nothing uses are left out; a connection with no catalog lists none.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Connection name"
// @Success      200  {object}  ConnectionUsedByView
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/api/{name}/used-by [get]
func (h *handler) connectionUsedBy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	view := ConnectionUsedByView{Connection: name, Operations: []UsedByView{}}
	if h.cfg.CatalogOf != nil {
		view.CatalogID = h.cfg.CatalogOf(r.Context(), name)
	}
	if view.CatalogID == "" || h.cfg.Callers == nil {
		httpjson.WriteJSON(w, http.StatusOK, view)
		return
	}
	specs, err := h.cfg.Catalogs.ListSpecs(r.Context(), view.CatalogID)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	contents := make([]string, 0, len(specs))
	for _, s := range specs {
		contents = append(contents, s.Effective())
	}
	idx := apichange.NewOperationIndex(contents...)
	callers, err := h.cfg.Callers(r.Context(), []string{name}, idx)
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	for _, op := range idx.Ops() {
		if list := callers[op.ID]; len(list) > 0 {
			view.Operations = append(view.Operations, UsedByView{Operation: op.ID, UsedBy: list})
		}
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// WarningsView is what upstreams said about a connection's operations.
type WarningsView struct {
	Connection string              `json:"connection"`
	Warnings   []apichange.Warning `json:"warnings"`
}

// connectionWarnings handles GET /api/v1/admin/connection-instances/api/{name}/warnings.
//
// @Summary      Get an api connection's upstream warnings
// @Description  Lists what the connection's upstream said about its operations in its answers, the most recent first: a Deprecation or Sunset header (with its value), and a removal, a 404 or 410 from an operation that answered 2xx before. Each is recorded once per operation and kind, with when it was first and last seen, how often, and when it was sent to the change notification channels.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Connection name"
// @Success      200  {object}  WarningsView
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/api/{name}/warnings [get]
func (h *handler) connectionWarnings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	view := WarningsView{Connection: name, Warnings: []apichange.Warning{}}
	if h.cfg.Warnings != nil {
		list, err := h.cfg.Warnings(r.Context(), name)
		if err != nil {
			h.readError(r.Context(), w, err)
			return
		}
		view.Warnings = list
	}
	httpjson.WriteJSON(w, http.StatusOK, view)
}

// getSettings handles GET /api/v1/admin/settings/api-changes.
//
// @Summary      Get where API change reports are sent
// @Description  Returns the notification channels API catalog change reports and upstream warnings are sent to; none by default.
// @Tags         API Catalogs
// @Produce      json
// @Success      200  {object}  apichange.Settings
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/api-changes [get]
func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.cfg.ReadSettings(r.Context())
	if err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, s)
}

// putSettings handles PUT /api/v1/admin/settings/api-changes.
//
// @Summary      Set where API change reports are sent
// @Description  Sets the notification channels API catalog change reports and upstream warnings are sent to. Each must name a configured channel.
// @Tags         API Catalogs
// @Accept       json
// @Produce      json
// @Param        body  body  apichange.Settings  true  "Settings"
// @Success      200  {object}  apichange.Settings
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/api-changes [put]
func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in apichange.Settings
	if err := decode(w, r, &in); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Channels == nil {
		in.Channels = []string{}
	}
	for _, name := range in.Channels {
		if h.cfg.ChannelExists != nil && !h.cfg.ChannelExists(r.Context(), name) {
			httpjson.WriteError(w, http.StatusBadRequest, "no notification channel is named "+name)
			return
		}
	}
	if err := h.cfg.SaveSettings(r.Context(), in, h.author(r)); err != nil {
		h.readError(r.Context(), w, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, in)
}
