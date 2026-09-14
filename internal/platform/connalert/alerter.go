package connalert

import (
	"context"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// writeTimeout bounds the work done on the refresh path. Announcing a
// revocation is two statements, and a refresh that has already reached its
// verdict must not be held open behind a slow database for the sake of telling
// somebody about it.
const writeTimeout = 5 * time.Second

// Structured-logging keys.
const (
	logKeyKind  = "connection_kind"
	logKeyName  = "connection_name"
	logKeyError = "error"
)

// Config carries what both the Alerter and the Escalator need. Every field is
// required; the constructors return nil when one is missing, which is how the
// composition root says "this deployment has no database" without a flag.
type Config struct {
	// Settings holds the operator's escalation window and recipients.
	Settings SettingsStore
	// Alerts holds the open revocations.
	Alerts AlertStore
	// Enqueuer is the notification substrate's trigger-side entry point.
	Enqueuer *notification.Enqueuer
	// BaseURL is the portal's public base URL, for the alert's deep link.
	BaseURL string
	// Interval overrides DefaultSweepInterval. Testing hook.
	Interval time.Duration
	// Now overrides time.Now. Testing hook.
	Now func() time.Time
}

// ready reports whether the config can do any work at all.
func (c Config) ready() bool {
	return c.Settings != nil && c.Alerts != nil && c.Enqueuer != nil
}

// Alerter is the authevents.RevocationSink that records a revocation and tells
// the person who authorized the connection.
type Alerter struct {
	cfg Config
}

// NewAlerter builds the sink, or nil when a dependency is absent. A nil Alerter
// is still a usable sink (its method is nil-safe), so the caller can wire it
// unconditionally.
func NewAlerter(cfg Config) *Alerter {
	if !cfg.ready() {
		return nil
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Alerter{cfg: cfg}
}

// Revoked records the revocation and mails the identity that authorized the
// connection.
//
// It runs on the refresh path, so everything it does is bounded and nothing it
// does can fail the refresh: the credential is already gone either way, and a
// caller that returned an error here would turn "we could not send an email"
// into a second, unrelated failure for the agent that made the call.
func (a *Alerter) Revoked(ctx context.Context, rev authevents.Revocation) {
	if a == nil {
		return
	}
	// The revocation is already recorded, so this write outlives the
	// cancellation that may have raced it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	settings, err := SettingsOf(ctx, a.cfg.Settings)
	if err != nil {
		warn("connection revocation alert: reading settings failed", rev.Kind, rev.Name, err)
		return
	}
	if !settings.Enabled {
		return
	}
	alert := Alert{
		Kind:            rev.Kind,
		Name:            rev.Name,
		AuthorizedBy:    rev.AuthorizedBy,
		IDPHost:         rev.IDPHost,
		Reason:          rev.Reason,
		RevokedAt:       revokedAt(rev, a.cfg.Now),
		SignedAssertion: rev.SignedAssertion,
		Description:     rev.Description,
	}
	if alert.SignedAssertion {
		a.announceRefusedAssertion(ctx, settings, alert)
		return
	}
	// The row is opened even when there is nobody to mail. An unattributed
	// connection is exactly the one whose revocation needs to reach the
	// operator's recipients, and the escalation reads this table.
	opened, err := a.cfg.Alerts.Open(ctx, alert)
	if err != nil {
		warn("connection revocation alert: recording the revocation failed", rev.Kind, rev.Name, err)
		return
	}
	if !opened {
		// The connection already had an open revocation: announced once, not
		// once per rejected call.
		return
	}
	if alert.AuthorizedBy == "" {
		slog.Warn("a connection's credential was discarded and the platform does not know who authorized it",
			logKeyKind, logsan.SanitizeForLog(rev.Kind), logKeyName, logsan.SanitizeForLog(rev.Name))
		return
	}
	a.deliver(ctx, []string{alert.AuthorizedBy}, alert, nil)
}

// announceRefusedAssertion opens the row for a refused jwt_bearer assertion and
// mails the operator's recipients straight away. There is no person who
// authorized the connection to hear first and no escalation later: the
// recipients are the only people the platform can tell, and the escalation
// sweep skips the row.
//
// With no recipients configured the row is not opened. Nothing else reads it
// and it would never be escalated, so an open row would only swallow the
// refusals after an operator names recipients; left unopened, the next refusal
// after that is announced.
func (a *Alerter) announceRefusedAssertion(ctx context.Context, settings Settings, alert Alert) {
	recipients := settings.EscalatesTo()
	if len(recipients) == 0 {
		slog.Warn("a connection's signed assertion was refused and no connection alert recipients are configured",
			logKeyKind, logsan.SanitizeForLog(alert.Kind), logKeyName, logsan.SanitizeForLog(alert.Name))
		return
	}
	opened, err := a.cfg.Alerts.Open(ctx, alert)
	if err != nil {
		warn("connection alert: recording a refused assertion failed", alert.Kind, alert.Name, err)
		return
	}
	if !opened {
		return
	}
	a.deliver(ctx, recipients, alert, nil)
}

// Restored forgets a connection's open alert once a jwt_bearer exchange has
// been accepted. It runs on every accepted exchange, on every replica, so
// clearing a connection with nothing open is the common case and costs one
// statement; a failure is logged and the exchange's caller is unaffected.
func (a *Alerter) Restored(ctx context.Context, kind, name string) {
	if a == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := a.cfg.Alerts.Clear(ctx, kind, name); err != nil {
		warn("connection alert: clearing after an accepted exchange failed", kind, name, err)
	}
}

// escalation describes the second alert: the window that elapsed before the
// operator's recipients were told. A nil *escalation is the first alert, sent to
// the person who authorized the connection.
//
// It is a type rather than a bool-and-int pair so the two can never be wired to
// disagree — an escalation with no window, or a window on a first alert.
type escalation struct {
	// afterHours is the operator's configured window, in hours.
	afterHours int
}

// revokedAt is when the credential was discarded, falling back to the clock
// when the caller left it unset — a queued row with no time renders a worse
// email than one with an approximate time.
func revokedAt(rev authevents.Revocation, now func() time.Time) time.Time {
	if !rev.At.IsZero() {
		return rev.At
	}
	return now().UTC()
}

// deliver enqueues one alert for each recipient. esc is nil on the first alert
// and carries the elapsed window on the escalation, which is what the email
// reads to say why it arrived.
//
// A failure for one address is logged and the rest still go out: the row has
// already been opened or stamped, so a caller that gave up here would silence
// the whole incident over one bad entry.
func (a *Alerter) deliver(ctx context.Context, recipients []string, alert Alert, esc *escalation) {
	payload := notification.Payload{
		Kind:      notification.KindConnectionAuth,
		ItemID:    alert.Kind + "/" + alert.Name,
		ItemTitle: alert.Name,
		Link:      notification.PortalLink(a.cfg.BaseURL, Route),
		Connection: &notification.ConnectionAuth{
			Kind:                alert.Kind,
			Name:                alert.Name,
			IDPHost:             alert.IDPHost,
			Reason:              alert.Reason,
			Description:         alert.Description,
			SignedAssertion:     alert.SignedAssertion,
			AuthorizedBy:        alert.AuthorizedBy,
			RevokedAt:           alert.RevokedAt,
			Escalated:           esc != nil,
			EscalatedAfterHours: escalatedHours(esc),
		},
	}
	queued := 0
	for _, recipient := range recipients {
		// No actor: the platform raised this, not a person. The enqueuer then
		// rate-limits per recipient, which is the right bucket for an alert
		// nobody addressed by hand.
		wrote, err := a.cfg.Enqueuer.Notify(ctx, recipient, notification.CategoryConnectionAuth, payload)
		if err != nil {
			warn("connection revocation alert: queueing failed", alert.Kind, alert.Name, err)
			continue
		}
		if wrote {
			queued++
		}
	}
	// queued is what was actually written, not what was attempted: a recipient
	// who opted out, or whose enqueue failed, must not read as notified in the
	// log an operator checks after a silent alert.
	slog.Info("connection revocation alert enqueued",
		logKeyKind, logsan.SanitizeForLog(alert.Kind),
		logKeyName, logsan.SanitizeForLog(alert.Name),
		"escalated", esc != nil,
		"recipients", len(recipients), "queued", queued)
}

// escalatedHours reads the window off an escalation, or zero when this is the
// first alert.
func escalatedHours(esc *escalation) int {
	if esc == nil {
		return 0
	}
	return esc.afterHours
}

// warn logs a failure that must not propagate, with the connection it was about.
func warn(msg, kind, name string, err error) {
	slog.Warn(msg, // #nosec G706 -- structured slog call; every value sanitized
		logKeyKind, logsan.SanitizeForLog(kind),
		logKeyName, logsan.SanitizeForLog(name),
		logKeyError, logsan.SanitizeForLog(err.Error()))
}

// Verify interface compliance.
var _ authevents.RevocationSink = (*Alerter)(nil)
