package connalert

import (
	"fmt"
	"net/mail"
	"time"

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
const NoRecipientsWarning = "no escalation recipients are configured, so only the person who authorized a connection is told when it is revoked"

// SettingsInput is the write shape for the admin alert configuration.
type SettingsInput struct {
	Enabled            bool     `json:"enabled" example:"true"`
	EscalateAfterHours int      `json:"escalate_after_hours" example:"24"`
	Recipients         []string `json:"recipients" example:"platform-admin@example.com"`
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
	return in.normalizeRecipients()
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
	}
}

// SettingsView is the read shape for the admin alert configuration.
type SettingsView struct {
	Enabled            bool      `json:"enabled" example:"true"`
	EscalateAfterHours int       `json:"escalate_after_hours" example:"24"`
	Recipients         []string  `json:"recipients"`
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
	if !s.Enabled || len(s.EscalatesTo()) > 0 {
		return nil
	}
	return []string{NoRecipientsWarning}
}
