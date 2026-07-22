package admin

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/admin/settingsapi"
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
// first), implemented in the settingsapi subpackage.
func (h *Handler) registerSettingsRoutes() {
	settingsapi.Register(h.mux, settingsapi.Config{
		Settings: h.deps.NotificationSettings,
		SendTest: h.deps.SendTestEmail,
		Prefs:    h.deps.NotificationPrefs,
		Mutable:  h.isMutable(),
		Author:   requestAuthor,
		Decode:   decodeStrict,
		ReadOnly: h.readOnlyMethod(),
	})
}
