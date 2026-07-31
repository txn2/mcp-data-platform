package httpserver

import (
	"context"
	"database/sql"
	"encoding/base64"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpserver/unsubhttp"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// unsubscribeKeyLabel domain-separates the unsubscribe-token MAC key from the
// browser-session signing key it is derived from.
const unsubscribeKeyLabel = "notification-unsubscribe-v1"

// unsubscribePath is the no-login opt-out endpoint linked from notification
// email footers (#1001). It lives outside the portal handler's mounts: it
// must work for recipients with no account and no session.
const unsubscribePath = "/portal/notifications/unsubscribe"

// browserSessionSigningKey returns the decoded browser-session signing key,
// or nil when browser sessions are unconfigured or the key does not decode.
// It is the master secret the guest-session and unsubscribe-token keys are
// derived from; without it both features degrade away.
func browserSessionSigningKey(p *platform.Platform) []byte {
	if p == nil {
		return nil
	}
	bs := p.Config().Auth.BrowserSession
	if !bs.Enabled || bs.SigningKey == "" {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(bs.SigningKey)
	if err != nil {
		return nil
	}
	return key
}

// shareTokenReader is the one share-store call the guest service resolves
// through.
type shareTokenReader interface {
	GetByToken(ctx context.Context, token string) (*portal.Share, error)
}

// newShareGuestService assembles the portal's guest access service (#1001)
// over the portal share store the caller has already null-checked. The
// service always renders branded denial pages once the portal is mounted; the
// one-time-link flow additionally needs the database (link store), the
// notification substrate for the transactional email, the browser-session
// signing key, and a public base URL, and disables itself when any is absent.
func newShareGuestService(p *platform.Platform, notify *notifydelivery.Handle, store shareTokenReader, db *sql.DB) *shareguest.Service {
	cfg := shareguest.Config{
		Resolve:      shareGuestResolver(store),
		SessionKey:   browserSessionSigningKey(p),
		BaseURL:      p.Config().Portal.PublicBaseURL,
		SecureCookie: p.Config().Auth.BrowserSession.IsSecure(),
		Brand: shareguest.Brand{
			Name:               portalBrandName(p),
			LogoSVG:            p.BrandLogoSVG(),
			URL:                p.BrandURL(),
			ImplementorName:    p.Config().Portal.Implementor.Name,
			ImplementorLogoSVG: p.ResolveImplementorLogo(),
			ImplementorURL:     p.Config().Portal.Implementor.URL,
		},
	}
	if db != nil {
		cfg.Links = shareguest.NewPostgresLinkStore(db)
	}
	if notify != nil {
		cfg.SendLink = notify.SendGuestLink
	}
	if prefs := notify.Prefs(); prefs != nil {
		cfg.OptOutStatus = optOutStatusFn(prefs)
		cfg.Resubscribe = resubscribeFn(prefs)
	}
	svc := shareguest.New(cfg)
	if svc.LinksAvailable() {
		log.Println("Portal share guest links enabled (one-time email links)")
	}
	return svc
}

// optOutStatusFn adapts the notification preference store to the guest
// landing page's opt-out check (#1022). Addresses are canonicalized as the
// prefs writers canonicalize them, so a mixed-case stored recipient still
// finds its row.
func optOutStatusFn(prefs notification.PrefsStore) func(ctx context.Context, email string) (bool, error) {
	return func(ctx context.Context, email string) (bool, error) {
		p, err := prefs.Get(ctx, canonicalEmail(email))
		if err != nil {
			return false, err //nolint:wrapcheck // store error already carries context
		}
		return p.Mode == notification.ModeOff, nil
	}
}

// resubscribeFn adapts the preference store to the guest landing page's
// opt-back-in action (#1022): delivery returns to the immediate-mode default,
// touching nothing but the mode.
func resubscribeFn(prefs notification.PrefsStore) func(ctx context.Context, email string) error {
	return func(ctx context.Context, email string) error {
		mode := notification.ModeImmediate
		_, err := prefs.Set(ctx, canonicalEmail(email), notification.PrefsUpdate{Mode: &mode})
		return err //nolint:wrapcheck // store error already carries context
	}
}

// canonicalEmail matches the lowercase/trim keying the notification package
// applies on every preference write.
func canonicalEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// shareGuestResolver adapts the portal share store to the guest service's
// view of a share. Mode interpretation mirrors shareaccess.Authorize: an
// empty stored mode defaults by the share's shape and is never public.
func shareGuestResolver(store shareTokenReader) shareguest.Resolver {
	return func(ctx context.Context, token string) (shareguest.ShareInfo, bool) {
		share, err := store.GetByToken(ctx, token)
		if err != nil || share == nil {
			return shareguest.ShareInfo{}, false
		}
		return shareguest.ShareInfo{
			ID:             share.ID,
			Token:          share.Token,
			RecipientEmail: share.SharedWithEmail,
			Public:         share.AccessMode == shareaccess.ModePublic,
			Revoked:        share.Revoked,
			Expired:        share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()),
		}, true
	}
}

// unsubscribeURLFn returns the builder for the notification footer's
// no-login unsubscribe link, or nil when the endpoint cannot be served
// (no signing key or no public base URL to absolutize the link with).
func unsubscribeURLFn(p *platform.Platform) func(email string) string {
	master := browserSessionSigningKey(p)
	if master == nil || p.Config().Portal.PublicBaseURL == "" {
		return nil
	}
	key := shareguest.DeriveKey(master, unsubscribeKeyLabel)
	base := p.Config().Portal.PublicBaseURL
	return func(email string) string {
		return base + unsubscribePath + "?tok=" + unsubhttp.UnsubToken(key, email)
	}
}

// mountNotificationUnsubscribe registers the no-login opt-out endpoint when
// the notification substrate and the token key are both available.
func mountNotificationUnsubscribe(mux *http.ServeMux, p *platform.Platform, notify *notifydelivery.Handle) {
	master := browserSessionSigningKey(p)
	if notify == nil || notify.Prefs() == nil || master == nil {
		return
	}
	handler := &unsubhttp.UnsubscribeHandler{
		Prefs:     notify.Prefs(),
		Key:       shareguest.DeriveKey(master, unsubscribeKeyLabel),
		BrandName: portalBrandName(p),
	}
	mux.Handle("GET "+unsubscribePath, handler)
	// RFC 8058 one-click: mail providers POST to the same URL the header
	// names, so the opt-out records without any page interaction.
	mux.Handle("POST "+unsubscribePath, handler)
	log.Println("Notification unsubscribe endpoint enabled on " + unsubscribePath)
}
