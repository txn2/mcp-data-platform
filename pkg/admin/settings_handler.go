package admin

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/settingsapi"
)

// requestAuthor resolves the acting admin for audit columns: email, else
// user ID, else empty.
func requestAuthor(r *http.Request) string {
	user := GetUser(r.Context())
	if user == nil {
		return ""
	}
	if user.Email != "" {
		return user.Email
	}
	return user.UserID
}

// registerSettingsRoutes mounts the platform settings surface (#631; SMTP
// first, then the review-queue alert threshold in #803, the
// connection-revocation alert in #1694, and the notification channels in
// #1720), implemented in the settingsapi subpackage.
func (h *Handler) registerSettingsRoutes() {
	settingsapi.Register(h.mux, settingsapi.Config{
		Settings:         h.deps.NotificationSettings,
		SendTest:         h.deps.SendTestEmail,
		Prefs:            h.deps.NotificationPrefs,
		ReviewAlert:      h.deps.ReviewQueueAlert,
		ConnectionAlert:  h.deps.ConnectionAlert,
		Channels:         h.deps.NotificationChannels,
		SendChannelTest:  h.deps.SendChannelTest,
		ConnectionExists: h.deps.APIConnectionExists,
		Mutable:          h.isMutable(),
		Author:           requestAuthor,
		Decode:           decodeStrict,
		ReadOnly:         h.readOnlyMethod(),
	})
}
