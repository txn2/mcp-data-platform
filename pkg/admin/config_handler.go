package admin

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// defaultHistoryLimit is the maximum number of config revisions to return.
const defaultHistoryLimit = 50

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

// configImportResponse is returned after importing a configuration.
type configImportResponse struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// importConfig handles POST /api/v1/admin/config/import.
//
// @Summary      Import config
// @Description  Imports a YAML configuration into the config store. Only available in database config mode.
// @Tags         Config
// @Accept       application/x-yaml
// @Produce      json
// @Param        comment  query  string  false  "Revision comment"
// @Param        body     body   string  true   "YAML configuration"
// @Success      200  {object}  configImportResponse
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /config/import [post]
func (h *Handler) importConfig(w http.ResponseWriter, r *http.Request) {
	if !h.isMutable() {
		writeError(w, http.StatusConflict, "config is read-only in file mode")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Validate the YAML is parseable
	cfg, err := platform.LoadConfigFromBytes(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid config YAML: %v", err))
		return
	}

	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("config validation failed: %v", err))
		return
	}

	// Save to config store
	user := GetUser(r.Context())
	author := ""
	if user != nil {
		author = user.UserID
	}
	comment := r.URL.Query().Get("comment")

	if err := h.deps.ConfigStore.Save(r.Context(), body, configstore.SaveMeta{
		Author:  author,
		Comment: comment,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config")
		return
	}

	writeJSON(w, http.StatusOK, configImportResponse{
		Status: "saved",
		Note:   "changes take effect on next restart",
	})
}

// configHistoryResponse wraps a list of config revisions.
type configHistoryResponse struct {
	Revisions []configstore.Revision `json:"revisions"`
	Total     int                    `json:"total"`
}

// configHistory handles GET /api/v1/admin/config/history.
//
// @Summary      Config history
// @Description  Returns config revision history. Only available in database config mode.
// @Tags         Config
// @Produce      json
// @Success      200  {object}  configHistoryResponse
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /config/history [get]
func (h *Handler) configHistory(w http.ResponseWriter, r *http.Request) {
	if !h.isMutable() {
		writeError(w, http.StatusConflict, "config history not available in file mode")
		return
	}

	revisions, err := h.deps.ConfigStore.History(r.Context(), defaultHistoryLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config history")
		return
	}
	if revisions == nil {
		revisions = []configstore.Revision{}
	}
	writeJSON(w, http.StatusOK, configHistoryResponse{
		Revisions: revisions,
		Total:     len(revisions),
	})
}

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

// syncConfig persists the current in-memory config to the config store.
// It is a no-op in file mode (routes are blocked, but this is defense in depth).
func (h *Handler) syncConfig(r *http.Request, comment string) {
	if !h.isMutable() {
		return
	}
	data, err := yaml.Marshal(h.deps.Config)
	if err != nil {
		slog.Error("failed to marshal config for sync", "error", err)
		return
	}
	user := GetUser(r.Context())
	author := ""
	if user != nil {
		author = user.UserID
	}
	if err := h.deps.ConfigStore.Save(r.Context(), data, configstore.SaveMeta{
		Author:  author,
		Comment: comment,
	}); err != nil {
		slog.Error("failed to persist config after mutation", "error", err)
	}
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
