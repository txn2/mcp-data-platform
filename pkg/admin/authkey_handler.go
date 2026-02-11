package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// authKeyCreateRequest is the request body for creating an API key.
type authKeyCreateRequest struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

// authKeyCreateResponse is the response after creating an API key.
type authKeyCreateResponse struct {
	Name    string   `json:"name"`
	Key     string   `json:"key"`
	Roles   []string `json:"roles"`
	Warning string   `json:"warning"`
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
// @Description  Generates a new API key. Only available in database config mode. The key value is returned only once.
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

	keyValue, err := h.deps.APIKeyManager.GenerateKey(req.Name, req.Roles)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	h.syncConfig(r, fmt.Sprintf("create auth key %s", req.Name))

	writeJSON(w, http.StatusCreated, authKeyCreateResponse{
		Name:    req.Name,
		Key:     keyValue,
		Roles:   req.Roles,
		Warning: "Store this key securely. It will not be shown again.",
	})
}

// deleteAuthKey handles DELETE /api/v1/admin/auth/keys/{name}.
//
// @Summary      Delete auth key
// @Description  Deletes an API key. Only available in database config mode.
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

	h.syncConfig(r, fmt.Sprintf("delete auth key %s", name))

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}
