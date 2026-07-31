// Package notifyapi serves the /api/v1/admin/notifications surface: the
// delivery history the notification queue leaves behind, and the per-status
// counts that give an admin an at-a-glance read on whether email is working.
//
// It is a decomposition seam of pkg/admin (which sits at its package size
// budget); the parent registers it on the admin mux, so every route here is
// already behind the admin persona gate. Admin is unrestricted by design: the
// rows carry the recipient address, the SMTP error text, and the attempt
// count, because an admin diagnosing a failed delivery needs all three.
package notifyapi

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// HistoryStore reads the notification delivery history. Declared here as the
// seam's dependency (the auditapi pattern) and satisfied by
// notification.HistoryStore.
type HistoryStore = notification.HistoryStore

// Config carries what the routes need. A nil History leaves them unregistered,
// which is the parent's cue to mount its feature-unavailable fallback.
type Config struct {
	// History reads the queue's delivery history.
	History HistoryStore
	// Retention is how long resolved rows survive the worker's purge. It is
	// reported to the UI so the tab can say plainly that it shows recent
	// history rather than an archive. Zero omits the claim rather than
	// stating a window nothing enforces.
	Retention time.Duration
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the notification routes on mux. Every route is read-only.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.History == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/notifications", h.listNotifications)
	mux.HandleFunc("GET /api/v1/admin/notifications/stats", h.notificationStats)
}
