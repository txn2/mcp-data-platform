// Package reviewalert pushes an operator alert when the knowledge review
// queue crosses its staleness threshold (#803).
//
// #764 made review debt visible to anyone who looks: bulk_review,
// platform_info, and the portal all report the pending count and its age.
// This package supplies the signal for everyone who does not look. It reads
// the same lightweight rollup on a timer and, when the queue crosses the
// operator's threshold, enqueues a digest through the notification substrate
// (pkg/notification) rather than sending mail of its own.
//
// Three pieces, none of which the substrate already owned:
//
//	Settings   the operator's threshold, cooldown, and recipient list,
//	           stored in the platform_settings section the admin API writes
//	StateStore the last-alert marker, claimed with one conditional UPDATE so
//	           a persistently stale queue alerts once per cooldown and only
//	           one replica's check wins a given window
//	Checker    the timer that joins them
//
// It must not import pkg/platform: the HTTP composition root supplies the
// insight store, the enqueuer, and the portal base URL it holds already.
package reviewalert

import (
	"errors"
	"time"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// SettingsSection is the platform_settings section key for the alert
// configuration.
const SettingsSection = "review_queue_alert"

// ErrNotFound is returned by the settings store when the alert has never been
// configured. Callers apply DefaultSettings instead.
var ErrNotFound = errors.New("reviewalert: settings not found")

// Default threshold values. The age threshold matches the platform's existing
// definition of stale review debt (#764) rather than inventing a second
// number, so the alert fires on exactly the rows the portal already badges.
const (
	// DefaultOldestPendingDays is the default age threshold, in days.
	DefaultOldestPendingDays = knowledgekit.PendingStalenessThresholdDays
	// DefaultCooldownHours is the default minimum gap between two alerts
	// about a queue that stays over threshold.
	DefaultCooldownHours = 24
)

// Settings is the operator's review-queue alert configuration. It is the
// review_queue_alert section of the platform_settings table.
type Settings struct {
	// Enabled turns the scheduled check on. A check with no recipients or no
	// threshold delivers nothing regardless; SettingsView reports both as
	// warnings rather than refusing the save.
	Enabled bool `json:"enabled"`
	// PendingThreshold alerts when the pending count reaches it. Zero
	// disables the count condition.
	PendingThreshold int `json:"pending_threshold"`
	// OldestPendingDays alerts when the oldest pending insight reaches this
	// age in days. Zero disables the age condition.
	OldestPendingDays int `json:"oldest_pending_days"`
	// CooldownHours is the minimum gap between two alerts while the queue
	// stays over threshold. A queue that drops back under and crosses again
	// alerts immediately: the cooldown suppresses repetition, not news.
	CooldownHours int `json:"cooldown_hours"`
	// Recipients are the addresses the alert is delivered to, in
	// notification.NormalizeAddress form.
	Recipients []string `json:"recipients"`
	// UpdatedBy and UpdatedAt describe the last admin write. They live in the
	// platform_settings audit columns, which are authoritative, so they are
	// excluded from the section value rather than written into it twice.
	UpdatedBy string    `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// DefaultSettings returns the configuration applied before an operator has
// written one: on, alerting on the platform's own staleness definition, once
// a day at most, to nobody yet.
func DefaultSettings() Settings {
	return Settings{
		Enabled:           true,
		OldestPendingDays: DefaultOldestPendingDays,
		CooldownHours:     DefaultCooldownHours,
	}
}

// Cooldown returns the configured re-alert gap, falling back to the default
// so a zero (or negative) stored value cannot turn every check into a send.
func (s Settings) Cooldown() time.Duration {
	if s.CooldownHours <= 0 {
		return DefaultCooldownHours * time.Hour
	}
	return time.Duration(s.CooldownHours) * time.Hour
}

// Crossed reports whether a queue of pending insights whose oldest is
// oldestAgeDays old has crossed the configured threshold. Either condition
// alone is enough; a zero threshold disables its condition, and an empty
// queue never crosses.
func (s Settings) Crossed(pending, oldestAgeDays int) bool {
	if pending <= 0 {
		return false
	}
	if s.PendingThreshold > 0 && pending >= s.PendingThreshold {
		return true
	}
	return s.OldestPendingDays > 0 && oldestAgeDays >= s.OldestPendingDays
}

// Deliverable reports whether a crossing could actually reach anyone: the
// check is on, at least one threshold can fire, and someone is listed.
func (s Settings) Deliverable() bool {
	return s.Enabled && len(s.Recipients) > 0 &&
		(s.PendingThreshold > 0 || s.OldestPendingDays > 0)
}
