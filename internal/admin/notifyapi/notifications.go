package notifyapi

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// notificationRow is one queue row as the admin tab shows it: the delivery
// record plus the one-line summary the recipient's email carries, so a row
// can be matched against a reported message without opening the payload.
type notificationRow struct {
	ID        int64      `json:"id" example:"4211"`
	Recipient string     `json:"recipient" example:"bob@example.com"`
	Category  string     `json:"category" example:"share"`
	Subject   string     `json:"subject" example:"alice@example.com shared the asset \"Q3 Revenue\" with you"`
	Digest    bool       `json:"digest" example:"false"`
	Status    string     `json:"status" example:"failed"`
	Attempts  int        `json:"attempts" example:"5"`
	LastError string     `json:"last_error,omitempty" example:"dial tcp: connection refused"`
	ItemTitle string     `json:"item_title,omitempty" example:"Q3 Revenue"`
	Actor     string     `json:"actor,omitempty" example:"alice@example.com"`
	Link      string     `json:"link,omitempty" example:"https://platform.example.com/portal/assets/a1"`
	Scheduled time.Time  `json:"scheduled_for"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// notificationListResponse wraps a paginated list of notification rows.
type notificationListResponse struct {
	Data    []notificationRow `json:"data"`
	Total   int               `json:"total" example:"196"`
	Page    int               `json:"page" example:"1"`
	PerPage int               `json:"per_page" example:"50"`
}

// notificationStatsResponse holds the per-status counts and the retention
// window they cover.
type notificationStatsResponse struct {
	Pending int `json:"pending" example:"3"`
	Sending int `json:"sending" example:"1"`
	Sent    int `json:"sent" example:"842"`
	Failed  int `json:"failed" example:"7"`
	Total   int `json:"total" example:"853"`
	// RetentionDays is how long a resolved row survives before the worker
	// purges it. Zero means the deployment did not report a window.
	RetentionDays int `json:"retention_days" example:"30"`
}

// toRow projects a queue row onto the admin view model.
func toRow(n notification.Notification) notificationRow {
	return notificationRow{
		ID:        n.ID,
		Recipient: n.Recipient,
		Category:  n.Category,
		Subject:   notifyrender.Subject(n),
		Digest:    n.Digest,
		Status:    n.Status,
		Attempts:  n.Attempts,
		LastError: n.LastError,
		ItemTitle: n.Payload.ItemTitle,
		Actor:     n.Payload.Actor,
		Link:      n.Payload.Link,
		Scheduled: n.ScheduledFor,
		SentAt:    n.SentAt,
		CreatedAt: n.CreatedAt,
	}
}

// parseFilter reads the shared query vocabulary into a history filter.
// Unknown status or category values are passed through and simply match
// nothing, which keeps the endpoint's failure mode an empty page rather than
// a 400 the UI has to model.
func parseFilter(r *http.Request) notification.HistoryFilter {
	q := r.URL.Query()
	filter := notification.HistoryFilter{
		Recipient: notification.NormalizeAddress(q.Get("recipient")),
		Status:    q.Get("status"),
		Category:  q.Get("category"),
		Limit:     httpjson.ParseLimit(q),
	}
	filter.Offset = httpjson.ParsePageOffset(q, filter.EffectiveLimit())
	return filter
}

// listNotifications handles GET /api/v1/admin/notifications.
//
// @Summary      List notification deliveries
// @Description  Returns paginated notification queue rows, newest first, with delivery status, attempt count, and failure detail. Bounded by the queue's retention window rather than being a full archive.
// @Tags         Notifications
// @Produce      json
// @Param        recipient  query  string   false  "Filter by recipient email"
// @Param        status     query  string   false  "Filter by status (pending, sending, sent, failed)"
// @Param        category   query  string   false  "Filter by category (share, comment, mention)"
// @Param        page       query  integer  false  "Page number, 1-based (default: 1)"
// @Param        per_page   query  integer  false  "Results per page (default: 50, max: 200)"
// @Success      200  {object}  notificationListResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notifications [get]
func (h *handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	filter := parseFilter(r)

	rows, err := h.cfg.History.List(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to list notifications")
		return
	}
	countFilter := filter
	countFilter.Limit, countFilter.Offset = 0, 0
	total, err := h.cfg.History.Count(r.Context(), countFilter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count notifications")
		return
	}

	limit := filter.EffectiveLimit()
	data := make([]notificationRow, 0, len(rows))
	for _, n := range rows {
		data = append(data, toRow(n))
	}
	httpjson.WriteJSON(w, http.StatusOK, notificationListResponse{
		Data:    data,
		Total:   total,
		Page:    filter.Offset/limit + 1,
		PerPage: limit,
	})
}

// notificationStats handles GET /api/v1/admin/notifications/stats.
//
// @Summary      Get notification delivery stats
// @Description  Returns per-status notification counts and the retention window they cover. The recipient and category filters apply; status does not, so a status-filtered list still shows the full breakdown.
// @Tags         Notifications
// @Produce      json
// @Param        recipient  query  string  false  "Filter by recipient email"
// @Param        category   query  string  false  "Filter by category (share, comment, mention)"
// @Success      200  {object}  notificationStatsResponse
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notifications/stats [get]
func (h *handler) notificationStats(w http.ResponseWriter, r *http.Request) {
	// The breakdown is the point of this endpoint, so it never inherits the
	// caller's status filter: a page narrowed to failures still shows how
	// many rows sent.
	filter := parseFilter(r)
	filter.Status, filter.Limit, filter.Offset = "", 0, 0

	counts, err := h.cfg.History.CountsByStatus(r.Context(), filter)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count notifications")
		return
	}

	resp := notificationStatsResponse{
		Pending:       counts[notification.StatusPending],
		Sending:       counts[notification.StatusSending],
		Sent:          counts[notification.StatusSent],
		Failed:        counts[notification.StatusFailed],
		RetentionDays: int(h.cfg.Retention / (24 * time.Hour)),
	}
	resp.Total = resp.Pending + resp.Sending + resp.Sent + resp.Failed
	httpjson.WriteJSON(w, http.StatusOK, resp)
}
