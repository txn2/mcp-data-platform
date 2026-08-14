// Package reviewalert pushes an operator alert when a review queue crosses its
// staleness threshold (#803, extended to managed scripts by #1287).
//
// #764 made knowledge review debt visible to anyone who looks: bulk_review,
// platform_info, and the portal all report the pending count and its age. This
// package supplies the signal for everyone who does not look. It reads a
// lightweight rollup on a timer and, when the queue crosses the operator's
// threshold, enqueues a digest through the notification substrate
// (pkg/notification) rather than sending mail of its own.
//
// Two queues are watched by one mechanism rather than by two copies of it: the
// knowledge insight queue and the managed-script review queue. What differs
// between them is named by a Target (which settings section holds the
// configuration, which state row records the last alert, what the email says
// and where it points) and read by a Source (what is pending, and how old the
// oldest is). Everything else — the threshold model, the recipient list, the
// cooldown, and the single-winner claim — is one implementation.
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
// queue sources, the enqueuer, and the portal base URL it holds already.
package reviewalert

import (
	"errors"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// ErrNotFound is returned by the settings store when the alert has never been
// configured. Callers apply the target's defaults instead.
var ErrNotFound = errors.New("reviewalert: settings not found")

// Default threshold values.
const (
	// DefaultCooldownHours is the default minimum gap between two alerts
	// about a queue that stays over threshold.
	DefaultCooldownHours = 24
	// DefaultKnowledgeOldestDays is the knowledge queue's default age
	// threshold, in days. It matches the platform's existing definition of
	// stale review debt (#764) rather than inventing a second number, so the
	// alert fires on exactly the rows the portal already badges.
	DefaultKnowledgeOldestDays = knowledgekit.PendingStalenessThresholdDays
	// DefaultScriptOldestDays is the script queue's default age threshold, in
	// days. It is far shorter than the knowledge queue's because the two queues
	// hold different debts: an unreviewed insight is knowledge nobody can use
	// yet, while an unreviewed script version is either automation that is not
	// running or a correction to automation that is still running the old code.
	DefaultScriptOldestDays = 7
)

// Portal deep links the alerts point at.
const (
	knowledgeQueueRoute = "/knowledge#review"
	scriptQueueRoute    = "/admin/scripts"
)

// Target names one review queue: where its configuration and its last-alert
// marker live, and what the email it raises says.
//
// It is a value rather than an interface because none of it is behavior. The
// behavior that does differ between queues — what counts as pending — is the
// Source.
type Target struct {
	// Queue is the stable key of this queue: the primary key of its state row
	// and the identifier in logs. It is never derived from a display string,
	// because renaming a queue in the UI must not orphan its cooldown.
	Queue string
	// SettingsSection is the platform_settings section holding the operator's
	// configuration for this queue.
	SettingsSection string
	// Category and Kind are the notification category the alert is enqueued
	// under and the payload kind its renderer dispatches on.
	Category string
	Kind     string
	// Title labels the queue in the email.
	Title string
	// Route is the portal path the alert links to, relative to the portal base
	// URL.
	Route string
	// DefaultOldestDays is the age threshold applied before an operator has
	// configured one.
	DefaultOldestDays int
}

// KnowledgeTarget describes the knowledge insight review queue (#803).
//
// Its settings section keeps the name it was written under: the section is a
// stored key, and renaming it would silently strand every deployment's
// configured recipients behind a section nothing reads.
func KnowledgeTarget() Target {
	return Target{
		Queue:             "knowledge_review",
		SettingsSection:   "review_queue_alert",
		Category:          notification.CategoryReviewQueue,
		Kind:              notification.KindReviewQueue,
		Title:             "Knowledge review queue",
		Route:             knowledgeQueueRoute,
		DefaultOldestDays: DefaultKnowledgeOldestDays,
	}
}

// ScriptTarget describes the managed-script review queue (#1287).
func ScriptTarget() Target {
	return Target{
		Queue:             "script_review",
		SettingsSection:   "script_review_alert",
		Category:          notification.CategoryScriptReview,
		Kind:              notification.KindScriptReview,
		Title:             "Script review queue",
		Route:             scriptQueueRoute,
		DefaultOldestDays: DefaultScriptOldestDays,
	}
}

// Settings is the operator's alert configuration for one queue. It is that
// queue's section of the platform_settings table.
type Settings struct {
	// Enabled turns the scheduled check on. A check with no recipients or no
	// threshold delivers nothing regardless; SettingsView reports both as
	// warnings rather than refusing the save.
	Enabled bool `json:"enabled"`
	// PendingThreshold alerts when the pending count reaches it. Zero
	// disables the count condition.
	PendingThreshold int `json:"pending_threshold"`
	// OldestPendingDays alerts when the oldest pending item reaches this age
	// in days. Zero disables the age condition.
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
// written one for this queue: on, alerting on the queue's own age threshold,
// once a day at most, to nobody yet.
func (t Target) DefaultSettings() Settings {
	return Settings{
		Enabled:           true,
		OldestPendingDays: t.DefaultOldestDays,
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

// Crossed reports whether a queue of pending items whose oldest is
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
