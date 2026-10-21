// Package alertwire assembles the platform's own alerts from the stores the
// platform holds (#803, #1694, #1724): the review-queue staleness check, the
// connection-revocation alert and its escalation, the scheduled-run failure
// alert's channels, and the admin API's view of all three beside the
// notification settings.
//
// It was split from notifywire, which assembles the delivery substrate the
// alerts send through: the two share the delivery handle and nothing of each
// other's.
package alertwire

import (
	"log/slog"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/connalert"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/notifywiring"
	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptalert"
	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whalert"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// reviewAlertStore builds one review queue's alert persistence (#803, #1287),
// or nil when the alert cannot exist in this deployment: no database, or
// notifications turned off in YAML. The checker and the admin API each build
// one; it is stateless over the pool, so there is nothing to share, and
// threading a handle through the admin wiring would imply a lifecycle it does
// not have.
func reviewAlertStore(p *platform.Platform, target reviewalert.Target) *reviewalert.PostgresStore {
	if p == nil || p.DB() == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	return reviewalert.NewPostgresStore(p.DB(), target)
}

// ReviewAlertSettings narrows one queue's store to the half the admin settings
// surface needs, or nil when the alert cannot exist here. A nil result unmounts
// the admin routes, matching what the SMTP section already does in the same
// states: an operator must not be able to configure an alert nothing will
// ever send. The explicit nil check keeps a typed nil out of the interface.
func ReviewAlertSettings(p *platform.Platform, target reviewalert.Target) reviewalert.SettingsStore {
	store := reviewAlertStore(p, target)
	if store == nil {
		return nil
	}
	return store
}

// BuildReviewAlert assembles the scheduled knowledge review-queue staleness
// check. Returns nil (a no-op checker) when anything it needs is absent: no
// database, notifications off, no knowledge insight store -- an alert with
// nowhere to send is not an alert.
func BuildReviewAlert(p *platform.Platform, notify *notifydelivery.Handle) *reviewalert.Checker {
	target := reviewalert.KnowledgeTarget()
	store := reviewAlertStore(p, target)
	if store == nil {
		return nil
	}
	var source reviewalert.Source
	// A typed nil in the interface would read as a live source and the check
	// would fail on every tick instead of never running.
	if insights := p.KnowledgeInsightStore(); insights != nil {
		source = reviewalert.InsightSource{Insights: insights}
	}
	checker := reviewalert.New(reviewalert.Config{
		Target:   target,
		Settings: store,
		State:    store,
		Source:   source,
		Enqueuer: notify.Enqueuer(),
		Channels: notify.AlertChannels(),
		BaseURL:  p.Config().Portal.PublicBaseURL,
		Interval: reviewAlertInterval(),
	})
	if checker != nil {
		slog.Info("Knowledge review-queue staleness alert enabled")
	}
	return checker
}

// ConnAlertStore builds the connection-revocation alert's persistence (#1694),
// or nil when the alert cannot exist in this deployment: no database, or
// notifications turned off in YAML. Like the review alert's store it is
// stateless over the pool, so the admin API and the sweep each build one rather
// than sharing a handle whose lifecycle it does not have.
func ConnAlertStore(p *platform.Platform) *connalert.PostgresStore {
	if p == nil || p.DB() == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	return connalert.NewPostgresStore(p.DB())
}

// ConnAlertSettings narrows the store to the half the admin settings surface
// needs, or nil when the alert cannot exist here. A nil result unmounts the
// admin routes: an operator must not be able to name recipients for an alert
// nothing will ever send. The explicit nil check keeps a typed nil out of the
// interface.
func ConnAlertSettings(p *platform.Platform) connalert.SettingsStore {
	store := ConnAlertStore(p)
	if store == nil {
		return nil
	}
	return store
}

// buildConnAlertConfig assembles what the revocation alert and its escalation
// both need, or the zero config when anything is absent.
func buildConnAlertConfig(p *platform.Platform, notify *notifydelivery.Handle) connalert.Config {
	store := ConnAlertStore(p)
	if store == nil || notify == nil {
		return connalert.Config{}
	}
	return connalert.Config{
		Settings: store,
		Alerts:   store,
		Enqueuer: notify.Enqueuer(),
		Channels: notify.AlertChannels(),
		BaseURL:  p.Config().Portal.PublicBaseURL,
	}
}

// WireConnRevocationAlert tells the auth-event writer where to announce a
// discarded credential (#1694), and returns the sweep that escalates one nobody
// has acted on. Both are nil when the alert cannot exist here.
//
// The writer is built with the token store, long before the notification
// substrate exists, so the sink is attached here rather than at construction.
// It is the one dependency every connoauth.Source already carries, which is why
// the announcement rides it instead of being threaded a second time through
// every toolkit that wires OAuth.
func WireConnRevocationAlert(p *platform.Platform, notify *notifydelivery.Handle) *connalert.Escalator {
	cfg := buildConnAlertConfig(p, notify)
	alerter := connalert.NewAlerter(cfg)
	if alerter == nil {
		return nil
	}
	p.AuthEventWriter().WithRevocations(alerter)
	slog.Info("Connection revocation alerts enabled")
	return connalert.NewEscalator(cfg)
}

// ScriptRunAlertSettings is the scheduled-run failure alert's channel list
// (#1724), or nil when the alert cannot exist here. The explicit nil check
// keeps a typed nil out of the interface.
func ScriptRunAlertSettings(p *platform.Platform) scriptalert.SettingsStore {
	if p == nil || p.DB() == nil || !p.Config().Notifications.IsEnabled() {
		return nil
	}
	return scriptalert.NewPostgresStore(p.DB())
}

// AdminDeps fills the admin API's notification dependencies: the SMTP
// settings and test send, the delivery history, the three alerts' settings,
// and the notification channels with their test send and activity. Every
// accessor is nil-safe, so a deployment with no database leaves the routes
// that need them unregistered.
func AdminDeps(deps *admin.Deps, p *platform.Platform, notify *notifydelivery.Handle) {
	// Email notification settings and monitoring surfaces (nil-safe: absent
	// without a DB, which also leaves the monitoring routes unregistered).
	deps.NotificationSettings = notify.Settings()
	deps.SendTestEmail = notify.SendTest
	deps.NotificationPrefs = notify.Prefs()
	deps.NotificationHistory = notify.History()
	deps.NotificationRetention = notifydelivery.HistoryRetention
	deps.ReviewQueueAlert = ReviewAlertSettings(p, reviewalert.KnowledgeTarget())
	deps.ConnectionAlert = ConnAlertSettings(p)
	deps.ScriptRunAlert = ScriptRunAlertSettings(p)
	if p.DB() != nil {
		deps.StorageSettings = storagecap.NewSettingsStore(p.DB())
		deps.WebhookAlert = whalert.NewSettingsStore(p.DB())
	}
	// Notification channels: the records, the test send, and the check that
	// tells an operator their channel names a connection nothing serves
	// (#1720).
	if channels := notify.Channels(); channels != nil {
		deps.NotificationChannels = channels
	}
	deps.SendChannelTest = notify.SendChannelTest
	deps.ChannelActivity = notify.ChannelActivity
	deps.APIConnectionExists = notifywiring.APIConnectionExists(p.ToolkitRegistry())
	// The OAuth callback forgets a connection's open revocation as it
	// authorizes it again (#1694). Assigned through the same nil-guarded
	// builder so a typed nil never reaches the interface.
	if store := ConnAlertStore(p); store != nil {
		deps.ConnectionRevocations = store
	}
}

// EnvReviewAlertInterval overrides how often the review queue is checked
// (default hourly), as a Go duration. It is an environment knob beside the
// other MCP_PLATFORM_* cadences rather than YAML: the hourly default is right
// for every deployment, and the knob exists so a local stack can be proved
// against in seconds rather than an hour.
const EnvReviewAlertInterval = "MCP_PLATFORM_REVIEW_ALERT_INTERVAL"

// reviewAlertInterval reads EnvReviewAlertInterval, or zero (the default)
// when it is unset or not a positive duration.
func reviewAlertInterval() time.Duration {
	raw := os.Getenv(EnvReviewAlertInterval)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("ignoring "+EnvReviewAlertInterval+"; it must be a positive duration such as 30s",
			"value", logsan.SanitizeForLog(raw))
		return 0
	}
	return d
}
