// Package apiroutesapi serves /api/v1/admin/api-route-connections: the
// operations a persona's API route rules can be written against.
//
// It is the authoring counterpart to the two browse surfaces #1478 shipped.
// Those answer what a reader reaches and are route-policy filtered; this one
// answers what exists, filtered by nothing. The difference matters here and
// nowhere else: an operator writing rules for one persona is not that persona,
// and a listing narrowed by the operator's own rules would hide exactly the
// operations they are trying to grant back.
//
// A decomposition seam of pkg/admin, which is at its package size budget; the
// parent registers it on the admin mux alongside the persona routes.
package apiroutesapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// ToolkitLister is the live toolkit registry, narrowed to the enumeration this
// surface needs. Satisfied by *registry.Registry.
type ToolkitLister interface {
	All() []registry.Toolkit
}

// Config wires the surface to the registry it reads.
type Config struct {
	// Toolkits is the live registry, read per request so a connection added
	// through the admin API appears without a restart. Nil leaves the route
	// unregistered.
	Toolkits ToolkitLister
}

type handler struct{ cfg Config }

// Register mounts the route. A deployment that cannot enumerate its toolkits
// registers nothing rather than answering "there are no API connections",
// which the editor would render as a persona having nothing to be granted.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Toolkits == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/api-route-connections", h.listConnections)
}

// operationResponse is one operation a rule can name. The path is the one the
// catalog declares, placeholders and all, because that is the form a rule is
// written in and the form the listing surfaces match on.
type operationResponse struct {
	OperationID string   `json:"operation_id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Spec        string   `json:"spec,omitempty"`
}

// connectionResponse is one api-kind connection with its whole operation index.
type connectionResponse struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	AuthMode    string `json:"auth_mode,omitempty"`
	CatalogID   string `json:"catalog_id,omitempty"`
	// Operations ships as an array, never null: the persona editor maps over
	// it. Empty for a connection with no catalog — callable by method and path
	// only, so its rules can be written as patterns but not selected.
	Operations []operationResponse `json:"operations"`
}

// connectionListResponse is the payload of the listing.
type connectionListResponse struct {
	Connections []connectionResponse `json:"connections"`
	Total       int                  `json:"total"`
}

// listConnections handles GET /api/v1/admin/api-route-connections.
//
// @Summary      List the API operations persona rules can name
// @Description  Returns every api-kind connection this deployment serves with the operations its catalog declares, narrowed by no persona. Backs the persona editor's API-endpoint scope, where an operator selects operations to allow or deny.
// @Tags         Personas
// @Produce      json
// @Success      200  {object}  connectionListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/api-route-connections [get]
func (h *handler) listConnections(w http.ResponseWriter, _ *http.Request) {
	out := connectionListResponse{Connections: []connectionResponse{}}
	for _, tk := range h.cfg.Toolkits.All() {
		api, ok := tk.(*apigatewaykit.Toolkit)
		if !ok {
			continue
		}
		for _, c := range api.CatalogConnections() {
			out.Connections = append(out.Connections, toConnectionResponse(c))
		}
	}
	out.Total = len(out.Connections)
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// toConnectionResponse projects the toolkit's view onto the wire shape.
func toConnectionResponse(c apigatewaykit.CatalogConnection) connectionResponse {
	out := connectionResponse{
		Name:        c.Name,
		Description: c.Description,
		BaseURL:     c.BaseURL,
		AuthMode:    c.AuthMode,
		CatalogID:   c.CatalogID,
		Operations:  make([]operationResponse, 0, len(c.Operations)),
	}
	for _, op := range c.Operations {
		out.Operations = append(out.Operations, operationResponse{
			OperationID: op.OperationID,
			Method:      op.Method,
			Path:        op.Path,
			Summary:     op.Summary,
			Tags:        op.Tags,
			Spec:        op.Spec,
		})
	}
	return out
}
