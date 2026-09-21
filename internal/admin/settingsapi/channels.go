package settingsapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// channelsPath is the collection route; one channel is addressed under it by
// name, through the channelNameParam path value.
const (
	channelsPath     = "/api/v1/admin/notification-channels"
	channelNameParam = "name"
)

// channelTestFailureTitle prefixes what a failed test send reports. Unlike
// the SMTP test, whose failure is deliberately opaque, a channel test returns
// the transport's own words: the administrator is configuring a destination
// they chose, the upstream's refusal ("not_in_channel", "channel not found")
// is the only thing that tells them what to fix, and it names nothing the
// platform holds in confidence.
const channelTestFailureTitle = "the channel's upstream refused the test message: "

// registerChannels mounts the notification-channel routes. Reads need only
// the store; writes need database config mode, as every other admin
// configuration surface does.
func registerChannels(mux *http.ServeMux, h *handler) {
	if h.cfg.Channels == nil {
		return
	}
	mux.HandleFunc("GET "+channelsPath, h.listChannels)
	mux.HandleFunc("GET "+channelsPath+"/{name}", h.getChannel)
	if !h.cfg.Mutable {
		mux.Handle("PUT "+channelsPath+"/{name}", h.cfg.ReadOnly)
		mux.Handle("DELETE "+channelsPath+"/{name}", h.cfg.ReadOnly)
		mux.Handle("POST "+channelsPath+"/{name}/test", h.cfg.ReadOnly)
		return
	}
	mux.HandleFunc("PUT "+channelsPath+"/{name}", h.setChannel)
	mux.HandleFunc("DELETE "+channelsPath+"/{name}", h.deleteChannel)
	if h.cfg.SendChannelTest != nil {
		mux.HandleFunc("POST "+channelsPath+"/{name}/test", h.testChannel)
	}
}

// ChannelInput is the write shape for a notification channel. The name is the
// path, not a field, so a body cannot rename the channel it is addressed to.
type ChannelInput struct {
	Kind        string   `json:"kind" example:"mattermost"`
	Description string   `json:"description,omitempty" example:"Operations alerts"`
	Enabled     *bool    `json:"enabled,omitempty" example:"true"`
	Connection  string   `json:"connection,omitempty" example:"mattermost-bot"`
	Target      string   `json:"target,omitempty" example:"C0123456789"`
	Recipients  []string `json:"recipients,omitempty"`
	Mode        string   `json:"mode,omitempty" example:"immediate"`
	// RepeatAfter is a duration string ("1h", "30m"). Empty applies the
	// platform default.
	RepeatAfter string `json:"repeat_after,omitempty" example:"1h"`
	MaxPerHour  int    `json:"max_per_hour,omitempty" example:"60"`
}

// ChannelView is the read shape. It carries no credential because a channel
// holds none: what authorizes a post is the named connection's, and this
// surface names the connection alone.
type ChannelView struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
	Connection  string   `json:"connection,omitempty"`
	Target      string   `json:"target,omitempty"`
	Recipients  []string `json:"recipients,omitempty"`
	Mode        string   `json:"mode"`
	RepeatAfter string   `json:"repeat_after"`
	MaxPerHour  int      `json:"max_per_hour"`
	CreatedBy   string   `json:"created_by,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	// Warnings report a channel that saves cleanly but cannot deliver -- a
	// connection no live toolkit serves. They are warnings rather than
	// refusals so an operator can create the channel and the connection in
	// either order.
	Warnings []string `json:"warnings,omitempty"`
}

// ChannelListView is the collection response.
type ChannelListView struct {
	Channels []ChannelView `json:"channels"`
	// Kinds names the kinds this deployment can deliver to, so the admin form
	// offers what will work rather than what compiles.
	Kinds []string `json:"kinds"`
}

// listChannels handles GET /api/v1/admin/notification-channels.
//
// @Summary      List notification channels
// @Description  Returns every configured notification channel. A channel holds no credential; the three HTTP kinds name the api connection whose credential delivers for them.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  ChannelListView
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notification-channels [get]
func (h *handler) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.cfg.Channels.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading notification channels failed")
		return
	}
	out := ChannelListView{
		Channels: make([]ChannelView, 0, len(channels)),
		Kinds: []string{
			notification.ChannelKindMattermost,
			notification.ChannelKindWebhook,
			notification.ChannelKindEmail,
		},
	}
	for _, ch := range channels {
		out.Channels = append(out.Channels, h.channelView(r.Context(), ch))
	}
	writeJSON(w, http.StatusOK, out)
}

// getChannel handles GET /api/v1/admin/notification-channels/{name}.
//
// @Summary      Get a notification channel
// @Description  Returns one notification channel by name.
// @Tags         Settings
// @Produce      json
// @Param        name  path  string  true  "Channel name"
// @Success      200  {object}  ChannelView
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notification-channels/{name} [get]
func (h *handler) getChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := h.loadChannel(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.channelView(r.Context(), *ch))
}

// setChannel handles PUT /api/v1/admin/notification-channels/{name}.
//
// @Summary      Create or update a notification channel
// @Description  Upserts a notification channel. The three HTTP kinds require an api connection and refuse a recipient list; the email kind requires recipients and refuses a connection.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        name     path  string        true  "Channel name"
// @Param        request  body  ChannelInput  true  "Channel"
// @Success      200  {object}  ChannelView
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notification-channels/{name} [put]
func (h *handler) setChannel(w http.ResponseWriter, r *http.Request) {
	var in ChannelInput
	if err := h.cfg.Decode(w, r, &in); err != nil {
		return
	}
	ch, err := channelFrom(r.PathValue(channelNameParam), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := notification.ValidateChannel(ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The author is recorded only on a create: the administrator who owns a
	// channel is the one who made it, and an edit by a colleague does not
	// transfer that.
	if existing, err := h.cfg.Channels.Get(r.Context(), ch.Name); err == nil {
		ch.CreatedBy = existing.CreatedBy
	} else if !errors.Is(err, notifychannel.ErrChannelNotFound) {
		writeError(w, http.StatusInternalServerError, "reading notification channel failed")
		return
	} else if h.cfg.Author != nil {
		ch.CreatedBy = h.cfg.Author(r)
	}
	if err := h.cfg.Channels.Set(r.Context(), ch); err != nil {
		writeError(w, http.StatusInternalServerError, "writing notification channel failed")
		return
	}
	writeJSON(w, http.StatusOK, h.channelView(r.Context(), ch))
}

// deleteChannel handles DELETE /api/v1/admin/notification-channels/{name}.
//
// @Summary      Delete a notification channel
// @Description  Removes a notification channel. Rows already queued for it fail on their next delivery attempt rather than being sent elsewhere.
// @Tags         Settings
// @Param        name  path  string  true  "Channel name"
// @Success      204  "deleted"
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notification-channels/{name} [delete]
func (h *handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if err := h.cfg.Channels.Delete(r.Context(), r.PathValue(channelNameParam)); err != nil {
		writeError(w, http.StatusInternalServerError, "deleting notification channel failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ChannelTestResult reports what the channel's upstream said.
type ChannelTestResult struct {
	Delivered bool   `json:"delivered"`
	Detail    string `json:"detail"`
}

// testChannel handles POST /api/v1/admin/notification-channels/{name}/test.
//
// @Summary      Send a test message to a notification channel
// @Description  Delivers a test message through the channel's own transport and reports what the upstream answered. The send bypasses the queue so the answer is the transport's, not a confirmation that a row was written.
// @Tags         Settings
// @Produce      json
// @Param        name  path  string  true  "Channel name"
// @Success      200  {object}  ChannelTestResult
// @Failure      404  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/notification-channels/{name}/test [post]
func (h *handler) testChannel(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.loadChannel(w, r); !ok {
		return
	}
	if err := h.cfg.SendChannelTest(r.Context(), r.PathValue(channelNameParam)); err != nil {
		// 503, not 502: a CDN in front of the deployment replaces a 502 body
		// with its own page, and this body is the whole answer -- the
		// upstream's own words about why it refused (#1704).
		writeError(w, http.StatusServiceUnavailable, channelTestFailureTitle+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ChannelTestResult{
		Delivered: true,
		Detail:    "the channel's upstream accepted the test message",
	})
}

// loadChannel reads the addressed channel, answering 404 or 500 itself when
// it cannot.
func (h *handler) loadChannel(w http.ResponseWriter, r *http.Request) (*notification.Channel, bool) {
	ch, err := h.cfg.Channels.Get(r.Context(), r.PathValue(channelNameParam))
	if errors.Is(err, notifychannel.ErrChannelNotFound) {
		writeError(w, http.StatusNotFound, "no notification channel with that name")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading notification channel failed")
		return nil, false
	}
	return ch, true
}

// channelFrom turns the request into a channel record, applying the defaults
// an omitted field means.
func channelFrom(name string, in ChannelInput) (notification.Channel, error) {
	ch := notification.Channel{
		Name:        strings.TrimSpace(name),
		Kind:        strings.TrimSpace(in.Kind),
		Description: strings.TrimSpace(in.Description),
		Enabled:     in.Enabled == nil || *in.Enabled,
		Connection:  strings.TrimSpace(in.Connection),
		Target:      strings.TrimSpace(in.Target),
		Recipients:  trimAll(in.Recipients),
		Mode:        strings.TrimSpace(in.Mode),
		MaxPerHour:  in.MaxPerHour,
	}
	if ch.Mode == "" {
		ch.Mode = notification.ChannelModeImmediate
	}
	if in.RepeatAfter != "" {
		d, err := time.ParseDuration(in.RepeatAfter)
		if err != nil {
			return ch, errors.New("repeat_after must be a duration such as \"1h\" or \"30m\"")
		}
		ch.RepeatAfter = d
	}
	return ch, nil
}

// trimAll trims each entry and drops the empty ones, so a recipient list
// edited in a textarea does not save a blank line as an address.
func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// channelView renders a channel for the API, including the warnings that say
// it cannot deliver as configured.
func (h *handler) channelView(ctx context.Context, ch notification.Channel) ChannelView {
	view := ChannelView{
		Name:        ch.Name,
		Kind:        ch.Kind,
		Description: ch.Description,
		Enabled:     ch.Enabled,
		Connection:  ch.Connection,
		Target:      ch.Target,
		Recipients:  ch.Recipients,
		Mode:        ch.Mode,
		RepeatAfter: ch.RepeatWindow().String(),
		MaxPerHour:  ch.HourlyCap(),
		CreatedBy:   ch.CreatedBy,
		Warnings:    h.channelWarnings(ctx, ch),
	}
	if !ch.UpdatedAt.IsZero() {
		view.UpdatedAt = ch.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return view
}

// channelWarnings reports what will stop this channel delivering. A named
// connection no live toolkit serves is the one an operator actually meets:
// they created the channel first, or deleted the connection later, and
// without this the channel looks configured and silently fails at send time.
func (h *handler) channelWarnings(ctx context.Context, ch notification.Channel) []string {
	if !notification.ChannelNeedsConnection(ch.Kind) || ch.Connection == "" || h.cfg.ConnectionExists == nil {
		return nil
	}
	if h.cfg.ConnectionExists(ctx, ch.Connection) {
		return nil
	}
	return []string{"no api connection named \"" + ch.Connection +
		"\" is served here, so nothing sent to this channel can be delivered"}
}
