package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// authKeyCreateRequest is the request body for creating an API key.
type authKeyCreateRequest struct {
	Name        string   `json:"name"`
	Email       string   `json:"email,omitempty"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	ExpiresIn   string   `json:"expires_in,omitempty"` // e.g. "24h", "720h", "8760h"
}

// authKeyCreateResponse is the response after creating an API key.
type authKeyCreateResponse struct {
	Name        string     `json:"name"`
	Email       string     `json:"email,omitempty"`
	Description string     `json:"description,omitempty"`
	Key         string     `json:"key"`
	Roles       []string   `json:"roles"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Warning     string     `json:"warning"`
}

// authKeyListResponse wraps a list of API keys.
type authKeyListResponse struct {
	Keys  []auth.APIKeySummary `json:"keys"`
	Total int                  `json:"total"`
}

// listAuthKeys handles GET /api/v1/admin/auth/keys.
//
// @Summary      List auth keys
// @Description  Returns all API keys (key values are never exposed, only names and roles).
// @Tags         Auth Keys
// @Produce      json
// @Success      200  {object}  authKeyListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /auth/keys [get]
func (h *Handler) listAuthKeys(w http.ResponseWriter, _ *http.Request) {
	keys := h.deps.APIKeyManager.ListKeys()
	writeJSON(w, http.StatusOK, authKeyListResponse{Keys: keys, Total: len(keys)})
}

// createAuthKey handles POST /api/v1/admin/auth/keys.
//
// @Summary      Create auth key
// @Description  Generates a new API key. The key value is returned only once.
// @Tags         Auth Keys
// @Accept       json
// @Produce      json
// @Param        body  body  authKeyCreateRequest  true  "Key definition"
// @Success      201  {object}  authKeyCreateResponse
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /auth/keys [post]
func (h *Handler) createAuthKey(w http.ResponseWriter, r *http.Request) {
	var req authKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Roles) == 0 {
		writeError(w, http.StatusBadRequest, "roles is required")
		return
	}

	def := auth.APIKey{
		Name:        req.Name,
		Email:       req.Email,
		Description: req.Description,
		Roles:       req.Roles,
	}

	// Parse expiration if provided.
	if req.ExpiresIn != "" {
		dur, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in duration: "+err.Error())
			return
		}
		if dur <= 0 {
			writeError(w, http.StatusBadRequest, "expires_in must be a positive duration")
			return
		}
		exp := time.Now().Add(dur)
		def.ExpiresAt = &exp
	}

	keyValue, err := h.deps.APIKeyManager.GenerateKey(def)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// Persist to database FIRST — if it fails, return error.
	if err := h.persistAPIKey(r, keyValue, def); err != nil {
		slog.Warn("failed to persist api key", logKeyName, def.Name, logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to persist api key")
		return
	}

	writeJSON(w, http.StatusCreated, authKeyCreateResponse{
		Name:        req.Name,
		Email:       apiKeyEmailFallback(req.Email, req.Name),
		Description: req.Description,
		Key:         keyValue,
		Roles:       req.Roles,
		ExpiresAt:   def.ExpiresAt,
		Warning:     "Store this key securely. It will not be shown again.",
	})
}

// deleteAuthKey handles DELETE /api/v1/admin/auth/keys/{name}.
//
// @Summary      Delete auth key
// @Description  Deletes an API key.
// @Tags         Auth Keys
// @Produce      json
// @Param        name  path  string  true  "Key name"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /auth/keys/{name} [delete]
func (h *Handler) deleteAuthKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	// Block deletion of file-only keys — they would reappear on restart.
	if source := h.keySourceByName(name); source == platform.SourceFile {
		writeError(w, http.StatusConflict,
			"this key is defined in the config file and cannot be deleted via the admin API")
		return
	}

	// Delete from database FIRST — if it fails, don't remove from in-memory manager.
	if h.deps.APIKeyStore != nil {
		if err := h.deps.APIKeyStore.Delete(r.Context(), name); err != nil {
			slog.Warn("failed to delete api key from database", logKeyName, sanitizeLogValue(name), logKeyError, err) // #nosec G706 -- name is sanitized
			writeError(w, http.StatusInternalServerError, "failed to delete api key from database")
			return
		}
	}

	if !h.deps.APIKeyManager.RemoveByName(name) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}

// keySourceByName returns the source of an API key by name, or "" if not found.
func (h *Handler) keySourceByName(name string) string {
	if h.deps.APIKeyManager == nil {
		return ""
	}
	for _, k := range h.deps.APIKeyManager.ListKeys() {
		if k.Name == name {
			return k.Source
		}
	}
	return ""
}

// persistAPIKey hashes the raw key value and persists it to the database.
// Returns an error if persistence fails.
func (h *Handler) persistAPIKey(r *http.Request, keyValue string, def auth.APIKey) error {
	if h.deps.APIKeyStore == nil {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(keyValue), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing api key: %w", err)
	}

	dbDef := platform.APIKeyDefinition{
		Name:        def.Name,
		KeyHash:     string(hash),
		Email:       apiKeyEmailFallback(def.Email, def.Name),
		Description: def.Description,
		Roles:       def.Roles,
		ExpiresAt:   def.ExpiresAt,
		CreatedBy:   extractAuthor(r),
	}

	if err := h.deps.APIKeyStore.Set(r.Context(), dbDef); err != nil {
		return fmt.Errorf("persisting api key: %w", err)
	}
	return nil
}

// apiKeyEmailFallback returns the email or a default based on name.
func apiKeyEmailFallback(email, name string) string {
	if email != "" {
		return email
	}
	return name + "@apikey.local"
}
