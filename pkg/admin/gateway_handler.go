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
	h.mux.HandleFunc("GET /api/v1/admin/gateway/connections/{name}/status", h.getGatewayConnectionStatus)
	if h.deps.ConnectionStore != nil {
		h.mux.HandleFunc("POST /api/v1/admin/gateway/connections/{name}/refresh", h.refreshGatewayConnection)
		h.mux.HandleFunc("POST /api/v1/admin/gateway/connections/{name}/reacquire-oauth", h.reacquireGatewayOAuth)
	}
}

// getGatewayConnectionStatus handles GET /api/v1/admin/gateway/connections/{name}/status.
//
// @Summary      Get a gateway connection's runtime status
// @Description  Reports whether the connection is healthy, its tool count, and (when AuthMode=oauth) the current token state — expiry, last refreshed, last error.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Gateway connection name"
// @Success      200  {object}  gatewaykit.ConnectionStatus
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/status [get]
func (h *Handler) getGatewayConnectionStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	tk := h.findGatewayToolkit()
	if tk == nil {
		writeError(w, http.StatusConflict, "gateway toolkit is not registered")
		return
	}
	status := tk.Status(name)
	if status == nil {
		writeError(w, http.StatusNotFound, "gateway connection not found")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// reacquireGatewayOAuth handles POST /api/v1/admin/gateway/connections/{name}/reacquire-oauth.
//
// @Summary      Force a fresh OAuth token exchange
// @Description  Triggers a client_credentials grant against the configured OAuth token URL, replacing the cached token. Used to recover from upstream-side credential rotations or to verify the configured client_id/client_secret without waiting for token expiry.
// @Tags         Connections
// @Produce      json
// @Param        name  path  string  true  "Gateway connection name"
// @Success      200  {object}  gatewaykit.ConnectionStatus
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      502  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/gateway/connections/{name}/reacquire-oauth [post]
func (h *Handler) reacquireGatewayOAuth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue(pathKeyName)
	tk := h.findGatewayToolkit()
	if tk == nil {
		writeError(w, http.StatusConflict, "gateway toolkit is not registered")
		return
	}
	if err := tk.ReacquireOAuthToken(r.Context(), name); err != nil {
		if errors.Is(err, gatewaykit.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "gateway connection not found")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	status := tk.Status(name)
	writeJSON(w, http.StatusOK, status)
}

// findGatewayToolkit returns the live *gatewaykit.Toolkit (if any) so the
// status / reacquire endpoints can call its methods directly. Returns nil
// when no gateway toolkit is registered.
func (h *Handler) findGatewayToolkit() *gatewaykit.Toolkit {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	for _, tk := range h.deps.ToolkitRegistry.All() {
		if gw, ok := tk.(*gatewaykit.Toolkit); ok {
			return gw
		}
	}
	return nil
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
	name := r.PathValue(pathKeyName)
	cfg, ok := h.parseTestConnectionConfig(w, r, name)
	if !ok {
		return
	}
	if h.shortCircuitUnauthorizedAuthCode(w, name, cfg) {
		return
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

// parseTestConnectionConfig decodes the request body, merges any redacted
// fields from the stored row, parses the config, and applies defaults.
// Writes the appropriate HTTP error and returns ok=false on any failure path.
func (h *Handler) parseTestConnectionConfig(w http.ResponseWriter, r *http.Request, name string) (gatewaykit.Config, bool) {
	var req testGatewayConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return gatewaykit.Config{}, false
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	if hasRedactedValues(req.Config) && h.deps.ConnectionStore != nil {
		existing, err := h.deps.ConnectionStore.Get(r.Context(), gatewaykit.Kind, name)
		if err == nil && existing != nil {
			req.Config = mergeRedactedFields(req.Config, existing.Config)
		}
	}
	cfg, err := gatewaykit.ParseConfig(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return gatewaykit.Config{}, false
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	return cfg, true
}

// shortCircuitUnauthorizedAuthCode handles the special case where a Test
// click hits an authorization_code OAuth connection with no stored token.
// Probe would fail with a cryptic upstream error; instead we return a
// clear 200 message pointing the operator at the Connect button. Returns
// true when it wrote a response (caller must stop).
func (h *Handler) shortCircuitUnauthorizedAuthCode(w http.ResponseWriter, name string, cfg gatewaykit.Config) bool {
	if cfg.AuthMode != gatewaykit.AuthModeOAuth ||
		cfg.OAuth.Grant != gatewaykit.OAuthGrantAuthorizationCode ||
		h.connectionHasOAuthToken(name) {
		return false
	}
	writeJSON(w, http.StatusOK, testGatewayConnectionResponse{
		Healthy: false,
		Error:   "Not connected: this OAuth connection needs browser sign-in before it can be tested. Use the Connect button in the OAuth status panel to authorize, then test again.",
	})
	return true
}

// connectionHasOAuthToken reports whether the live gateway toolkit has an
// authorized OAuth token for the named connection. Used by the test
// endpoint to short-circuit authorization_code connections that cannot be
// probed without a stored token. Returns false when no toolkit is wired,
// the connection is not registered, or the toolkit reports needs_reauth.
func (h *Handler) connectionHasOAuthToken(name string) bool {
	tk := h.findGatewayToolkit()
	if tk == nil {
		return false
	}
	status := tk.Status(name)
	if status == nil || status.OAuth == nil {
		return false
	}
	return !status.OAuth.NeedsReauth
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
	name := r.PathValue(pathKeyName)

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
