// Package shareguest gives the named recipient of an email share a way in
// without a platform account (#1001).
//
// v1.104.0 (#999) made share tokens enforce their audience but shipped only
// the refusal half: an external recipient with no account hit a bare text 403
// with no path forward. This package supplies the admission half:
//
//   - a branded landing page rendered for browser navigations the share gate
//     refuses, offering sign-in and, for email shares, a one-time view link;
//   - single-use, short-lived link tokens emailed only to the address the
//     share names, so a forwarded email transfers nothing;
//   - a signed guest session scoped to one share, admitting a view-only
//     variant of the public viewer and never the portal.
//
// The package is composed by the portal handler (which registers the two
// public routes and consults Admit/Deny from its share gate) and wired by the
// composition root, which supplies the share resolver, the link store, the
// transactional mailer, and the browser-session signing key the guest-session
// key is derived from.
package shareguest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"time"
)

// Link lifetimes and issue caps. The one-time link is deliberately short-lived
// and single-use: the recipient requests a fresh one per viewing session, and
// a forwarded or replayed link is dead. The guest session that a claimed link
// opens is a browser-session cookie whose signed lifetime is capped so access
// ends with the visit.
const (
	// LinkTTL is how long an emailed one-time link stays claimable.
	LinkTTL = 15 * time.Minute
	// GuestSessionTTL caps the signed lifetime of a guest session cookie.
	GuestSessionTTL = 12 * time.Hour
	// maxLinksPerWindow is the per-share issue cap inside linkCapWindow, so
	// the request endpoint cannot be used to mailbomb the recipient.
	maxLinksPerWindow = 5
	// linkCapWindow is the sliding window the per-share cap counts within.
	linkCapWindow = time.Hour
)

// guestKeyLabel domain-separates the guest-session signing key from the
// browser-session key it is derived from: a token signed with one key can
// never verify under the other, so a guest cookie cannot pose as a portal
// session no matter how the cookies are replayed.
const guestKeyLabel = "portal-share-guest-v1"

// DeriveKey returns the HMAC-SHA256 of label under master. It yields the
// domain-separated subkeys this package and the notification unsubscribe
// path sign with, so one configured master secret (the browser-session
// signing key) backs every derived credential without any key reuse.
func DeriveKey(master []byte, label string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte(label))
	return mac.Sum(nil)
}

// ShareInfo is the slice of a portal share the guest path depends on. The
// composition root adapts the portal's share store to this shape so the
// package needs no portal types.
type ShareInfo struct {
	ID string
	// Token is the share's public viewer token (the {token} path segment).
	Token string
	// RecipientEmail is the stored shared_with_email; empty for link shares.
	// It is the only address a one-time link is ever sent to.
	RecipientEmail string
	// Public reports whether the share admits anyone holding the token; a
	// public share has no use for guest links.
	Public bool
	// Revoked and Expired mirror the share's availability. A dead share
	// issues no links and admits no guests.
	Revoked bool
	Expired bool
}

// Live reports whether the share can still be opened by anyone.
func (s ShareInfo) Live() bool { return !s.Revoked && !s.Expired }

// Resolver looks up the share behind a public viewer token. ok is false when
// the token names no share.
type Resolver func(ctx context.Context, token string) (ShareInfo, bool)

// LinkMailer delivers one one-time-link email. The send is transactional
// (the recipient asked for it), so implementations must deliver directly
// rather than through the notification queue and its digest deferral.
type LinkMailer func(ctx context.Context, to, link string) error

// Config carries the dependencies for New. Links, SendLink, SessionKey, and
// BaseURL are each individually optional: when any of them is absent the
// one-time-link flow is disabled and the service still renders landing and
// denial pages (with the link request offer omitted).
type Config struct {
	// Resolve looks up shares by viewer token. Required.
	Resolve Resolver
	// Links persists one-time link tokens. nil disables the link flow.
	Links LinkStore
	// SendLink delivers the one-time-link email. nil disables the link flow.
	SendLink LinkMailer
	// SessionKey is the browser-session signing key; the guest-session key is
	// derived from it under guestKeyLabel. Empty disables the link flow.
	SessionKey []byte
	// BaseURL is the portal's public base URL, needed to build the absolute
	// link the email carries. Empty disables the link flow.
	BaseURL string
	// SecureCookie marks the guest cookie HTTPS-only. It should match the
	// browser-session cookie's setting.
	SecureCookie bool
	// Brand is the chrome the landing pages render with.
	Brand Brand
	// OptOutStatus reports whether an address has opted out of notification
	// emails (#1022). nil omits the landing page's opt-out notice.
	OptOutStatus func(ctx context.Context, email string) (bool, error)
	// Resubscribe re-enables notification delivery for an address (#1022).
	// nil omits the landing page's opt-back-in action.
	Resubscribe func(ctx context.Context, email string) error
}

// Service implements the guest access path. A nil *Service is inert: Admit
// admits nobody, and the portal falls back to plain-text denials.
type Service struct {
	resolve      Resolver
	links        LinkStore
	send         LinkMailer
	guestKey     []byte
	baseURL      string
	secureCookie bool
	brand        Brand
	optOutStatus func(ctx context.Context, email string) (bool, error)
	resubscribe  func(ctx context.Context, email string) error
	now          func() time.Time
}

// New builds the service. See Config for which absences disable the
// one-time-link flow while keeping page rendering available.
func New(cfg Config) *Service {
	s := &Service{
		resolve:      cfg.Resolve,
		links:        cfg.Links,
		send:         cfg.SendLink,
		baseURL:      cfg.BaseURL,
		secureCookie: cfg.SecureCookie,
		brand:        cfg.Brand,
		optOutStatus: cfg.OptOutStatus,
		resubscribe:  cfg.Resubscribe,
		now:          time.Now,
	}
	if len(cfg.SessionKey) > 0 {
		s.guestKey = DeriveKey(cfg.SessionKey, guestKeyLabel)
	}
	return s
}

// linksEnabled reports whether every dependency of the one-time-link flow is
// present.
func (s *Service) linksEnabled() bool {
	return s.links != nil && s.send != nil && len(s.guestKey) > 0 && s.baseURL != ""
}

// LinksAvailable reports whether the service can issue one-time links at all
// (not whether a particular share qualifies). Nil-safe.
func (s *Service) LinksAvailable() bool {
	return s != nil && s.linksEnabled()
}
