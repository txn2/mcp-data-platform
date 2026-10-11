package connalert

import (
	"fmt"
	"net/mail"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Input bounds. They keep a typo from turning the escalation into a mail loop
// or an unreachable window, not to express policy.
const (
	// MaxRecipients caps the escalation's distribution list. It is the list of
	// people accountable for the deployment's connections, not an announcement
	// channel.
	MaxRecipients = 20
	// maxEscalateAfterHours is a sanity bound on the window (30 days).
	maxEscalateAfterHours = 720
)

// NoRecipientsWarning is reported for a configuration that saves cleanly and
// escalates nowhere. It is a warning rather than a validation error because
// that configuration is the default and a reasonable choice: the person who
// authorized the connection is still told, and nobody else is.
const NoRecipientsWarning = "no escalation recipients or channels are configured, so only the person who authorized a connection is told when it is revoked"

// SettingsInput is the write shape for the admin alert configuration.
type SettingsInput struct {
	Enabled            bool     `json:"enabled" example:"true"`
	EscalateAfterHours int      `json:"escalate_after_hours" example:"24"`
	Recipients         []string `json:"recipients" example:"platform-admin@example.com"`
	// Channels names the notification channels a revocation is also posted
	// to. Absent keeps the stored list; an empty list clears it.
	Channels *[]string `json:"channels,omitempty"`
}

// Validate normalizes the input in place and returns a non-empty message when
// it is invalid. Recipients are reduced to their storage form and
// de-duplicated, so the sweep never mails one person twice because their
// address was listed in two shapes.
func (in *SettingsInput) Validate() string {
	if in.EscalateAfterHours == 0 {
		in.EscalateAfterHours = DefaultEscalateAfterHours
	}
	if in.EscalateAfterHours < 1 || in.EscalateAfterHours > maxEscalateAfterHours {
		return fmt.Sprintf("escalate_after_hours must be between 1 and %d", maxEscalateAfterHours)
	}
	if msg := in.normalizeChannels(); msg != "" {
		return msg
	}
	return in.normalizeRecipients()
}

// normalizeChannels trims and de-duplicates a submitted channel list in place.
// Whether each name is a channel is checked by the admin route, which holds
// the channel store.
func (in *SettingsInput) normalizeChannels() string {
	if in.Channels == nil {
		return ""
	}
	names := notifyalert.Normalize(*in.Channels)
	if len(names) > notifyalert.MaxChannels {
		return fmt.Sprintf("at most %d channels may be configured", notifyalert.MaxChannels)
	}
	in.Channels = &names
	return ""
}

// normalizeRecipients validates, normalizes, and de-duplicates the recipient
// list in place.
func (in *SettingsInput) normalizeRecipients() string {
	if len(in.Recipients) > MaxRecipients {
		return fmt.Sprintf("at most %d recipients may be configured", MaxRecipients)
	}
	seen := make(map[string]struct{}, len(in.Recipients))
	out := make([]string, 0, len(in.Recipients))
	for _, raw := range in.Recipients {
		if _, err := mail.ParseAddress(raw); err != nil {
			return fmt.Sprintf("%q is not a valid email address", raw)
		}
		addr := notification.NormalizeAddress(raw)
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	in.Recipients = out
	return ""
}

// Settings maps the validated input to stored settings.
func (in *SettingsInput) Settings() Settings {
	return Settings{
		Enabled:            in.Enabled,
		EscalateAfterHours: in.EscalateAfterHours,
		Recipients:         in.Recipients,
		Channels:           channelsOf(in.Channels),
	}
}

// channelsOf reads the submitted channel list; the caller has already put the
// stored list in place of an absent one.
func channelsOf(in *[]string) []string {
	if in == nil {
		return []string{}
	}
	return *in
}

// SettingsView is the read shape for the admin alert configuration.
type SettingsView struct {
	Enabled            bool      `json:"enabled" example:"true"`
	EscalateAfterHours int       `json:"escalate_after_hours" example:"24"`
	Recipients         []string  `json:"recipients"`
	Channels           []string  `json:"channels"`
	UpdatedBy          string    `json:"updated_by,omitempty" example:"admin@example.com"`
	UpdatedAt          time.Time `json:"updated_at"`
	// Warnings describes a configuration that saves cleanly but escalates
	// nowhere. They never block a save; they exist so the operator sees what
	// the setting does at the surface where it was chosen.
	Warnings []string `json:"warnings,omitempty"`
}

// View maps stored settings to the read shape.
func (s Settings) View() SettingsView {
	return SettingsView{
		Enabled:            s.Enabled,
		EscalateAfterHours: s.EscalateAfterHours,
		Recipients:         s.recipientsForView(),
		Channels:           nonNil(s.Channels),
		UpdatedBy:          s.UpdatedBy,
		UpdatedAt:          s.UpdatedAt,
		Warnings:           s.warnings(),
	}
}

// recipientsForView returns a non-nil slice so the JSON carries [] rather than
// null: the admin UI binds it to a list editor, which must not have to treat
// "never configured" as a distinct state from "emptied".
func (s Settings) recipientsForView() []string {
	if s.Recipients == nil {
		return []string{}
	}
	return s.Recipients
}

// warnings reports an enabled configuration that escalates nowhere.
func (s Settings) warnings() []string {
	if !s.Enabled || len(s.EscalatesTo()) > 0 || len(s.Channels) > 0 {
		return nil
	}
	return []string{NoRecipientsWarning}
}

// nonNil returns a non-nil slice so the JSON carries [] rather than null.
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
