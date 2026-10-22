// Package extaccountapi is the REST surface of external accounts (#2092): the
// admin routes that create, list, extend, suspend, resume and remove the
// accounts and edit their settings, and the two public routes a person signs
// in through, asking for a link and claiming it.
package extaccountapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/extaccount"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
)

const (
	usersPath    = "/api/v1/admin/external-users"
	settingsPath = "/api/v1/admin/settings/external-accounts"
	// RequestPath is the public route a sign-in link is asked for on.
	RequestPath  = "/portal/auth/email-link"
	maxBodyBytes = 16 << 10
	// signedInPath is where a claimed link lands, and failedPath where a
	// dead one does, so the sign-in screen can say so.
	signedInPath = "/portal/"
	// claimPath is extaccount.ClaimPath, the route the emailed link opens,
	// written out so the route table can be read from this file.
	claimPath = "/portal/auth/email-link/claim"
	// pathID is the path value naming an account.
	pathID     = "id"
	failedPath = "/portal/?login=email&error=link_invalid"
)

// Service is the account service the routes call.
type Service interface {
	Create(ctx context.Context, n extaccount.NewAccount, by string) (extaccount.Account, error)
	List(ctx context.Context) ([]extaccount.Account, error)
	Extend(ctx context.Context, id string, expiresAt *time.Time) error
	Suspend(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	RequestLink(ctx context.Context, email string)
	Claim(ctx context.Context, w http.ResponseWriter, token string) (extaccount.Account, error)
}

// SettingsStore reads and writes the settings section.
type SettingsStore interface {
	Read(ctx context.Context) extaccount.Settings
	Write(ctx context.Context, s extaccount.Settings, author string) error
}

// Config is what the routes are built from.
type Config struct {
	Service  Service
	Settings SettingsStore
	// SharedItems counts the live shares to an address.
	SharedItems func(ctx context.Context, email string) (int, error)
	// Author resolves the acting admin.
	Author func(*http.Request) string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

type handler struct{ cfg Config }

// RegisterAdmin mounts the admin routes behind wrap, the admin API's
// authentication.
func RegisterAdmin(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &handler{cfg: cfg}
	mux.Handle("GET "+usersPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("POST "+usersPath, wrap(http.HandlerFunc(h.create)))
	mux.Handle("POST "+usersPath+"/{id}/extend", wrap(http.HandlerFunc(h.extend)))
	mux.Handle("POST "+usersPath+"/{id}/suspend", wrap(http.HandlerFunc(h.suspend)))
	mux.Handle("POST "+usersPath+"/{id}/resume", wrap(http.HandlerFunc(h.resume)))
	mux.Handle("DELETE "+usersPath+"/{id}", wrap(http.HandlerFunc(h.remove)))
	mux.Handle("GET "+settingsPath, wrap(http.HandlerFunc(h.getSettings)))
	mux.Handle("PUT "+settingsPath, wrap(http.HandlerFunc(h.putSettings)))
}

// RegisterPublic mounts the routes a person signs in through. They take no
// authentication: asking for a link answers the same for every address, and
// a link is its own credential.
func RegisterPublic(mux *http.ServeMux, svc Service) {
	h := &handler{cfg: Config{Service: svc}}
	mux.HandleFunc("POST "+RequestPath, h.requestLink)
	mux.HandleFunc("GET "+claimPath, h.claim)
}

// list handles GET /api/v1/admin/external-users.
//
// @Summary      List external users
// @Description  Returns every account the platform created for a person outside the organization, newest first: the address, name, external persona, expiry, when it was suspended, its last sign-in, when the person's first identity-provider sign-in converted it, its status (active, suspended, expired or converted) and the live shares to its address.
// @Tags         External users
// @Produce      json
// @Success      200  {object}  UserList
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/external-users [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.cfg.Service.List(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	out := UserList{Users: make([]UserView, 0, len(accounts))}
	now := h.cfg.Now()
	for _, a := range accounts {
		v := UserView{Account: a, Status: a.Status(now)}
		if h.cfg.SharedItems != nil {
			v.SharedItems, _ = h.cfg.SharedItems(r.Context(), a.Email)
		}
		out.Users = append(out.Users, v)
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// create handles POST /api/v1/admin/external-users.
//
// @Summary      Add an external user
// @Description  Creates an account for a person outside the organization under an external persona and emails them an invitation holding a one-time sign-in link, valid for the invite_hours setting. The persona must be external and its own roles must resolve to it. An address that already has an account is refused with 409, and so is one that already signs in through the identity provider. expires_at is optional; after it the account's sessions end and it cannot sign in.
// @Tags         External users
// @Accept       json
// @Produce      json
// @Param        body  body  extaccount.NewAccount  true  "The account"
// @Success      201  {object}  UserView
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      409  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/external-users [post]
func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var in extaccount.NewAccount
	if !decode(w, r, &in) {
		return
	}
	a, err := h.cfg.Service.Create(r.Context(), in, h.cfg.Author(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, UserView{Account: a, Status: a.Status(h.cfg.Now())})
}

// extend handles POST /api/v1/admin/external-users/{id}/extend.
//
// @Summary      Extend an external user
// @Description  Sets the account's expiry to expires_at, which must be in the future, or removes it when expires_at is null.
// @Tags         External users
// @Accept       json
// @Param        id    path  string         true  "Account id"
// @Param        body  body  ExtendRequest  true  "The new expiry"
// @Success      204
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/external-users/{id}/extend [post]
func (h *handler) extend(w http.ResponseWriter, r *http.Request) {
	var in ExtendRequest
	if !decode(w, r, &in) {
		return
	}
	h.done(w, r, h.cfg.Service.Extend(r.Context(), r.PathValue(pathID), in.ExpiresAt))
}

// suspend handles POST /api/v1/admin/external-users/{id}/suspend.
//
// @Summary      Suspend an external user
// @Description  Stops the account signing in and ends its sessions on their next request. Its shares stay; Resume restores it.
// @Tags         External users
// @Param        id  path  string  true  "Account id"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/external-users/{id}/suspend [post]
func (h *handler) suspend(w http.ResponseWriter, r *http.Request) {
	h.done(w, r, h.cfg.Service.Suspend(r.Context(), r.PathValue(pathID)))
}

// resume handles POST /api/v1/admin/external-users/{id}/resume.
//
// @Summary      Resume an external user
// @Description  Lets a suspended account sign in again.
// @Tags         External users
// @Param        id  path  string  true  "Account id"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/external-users/{id}/resume [post]
func (h *handler) resume(w http.ResponseWriter, r *http.Request) {
	h.done(w, r, h.cfg.Service.Resume(r.Context(), r.PathValue(pathID)))
}

// remove handles DELETE /api/v1/admin/external-users/{id}.
//
// @Summary      Remove an external user
// @Description  Deletes the account, ends its sessions on their next request, and revokes every share to its address, so no guest link reopens what was shared.
// @Tags         External users
// @Param        id  path  string  true  "Account id"
// @Success      204
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/external-users/{id} [delete]
func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	h.done(w, r, h.cfg.Service.Remove(r.Context(), r.PathValue(pathID)))
}

// getSettings handles GET /api/v1/admin/settings/external-accounts.
//
// @Summary      Get the external account settings
// @Description  Returns how long an external user's signed-in session lasts (session_hours, default 12), how long the invitation email's link can be used (invite_hours, default 168), and how long a link asked for on the sign-in screen can be used (link_minutes, default 15).
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  extaccount.Settings
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/external-accounts [get]
func (h *handler) getSettings(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, http.StatusOK, h.cfg.Settings.Read(r.Context()))
}

// putSettings handles PUT /api/v1/admin/settings/external-accounts.
//
// @Summary      Update the external account settings
// @Description  Sets the three durations. session_hours and invite_hours are 1 to 720, link_minutes 1 to 1440. A session already signed in keeps the length it was signed with.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        body  body  extaccount.Settings  true  "The settings"
// @Success      200  {object}  extaccount.Settings
// @Failure      400  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/external-accounts [put]
func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var in extaccount.Settings
	if !decode(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.cfg.Settings.Write(r.Context(), in, h.cfg.Author(r)); err != nil {
		fail(w, r, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, in)
}

// requestLink handles POST /portal/auth/email-link: it emails a sign-in link
// to the address when it has an active external account, and answers 202
// whether or not it does.
func (h *handler) requestLink(w http.ResponseWriter, r *http.Request) {
	var in LinkRequest
	if !decode(w, r, &in) {
		return
	}
	h.cfg.Service.RequestLink(r.Context(), in.Email)
	w.WriteHeader(http.StatusAccepted)
}

// claim handles GET /portal/auth/email-link/claim: it spends the link, sets
// the session cookie and sends the person to the portal, or to the sign-in
// screen saying the link cannot be used.
func (h *handler) claim(w http.ResponseWriter, r *http.Request) {
	if _, err := h.cfg.Service.Claim(r.Context(), w, r.URL.Query().Get("token")); err != nil {
		if !errors.Is(err, extaccount.ErrLink) {
			slog.WarnContext(r.Context(), "external account: claim failed", "error", logsan.SanitizeForLog(err.Error()))
		}
		http.Redirect(w, r, failedPath, http.StatusFound)
		return
	}
	http.Redirect(w, r, signedInPath, http.StatusFound)
}

func (*handler) done(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, extaccount.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, extaccount.ErrExists), errors.Is(err, extaccount.ErrInternal):
		httpjson.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, extaccount.ErrInvalid), errors.Is(err, extaccount.ErrPersona):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		slog.ErrorContext(r.Context(), "external accounts", "error", logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "external accounts are unavailable")
	}
}
