package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/notifyapi"
)

// NotificationHistory reads the notification delivery history the admin
// monitoring surface lists. Aliased to the domain contract rather than
// restated so the two cannot drift.
type NotificationHistory = notifyapi.HistoryStore

// registerNotificationRoutes mounts the notification monitoring surface,
// implemented in the notifyapi subpackage. A deployment without a database
// has no queue to monitor and no rows to show, so the routes stay off rather
// than serving an empty list that reads as "nothing failed".
func (h *Handler) registerNotificationRoutes() {
	notifyapi.Register(h.mux, notifyapi.Config{
		History:   h.deps.NotificationHistory,
		Retention: h.deps.NotificationRetention,
	})
}
