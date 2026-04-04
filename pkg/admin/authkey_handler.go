package admin

import (
	"encoding/json"
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
		exp := time.Now().Add(dur)
		def.ExpiresAt = &exp
	}

	keyValue, err := h.deps.APIKeyManager.GenerateKey(def)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// Persist to database (best-effort).
	h.persistAPIKey(r, keyValue, def)

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

	if !h.deps.APIKeyManager.RemoveByName(name) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	// Remove from database (best-effort).
	if h.deps.APIKeyStore != nil {
		if err := h.deps.APIKeyStore.Delete(r.Context(), name); err != nil {
			slog.Warn("failed to delete api key from database", logKeyName, sanitizeLogValue(name), "error", err) // #nosec G706 -- name is sanitized via sanitizeLogValue
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}

// persistAPIKey hashes the raw key value and persists it to the database.
// This is best-effort: failures are logged but do not block the response.
func (h *Handler) persistAPIKey(r *http.Request, keyValue string, def auth.APIKey) {
	if h.deps.APIKeyStore == nil {
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(keyValue), bcrypt.DefaultCost)
	if err != nil {
		slog.Warn("failed to hash api key for persistence", logKeyName, def.Name, "error", err)
		return
	}

	dbDef := platform.APIKeyDefinition{
		Name:        def.Name,
		KeyHash:     string(hash),
		Email:       apiKeyEmailFallback(def.Email, def.Name),
		Description: def.Description,
		Roles:       def.Roles,
		ExpiresAt:   def.ExpiresAt,
		CreatedBy:   "", // could be extracted from request context
		CreatedAt:   time.Now(),
	}

	if err := h.deps.APIKeyStore.Set(r.Context(), dbDef); err != nil {
		slog.Warn("failed to persist api key to database", logKeyName, def.Name, "error", err)
	}
}

// apiKeyEmailFallback returns the email or a default based on name.
func apiKeyEmailFallback(email, name string) string {
	if email != "" {
		return email
	}
	return name + "@apikey.local"
}
