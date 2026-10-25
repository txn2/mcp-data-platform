// Package endpointwire mounts endpoints (#2170) on the platform's mux: the
// /endpoints/{name}/... routes and the bot check's, which authenticate a
// signed-in caller the way the portal does (a browser session, CSRF-checked
// on a write, or an API key), and the bot check's settings behind the admin
// API.
package endpointwire

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/botcheckapi"
	"github.com/txn2/mcp-data-platform/internal/admin/endpointapi"
	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Mount registers the routes. A deployment with no database has no
// endpoints and mounts nothing. The settings routes are mounted where the
// admin API is on, behind adminAuth.
func Mount(mux *http.ServeMux, p *platform.Platform, adminAuth func() func(http.Handler) http.Handler, author func(*http.Request) string) {
	if p == nil {
		return
	}
	e := p.Endpoints()
	if e == nil {
		return
	}
	var opts []portal.AuthenticatorOption
	if p.BrowserSessionAuth() != nil {
		opts = append(opts, portal.WithBrowserAuth(p.BrowserSessionAuth()))
	}
	var settings func(http.Handler) http.Handler
	if p.Config().Admin.IsEnabled() {
		settings = adminAuth()
	}
	mountOver(mux, e, Authenticator(portal.NewAuthenticator(p.Authenticator(), opts...), p.PersonaRegistry().IsExternalRoles), settings, author)
	if settings != nil && p.DB() != nil {
		scripts := scriptstore.New(p.DB())
		endpointapi.Register(mux, settings, endpointapi.Config{
			DB: p.DB(), Bindings: e.Bindings, Script: scripts.GetByID, BaseURL: p.Config().Portal.PublicBaseURL,
		})
		slog.Info("Endpoints admin API on /api/v1/admin/endpoints enabled")
	}
}

// mountOver registers the routes over the assembled feature; the settings
// routes only when settings, the admin API's authentication, is not nil.
func mountOver(mux *http.ServeMux, e *epwire.Endpoints, auth func(r *http.Request) (*eprun.Caller, error), settings func(http.Handler) http.Handler, author func(*http.Request) string) {
	h := &ephttp.Handler{Runner: e.Runner, Checker: e.Checker, Authenticate: auth}
	if e.Refusals != nil {
		h.Refused = e.Refusals.Refused
	}
	h.Mount(mux)
	if settings == nil {
		return
	}
	botcheckapi.Register(mux, settings, botcheckapi.Config{
		Settings: e.Checker.Settings, KeyAvailable: e.Checker.Key != nil, Secret: e.Checker.Secret, Author: author,
	})
	slog.Info("Endpoints on /endpoints/ and the bot check settings admin API on /api/v1/admin/settings/bot-check enabled")
}

// portalAuth is the portal's authentication of a request.
type portalAuth interface {
	Authenticate(r *http.Request) (*portal.User, error)
}

// Authenticator adapts the portal's authentication to an endpoint's caller.
// The portal reports an error only for a browser session refused its CSRF
// token, which is the caller's credential refused rather than absent. A person
// of an external persona is refused too (#2290): a signed-in endpoint runs a
// script, which reaches far more than what is shared with them.
func Authenticator(a portalAuth, external func(roles []string) bool) func(r *http.Request) (*eprun.Caller, error) {
	return func(r *http.Request) (*eprun.Caller, error) {
		u, err := a.Authenticate(r)
		if err != nil {
			return nil, errors.Join(ephttp.ErrForbidden, err)
		}
		if u == nil {
			return nil, nil
		}
		if external != nil && external(u.Roles) {
			return nil, fmt.Errorf("an external persona reaches no endpoint: %w", ephttp.ErrForbidden)
		}
		return &eprun.Caller{UserID: u.UserID, Email: u.Email, AuthType: u.AuthType}, nil
	}
}
