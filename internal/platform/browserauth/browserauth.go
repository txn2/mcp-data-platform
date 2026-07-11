// Package browserauth builds the browser-facing authentication stack for the
// portal and admin web UI: the OIDC login/callback flow and the cookie
// authenticator, held together behind one Session handle.
//
// New takes an explicit Config so the stack can be built and tested on its own;
// the package imports only pkg/browsersession, not pkg/platform.
package browserauth

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
)

// Config carries the values needed to build a Session. Callers translate their
// own config into it so this package stays free of platform config types.
type Config struct {
	// Cookie settings.
	CookieName string
	Domain     string
	SameSite   string
	Secure     bool
	TTL        time.Duration
	SigningKey []byte

	// OIDC / login-flow settings.
	Issuer             string
	ClientID           string
	ClientSecret       string
	Scopes             []string
	RoleClaim          string
	RolePrefix         string
	RedirectURI        string
	PostLogoutRedirect string

	// OnLogin records a user at login; may be nil.
	OnLogin func(email, firstName, lastName string)
}

// Session holds the login flow and cookie authenticator for the browser UI.
type Session struct {
	flow *browsersession.Flow
	auth *browsersession.Authenticator
}

// New builds the login flow and cookie authenticator from cfg.
func New(ctx context.Context, cfg Config) (*Session, error) {
	cookieCfg := browsersession.CookieConfig{
		Name:     cfg.CookieName,
		Domain:   cfg.Domain,
		Secure:   cfg.Secure,
		TTL:      cfg.TTL,
		Key:      cfg.SigningKey,
		SameSite: browsersession.ParseSameSite(cfg.SameSite),
	}

	// SameSite=None disables the browser's built-in cross-site cookie defense,
	// leaving the X-CSRF-Token check as the sole protection; warn on it.
	if cookieCfg.IsCrossSiteCookieMode() {
		slog.Warn("session cookie SameSite=None permits cross-site submission; " +
			"the X-CSRF-Token header is the sole CSRF protection")
	}

	flowCfg := browsersession.FlowConfig{
		Issuer:             cfg.Issuer,
		ClientID:           cfg.ClientID,
		ClientSecret:       cfg.ClientSecret,
		RedirectURI:        cfg.RedirectURI,
		Scopes:             cfg.Scopes,
		RoleClaim:          cfg.RoleClaim,
		RolePrefix:         cfg.RolePrefix,
		Cookie:             cookieCfg,
		PostLoginRedirect:  browsersession.DefaultPortalPath,
		PostLogoutRedirect: cfg.PostLogoutRedirect,
		OnLogin:            cfg.OnLogin,
	}

	flow, err := browsersession.NewFlow(ctx, flowCfg)
	if err != nil {
		return nil, fmt.Errorf("creating OIDC flow: %w", err)
	}

	return NewSession(flow, browsersession.NewAuthenticator(cookieCfg)), nil
}

// NewSession wraps an already-built login flow and cookie authenticator into a
// Session. New uses it after constructing both; it is also the seam for wiring
// a pre-built authenticator (e.g. in tests) without a live OIDC flow.
func NewSession(flow *browsersession.Flow, auth *browsersession.Authenticator) *Session {
	return &Session{flow: flow, auth: auth}
}

// Flow returns the OIDC login flow, or nil when browser sessions are disabled
// (a nil Session).
func (s *Session) Flow() *browsersession.Flow {
	if s == nil {
		return nil
	}
	return s.flow
}

// Authenticator returns the cookie authenticator, or nil when browser sessions
// are disabled (a nil Session).
func (s *Session) Authenticator() *browsersession.Authenticator {
	if s == nil {
		return nil
	}
	return s.auth
}
