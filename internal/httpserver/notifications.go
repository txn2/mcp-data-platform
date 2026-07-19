package httpserver

import (
	"log"
	"net/http"

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
		},
		DigestHourUTC: p.Config().Notifications.DigestHour(),
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
