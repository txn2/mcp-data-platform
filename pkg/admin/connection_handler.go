package admin

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
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

// Connection kind discriminators identifying which toolkit family a
// connection instance targets.
const (
	connectionKindMCP   = "mcp"
	connectionKindTrino = "trino"
	connectionKindS3    = "s3"
	connectionKindAPI   = "api"
)

// connectionCreatorSystem is the created_by attribution for connections
// provisioned by the platform itself (YAML config or a built-in
// registration) rather than authored by a portal user.
const connectionCreatorSystem = "system"

// knownConnectionKinds lists the toolkit kinds that support multiple configurable
// connection instances. DataHub is excluded because the platform connects to a
// single catalog instance configured in the YAML file.
var knownConnectionKinds = map[string]bool{
	connectionKindTrino: true,
	connectionKindS3:    true,
	connectionKindMCP:   true,
	connectionKindAPI:   true,
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

	// Drop server-derived fields from the incoming body so a UI that
	// loads the GET response into its form (with mtls_cert_not_after
	// present) and POSTs it back does NOT persist the snapshot. The
	// expiry is recomputed from the leaf cert on every GET; storing
	// it would let a stale value survive a cert removal.
	for _, key := range platformInternalKeys {
		delete(req.Config, key)
	}

	// If any sensitive field is "[REDACTED]", preserve the existing value from the store.
	if hasRedactedValues(req.Config) {
		existing, err := h.deps.ConnectionStore.Get(r.Context(), kind, name)
		if err == nil && existing != nil {
			req.Config = mergeRedactedFields(req.Config, existing.Config)
		}
	}

	if msg, ok := h.validateConnectionCatalog(r.Context(), kind, req.Config); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	if err := registry.ValidateConnectionConfig(kind, req.Config); err != nil {
		writeError(w, http.StatusBadRequest, "invalid connection config: "+err.Error())
		return
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

	// Capture whether a token row existed before the delete so we know
	// whether to emit token_deleted_admin. The ConnectionStore.Delete
	// may cascade-clear the connection_oauth_tokens row depending on
	// FK config; either way we record the operator-initiated delete.
	hadToken := false
	if h.deps.ConnOAuthStore != nil {
		if _, getErr := h.deps.ConnOAuthStore.Get(r.Context(),
			connoauth.Key{Kind: kind, Name: name}); getErr == nil {
			hadToken = true
		}
	}

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

	if hadToken {
		// Best-effort: also wipe the token row so a re-created connection
		// with the same name doesn't inherit a dead credential.
		_ = h.deps.ConnOAuthStore.Delete(r.Context(),
			connoauth.Key{Kind: kind, Name: name})
		actor := authorEmailOrID(r.Context())
		if actor == "" {
			// Fallback so the event validates even when an unauthenticated
			// delete makes it past the auth middleware (test paths,
			// future API-key flows that don't populate User). Marker
			// distinct from any real email/UUID so dashboards can find
			// these in the audit history.
			actor = "admin:unattributed"
		}
		h.deps.AuthEvents.TokenDeletedAdmin(r.Context(), kind, name, actor)
	}

	w.WriteHeader(http.StatusNoContent)
}

// effectiveConnection merges a live toolkit connection with its DB instance (if any).
type effectiveConnection struct {
	Kind        string                        `json:"kind" example:"trino"`
	Name        string                        `json:"name" example:"acme-warehouse"`
	Connection  string                        `json:"connection" example:"acme-warehouse"`
	Description string                        `json:"description,omitempty" example:"Production data warehouse"`
	Source      string                        `json:"source" example:"file"` // "file", "database", or "both"
	Tools       []string                      `json:"tools" example:"trino_query,trino_describe_table"`
	Config      map[string]any                `json:"config,omitempty"`
	CreatedBy   string                        `json:"created_by,omitempty" example:"admin@example.com"`
	UpdatedAt   *time.Time                    `json:"updated_at,omitempty"`
	Health      *toolkit.ConnectionHealthWire `json:"health,omitempty"`
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
	health                 *toolkit.ConnectionHealthWire
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
			health: conn.Health.Wire(),
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
		// A live connection with no DB instance is provisioned by the
		// platform itself (YAML config or a built-in registration such as
		// the platform-admin self-connection), never authored by a portal
		// user. Attribute it to "system" rather than leaving created_by
		// empty, which the UI renders as "unknown". A matching DB instance
		// below overrides this with the real author.
		ec := effectiveConnection{
			Kind: l.kind, Name: l.name, Connection: l.connection, Source: platform.SourceFile, Tools: l.tools,
			Description: l.description, Config: l.config, CreatedBy: connectionCreatorSystem,
			// Health reflects the live runtime session; a DB-only instance
			// (handled in the loop below) has no session, so health stays nil.
			Health: l.health,
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
	// Normalize Tools to a non-nil slice so the JSON wire shape is
	// always [] for "no tools" instead of null. Some toolkits (notably
	// the mcp gateway kind, which discovers tools per session) return
	// a nil Tools() slice; without this, clients written against the
	// documented []string shape break on the null.
	for i := range result {
		if result[i].Tools == nil {
			result[i].Tools = []string{}
		}
	}
	return result
}

// redactedValue is the placeholder used for sensitive config fields in API responses.
const redactedValue = "[REDACTED]"

// Field-name constants for legacy or connection-specific sensitive keys
// kept distinct from the shared sensitive-key set in config_handler.go so
// the connection schema can evolve independently.
const (
	sensKeyOAuthClientSecret  = "oauth_client_secret"  // #nosec G101 -- field name, not a credential
	sensKeyOAuth2ClientSecret = "oauth2_client_secret" // #nosec G101 -- field name, not a credential
	sensKeySecretKey          = "secret_key"           // #nosec G101 -- field name, not a credential
	sensKeyAccessToken        = "access_token"         // #nosec G101 -- field name, not a credential
	sensKeyRefreshToken       = "refresh_token"        // #nosec G101 -- field name, not a credential
	sensKeyMTLSClientKeyPEM   = "mtls_client_key_pem"  // #nosec G101 -- field name, not a credential
)

// connectionSensitiveKeys lists config keys that contain secrets and must be
// redacted when returning connection instances via the API.
var connectionSensitiveKeys = []string{
	sensKeyPassword, sensKeySecretAccessKey, sensKeySecretKey,
	sensKeyToken, sensKeyAccessToken, sensKeyRefreshToken, sensKeyAPIKey,
	sensKeyCredential,
	sensKeyClientSecret, sensKeyOAuthClientSecret, sensKeyOAuth2ClientSecret,
	sensKeyMTLSClientKeyPEM,
}

// nestedMapSensitiveKeys lists config keys whose value is itself a
// map of strings; the inner values must be redacted while the inner
// names remain visible. Mirrors platform.SensitiveNestedMapKeyList().
var nestedMapSensitiveKeys = []string{
	platform.CfgKeyStaticHeaders,
}

// platformInternalKeys lists config keys injected by the platform at runtime
// (e.g., elicitation, progress) that should not be exposed in admin API
// responses. mtls_cert_not_after is also here because it is a server-derived
// view of the leaf certificate's NotAfter, not operator config: a PUT must
// never store it and a GET must always recompute it.
var platformInternalKeys = []string{
	"elicitation", "progress_enabled", "mtls_cert_not_after",
}

// redactConnectionConfig returns a copy of config with sensitive fields replaced
// by "[REDACTED]" and platform-internal keys removed. Non-sensitive fields are
// copied as-is. Derived metadata that the portal benefits from (the
// mTLS leaf certificate's NotAfter, surfaced as mtls_cert_not_after) is
// computed from the public cert PEM and added to the response so the
// portal can render an expiry badge without re-parsing the cert client-side.
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
	for _, key := range nestedMapSensitiveKeys {
		if raw, ok := result[key]; ok {
			result[key] = redactNestedMapValues(raw)
		}
	}
	// platformInternalKeys is applied BEFORE recomputing
	// mtls_cert_not_after so a stale persisted value (left over from
	// a pre-fix deployment that round-tripped the field through PUT)
	// is dropped, and only the fresh server-computed value survives
	// to the response. Without this ordering, removing the cert from
	// a connection would leave the stale expiry in place and the
	// portal would falsely report the connection still had a valid
	// cert.
	for _, key := range platformInternalKeys {
		delete(result, key)
	}
	if expiry := mtlsCertNotAfter(config); !expiry.IsZero() {
		result["mtls_cert_not_after"] = expiry.UTC().Format(time.RFC3339)
	}
	return result
}

// mtlsCertNotAfter parses the apigateway connection's leaf
// certificate (when present) and returns its NotAfter timestamp.
// Returns the zero time when no cert is configured or the PEM is
// unparseable; callers MUST treat a zero return as "expiry unknown"
// rather than "expires at the Unix epoch". Kept here (not pulled
// from pkg/toolkits/apigateway) so the admin handler does not take
// a transitive import on the full apigateway toolkit; the parse
// logic is five lines and the duplication is intentional.
func mtlsCertNotAfter(config map[string]any) time.Time {
	raw, ok := config["mtls_client_cert_pem"].(string)
	if !ok || raw == "" {
		return time.Time{}
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil || block.Type != "CERTIFICATE" {
		return time.Time{}
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return leaf.NotAfter
}

// redactNestedMapValues returns a copy of a map[string]any whose inner
// string values are replaced with redactedValue. Inner names are
// preserved so the admin client can see WHICH headers are configured
// without seeing the secret values. Non-map inputs round-trip unchanged.
func redactNestedMapValues(raw any) any {
	switch v := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if _, isStr := val.(string); isStr {
				out[k] = redactedValue
			} else {
				out[k] = val
			}
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(v))
		for k := range v {
			out[k] = redactedValue
		}
		return out
	}
	return raw
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
	for _, key := range nestedMapSensitiveKeys {
		if nestedMapHasRedacted(config[key]) {
			return true
		}
	}
	return false
}

// nestedMapHasRedacted reports whether any inner string value equals
// redactedValue. Used so an update that only re-asserts the existing
// (redacted) header values triggers mergeRedactedFields and preserves
// the stored secret instead of overwriting it with "[REDACTED]".
func nestedMapHasRedacted(raw any) bool {
	switch v := raw.(type) {
	case map[string]any:
		for _, val := range v {
			if s, ok := val.(string); ok && s == redactedValue {
				return true
			}
		}
	case map[string]string:
		for _, s := range v {
			if s == redactedValue {
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

// hotAddConnection makes the given config the live config for the
// named connection. On CREATE it adds; on UPDATE it removes the
// existing in-memory connection first and re-adds with the new
// config so changes (notably config.catalog_id) take effect without
// a process restart. Without the remove-then-add, the in-memory
// connection retains its registration-time config while the DB row
// has the new one, and list_connections and api_list_endpoints
// disagree with the admin UI until restart.
func (h *Handler) hotAddConnection(kind, name string, config map[string]any) {
	cm := h.findConnectionManager(kind)
	if cm == nil {
		return
	}
	if cm.HasConnection(name) {
		if err := cm.RemoveConnection(name); err != nil { // #nosec G706 -- structured slog call, not a format string
			slog.Warn("failed to hot-remove connection before re-add",
				logKeyKind, kind, logKeyName, name, logKeyError, err)
			return
		}
	}
	if err := cm.AddConnection(name, config); err != nil { // #nosec G706 -- structured slog call, not a format string
		slog.Warn("failed to hot-add connection",
			logKeyKind, kind, logKeyName, name, logKeyError, err)
	}
	// Tell peer replicas to rebuild this connection from the store too;
	// the hot-add above only updates this replica (issue #501).
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishConnectionReload(kind, name)
	}
}

// hotRemoveConnection attempts to remove the connection from a live toolkit that
// implements toolkit.ConnectionManager.
func (h *Handler) hotRemoveConnection(kind, name string) {
	if cm := h.findConnectionManager(kind); cm != nil && cm.HasConnection(name) {
		if err := cm.RemoveConnection(name); err != nil { // #nosec G706 -- structured slog call, not a format string
			slog.Warn("failed to hot-remove connection",
				logKeyKind, kind, logKeyName, name, logKeyError, err)
		}
	}
	// Always broadcast: peer replicas reconcile from the store (now
	// missing this row) and drop their copy of the connection (#501).
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishConnectionReload(kind, name)
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
	for _, key := range nestedMapSensitiveKeys {
		if _, present := submitted[key]; !present {
			continue
		}
		submitted[key] = mergeRedactedNestedMap(submitted[key], existing[key])
	}
	return submitted
}

// mergeRedactedNestedMap merges a submitted nested map with the stored
// one: any inner string value equal to redactedValue is replaced with
// the stored counterpart. Inner names absent from submitted are
// dropped (operator deleted that header); names absent from existing
// are kept (operator added a new header). Returns the submitted value
// unchanged when it is not a map (defensive passthrough — a non-map
// shape under a sensitive nested key is an upstream bug, not this
// function's to silently rewrite).
func mergeRedactedNestedMap(submitted, existing any) any {
	subMap := nestedMapAsAny(submitted)
	if subMap == nil {
		return submitted
	}
	existMap := nestedMapAsAny(existing)
	for k, v := range subMap {
		s, isStr := v.(string)
		if !isStr || s != redactedValue {
			continue
		}
		if existVal, ok := existMap[k]; ok {
			subMap[k] = existVal
		}
	}
	return subMap
}

// nestedMapAsAny coerces a value into map[string]any if it's a map,
// or returns nil. map[string]string is upcast for uniform handling.
func nestedMapAsAny(raw any) map[string]any {
	switch v := raw.(type) {
	case map[string]any:
		return v
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, s := range v {
			out[k] = s
		}
		return out
	}
	return nil
}
