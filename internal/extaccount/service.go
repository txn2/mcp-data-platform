package extaccount

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// ClaimPath is the route a sign-in link opens.
const ClaimPath = "/portal/auth/email-link/claim"

// tokenBytes is the entropy of a sign-in link's token.
const tokenBytes = 32

// Personas is the persona registry the service resolves an account's persona
// through.
type Personas interface {
	Get(name string) (*persona.Persona, bool)
	GetForRoles(roles []string) (*persona.Persona, bool)
}

// Mailer sends a sign-in link: the invitation when invitation is true, and a
// link the person asked for otherwise. valid is how long the link works.
type Mailer func(ctx context.Context, to, name, link string, invitation bool, valid time.Duration) error

// Config is what the service is built from.
type Config struct {
	Store    Store
	Personas Personas
	// StoredPersona reads a persona from the persona store, which another
	// replica writes before this one's registry hears of it; nil reads the
	// registry alone. Create asks it, so an account can be created the moment
	// its persona is saved on any replica.
	StoredPersona func(ctx context.Context, name string) (*persona.Persona, bool)
	// Settings reads the stored configuration; nil uses Defaults.
	Settings func(ctx context.Context) Settings
	// KnownInternal reports whether an address already signs in through
	// the identity provider; nil knows none.
	KnownInternal func(ctx context.Context, email string) (bool, error)
	// Mail sends the links; nil sends none, and Create still stores the
	// account.
	Mail Mailer
	// RevokeShares revokes every share to an address, run when its account
	// is removed so no guest link reopens what was shared.
	RevokeShares func(ctx context.Context, email string) error
	// BaseURL is the portal's public origin a link is built on.
	BaseURL string
	// Cookie is the browser-session cookie a claimed link sets; its TTL is
	// replaced by the session setting.
	Cookie browsersession.CookieConfig
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Service manages external accounts.
type Service struct {
	cfg Config
}

// New returns the service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg}
}

func (s *Service) settings(ctx context.Context) Settings {
	if s.cfg.Settings == nil {
		return Defaults()
	}
	return s.cfg.Settings(ctx)
}

// externalPersona returns the persona an account may be created or served
// under: external, and the one its own roles resolve to, so a session
// carrying those roles is held to it.
func (s *Service) externalPersona(name string) (*persona.Persona, error) {
	if s.cfg.Personas == nil {
		return nil, ErrPersona
	}
	p, ok := s.cfg.Personas.Get(name)
	if !ok || !p.External || len(p.Roles) == 0 {
		return nil, ErrPersona
	}
	if got, ok := s.cfg.Personas.GetForRoles(p.Roles); !ok || got.Name != p.Name {
		return nil, ErrPersona
	}
	return p, nil
}

// creatable reports whether an account may be created under the persona:
// external and resolved to by its own roles in this replica's registry, or,
// before the registry has heard of it, external in the persona store. A
// session is still judged against the registry on every request.
func (s *Service) creatable(ctx context.Context, name string) error {
	_, err := s.externalPersona(name)
	if err == nil || s.cfg.StoredPersona == nil {
		return err
	}
	if s.cfg.Personas != nil {
		if _, known := s.cfg.Personas.Get(name); known {
			return err
		}
	}
	if p, ok := s.cfg.StoredPersona(ctx, name); ok && p.External && len(p.Roles) > 0 {
		return nil
	}
	return ErrPersona
}

// Create stores a new account and emails its invitation.
func (s *Service) Create(ctx context.Context, n NewAccount, by string) (Account, error) {
	n, err := n.normalize(s.cfg.Now())
	if err != nil {
		return Account{}, err
	}
	if err := s.creatable(ctx, n.Persona); err != nil {
		return Account{}, err
	}
	if s.cfg.KnownInternal != nil {
		known, err := s.cfg.KnownInternal(ctx, n.Email)
		if err != nil {
			return Account{}, fmt.Errorf("checking the address: %w", err)
		}
		if known {
			return Account{}, ErrInternal
		}
	}
	id, _, err := mintToken()
	if err != nil {
		return Account{}, err
	}
	a := Account{
		ID: id[:accountIDLen], Email: n.Email, Name: n.Name, Persona: n.Persona,
		ExpiresAt: n.ExpiresAt, CreatedBy: by, CreatedAt: s.cfg.Now(),
	}
	if err := s.cfg.Store.Insert(ctx, a); err != nil {
		return Account{}, err //nolint:wrapcheck // ErrExists is the caller's to map
	}
	if err := s.sendLink(ctx, a, true); err != nil {
		slog.WarnContext(ctx, "external account: invitation not sent",
			"account", a.ID, "error", logsan.SanitizeForLog(err.Error()))
	}
	return a, nil
}

// RequestLink emails a sign-in link to an active account's address. It
// answers the same whether or not the address has one, so the sign-in screen
// tells nobody which addresses do; a mailbox is sent at most a few links an
// hour.
func (s *Service) RequestLink(ctx context.Context, email string) {
	a, err := s.cfg.Store.GetByEmail(ctx, strings.TrimSpace(email))
	if err != nil || !a.Active(s.cfg.Now()) {
		return
	}
	if n, err := s.cfg.Store.CountLinksSince(ctx, a.ID, s.cfg.Now().Add(-time.Hour)); err != nil || n >= maxLinksPerHour {
		return
	}
	if err := s.sendLink(ctx, *a, false); err != nil {
		slog.WarnContext(ctx, "external account: sign-in link not sent",
			"account", a.ID, "error", logsan.SanitizeForLog(err.Error()))
	}
}

func (s *Service) sendLink(ctx context.Context, a Account, invitation bool) error {
	token, hash, err := mintToken()
	if err != nil {
		return err
	}
	set := s.settings(ctx)
	valid := set.Link()
	if invitation {
		valid = set.Invite()
	}
	if err := s.cfg.Store.InsertLink(ctx, hash, a.ID, s.cfg.Now().Add(valid)); err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	if s.cfg.Mail == nil {
		return errors.New("no mail delivery configured")
	}
	link := strings.TrimSuffix(s.cfg.BaseURL, "/") + ClaimPath + "?token=" + url.QueryEscape(token)
	return s.cfg.Mail(ctx, a.Email, a.Name, link, invitation, valid)
}

// Claim spends a sign-in link and sets the session cookie for its account.
func (s *Service) Claim(ctx context.Context, w http.ResponseWriter, token string) (Account, error) {
	id, ok, err := s.cfg.Store.ClaimLink(ctx, hashToken(token), s.cfg.Now())
	if err != nil {
		return Account{}, err //nolint:wrapcheck // the store names the read
	}
	if !ok {
		return Account{}, ErrLink
	}
	a, err := s.cfg.Store.Get(ctx, id)
	if err != nil || !a.Active(s.cfg.Now()) {
		return Account{}, ErrLink
	}
	p, err := s.externalPersona(a.Persona)
	if err != nil {
		return Account{}, ErrLink
	}
	cookie := s.cfg.Cookie
	cookie.TTL = s.settings(ctx).Session()
	signed, err := browsersession.SignSession(browsersession.SessionClaims{
		UserID: a.UserID(), Email: a.Email, Roles: p.Roles,
	}, &cookie)
	if err != nil {
		return Account{}, fmt.Errorf("signing the session: %w", err)
	}
	browsersession.SetCookie(w, &cookie, signed)
	_ = s.cfg.Store.TouchSignIn(ctx, a.ID, s.cfg.Now())
	return *a, nil
}

// Admit answers for one request an external session makes: the roles of the
// account's persona as they stand now, while the account is active, and
// false otherwise. A user id that names no external account is not this
// service's to judge and is admitted with the roles it carried.
func (s *Service) Admit(ctx context.Context, userID string, roles []string) ([]string, bool) {
	id, ok := AccountID(userID)
	if !ok {
		return roles, true
	}
	a, err := s.cfg.Store.Get(ctx, id)
	if err != nil || !a.Active(s.cfg.Now()) {
		return nil, false
	}
	p, err := s.externalPersona(a.Persona)
	if err != nil {
		return nil, false
	}
	return p.Roles, true
}

// List returns every account.
func (s *Service) List(ctx context.Context) ([]Account, error) {
	return s.cfg.Store.List(ctx) //nolint:wrapcheck // the store names the read
}

// Get returns one account.
func (s *Service) Get(ctx context.Context, id string) (*Account, error) {
	return s.cfg.Store.Get(ctx, id) //nolint:wrapcheck // ErrNotFound is the caller's to map
}

// Extend sets the account's expiry; nil removes it.
func (s *Service) Extend(ctx context.Context, id string, expiresAt *time.Time) error {
	if expiresAt != nil && !expiresAt.After(s.cfg.Now()) {
		return invalid("expires_at must be in the future")
	}
	return s.cfg.Store.SetExpiry(ctx, id, expiresAt) //nolint:wrapcheck // ErrNotFound is the caller's to map
}

// Suspend stops the account signing in and ends its sessions on their next
// request.
func (s *Service) Suspend(ctx context.Context, id string) error {
	now := s.cfg.Now()
	return s.cfg.Store.SetSuspended(ctx, id, &now) //nolint:wrapcheck // ErrNotFound is the caller's to map
}

// Resume lets a suspended account sign in again.
func (s *Service) Resume(ctx context.Context, id string) error {
	return s.cfg.Store.SetSuspended(ctx, id, nil) //nolint:wrapcheck // ErrNotFound is the caller's to map
}

// Remove deletes the account and revokes every share to its address.
func (s *Service) Remove(ctx context.Context, id string) error {
	a, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // ErrNotFound is the caller's to map
	}
	if err := s.cfg.Store.Delete(ctx, id); err != nil {
		return err //nolint:wrapcheck // ErrNotFound is the caller's to map
	}
	if s.cfg.RevokeShares != nil {
		if err := s.cfg.RevokeShares(ctx, a.Email); err != nil {
			return fmt.Errorf("revoking the shares to a removed account: %w", err)
		}
	}
	return nil
}

// ConvertOnIdentityProviderSignIn closes the account for an address that has
// just signed in through the identity provider. Best effort: the sign-in
// does not depend on it.
func (s *Service) ConvertOnIdentityProviderSignIn(ctx context.Context, email string) {
	if email == "" {
		return
	}
	converted, err := s.cfg.Store.Convert(ctx, email, s.cfg.Now())
	if err != nil {
		slog.WarnContext(ctx, "external account: conversion failed", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	if converted {
		slog.InfoContext(ctx, "external account converted by an identity provider sign-in")
	}
}

// IsExternal reports whether an address has an external account, open or not.
// The share guest path asks it: such an address signs in, or is refused, and
// is never sent a guest link.
func (s *Service) IsExternal(ctx context.Context, email string) bool {
	a, err := s.cfg.Store.GetByEmail(ctx, email)
	return err == nil && a.ConvertedAt == nil
}

// accountIDLen is how much of a minted token an account id keeps: 22
// base64url characters, 132 random bits.
const accountIDLen = 22

func mintToken() (token, hash string, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("minting a token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
