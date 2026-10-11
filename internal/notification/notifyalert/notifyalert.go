// Package notifyalert sends the platform's own alerts -- a review queue past
// its threshold, a connection whose credential was revoked, a scheduled run
// that failed -- to the notification channels an operator named for each,
// beside the email recipients those alerts already reach (#1724).
//
// An alert sent to a channel is authorized as the platform. The operator who
// named the channel in the alert's settings is the authorization, as naming a
// recipient is: no persona is consulted, and the send is charged to no actor.
// Each alert passes its own key, so the channel's repeat window applies to an
// alert that keeps firing.
package notifyalert

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// MaxChannels bounds the channels one alert names, as
// notification.MaxChannelRecipients bounds an email list.
const MaxChannels = 10

// Sender sends an alert to channels. A nil Sender sends nothing, which is how
// a deployment with no channels is wired.
type Sender struct {
	channels notification.ChannelStore
	enqueuer *notification.Enqueuer
}

// New builds a Sender, or nil when either dependency is absent.
func New(channels notification.ChannelStore, enqueuer *notification.Enqueuer) *Sender {
	if channels == nil || enqueuer == nil {
		return nil
	}
	return &Sender{channels: channels, enqueuer: enqueuer}
}

// Send queues the alert's payload for each named channel under key and
// returns how many channels it was queued for. A channel that is gone,
// disabled, over its cap or failing is logged and the rest still go out: one
// bad entry must not silence the alert for every other channel.
func (s *Sender) Send(ctx context.Context, names []string, key string, p notification.Payload) int {
	if s == nil || len(names) == 0 {
		return 0
	}
	doc := notifyrender.AlertDocument(p)
	doc.Key = key
	queued := 0
	for _, name := range names {
		ch, err := s.channels.Get(ctx, name)
		if err != nil {
			warn(ctx, "alert channel unavailable", name, err)
			continue
		}
		res, err := s.enqueuer.NotifyChannel(ctx, notification.ChannelSend{Channel: *ch, Document: doc})
		if err != nil {
			warn(ctx, "queueing an alert for a channel failed", name, err)
			continue
		}
		if res.Queued > 0 {
			queued++
		}
	}
	return queued
}

// warn logs a channel an alert did not reach, with why.
func warn(ctx context.Context, msg, channel string, err error) {
	slog.WarnContext(ctx, msg, // #nosec G706 -- structured slog call; every value sanitized
		"channel", logsan.SanitizeForLog(channel),
		"error", logsan.SanitizeForLog(err.Error()))
}

// Normalize trims a submitted channel list, dropping blanks and repeats, so a
// form's empty last line or a doubled entry is not stored.
func Normalize(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// ErrUnknownChannel is wrapped by Validate for a name no channel is stored
// under.
var ErrUnknownChannel = errors.New("no notification channel has that name")

// Validate refuses a channel list an alert cannot send to: too many entries,
// or a name no channel is stored under. A channel that exists but is disabled
// is accepted, as it is everywhere else an operator names one: enabling it
// later should not mean re-entering it here.
func Validate(ctx context.Context, store notification.ChannelStore, names []string) error {
	if len(names) > MaxChannels {
		return fmt.Errorf("an alert names at most %d channels, not %d", MaxChannels, len(names))
	}
	if len(names) == 0 {
		return nil
	}
	if store == nil {
		return errors.New("this deployment has no notification channels to name")
	}
	for _, name := range names {
		_, err := store.Get(ctx, name)
		if errors.Is(err, notifychannel.ErrChannelNotFound) {
			return fmt.Errorf("channel %q: %w", name, ErrUnknownChannel)
		}
		if err != nil {
			return fmt.Errorf("reading channel %q: %w", name, err)
		}
	}
	return nil
}
