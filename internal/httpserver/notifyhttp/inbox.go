package notifyhttp

import (
	"encoding/json"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// The portal's unread notifications (#2512). A mention or a reply is a
// notification row whether or not a mail server delivers it, so the portal
// can count and list what a person has not read yet: a deployment with no
// SMTP, or mail that a filter swallowed, still tells people.

// UnreadCountResponse is how many of the caller's notifications are unread.
type UnreadCountResponse struct {
	Unread int `json:"unread"`
}

// MarkReadRequest names the rows to mark read; no ids marks every one.
type MarkReadRequest struct {
	IDs []int64 `json:"ids"`
}

// MarkReadResponse is how many rows changed.
type MarkReadResponse struct {
	Marked int64 `json:"marked"`
}

// maxMarkRead bounds one mark-read request.
const maxMarkRead = notification.MaxHistoryLimit

// unreadCount handles GET /api/v1/portal/notifications/unread-count.
//
// @Summary      Count my unread notifications
// @Description  Returns how many of the calling user's own notifications they have not read in the portal: mentions, replies and comments on threads they are part of, and shares. Self-scoped server-side. Works with no mail server configured.
// @Tags         Notifications
// @Produce      json
// @Success      200  {object}  UnreadCountResponse
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/notifications/unread-count [get]
func (a *HistoryAPI) unreadCount(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	n, err := a.Inbox.CountUnread(r.Context(), email)
	if err != nil {
		writePrefsError(w, http.StatusInternalServerError, "reading unread notifications failed")
		return
	}
	writePrefsJSON(w, UnreadCountResponse{Unread: n})
}

// markRead handles POST /api/v1/portal/notifications/read.
//
// @Summary      Mark my notifications read
// @Description  Marks the calling user's own notifications read: the ones named in ids, or every one when ids is empty. An id that is not the caller's changes nothing.
// @Tags         Notifications
// @Accept       json
// @Produce      json
// @Param        body  body  MarkReadRequest  false  "Rows to mark read"
// @Success      200  {object}  MarkReadResponse
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/notifications/read [post]
func (a *HistoryAPI) markRead(w http.ResponseWriter, r *http.Request) {
	email := a.callerEmail(w, r)
	if email == "" {
		return
	}
	var req MarkReadRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMarkReadBody)).Decode(&req); err != nil {
			writePrefsError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if len(req.IDs) > maxMarkRead {
		writePrefsError(w, http.StatusBadRequest, "too many ids")
		return
	}
	n, err := a.Inbox.MarkRead(r.Context(), email, req.IDs)
	if err != nil {
		writePrefsError(w, http.StatusInternalServerError, "marking notifications read failed")
		return
	}
	writePrefsJSON(w, MarkReadResponse{Marked: n})
}

// maxMarkReadBody bounds the request body: maxMarkRead ids and their commas.
const maxMarkReadBody = 64 << 10

// unreadAmong reports which listed rows are unread. A failed read reports
// none: the listing still answers.
func (a *HistoryAPI) unreadAmong(r *http.Request, email string, rows []notification.Notification) map[int64]bool {
	if a.Inbox == nil || len(rows) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(rows))
	for _, n := range rows {
		ids = append(ids, n.ID)
	}
	unread, err := a.Inbox.UnreadAmong(r.Context(), email, ids)
	if err != nil {
		return nil
	}
	return unread
}
