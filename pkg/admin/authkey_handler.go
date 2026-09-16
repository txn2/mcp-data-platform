package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/apikeyissue"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// authKeyCreateRequest is the request body for creating an API key.
type authKeyCreateRequest struct {
	Name        string `json:"name" example:"ci-pipeline"`
	Email       string `json:"email,omitempty" example:"ci@example.com"`
	Description string `json:"description,omitempty" example:"CI/CD pipeline integration"`
	// UserEmail issues the key against a person's account: it authenticates as
	// them, so what they do through a client that speaks only bearer tokens is
	// theirs and is there when they sign in to the portal (#1759). The person
	// must be somebody the platform has seen sign in. Leave it out for the
	// standalone service key a key has always been.
	UserEmail string `json:"user_email,omitempty" example:"analyst@example.com"`
	// Roles is required for a service key. On a key bound through UserEmail it
	// is optional: left out, the key carries whatever roles that person holds,
	// on every request, so a role their provider revokes stops reaching the
	// key. Given, it replaces theirs on this key and is used verbatim -- it is
	// not intersected with what they hold.
	Roles     []string `json:"roles" example:"analyst"`
	ExpiresIn string   `json:"expires_in,omitempty" example:"720h"` // e.g. "24h", "720h", "8760h"
}

// authKeyCreateResponse is the response after creating an API key.
type authKeyCreateResponse struct {
	Name        string     `json:"name" example:"ci-pipeline"`
	Email       string     `json:"email,omitempty" example:"ci@example.com"`
	Description string     `json:"description,omitempty" example:"CI/CD pipeline integration"`
	Key         string     `json:"key" example:"3f9a1c07e2b84d56a0c3e1f7b9d2468ace13579bdf02468ace13579bdf024681"`
	Roles       []string   `json:"roles" example:"analyst"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// UserEmail is the account the key was issued against, absent for a
	// service key.
	UserEmail string `json:"user_email,omitempty" example:"analyst@example.com"`
	Warning   string `json:"warning" example:"Store this key securely. It will not be shown again."`
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
		entry.Persona, entry.NoPersona = h.keyPersona(h.effectiveRoles(r, k))
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
	// Roles say what a service key may reach and are the only thing that does,
	// so one without them reaches nothing. A key bound to a person has a role
	// set already -- theirs -- and names one only to narrow it.
	if req.UserEmail == "" && len(req.Roles) == 0 {
		writeError(w, http.StatusBadRequest, "roles is required, or bind the key to a user with user_email")
		return
	}

	expiresAt, ok := authKeyExpiry(w, req.ExpiresIn)
	if !ok {
		return
	}

	if !h.syncAPIKeys(w, r) {
		return
	}
	issued, err := h.apiKeyIssuer().Issue(r.Context(), apikeyissue.Request{
		Name:        req.Name,
		Description: req.Description,
		Email:       apiKeyEmailFallback(req.Email, req.Name),
		UserEmail:   req.UserEmail,
		Roles:       req.Roles,
		ExpiresAt:   expiresAt,
		CreatedBy:   extractAuthor(r),
	})
	if err != nil {
		writeAuthKeyIssueError(w, req.Name, req.UserEmail, err)
		return
	}

	resp := authKeyCreateResponse{
		Name:        req.Name,
		Email:       issued.Email,
		Description: req.Description,
		Key:         issued.Key,
		Roles:       issued.Roles,
		ExpiresAt:   expiresAt,
		UserEmail:   req.UserEmail,
		Warning:     "Store this key securely. It will not be shown again.",
	}
	// A bound key with no roles of its own reaches the persona its person
	// reaches, so the persona reported is the one those roles map to.
	effective := issued.Roles
	if len(effective) == 0 && issued.Person != nil {
		effective = issued.Person.Roles
		resp.Roles = effective
	}
	var unmapped bool
	resp.Persona, unmapped = h.keyPersona(effective)
	if unmapped {
		resp.Warnings = []string{h.noPersonaWarning(effective)}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// effectiveRoles is what a listed key actually reaches: its own roles, or, for
// a key issued against an account with none of its own, the roles that person
// holds (#1759).
//
// Without this such a key lists as reaching no persona, which is the opposite
// of true: it reaches whatever persona its owner does. An account that cannot
// be resolved answers the key's own roles, so the listing reports what it can
// rather than a persona it has not checked.
func (h *Handler) effectiveRoles(r *http.Request, k auth.APIKeySummary) []string {
	if k.UserEmail == "" || len(k.Roles) > 0 || h.deps.BoundPrincipals == nil {
		return k.Roles
	}
	person, err := h.deps.BoundPrincipals.BoundPrincipal(r.Context(), k.UserEmail)
	if err != nil {
		return k.Roles
	}
	return person.Roles
}

// authKeyExpiry parses the requested lifetime, answering the refusal itself and
// reporting false when it cannot. A key with no lifetime never expires.
func authKeyExpiry(w http.ResponseWriter, expiresIn string) (*time.Time, bool) {
	if expiresIn == "" {
		return nil, true
	}
	dur, err := time.ParseDuration(expiresIn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid expires_in duration: "+err.Error())
		return nil, false
	}
	if dur <= 0 {
		writeError(w, http.StatusBadRequest, "expires_in must be a positive duration")
		return nil, false
	}
	exp := time.Now().Add(dur)
	return &exp, true
}

// apiKeyIssuer is the shared key-issuing core, wired to this handler's deps.
// The portal's self-service route issues through the same one, so a key a
// person makes for themselves is made by the same rules (#1759).
func (h *Handler) apiKeyIssuer() *apikeyissue.Issuer {
	issuer := &apikeyissue.Issuer{
		Manager:    h.deps.APIKeyManager,
		Principals: h.deps.BoundPrincipals,
	}
	if h.deps.APIKeyStore != nil {
		issuer.Store = h.deps.APIKeyStore
	}
	if h.deps.ReloadNotifier != nil {
		issuer.Announce = h.deps.ReloadNotifier.PublishAPIKeyReload
	}
	return issuer
}

// writeAuthKeyIssueError answers a refusal from the issuing core in the admin
// API's own words.
func writeAuthKeyIssueError(w http.ResponseWriter, name, userEmail string, err error) {
	switch {
	case errors.Is(err, apikeyissue.ErrNameTaken):
		writeError(w, http.StatusConflict, fmt.Sprintf("key with name %q already exists", name))
	case errors.Is(err, apikeyissue.ErrUnknownPerson):
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"no key can be issued against %q: the platform has no record of that person signing in, "+
				"so it does not know what they authenticate as or what roles they hold. "+
				"They can sign in once, or the key can be issued with roles of its own and no user_email.",
			userEmail))
	case errors.Is(err, apikeyissue.ErrReservedName):
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"a key name may not begin with %q unless it is issued against a user account: that is the namespace "+
				"of keys people issue for themselves, and a key bound to nobody there would take a name its "+
				"apparent owner can neither see nor revoke.", apikeyissue.SelfIssuedPrefix))
	case errors.Is(err, apikeyissue.ErrNoStore):
		writeError(w, http.StatusInternalServerError, "this deployment stores no api keys")
	default:
		slog.Warn("failed to issue api key", logKeyName, logsan.SanitizeForLog(name), logKeyError, logsan.SanitizeForLog(err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to persist api key")
	}
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
