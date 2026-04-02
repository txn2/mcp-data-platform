package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// defaultChangelogLimit is the maximum number of changelog entries to return.
const defaultChangelogLimit = 50

// configEntryWhitelist defines the keys that can be set via the admin API.
var configEntryWhitelist = map[string]bool{
	"server.description":        true,
	"server.agent_instructions": true,
}

// sensitiveKeys contains key name patterns that should be redacted.
var sensitiveKeys = []string{
	"key",
	"secret",
	"password",
	"token",
	"signing_key",
	"dsn",
	"secret_access_key",
	"client_secret",
}

// configToMap converts a config struct to map[string]any via YAML round-trip.
func configToMap(v any) (map[string]any, error) {
	yamlBytes, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(yamlBytes, &m); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	return m, nil
}

// getConfig handles GET /api/v1/admin/config.
//
// @Summary      Get config
// @Description  Returns the current configuration as JSON with sensitive values redacted.
// @Tags         Config
// @Produce      json
// @Success      200  {object}  map[string]any
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /config [get]
func (h *Handler) getConfig(w http.ResponseWriter, _ *http.Request) {
	configMap, err := configToMap(h.deps.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to process config")
		return
	}
	redactMap(configMap)
	writeJSON(w, http.StatusOK, configMap)
}

// configModeResponse describes the current config store mode.
type configModeResponse struct {
	Mode     string `json:"mode"`
	ReadOnly bool   `json:"read_only"`
}

// configMode handles GET /api/v1/admin/config/mode.
//
// @Summary      Get config mode
// @Description  Returns the current config store mode (file or database).
// @Tags         Config
// @Produce      json
// @Success      200  {object}  configModeResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /config/mode [get]
func (h *Handler) configMode(w http.ResponseWriter, _ *http.Request) {
	mode := configModeFile
	if h.deps.ConfigStore != nil {
		mode = h.deps.ConfigStore.Mode()
	}
	writeJSON(w, http.StatusOK, configModeResponse{
		Mode:     mode,
		ReadOnly: mode == configModeFile,
	})
}

// exportConfig handles GET /api/v1/admin/config/export.
//
// @Summary      Export config
// @Description  Returns the current configuration as downloadable YAML. Sensitive values are redacted by default.
// @Tags         Config
// @Produce      application/x-yaml
// @Param        secrets  query  string  false  "Set to 'true' to include sensitive values"
// @Success      200      {string}  string
// @Failure      500      {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /config/export [get]
func (h *Handler) exportConfig(w http.ResponseWriter, r *http.Request) {
	if h.deps.Config == nil {
		writeError(w, http.StatusInternalServerError, "no config available")
		return
	}

	yamlBytes, err := yaml.Marshal(h.deps.Config)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal config")
		return
	}

	// Redact by default unless ?secrets=true
	if r.URL.Query().Get("secrets") != "true" {
		yamlBytes, err = redactYAML(yamlBytes)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to redact config")
			return
		}
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="platform-config.yaml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(yamlBytes)
}

// --- Effective config (merged file defaults + DB overrides) ---

// effectiveConfigEntry is an entry with its source indicated.
type effectiveConfigEntry struct {
	Key       string  `json:"key"`
	Value     string  `json:"value"`
	Source    string  `json:"source"` // "file" or "database"
	UpdatedBy *string `json:"updated_by,omitempty"`
	UpdatedAt *string `json:"updated_at,omitempty"`
}

// listEffectiveConfig handles GET /api/v1/admin/config/effective.
// Returns the merged view: for each whitelisted key, the DB override if present, otherwise the file default.
func (h *Handler) listEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	// Get DB overrides.
	dbEntries, _ := h.deps.ConfigStore.List(r.Context())
	dbMap := make(map[string]configstore.Entry)
	for _, e := range dbEntries {
		dbMap[e.Key] = e
	}

	var result []effectiveConfigEntry
	for key := range configEntryWhitelist {
		if dbEntry, ok := dbMap[key]; ok {
			updatedAt := dbEntry.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
			result = append(result, effectiveConfigEntry{
				Key:       key,
				Value:     dbEntry.Value,
				Source:    "database",
				UpdatedBy: &dbEntry.UpdatedBy,
				UpdatedAt: &updatedAt,
			})
		} else if fileVal, ok := h.deps.FileDefaults[key]; ok {
			result = append(result, effectiveConfigEntry{
				Key:    key,
				Value:  fileVal,
				Source: "file",
			})
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// --- Config Entry CRUD ---

// listConfigEntries handles GET /api/v1/admin/config/entries.
func (h *Handler) listConfigEntries(w http.ResponseWriter, r *http.Request) {
	entries, err := h.deps.ConfigStore.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list config entries")
		return
	}
	if entries == nil {
		entries = []configstore.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// getConfigEntry handles GET /api/v1/admin/config/entries/{key}.
func (h *Handler) getConfigEntry(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	entry, err := h.deps.ConfigStore.Get(r.Context(), key)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "config entry not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get config entry")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// setConfigEntryRequest is the body for PUT /api/v1/admin/config/entries/{key}.
type setConfigEntryRequest struct {
	Value string `json:"value"`
}

// setConfigEntry handles PUT /api/v1/admin/config/entries/{key}.
func (h *Handler) setConfigEntry(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !configEntryWhitelist[key] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("key %q is not editable", key))
		return
	}

	var req setConfigEntryRequest
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

	if err := h.deps.ConfigStore.Set(r.Context(), key, req.Value, author); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config entry")
		return
	}

	// Hot-reload: apply to live config.
	applyConfigEntry(h.deps.Config, key, req.Value)

	// Return the stored entry.
	entry, err := h.deps.ConfigStore.Get(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusOK, configstore.Entry{Key: key, Value: req.Value})
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// deleteConfigEntry handles DELETE /api/v1/admin/config/entries/{key}.
func (h *Handler) deleteConfigEntry(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !configEntryWhitelist[key] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("key %q is not editable", key))
		return
	}

	author := ""
	if user := GetUser(r.Context()); user != nil {
		author = user.Email
		if author == "" {
			author = user.UserID
		}
	}

	if err := h.deps.ConfigStore.Delete(r.Context(), key, author); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "config entry not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete config entry")
		return
	}

	// Revert to file default.
	if fileVal, ok := h.deps.FileDefaults[key]; ok {
		applyConfigEntry(h.deps.Config, key, fileVal)
	}

	w.WriteHeader(http.StatusNoContent)
}

// getConfigChangelog handles GET /api/v1/admin/config/changelog.
func (h *Handler) getConfigChangelog(w http.ResponseWriter, r *http.Request) {
	entries, err := h.deps.ConfigStore.Changelog(r.Context(), defaultChangelogLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config changelog")
		return
	}
	if entries == nil {
		entries = []configstore.ChangelogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// applyConfigEntry updates the live in-memory config for a whitelisted key.
func applyConfigEntry(cfg *platform.Config, key, value string) {
	switch key {
	case "server.description":
		cfg.Server.Description = value
	case "server.agent_instructions":
		cfg.Server.AgentInstructions = value
	}
}

// --- Redaction helpers ---

// redactYAML marshals YAML, redacts sensitive values, then re-marshals.
func redactYAML(data []byte) ([]byte, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshaling for redaction: %w", err)
	}
	redactMap(m)
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshaling redacted config: %w", err)
	}
	return out, nil
}

// redactMap recursively walks the map and replaces sensitive values with "[REDACTED]".
func redactMap(m map[string]any) {
	for k, v := range m {
		if isSensitiveKey(k) {
			if s, ok := v.(string); ok && s != "" {
				m[k] = "[REDACTED]"
			}
			continue
		}
		switch val := v.(type) {
		case map[string]any:
			redactMap(val)
		case []any:
			redactSlice(val)
		}
	}
}

// redactSlice walks a slice and redacts any maps found within.
func redactSlice(s []any) {
	for _, item := range s {
		if m, ok := item.(map[string]any); ok {
			redactMap(m)
		}
	}
}

// isSensitiveKey checks if a key matches any sensitive pattern.
func isSensitiveKey(key string) bool {
	return slices.Contains(sensitiveKeys, strings.ToLower(key))
}
