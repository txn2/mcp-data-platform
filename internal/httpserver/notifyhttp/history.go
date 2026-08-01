package notifyhttp

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// HistoryAPI serves a user's own notification history: what the platform has
// sent them, what is queued, and what never arrived.
//
// It is self-scoped the same way PrefsAPI is, and by the same mechanism: the
// authenticated caller's address is the only recipient it ever queries. There
// is no recipient parameter to omit or forge -- a caller cannot express a
// request for someone else's rows. The cross-user view is a separate,
// admin-gated surface (internal/admin/notifyapi).
type HistoryAPI struct {
	// Store reads the delivery history.
	Store notification.HistoryStore
	// UserEmail resolves the authenticated user's email from the request,
	// returning "" when unauthenticated.
	UserEmail func(*http.Request) string
	// Retention is how long a resolved row survives the worker's purge,
	// reported so the screen can say it shows recent activity rather than a
	// complete record. Zero omits the claim.
	Retention time.Duration
}

// HistoryItem is one notification as its recipient sees it.
//
// It deliberately omits the delivery error text the admin view carries: a
// failed send fails for reasons that belong to the platform's mail
// infrastructure (host names, credentials, relay refusals), and the recipient
// can act on none of them. Status alone tells them whether to expect an email.
type HistoryItem struct {
	ID        int64      `json:"id"`
	Category  string     `json:"category"`
	Subject   string     `json:"subject"`
	ItemTitle string     `json:"item_title,omitempty"`
	Actor     string     `json:"actor,omitempty"`
	Link      string     `json:"link,omitempty"`
	Digest    bool       `json:"digest"`
	Status    string     `json:"status"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// HistoryResponse is one page of the caller's own notifications.
type HistoryResponse struct {
	Data    []HistoryItem `json:"data"`
	Total   int           `json:"total"`
	Page    int           `json:"page"`
	PerPage int           `json:"per_page"`
	// RetentionDays is the window this history covers. Zero means the
	// deployment did not report one.
	RetentionDays int `json:"retention_days"`
}

// Register mounts the history endpoint on mux.
func (a *HistoryAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/notifications", a.listMine)
}

// listMine handles GET /api/v1/portal/notifications.
//
// @Summary      List my notifications
// @Description  Returns the calling user's own notification history, newest first: category, subject, delivery status, and send time. Server-side self-scoped -- the caller's address is the only recipient queried, and there is no parameter to widen it. Bounded by the queue's retention window.
// @Tags         Notifications
// @Produce      json
// @Param        status    query  string   false  "Filter by status (pending, sending, sent, failed)"
// @Param        category  query  string   false  "Filter by category (share, comment, mention)"
// @Param        page      query  integer  false  "Page number, 1-based (default: 1)"
// @Param        per_page  query  integer  false  "Results per page (default: 50, max: 200)"
// @Success      200  {object}  HistoryResponse
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/notifications [get]
func (a *HistoryAPI) listMine(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	filter := historyFilter(r, email)

	rows, err := a.Store.List(r.Context(), filter)
	if err != nil {
		writePrefsError(w, http.StatusInternalServerError, "reading notification history failed")
		return
	}
	countFilter := filter
	countFilter.Limit, countFilter.Offset = 0, 0
	total, err := a.Store.Count(r.Context(), countFilter)
	if err != nil {
		writePrefsError(w, http.StatusInternalServerError, "reading notification history failed")
		return
	}

	limit := filter.EffectiveLimit()
	data := make([]HistoryItem, 0, len(rows))
	for _, n := range rows {
		data = append(data, historyItem(n))
	}
	writePrefsJSON(w, HistoryResponse{
		Data:          data,
		Total:         total,
		Page:          filter.Offset/limit + 1,
		PerPage:       limit,
		RetentionDays: int(a.Retention / (24 * time.Hour)),
	})
}

// historyFilter builds the history filter for this request. The recipient is
// always the caller: it is set last so no query parameter can displace it.
func historyFilter(r *http.Request, email string) notification.HistoryFilter {
	q := r.URL.Query()
	filter := notification.HistoryFilter{
		Status:   q.Get("status"),
		Category: q.Get("category"),
		Limit:    httpjson.ParseLimit(q),
	}
	filter.Offset = httpjson.ParsePageOffset(q, filter.EffectiveLimit())
	filter.Recipient = email
	return filter
}

// callerEmail resolves the authenticated caller, writing a 401 when absent.
func (a *HistoryAPI) callerEmail(w http.ResponseWriter, r *http.Request) string {
	email := ""
	if a.UserEmail != nil {
		// The queue keys rows by the normalized address, so a caller whose
		// identity carries a display name still reads their own history.
		email = notification.NormalizeAddress(a.UserEmail(r))
	}
	if email == "" {
		writePrefsError(w, http.StatusUnauthorized, "authentication required")
	}
	return email
}

// historyItem projects a queue row onto the recipient-facing shape.
func historyItem(n notification.Notification) HistoryItem {
	return HistoryItem{
		ID:        n.ID,
		Category:  n.Category,
		Subject:   notifyrender.Subject(n),
		ItemTitle: n.Payload.ItemTitle,
		Actor:     n.Payload.Actor,
		Link:      n.Payload.Link,
		Digest:    n.Digest,
		Status:    n.Status,
		SentAt:    n.SentAt,
		CreatedAt: n.CreatedAt,
	}
}
