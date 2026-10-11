package reviewalert

import (
	"fmt"
	"net/mail"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Input bounds. They exist to keep a typo from turning the alert into a mail
// loop or an unreadable configuration, not to express policy.
const (
	// MaxRecipients caps the alert's distribution list. This is an operator
	// alert about a review queue, not an announcement channel.
	MaxRecipients = 20
	// maxPendingThreshold is a sanity bound on the count threshold.
	maxPendingThreshold = 1_000_000
	// maxOldestPendingDays is a sanity bound on the age threshold (10 years).
	maxOldestPendingDays = 3650
	// maxCooldownHours is a sanity bound on the re-alert gap (30 days).
	maxCooldownHours = 720
)

// Warnings reported by SettingsView for a configuration that saves cleanly but
// delivers nothing. They are warnings rather than validation errors so an
// operator can enable the check and fill in the rest, in either order.
const (
	// NoRecipientsWarning is reported when the check is on with an empty
	// recipient list.
	NoRecipientsWarning = "no recipients or channels are configured, so no alert will be delivered; add an address or a channel"
	// NoThresholdWarning is reported when the check is on with both
	// thresholds cleared.
	NoThresholdWarning = "both thresholds are 0, so nothing can cross; set a pending count, an age in days, or both"
)

// SettingsInput is the write shape for the admin alert configuration.
type SettingsInput struct {
	Enabled           bool     `json:"enabled" example:"true"`
	PendingThreshold  int      `json:"pending_threshold" example:"25"`
	OldestPendingDays int      `json:"oldest_pending_days" example:"30"`
	CooldownHours     int      `json:"cooldown_hours" example:"24"`
	Recipients        []string `json:"recipients" example:"data-admin@example.com"`
	// Channels names the notification channels the alert is also sent to.
	// Absent keeps the stored list, so a client that predates channels does
	// not clear them; an empty list clears them.
	Channels *[]string `json:"channels,omitempty"`
}

// Validate normalizes the input in place and returns a non-empty message when
// it is invalid. Recipients are reduced to their storage form and
// de-duplicated, so the checker never mails one person twice because their
// address was listed in two shapes.
func (in *SettingsInput) Validate() string {
	if in.CooldownHours == 0 {
		in.CooldownHours = DefaultCooldownHours
	}
	if msg := in.validateBounds(); msg != "" {
		return msg
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

// validateBounds checks the numeric fields against their sanity bounds.
func (in *SettingsInput) validateBounds() string {
	if in.PendingThreshold < 0 || in.PendingThreshold > maxPendingThreshold {
		return fmt.Sprintf("pending_threshold must be between 0 and %d", maxPendingThreshold)
	}
	if in.OldestPendingDays < 0 || in.OldestPendingDays > maxOldestPendingDays {
		return fmt.Sprintf("oldest_pending_days must be between 0 and %d", maxOldestPendingDays)
	}
	if in.CooldownHours < 1 || in.CooldownHours > maxCooldownHours {
		return fmt.Sprintf("cooldown_hours must be between 1 and %d", maxCooldownHours)
	}
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
		Enabled:           in.Enabled,
		PendingThreshold:  in.PendingThreshold,
		OldestPendingDays: in.OldestPendingDays,
		CooldownHours:     in.CooldownHours,
		Recipients:        in.Recipients,
		Channels:          channelsOf(in.Channels),
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
	Enabled           bool      `json:"enabled" example:"true"`
	PendingThreshold  int       `json:"pending_threshold" example:"25"`
	OldestPendingDays int       `json:"oldest_pending_days" example:"30"`
	CooldownHours     int       `json:"cooldown_hours" example:"24"`
	Recipients        []string  `json:"recipients"`
	Channels          []string  `json:"channels"`
	UpdatedBy         string    `json:"updated_by,omitempty" example:"admin@example.com"`
	UpdatedAt         time.Time `json:"updated_at"`
	// Warnings describes a configuration that saves cleanly but delivers
	// nothing. They never block a save; they exist so the operator sees the
	// gap at the surface where the setting was chosen.
	Warnings []string `json:"warnings,omitempty"`
}

// View maps stored settings to the read shape.
func (s Settings) View() SettingsView {
	return SettingsView{
		Enabled:           s.Enabled,
		PendingThreshold:  s.PendingThreshold,
		OldestPendingDays: s.OldestPendingDays,
		CooldownHours:     s.CooldownHours,
		Recipients:        s.recipientsForView(),
		Channels:          nonNil(s.Channels),
		UpdatedBy:         s.UpdatedBy,
		UpdatedAt:         s.UpdatedAt,
		Warnings:          s.warnings(),
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

// warnings reports the ways an enabled configuration can still deliver
// nothing.
func (s Settings) warnings() []string {
	if !s.Enabled {
		return nil
	}
	var out []string
	if len(s.Recipients) == 0 && len(s.Channels) == 0 {
		out = append(out, NoRecipientsWarning)
	}
	if s.PendingThreshold <= 0 && s.OldestPendingDays <= 0 {
		out = append(out, NoThresholdWarning)
	}
	return out
}

// nonNil returns a non-nil slice so the JSON carries [] rather than null.
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
