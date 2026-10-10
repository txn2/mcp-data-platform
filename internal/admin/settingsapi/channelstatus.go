package settingsapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
)

// ChannelStatusView is what one channel has done recently, beside its cap: the
// difference between a quiet channel and a broken one, and between one far
// below its cap and one about to refuse sends.
type ChannelStatusView struct {
	Name string `json:"name" example:"ops-alerts"`
	// LastDeliveredAt is when a message to the channel was last delivered.
	LastDeliveredAt string `json:"last_delivered_at,omitempty" example:"2026-10-10T09:14:03Z"`
	// LastError is the most recent failure still recorded on one of the
	// channel's messages, and LastErrorAt when that message was queued.
	LastError   string `json:"last_error,omitempty" example:"upstream answered HTTP 403: not_in_channel"`
	LastErrorAt string `json:"last_error_at,omitempty" example:"2026-10-10T08:02:11Z"`
	// SentLastHour counts the sends the hourly cap counts, and MaxPerHour
	// is that cap.
	SentLastHour int `json:"sent_last_hour" example:"12"`
	MaxPerHour   int `json:"max_per_hour" example:"60"`
	// Pending counts messages not yet delivered.
	Pending int `json:"pending" example:"0"`
	// Suppressed counts repeats held back by the channel's keys since each
	// key last went through.
	Suppressed int `json:"suppressed" example:"3"`
}

// channelStatus handles GET /api/v1/admin/notification-channels/{name}/status.
//
// @Summary      Get a notification channel's recent activity
// @Description  Returns when the channel last delivered, the latest failure still recorded against it, how many sends it took in the last hour against its hourly cap, how many messages are waiting, and how many repeats its keys are holding back.
// @Tags         Settings
// @Produce      json
// @Param        name  path  string  true  "Channel name"
// @Success      200  {object}  ChannelStatusView
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notification-channels/{name}/status [get]
func (h *handler) channelStatus(w http.ResponseWriter, r *http.Request) {
	ch, ok := h.loadChannel(w, r)
	if !ok {
		return
	}
	act, err := h.cfg.ChannelActivity(r.Context(), ch.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading the channel's activity failed")
		return
	}
	view := ChannelStatusView{
		Name:         ch.Name,
		LastError:    act.LastError,
		SentLastHour: act.SentLastHour,
		MaxPerHour:   ch.HourlyCap(),
		Pending:      act.Pending,
		Suppressed:   act.Suppressed,
	}
	if act.LastDeliveredAt != nil {
		view.LastDeliveredAt = act.LastDeliveredAt.UTC().Format(time.RFC3339)
	}
	if act.LastErrorAt != nil {
		view.LastErrorAt = act.LastErrorAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, view)
}

// channelsExist refuses an alert's channel list naming a channel that does not
// exist, answering 400 itself, so an operator's typo is reported where it was
// made rather than discovered when the alert fires into nothing.
func (h *handler) channelsExist(w http.ResponseWriter, r *http.Request, names []string) bool {
	err := notifyalert.Validate(r.Context(), h.cfg.Channels, names)
	switch {
	case err == nil:
		return true
	case errors.Is(err, notifyalert.ErrUnknownChannel) || len(names) > notifyalert.MaxChannels || h.cfg.Channels == nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "reading notification channels failed")
	}
	return false
}

// alertChannels settles an alert PUT's channel list, answering the failure
// itself: an absent list is the stored one (so a client that predates
// channels does not clear them by saving recipients), and every name must be
// a channel. stored reads the alert's current list.
func (h *handler) alertChannels(w http.ResponseWriter, r *http.Request, submitted *[]string, stored func() ([]string, error)) ([]string, bool) {
	if submitted == nil {
		current, err := stored()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "reading the alert's settings failed")
			return nil, false
		}
		submitted = &current
	}
	if !h.channelsExist(w, r, *submitted) {
		return nil, false
	}
	return *submitted, true
}

// registerAlertSettings mounts one alert's settings routes: GET always, PUT in
// database config mode and the read-only responder otherwise. A route whose
// store is absent is not mounted, since an operator must not be able to
// configure an alert nothing will ever send.
func registerAlertSettings(mux *http.ServeMux, h *handler, path string, wired bool, handlers alertHandlers) {
	if !wired {
		return
	}
	mux.HandleFunc("GET "+path, handlers.get)
	if h.cfg.Mutable {
		mux.HandleFunc("PUT "+path, handlers.set)
		return
	}
	mux.Handle("PUT "+path, h.cfg.ReadOnly)
}

// alertHandlers are one alert's read and write handlers.
type alertHandlers struct {
	get, set http.HandlerFunc
}
