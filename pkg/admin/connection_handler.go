package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ConnectionStore abstracts platform.ConnectionStore for testability.
type ConnectionStore interface {
	List(ctx context.Context) ([]platform.ConnectionInstance, error)
	Get(ctx context.Context, kind, name string) (*platform.ConnectionInstance, error)
	Set(ctx context.Context, inst platform.ConnectionInstance) error
	Delete(ctx context.Context, kind, name string) error
}

// Structured log field name constants.
const (
	logKeyKind  = "kind"
	logKeyName  = "name"
	logKeyError = "error"
)

// knownConnectionKinds lists the toolkit kinds that support multiple configurable
// connection instances. DataHub is excluded because the platform connects to a
// single catalog instance configured in the YAML file.
var knownConnectionKinds = map[string]bool{
	"trino": true,
	"s3":    true,
	"mcp":   true,
}

// registerConnectionRoutes registers connection instance CRUD endpoints.
func (h *Handler) registerConnectionRoutes() {
	// Effective view: merges file connections with DB instances.
	h.mux.HandleFunc("GET /api/v1/admin/connection-instances/effective", h.listEffectiveConnections)
	if h.deps.ConnectionStore != nil {
		h.mux.HandleFunc("GET /api/v1/admin/connection-instances", h.listConnectionInstances)
		h.mux.HandleFunc("GET /api/v1/admin/connection-instances/{kind}/{name}", h.getConnectionInstance)
	}
	if h.isMutable() && h.deps.ConnectionStore != nil {
		h.mux.HandleFunc("PUT /api/v1/admin/connection-instances/{kind}/{name}", h.setConnectionInstance)
		h.mux.HandleFunc("DELETE /api/v1/admin/connection-instances/{kind}/{name}", h.deleteConnectionInstance)
	}
}

// listConnectionInstances handles GET /api/v1/admin/connection-instances.
//
// @Summary      List connection instances
// @Description  Returns all database-managed connection instances.
// @Tags         Connections
// @Produce      json
// @Success      200  {array}   platform.ConnectionInstance
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances [get]
func (h *Handler) listConnectionInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := h.deps.ConnectionStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connection instances")
		return
	}
	if instances == nil {
		instances = []platform.ConnectionInstance{}
	}
	for i := range instances {
		instances[i].Config = redactConnectionConfig(instances[i].Config)
	}
	writeJSON(w, http.StatusOK, instances)
}

// getConnectionInstance handles GET /api/v1/admin/connection-instances/{kind}/{name}.
//
// @Summary      Get connection instance
// @Description  Returns a single database-managed connection instance by kind and name.
// @Tags         Connections
// @Produce      json
// @Param        kind  path  string  true  "Toolkit kind (trino, datahub, s3)"
// @Param        name  path  string  true  "Instance name"
// @Success      200   {object}  platform.ConnectionInstance
// @Failure      404   {object}  problemDetail
// @Failure      500   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/{kind}/{name} [get]
func (h *Handler) getConnectionInstance(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name := r.PathValue("name")

	inst, err := h.deps.ConnectionStore.Get(r.Context(), kind, name)
	if err != nil {
		if errors.Is(err, platform.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "connection instance not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get connection instance")
		return
	}
	inst.Config = redactConnectionConfig(inst.Config)
	writeJSON(w, http.StatusOK, inst)
}

// setConnectionInstanceRequest is the JSON body for creating/updating a connection instance.
type setConnectionInstanceRequest struct {
	Config      map[string]any `json:"config"`
	Description string         `json:"description" example:"Production data warehouse"`
}

// setConnectionInstance handles PUT /api/v1/admin/connection-instances/{kind}/{name}.
//
// @Summary      Create or update connection instance
// @Description  Creates or updates a database-managed connection instance.
// @Tags         Connections
// @Accept       json
// @Produce      json
// @Param        kind  path  string                        true  "Toolkit kind (trino, datahub, s3)"
// @Param        name  path  string                        true  "Instance name"
// @Param        body  body  setConnectionInstanceRequest  true  "Connection instance data"
// @Success      200   {object}  platform.ConnectionInstance
// @Failure      400   {object}  problemDetail
// @Failure      500   {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/{kind}/{name} [put]
func (h *Handler) setConnectionInstance(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name := r.PathValue("name")

	if !knownConnectionKinds[kind] {
		writeError(w, http.StatusBadRequest, "unknown connection kind: "+kind)
		return
	}

	var req setConnectionInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	author := ""
	if user := GetUser(r.Context()); user != nil {
		author = user.Email
		if author == "" {
			author = user.UserID
		}
	}

	if req.Config == nil {
		req.Config = map[string]any{}
	}

	// If any sensitive field is "[REDACTED]", preserve the existing value from the store.
	if hasRedactedValues(req.Config) {
		existing, err := h.deps.ConnectionStore.Get(r.Context(), kind, name)
		if err == nil && existing != nil {
			req.Config = mergeRedactedFields(req.Config, existing.Config)
		}
	}

	inst := platform.ConnectionInstance{
		Kind:        kind,
		Name:        name,
		Config:      req.Config,
		Description: req.Description,
		CreatedBy:   author,
		UpdatedAt:   time.Now(),
	}

	if err := h.deps.ConnectionStore.Set(r.Context(), inst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save connection instance")
		return
	}

	h.activateConnection(inst)

	inst.Config = redactConnectionConfig(inst.Config)
	writeJSON(w, http.StatusOK, inst)
}

// deleteConnectionInstance handles DELETE /api/v1/admin/connection-instances/{kind}/{name}.
//
// @Summary      Delete connection instance
// @Description  Deletes a database-managed connection instance.
// @Tags         Connections
// @Param        kind  path  string  true  "Toolkit kind (trino, datahub, s3)"
// @Param        name  path  string  true  "Instance name"
// @Success      204
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connection-instances/{kind}/{name} [delete]
func (h *Handler) deleteConnectionInstance(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name := r.PathValue("name")

	if err := h.deps.ConnectionStore.Delete(r.Context(), kind, name); err != nil {
		if errors.Is(err, platform.ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, "connection instance not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete connection instance")
		return
	}

	// Hot-remove: if the toolkit supports dynamic connections, remove it live.
	h.hotRemoveConnection(kind, name)

	// Update the connection source map.
	if h.deps.ConnectionSources != nil {
		h.deps.ConnectionSources.Remove(kind, name)
	}

	w.WriteHeader(http.StatusNoContent)
}

// effectiveConnection merges a live toolkit connection with its DB instance (if any).
type effectiveConnection struct {
	Kind        string         `json:"kind" example:"trino"`
	Name        string         `json:"name" example:"acme-warehouse"`
	Connection  string         `json:"connection" example:"acme-warehouse"`
	Description string         `json:"description,omitempty" example:"Production data warehouse"`
	Source      string         `json:"source" example:"file"` // "file", "database", or "both"
	Tools       []string       `json:"tools" example:"trino_query,trino_describe_table"`
	Config      map[string]any `json:"config,omitempty"`
	CreatedBy   string         `json:"created_by,omitempty" example:"admin@example.com"`
	UpdatedAt   *time.Time     `json:"updated_at,omitempty"`
}

// listEffectiveConnections returns the merged view of file-configured and DB-managed connections.
func (h *Handler) listEffectiveConnections(w http.ResponseWriter, r *http.Request) {
	live := h.collectLiveConnections()

	var dbInstances []platform.ConnectionInstance
	if h.deps.ConnectionStore != nil {
		var err error
		dbInstances, err = h.deps.ConnectionStore.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load connection instances")
			return
		}
	}

	result := mergeConnections(live, dbInstances)
	for i := range result {
		result[i].Config = redactConnectionConfig(result[i].Config)
	}
	writeJSON(w, http.StatusOK, result)
}

// liveConnectionInfo holds metadata from a running toolkit instance.
type liveConnectionInfo struct {
	kind, name, connection string
	description            string
	tools                  []string
	config                 map[string]any
}

// collectLiveConnections returns info for running data toolkit instances (trino, s3).
// Built-in toolkits like knowledge and portal are excluded.
// Config is populated from the raw toolkits YAML when available.
// Multi-connection toolkits (those implementing toolkit.ConnectionLister) are
// expanded into one entry per connection so the admin UI shows all of them.
func (h *Handler) collectLiveConnections() []liveConnectionInfo {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	var live []liveConnectionInfo
	for _, tk := range h.deps.ToolkitRegistry.All() {
		if !knownConnectionKinds[tk.Kind()] {
			continue
		}
		if lister, ok := tk.(toolkit.ConnectionLister); ok {
			live = h.expandMultiConnections(live, tk, lister)
		} else {
			info := liveConnectionInfo{
				kind: tk.Kind(), name: tk.Name(), connection: tk.Connection(), tools: tk.Tools(),
			}
			info.config = h.lookupToolkitInstanceConfig(tk.Kind(), tk.Name())
			live = append(live, info)
		}
	}
	return live
}

// expandMultiConnections appends one liveConnectionInfo per connection from a
// multi-connection toolkit. Each entry gets its own config from the YAML map.
func (h *Handler) expandMultiConnections(live []liveConnectionInfo, tk registry.Toolkit, lister toolkit.ConnectionLister) []liveConnectionInfo {
	tools := tk.Tools()
	for _, conn := range lister.ListConnections() {
		info := liveConnectionInfo{
			kind: tk.Kind(), name: conn.Name, connection: conn.Name,
			description: conn.Description, tools: tools,
		}
		info.config = h.lookupToolkitInstanceConfig(tk.Kind(), conn.Name)
		live = append(live, info)
	}
	return live
}

// lookupToolkitInstanceConfig extracts an instance's config from the raw toolkits map.
// Returns a shallow copy to avoid mutating the live platform config.
func (h *Handler) lookupToolkitInstanceConfig(kind, name string) map[string]any {
	if h.deps.ToolkitsConfig == nil {
		return nil
	}
	kindVal, ok := h.deps.ToolkitsConfig[kind]
	if !ok {
		return nil
	}
	kindMap, ok := kindVal.(map[string]any)
	if !ok {
		return nil
	}
	instancesVal, ok := kindMap["instances"]
	if !ok {
		return nil
	}
	instances, ok := instancesVal.(map[string]any)
	if !ok {
		return nil
	}
	instVal, ok := instances[name]
	if !ok {
		return nil
	}
	instMap, ok := instVal.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]any, len(instMap))
	maps.Copy(result, instMap)
	return result
}

// mergeConnections combines live connections with DB instances, marking each with its source.
func mergeConnections(live []liveConnectionInfo, dbInstances []platform.ConnectionInstance) []effectiveConnection {
	dbMap := make(map[string]platform.ConnectionInstance, len(dbInstances))
	for _, inst := range dbInstances {
		dbMap[inst.Kind+"/"+inst.Name] = inst
	}

	seen := make(map[string]bool)
	var result []effectiveConnection
	for _, l := range live {
		key := l.kind + "/" + l.name
		seen[key] = true
		ec := effectiveConnection{
			Kind: l.kind, Name: l.name, Connection: l.connection, Source: platform.SourceFile, Tools: l.tools,
			Description: l.description, Config: l.config,
		}
		if inst, ok := dbMap[key]; ok {
			ec.Source = platform.SourceBoth
			ec.Description = inst.Description
			ec.Config = inst.Config
			ec.CreatedBy = inst.CreatedBy
			ec.UpdatedAt = &inst.UpdatedAt
		}
		result = append(result, ec)
	}

	for _, inst := range dbInstances {
		if seen[inst.Kind+"/"+inst.Name] {
			continue
		}
		result = append(result, effectiveConnection{
			Kind: inst.Kind, Name: inst.Name, Connection: inst.Name, Source: platform.SourceDatabase,
			Description: inst.Description, Config: inst.Config, CreatedBy: inst.CreatedBy,
			UpdatedAt: &inst.UpdatedAt,
		})
	}

	if result == nil {
		result = []effectiveConnection{}
	}
	return result
}

// redactedValue is the placeholder used for sensitive config fields in API responses.
const redactedValue = "[REDACTED]"

// connectionSensitiveKeys lists config keys that contain secrets and must be
// redacted when returning connection instances via the API.
var connectionSensitiveKeys = []string{
	"password", "secret_access_key", "secret_key",
	"token", "access_token", "refresh_token", "api_key",
	"credential",
	"client_secret", "oauth_client_secret",
}

// platformInternalKeys lists config keys injected by the platform at runtime
// (e.g., elicitation, progress) that should not be exposed in admin API responses.
var platformInternalKeys = []string{
	"elicitation", "progress_enabled",
}

// redactConnectionConfig returns a copy of config with sensitive fields replaced
// by "[REDACTED]" and platform-internal keys removed. Non-sensitive fields are
// copied as-is.
func redactConnectionConfig(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	result := make(map[string]any, len(config))
	maps.Copy(result, config)
	for _, key := range connectionSensitiveKeys {
		if _, ok := result[key]; ok {
			result[key] = redactedValue
		}
	}
	for _, key := range platformInternalKeys {
		delete(result, key)
	}
	return result
}

// hasRedactedValues returns true if any sensitive key has the "[REDACTED]" placeholder.
func hasRedactedValues(config map[string]any) bool {
	for _, key := range connectionSensitiveKeys {
		if v, ok := config[key]; ok {
			if s, isStr := v.(string); isStr && s == redactedValue {
				return true
			}
		}
	}
	return false
}

// activateConnection hot-adds the connection to its toolkit and updates the
// connection source map. Extracted to reduce setConnectionInstance complexity.
func (h *Handler) activateConnection(inst platform.ConnectionInstance) {
	h.hotAddConnection(inst.Kind, inst.Name, inst.Config)
	if h.deps.ConnectionSources != nil {
		h.deps.ConnectionSources.Add(platform.ConnectionSourceFromInstance(inst))
	}
}

// findConnectionManager returns the ConnectionManager for the given toolkit kind,
// or nil if no matching toolkit exists or the toolkit does not implement ConnectionManager.
func (h *Handler) findConnectionManager(kind string) toolkit.ConnectionManager {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	for _, tk := range h.deps.ToolkitRegistry.All() {
		if tk.Kind() == kind {
			cm, _ := tk.(toolkit.ConnectionManager)
			return cm
		}
	}
	return nil
}

// hotAddConnection attempts to add the connection to a live toolkit that
// implements toolkit.ConnectionManager.
func (h *Handler) hotAddConnection(kind, name string, config map[string]any) {
	cm := h.findConnectionManager(kind)
	if cm == nil || cm.HasConnection(name) {
		return
	}
	if err := cm.AddConnection(name, config); err != nil { // #nosec G706 -- structured slog call, not a format string
		slog.Warn("failed to hot-add connection",
			logKeyKind, kind, logKeyName, name, logKeyError, err)
	}
}

// hotRemoveConnection attempts to remove the connection from a live toolkit that
// implements toolkit.ConnectionManager.
func (h *Handler) hotRemoveConnection(kind, name string) {
	cm := h.findConnectionManager(kind)
	if cm == nil || !cm.HasConnection(name) {
		return
	}
	if err := cm.RemoveConnection(name); err != nil { // #nosec G706 -- structured slog call, not a format string
		slog.Warn("failed to hot-remove connection",
			logKeyKind, kind, logKeyName, name, logKeyError, err)
	}
}

// mergeRedactedFields replaces "[REDACTED]" values in submitted config with
// their existing counterparts from the stored config.
func mergeRedactedFields(submitted, existing map[string]any) map[string]any {
	for _, key := range connectionSensitiveKeys {
		if v, ok := submitted[key]; ok {
			if s, isStr := v.(string); isStr && s == redactedValue {
				if existingVal, found := existing[key]; found {
					submitted[key] = existingVal
				}
			}
		}
	}
	return submitted
}
