package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/user"
)

// userSummary is the admin-facing representation of a directory entry.
type userSummary struct {
	Email      string     `json:"email" example:"marcus.johnson@example.com"`
	FirstName  string     `json:"first_name" example:"Marcus"`
	LastName   string     `json:"last_name" example:"Johnson"`
	Source     string     `json:"source" example:"auth"`
	Confirmed  bool       `json:"confirmed" example:"true"`
	AddedBy    string     `json:"added_by,omitempty" example:"admin@example.com"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// userListResponse wraps a page of directory users.
type userListResponse struct {
	Users []userSummary `json:"users"`
	Total int           `json:"total" example:"42"`
}

// userCreateRequest is the body for adding a person by email.
type userCreateRequest struct {
	Email     string `json:"email" example:"marcus.johnson@example.com"`
	FirstName string `json:"first_name,omitempty" example:"Marcus"`
	LastName  string `json:"last_name,omitempty" example:"Johnson"`
}

// userUpdateRequest is the body for editing a person's name. Omitted fields are
// left unchanged.
type userUpdateRequest struct {
	FirstName *string `json:"first_name,omitempty" example:"Marcus"`
	LastName  *string `json:"last_name,omitempty" example:"Johnson"`
}

// registerUserRoutes registers the known-users directory admin endpoints
// (#614). Read endpoints are always available when a directory store is wired;
// write endpoints require database config mode.
func (h *Handler) registerUserRoutes() {
	if h.deps.UserStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/users", h.listUsers)
	h.mux.HandleFunc("GET /api/v1/admin/users/{email}", h.getUser)
	if h.isMutable() {
		h.mux.HandleFunc("POST /api/v1/admin/users", h.createUser)
		h.mux.HandleFunc("PUT /api/v1/admin/users/{email}", h.updateUser)
		h.mux.HandleFunc("DELETE /api/v1/admin/users/{email}", h.deleteUser)
	} else {
		// Register write patterns as 405 so the mux distinguishes "method not
		// allowed in file mode" from "route not found".
		h.mux.HandleFunc("POST /api/v1/admin/users", h.readOnlyMethod())
		h.mux.HandleFunc("PUT /api/v1/admin/users/{email}", h.readOnlyMethod())
		h.mux.HandleFunc("DELETE /api/v1/admin/users/{email}", h.readOnlyMethod())
	}
}

// listUsers handles GET /api/v1/admin/users.
//
// @Summary      List directory users
// @Description  Returns known users (people seen via authentication or pre-added by an admin) for sharing.
// @Tags         Users
// @Produce      json
// @Param        q       query  string   false  "Case-insensitive match on email or name"
// @Param        limit   query  integer  false  "Results per page (default: 100)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  userListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/users [get]
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, total, err := h.deps.UserStore.List(r.Context(), user.Filter{
		Query:  r.URL.Query().Get("q"),
		Limit:  intQueryParam(r.URL.Query().Get("limit"), user.DefaultListLimit),
		Offset: intQueryParam(r.URL.Query().Get("offset"), 0),
	})
	if err != nil {
		slog.Warn("failed to list directory users", logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	summaries := make([]userSummary, 0, len(users))
	for i := range users {
		summaries = append(summaries, toUserSummary(users[i]))
	}
	writeJSON(w, http.StatusOK, userListResponse{Users: summaries, Total: total})
}

// getUser handles GET /api/v1/admin/users/{email}.
//
// @Summary      Get directory user
// @Tags         Users
// @Produce      json
// @Param        email  path  string  true  "User email"
// @Success      200  {object}  userSummary
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/users/{email} [get]
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	email, ok := h.normalizeEmailParam(w, r)
	if !ok {
		return
	}
	u, err := h.deps.UserStore.Get(r.Context(), email)
	if errors.Is(err, user.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	writeJSON(w, http.StatusOK, toUserSummary(*u))
}

// createUser handles POST /api/v1/admin/users.
//
// @Summary      Add a directory user
// @Description  Pre-adds a person by email so they are selectable for sharing before they have logged in. Only available in database config mode.
// @Tags         Users
// @Accept       json
// @Produce      json
// @Param        body  body  userCreateRequest  true  "User to add"
// @Success      201  {object}  userSummary
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/users [post]
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req userCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email, err := user.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateUserNames(req.FirstName, req.LastName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	u := user.User{
		Email: email,
		// Sanitize even admin-entered names (strip control characters): the
		// directory is served to every authenticated user via the share
		// picker, so the admin write path must not be a weaker injection
		// surface than the auth path (which sanitizes in pkg/user.Directory).
		FirstName: user.SanitizeName(req.FirstName),
		LastName:  user.SanitizeName(req.LastName),
		Source:    user.SourceAdmin,
		AddedBy:   extractAuthor(r),
	}
	err = h.deps.UserStore.Insert(r.Context(), u)
	if errors.Is(err, user.ErrAlreadyExists) {
		writeError(w, http.StatusConflict, "user already exists")
		return
	}
	if err != nil {
		slog.Warn("failed to insert directory user", logKeyError, err)
		writeError(w, http.StatusInternalServerError, "failed to add user")
		return
	}

	created, err := h.deps.UserStore.Get(r.Context(), email)
	if err != nil {
		// The row was inserted; fall back to echoing the request.
		writeJSON(w, http.StatusCreated, toUserSummary(u))
		return
	}
	writeJSON(w, http.StatusCreated, toUserSummary(*created))
}

// updateUser handles PUT /api/v1/admin/users/{email}.
//
// @Summary      Edit a directory user's name
// @Tags         Users
// @Accept       json
// @Produce      json
// @Param        email  path  string             true  "User email"
// @Param        body   body  userUpdateRequest  true  "Fields to update"
// @Success      200  {object}  userSummary
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/users/{email} [put]
func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	email, ok := h.normalizeEmailParam(w, r)
	if !ok {
		return
	}
	var req userUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := validateUserUpdate(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err := h.deps.UserStore.Update(r.Context(), email, sanitizedUpdate(req))
	if errors.Is(err, user.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	updated, err := h.deps.UserStore.Get(r.Context(), email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated user")
		return
	}
	writeJSON(w, http.StatusOK, toUserSummary(*updated))
}

// deleteUser handles DELETE /api/v1/admin/users/{email}.
//
// @Summary      Remove a directory user
// @Tags         Users
// @Produce      json
// @Param        email  path  string  true  "User email"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/users/{email} [delete]
func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	email, ok := h.normalizeEmailParam(w, r)
	if !ok {
		return
	}
	err := h.deps.UserStore.Delete(r.Context(), email)
	if errors.Is(err, user.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: statusDeleted})
}

// intQueryParam parses a query value as a non-negative int, returning def when
// the value is absent or unparseable.
func intQueryParam(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// normalizeEmailParam reads, normalizes, and validates the {email} path value,
// writing a 400 and returning ok=false on failure.
func (*Handler) normalizeEmailParam(w http.ResponseWriter, r *http.Request) (email string, ok bool) {
	normalized, err := user.NormalizeEmail(r.PathValue("email"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return normalized, true
}

// validateUserNames validates first and last name lengths.
func validateUserNames(first, last string) error {
	if err := user.ValidateName(first); err != nil {
		return fmt.Errorf("first_name: %w", err)
	}
	if err := user.ValidateName(last); err != nil {
		return fmt.Errorf("last_name: %w", err)
	}
	return nil
}

// sanitizedUpdate builds a store Update from the request, stripping control
// characters from any provided name (see createUser for the rationale).
func sanitizedUpdate(req userUpdateRequest) user.Update {
	var u user.Update
	if req.FirstName != nil {
		s := user.SanitizeName(*req.FirstName)
		u.FirstName = &s
	}
	if req.LastName != nil {
		s := user.SanitizeName(*req.LastName)
		u.LastName = &s
	}
	return u
}

// validateUserUpdate validates the non-nil fields of an update request.
func validateUserUpdate(req userUpdateRequest) error {
	if req.FirstName != nil {
		if err := user.ValidateName(*req.FirstName); err != nil {
			return fmt.Errorf("first_name: %w", err)
		}
	}
	if req.LastName != nil {
		if err := user.ValidateName(*req.LastName); err != nil {
			return fmt.Errorf("last_name: %w", err)
		}
	}
	return nil
}

// toUserSummary converts a domain user to its admin API representation.
func toUserSummary(u user.User) userSummary {
	return userSummary{
		Email:      u.Email,
		FirstName:  u.FirstName,
		LastName:   u.LastName,
		Source:     u.Source,
		Confirmed:  u.Confirmed,
		AddedBy:    u.AddedBy,
		LastSeenAt: u.LastSeenAt,
		CreatedAt:  u.CreatedAt,
		UpdatedAt:  u.UpdatedAt,
	}
}
