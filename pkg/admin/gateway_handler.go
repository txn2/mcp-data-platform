package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// registerGatewayRoutes registers admin endpoints specific to the gateway
// toolkit kind. Connection CRUD is handled by the generic connection-instance
// endpoints in connection_handler.go; these routes cover operations that
// only make sense for gateway connections.
func (h *Handler) registerGatewayRoutes() {
	if !h.isMutable() {
		return
	}
	h.mux.HandleFunc("POST /api/v1/admin/gateway/connections/{name}/test", h.testGatewayConnection)
	if h.deps.ConnectionStore != nil {
		h.mux.HandleFunc("POST /api/v1/admin/gateway/connections/{name}/refresh", h.refreshGatewayConnection)
	}
}

// testGatewayConnectionRequest is the JSON body for the test endpoint.
type testGatewayConnectionRequest struct {
	Config map[string]any `json:"config"`
}

// testGatewayConnectionResponse reports the outcome of a test dial.
type testGatewayConnectionResponse struct {
	Healthy bool                   `json:"healthy"`
	Tools   []gatewaykit.ProbeTool `json:"tools,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

// testGatewayConnection handles POST /api/v1/admin/gateway/connections/{name}/test.
//
// @Summary      Test a gateway connection config
// @Description  Dials the upstream described by the submitted config, lists its tools, and returns them. Does not persist anything. When the submitted config contains "[REDACTED]" sensitive fields and a row with this name already exists, the redacted fields are merged from the stored config.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        name  path  string                        true  "Gateway connection name"
// @Param        body  body  testGatewayConnectionRequest  true  "Config to test"
// @Success      200   {object}  testGatewayConnectionResponse
// @Failure      400   {object}  problemDetail
// @Failure      502   {object}  testGatewayConnectionResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/test [post]
func (h *Handler) testGatewayConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req testGatewayConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}

	// Merge redacted credentials from the stored row if any sensitive field
	// came in as "[REDACTED]".
	if hasRedactedValues(req.Config) && h.deps.ConnectionStore != nil {
		existing, err := h.deps.ConnectionStore.Get(r.Context(), gatewaykit.Kind, name)
		if err == nil && existing != nil {
			req.Config = mergeRedactedFields(req.Config, existing.Config)
		}
	}

	cfg, err := gatewaykit.ParseConfig(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}

	ctx, cancel := context.WithTimeout(r.Context(), cfg.ConnectTimeout)
	defer cancel()

	tools, err := gatewaykit.Probe(ctx, cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, testGatewayConnectionResponse{
			Healthy: false,
			Error:   err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, testGatewayConnectionResponse{
		Healthy: true,
		Tools:   tools,
	})
}

// refreshGatewayConnectionResponse reports the post-refresh tool set.
type refreshGatewayConnectionResponse struct {
	Healthy bool     `json:"healthy"`
	Tools   []string `json:"tools,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// refreshGatewayConnection handles POST /api/v1/admin/gateway/connections/{name}/refresh.
//
// @Summary      Refresh a live gateway connection
// @Description  Re-dials a configured gateway connection using the stored config, re-discovers its tool set, and swaps the live connection atomically. Used after an upstream adds, removes, or changes tools.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Gateway connection name"
// @Success      200   {object}  refreshGatewayConnectionResponse
// @Failure      404   {object}  problemDetail
// @Failure      502   {object}  refreshGatewayConnectionResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/refresh [post]
func (h *Handler) refreshGatewayConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	inst, err := h.deps.ConnectionStore.Get(r.Context(), gatewaykit.Kind, name)
	if err != nil {
		if errors.Is(err, platform.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "gateway connection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load connection config")
		return
	}

	cm := h.findConnectionManager(gatewaykit.Kind)
	if cm == nil {
		writeError(w, http.StatusConflict, "gateway toolkit is not registered")
		return
	}

	// Swap: remove if present, then add. The Remove is best-effort so a
	// connection that went unhealthy (dial failed at last AddConnection and
	// wasn't stored in the live toolkit) can still be refreshed.
	if cm.HasConnection(name) {
		if rerr := cm.RemoveConnection(name); rerr != nil {
			writeError(w, http.StatusInternalServerError, "failed to remove existing connection")
			return
		}
	}
	if aerr := cm.AddConnection(name, inst.Config); aerr != nil {
		writeJSON(w, http.StatusBadGateway, refreshGatewayConnectionResponse{
			Healthy: false,
			Error:   aerr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, refreshGatewayConnectionResponse{
		Healthy: true,
		Tools:   connectionTools(cm, name),
	})
}

// connectionTools returns the current tool names for a named connection, if
// the live toolkit exposes a ConnectionLister.
func connectionTools(cm toolkit.ConnectionManager, name string) []string {
	lister, ok := cm.(toolkit.ConnectionLister)
	if !ok {
		return nil
	}
	// The ConnectionLister interface doesn't expose tools per-connection, so
	// we fall back to the whole toolkit's Tools() by casting to a minimal
	// shape. We only use this for presentation.
	type toolLister interface {
		Tools() []string
	}
	tl, ok := cm.(toolLister)
	if !ok {
		_ = lister
		return nil
	}
	prefix := name + gatewaykit.NamespaceSeparator
	var out []string
	for _, t := range tl.Tools() {
		if len(t) > len(prefix) && t[:len(prefix)] == prefix {
			out = append(out, t)
		}
	}
	return out
}
