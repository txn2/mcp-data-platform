package httpserver

import (
	"log"
	"net/http"
	"net/mail"

	"github.com/txn2/mcp-data-platform/internal/platform/branding"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// buildNotifications assembles the email-notification substrate from the
// platform's database, encryptor, and branding. Returns nil when the
// feature is unavailable (no platform, no database) or disabled by config;
// every consumer of the handle is nil-safe.
func buildNotifications(p *platform.Platform) *notifydelivery.Handle {
	if p == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	handle, err := notifydelivery.New(notifydelivery.Config{
		DB:        p.DB(),
		DSN:       p.Config().Database.DSN,
		Encryptor: p.RestEncryptor(),
		Branding: notification.Branding{
			Name:            portalBrandName(p),
			BaseURL:         p.Config().Portal.PublicBaseURL,
			ImplementorName: p.Config().Portal.Implementor.Name,
			ImplementorURL:  p.Config().Portal.Implementor.URL,
			TermsURL:        p.Config().Portal.TermsURL,
			PrivacyURL:      p.Config().Portal.PrivacyURL,
			AboutText:       p.Config().Portal.AboutText,
			SupportContact:  p.Config().Portal.SupportContact,
			ReplyTo:         emailReplyTo(p.Config().Portal.ReplyTo),
			LogoPNG:         emailLogo(p.Config().Portal.LogoEmail),
		},
		DigestHourUTC:  p.Config().Notifications.DigestHour(),
		UnsubscribeURL: unsubscribeURLFn(p),
	})
	if err != nil {
		// A renderer build failure means broken embedded templates — a build
		// defect, not an operator error. Degrade to no notifications.
		log.Println("WARNING: email notifications unavailable:", err)
		return nil
	}
	if handle != nil {
		log.Println("Email notifications enabled (queue + send worker)")
	}
	return handle
}

// emailReplyTo validates the configured portal.reply_to once at startup. An
// invalid address is dropped with a warning rather than failing every send
// with an opaque per-message error.
func emailReplyTo(addr string) string {
	if addr == "" {
		return ""
	}
	if _, err := mail.ParseAddress(addr); err != nil {
		log.Println("WARNING: portal.reply_to is not a valid email address; leaving Reply-To off:", err)
		return ""
	}
	return addr
}

// emailLogo resolves the raster logo for notification emails once at startup,
// so no send path pays a fetch or races on a shared cache. An unset URL is the
// normal case and a failed fetch is not fatal: both leave the logo empty and
// emails render the text wordmark alone.
func emailLogo(url string) []byte {
	if url == "" {
		return nil
	}
	png, err := branding.FetchEmailLogoPNG(url)
	if err != nil {
		log.Println("WARNING: email logo unavailable, using text wordmark:", err)
		return nil
	}
	return png
}

// wirePortalNotifications attaches the notification substrate to the portal
// dependency set: the share/thread trigger bridge and the self-scoped
// preference routes. A nil handle leaves both unset (feature unavailable).
func wirePortalNotifications(deps *portal.Deps, p *platform.Platform, notify *notifydelivery.Handle) {
	if notify == nil {
		return
	}
	if bridge := notify.PortalNotifier(notifydelivery.PortalStores{
		Assets:         p.PortalAssetStore(),
		Collections:    p.PortalCollectionStore(),
		Prompts:        p.PromptStore(),
		KnowledgePages: p.PortalKnowledgePageStore(),
	}, p.Config().Portal.PublicBaseURL); bridge != nil {
		deps.Notifier = bridge
	}
	prefsAPI := &notification.PrefsAPI{
		Store: notify.Prefs(),
		UserEmail: func(r *http.Request) string {
			if user := portal.GetUser(r.Context()); user != nil {
				return user.Email
			}
			return ""
		},
	}
	deps.NotificationRegistrar = prefsAPI.Register
}
