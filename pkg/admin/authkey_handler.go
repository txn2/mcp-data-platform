package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// errNoAPIKeyStore is the create failure for a handler built without a key store.
var errNoAPIKeyStore = errors.New("no api key store")

// authKeyCreateRequest is the request body for creating an API key.
type authKeyCreateRequest struct {
	Name        string   `json:"name" example:"ci-pipeline"`
	Email       string   `json:"email,omitempty" example:"ci@example.com"`
	Description string   `json:"description,omitempty" example:"CI/CD pipeline integration"`
	Roles       []string `json:"roles" example:"analyst"`
	ExpiresIn   string   `json:"expires_in,omitempty" example:"720h"` // e.g. "24h", "720h", "8760h"
}

// authKeyCreateResponse is the response after creating an API key.
type authKeyCreateResponse struct {
	Name        string     `json:"name" example:"ci-pipeline"`
	Email       string     `json:"email,omitempty" example:"ci@example.com"`
	Description string     `json:"description,omitempty" example:"CI/CD pipeline integration"`
	Key         string     `json:"key" example:"3f9a1c07e2b84d56a0c3e1f7b9d2468ace13579bdf02468ace13579bdf024681"`
	Roles       []string   `json:"roles" example:"analyst"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Warning     string     `json:"warning" example:"Store this key securely. It will not be shown again."`
	// Persona is the persona the key acts as. Absent when its roles reach none.
	Persona string `json:"persona,omitempty" example:"analyst"`
	// Warnings name what is wrong with the key as created. The key is created
	// regardless: a persona carrying its roles may be defined afterward.
	Warnings []string `json:"warnings,omitempty"`
}

// authKeyListResponse wraps a list of API keys.
type authKeyListResponse struct {
	Keys  []authKeySummary `json:"keys"`
	Total int              `json:"total" example:"3"`
}

// authKeySummary is one listed key and the persona its roles reach (#1705).
type authKeySummary struct {
	auth.APIKeySummary
	// Persona is the persona the key acts as. Absent when its roles reach none.
	Persona string `json:"persona,omitempty" example:"analyst"`
	// NoPersona is true when the key's roles reach no persona, so the key
	// authenticates and lists no tools.
	NoPersona bool `json:"no_persona,omitempty" example:"false"`
}

// Every key route first brings the keys held in memory up to the key store,
// which every replica writes to: a key created or deleted through another
// replica a moment ago is part of the answer (#1715).

// listAuthKeys handles GET /api/v1/admin/auth/keys.
//
// @Summary      List auth keys
// @Description  Returns all API keys (key values are never exposed, only names and roles), each with the persona its roles reach, or no_persona when they reach none.
// @Tags         Auth Keys
// @Produce      json
// @Success      200  {object}  authKeyListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/auth/keys [get]
func (h *Handler) listAuthKeys(w http.ResponseWriter, r *http.Request) {
	if !h.syncAPIKeys(w, r) {
		return
	}
	keys := h.deps.APIKeyManager.ListKeys()
	out := make([]authKeySummary, 0, len(keys))
	for _, k := range keys {
		entry := authKeySummary{APIKeySummary: k}
		entry.Persona, entry.NoPersona = h.keyPersona(k.Roles)
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, authKeyListResponse{Keys: out, Total: len(out)})
}

// createAuthKey handles POST /api/v1/admin/auth/keys.
//
// @Summary      Create auth key
// @Description  Generates a new API key. The key value is returned only once. A key whose roles reach no persona is still created, and the response carries a warning naming the roles the personas carry.
// @Tags         Auth Keys
// @Accept       json
// @Produce      json
// @Param        body  body  authKeyCreateRequest  true  "Key definition"
// @Success      201  {object}  authKeyCreateResponse
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/auth/keys [post]
func (h *Handler) createAuthKey(w http.ResponseWriter, r *http.Request) {
	var req authKeyCreateRequest
	if err := decodeStrict(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	if !h.syncAPIKeys(w, r) {
		return
	}
	keyValue, err := h.deps.APIKeyManager.GenerateKey(def)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// The store is where the key comes into being: it authenticates once it is
	// stored, and a name another replica stored first is refused here.
	if err := h.persistAPIKey(r, keyValue, def); err != nil {
		if errors.Is(err, platform.ErrAPIKeyExists) {
			writeError(w, http.StatusConflict, fmt.Sprintf("key with name %q already exists", def.Name))
			return
		}
		slog.Warn("failed to persist api key", logKeyName, logsan.SanitizeForLog(def.Name), logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to persist api key")
		return
	}
	h.afterAPIKeyWrite(r)

	resp := authKeyCreateResponse{
		Name:        req.Name,
		Email:       apiKeyEmailFallback(req.Email, req.Name),
		Description: req.Description,
		Key:         keyValue,
		Roles:       req.Roles,
		ExpiresAt:   def.ExpiresAt,
		Warning:     "Store this key securely. It will not be shown again.",
	}
	var unmapped bool
	resp.Persona, unmapped = h.keyPersona(req.Roles)
	if unmapped {
		resp.Warnings = []string{h.noPersonaWarning(req.Roles)}
	}
	writeJSON(w, http.StatusCreated, resp)
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
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/auth/keys/{name} [delete]
func (h *Handler) deleteAuthKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if !h.syncAPIKeys(w, r) {
		return
	}
	// Block deletion of file-only keys — they would reappear on restart.
	if source := h.keySourceByName(name); source == platform.SourceFile {
		writeError(w, http.StatusConflict,
			"this key is defined in the config file and cannot be deleted via the admin API")
		return
	}
	if h.deps.APIKeyStore == nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	// A key is deleted when the store no longer holds it: every replica
	// confirms a database key against the store before accepting it.
	if err := h.deps.APIKeyStore.Delete(r.Context(), name); err != nil {
		if errors.Is(err, platform.ErrAPIKeyNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		slog.Warn("failed to delete api key from database", logKeyName, logsan.SanitizeForLog(name), logKeyError, err) // #nosec G706 -- name is sanitized
		writeError(w, http.StatusInternalServerError, "failed to delete api key from database")
		return
	}
	h.afterAPIKeyWrite(r)

	writeJSON(w, http.StatusOK, statusResponse{Status: statusDeleted})
}

// syncAPIKeys brings the keys held in memory up to the key store, answering
// 500 and reporting false when the store cannot be read: an answer from memory
// alone is the stale answer the sync exists to prevent.
func (h *Handler) syncAPIKeys(w http.ResponseWriter, r *http.Request) bool {
	if err := h.deps.APIKeyManager.SyncHashedKeys(r.Context()); err != nil {
		slog.Warn("failed to read api keys from the key store", logKeyError, logsan.SanitizeForLog(err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to read api keys")
		return false
	}
	return true
}

// afterAPIKeyWrite brings this replica's keys up to the write it just made and
// tells the other replicas. Neither is what makes the write take effect, which
// the store does, so a sync that fails here is logged and the write stands.
func (h *Handler) afterAPIKeyWrite(r *http.Request) {
	if err := h.deps.APIKeyManager.SyncHashedKeys(r.Context()); err != nil {
		slog.Warn("failed to re-read api keys after a key write", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
	if h.deps.ReloadNotifier != nil {
		h.deps.ReloadNotifier.PublishAPIKeyReload()
	}
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
// Returns an error if persistence fails, platform.ErrAPIKeyExists among them.
// With no store there is nowhere for the key to exist, so that is an error too.
func (h *Handler) persistAPIKey(r *http.Request, keyValue string, def auth.APIKey) error {
	if h.deps.APIKeyStore == nil {
		return errNoAPIKeyStore
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

	if err := h.deps.APIKeyStore.Create(r.Context(), dbDef); err != nil {
		return fmt.Errorf("persisting api key: %w", err)
	}
	return nil
}

// apiKeyEmailFallback returns the email or the synthetic address built from the
// key's name. The synthetic form comes from pkg/auth rather than being spelled
// again here, so a key created through the admin API and one authenticated from
// config carry the same address.
func apiKeyEmailFallback(email, name string) string {
	if email != "" {
		return email
	}
	return auth.SyntheticEmail(name)
}
