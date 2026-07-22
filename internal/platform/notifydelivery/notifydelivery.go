// Package notifydelivery assembles the email-notification substrate
// (pkg/notification) into one startable handle: the settings, preference,
// and queue stores, the trigger-side enqueuer, and the send worker with its
// LISTEN/NOTIFY wakeup adapter.
//
// The package must not import pkg/platform. The composition root (the HTTP
// server, which owns the portal and admin surfaces the feature serves)
// passes in the *sql.DB, DSN, encryptor, and branding values it already
// holds, and brackets Start/Stop around its serve loop.
package notifydelivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// logKeyError is the structured-logging key for an error value.
const logKeyError = "error"

// Config carries everything the substrate needs. No platform types.
type Config struct {
	// DB is the shared platform database pool. Required.
	DB *sql.DB
	// DSN is the raw database DSN for the LISTEN connection. Empty disables
	// LISTEN/NOTIFY; the worker degrades to poll-only.
	DSN string
	// Encryptor protects the SMTP password at rest. Required (may be a
	// nil-wrapping passthrough when encryption is disabled).
	Encryptor notification.StringEncryptor
	// Branding is the deployment identity emails render with.
	Branding notification.Branding
	// DigestHourUTC is the UTC hour daily digests are scheduled for.
	DigestHourUTC int
	// UnsubscribeURL builds the no-login unsubscribe link for a recipient
	// address (#1001). nil omits the footer link from notification emails.
	UnsubscribeURL func(email string) string
}

// listenerControl narrows the LISTEN adapter to the two calls the handle
// makes, so the degraded-startup path is testable without a live Postgres.
type listenerControl interface {
	Start(ctx context.Context) error
	Stop()
}

// Handle owns the running substrate.
type Handle struct {
	settings notification.SettingsStore
	prefs    notification.PrefsStore
	queue    notification.QueueStore
	enqueuer *notification.Enqueuer
	renderer *notification.Renderer
	sender   notification.Sender
	worker   *notification.Worker
	listener listenerControl
}

// New composes the substrate. Returns nil when cfg.DB is nil (no database:
// the feature is unavailable and every accessor degrades gracefully).
func New(cfg Config) (*Handle, error) {
	if cfg.DB == nil {
		return nil, nil //nolint:nilnil // nil handle = feature unavailable
	}
	renderer, err := notification.NewRenderer(cfg.Branding)
	if err != nil {
		return nil, fmt.Errorf("building notification renderer: %w", err)
	}
	if cfg.UnsubscribeURL != nil {
		renderer.SetUnsubscribeURLFn(cfg.UnsubscribeURL)
	}
	h := &Handle{
		settings: notification.NewPostgresSettingsStore(cfg.DB, cfg.Encryptor),
		prefs:    notification.NewPostgresPrefsStore(cfg.DB),
		queue:    notification.NewPostgresQueueStore(cfg.DB),
		renderer: renderer,
		sender:   notification.NewSMTPSender(),
	}
	h.enqueuer = notification.NewEnqueuer(h.prefs, h.queue, cfg.DigestHourUTC)
	h.worker = notification.NewWorker(notification.WorkerConfig{
		Queue:    h.queue,
		Settings: h.settings,
		Renderer: renderer,
		Sender:   h.sender,
	})
	if cfg.DSN != "" {
		h.listener = notification.NewListener(cfg.DSN, h.worker)
	}
	return h, nil
}

// Start launches the send worker and, when configured, the LISTEN adapter.
// A LISTEN failure (e.g. missing privilege) degrades to poll-only delivery
// rather than failing startup. Nil-safe.
func (h *Handle) Start(ctx context.Context) {
	if h == nil {
		return
	}
	h.worker.Start(ctx)
	if h.listener == nil {
		return
	}
	if err := h.listener.Start(ctx); err != nil {
		slog.Warn("notification: LISTEN unavailable; falling back to polling", logKeyError, err)
		h.listener = nil
	}
}

// Stop shuts down the listener, worker, and enqueuer limiter, waiting for
// in-flight sends. Nil-safe and idempotent.
func (h *Handle) Stop() {
	if h == nil {
		return
	}
	if h.listener != nil {
		h.listener.Stop()
	}
	h.worker.Stop()
	h.enqueuer.Close()
}

// Enqueuer returns the trigger-side entry point. Nil-safe: a nil handle
// returns a nil enqueuer, whose Notify drops everything.
func (h *Handle) Enqueuer() *notification.Enqueuer {
	if h == nil {
		return nil
	}
	return h.enqueuer
}

// Prefs returns the preference store, or nil when the feature is unavailable.
func (h *Handle) Prefs() notification.PrefsStore {
	if h == nil {
		return nil
	}
	return h.prefs
}

// Settings returns the settings store, or nil when the feature is unavailable.
func (h *Handle) Settings() notification.SettingsStore {
	if h == nil {
		return nil
	}
	return h.settings
}

// SendGuestLink delivers a one-time view link email directly through the
// sender (#1001). The send is transactional: the recipient requested it from
// a share landing page, so it bypasses the queue (no digest deferral) and
// the preference gate (an opted-out recipient still gets the link they asked
// for). It uses the same stored SMTP settings and deliverability gate as
// every other send.
func (h *Handle) SendGuestLink(ctx context.Context, to, link string) error {
	if h == nil {
		return errors.New("notifications unavailable: no database configured")
	}
	settings, err := h.smtpSettings(ctx)
	if err != nil {
		return err
	}
	email, err := h.renderer.RenderGuestLink(to, link, settings.Footer())
	if err != nil {
		return fmt.Errorf("rendering guest link email: %w", err)
	}
	if err := h.sender.Send(ctx, *settings, *email); err != nil {
		slog.Error("notification: guest link send failed", logKeyError, err)
		return err //nolint:wrapcheck // sender error already carries context
	}
	return nil
}

// smtpSettings loads the stored SMTP settings, refusing when the feature is
// unconfigured or disabled. Shared by the direct (non-queued) send paths.
func (h *Handle) smtpSettings(ctx context.Context) (*notification.SMTPSettings, error) {
	settings, err := h.settings.GetSMTP(ctx)
	if errors.Is(err, notification.ErrNotFound) {
		return nil, notification.ErrSMTPNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("loading smtp settings: %w", err)
	}
	if !settings.Enabled || settings.Host == "" {
		return nil, notification.ErrSMTPNotConfigured
	}
	return settings, nil
}

// SendTest delivers a test email to the given recipient using the currently
// stored SMTP settings, so an admin can verify the configuration end to end.
func (h *Handle) SendTest(ctx context.Context, to string) error {
	if h == nil {
		return errors.New("notifications unavailable: no database configured")
	}
	// Mirror the worker's deliverability gate: a disabled or hostless
	// configuration refuses the test instead of sending around the switch.
	settings, err := h.smtpSettings(ctx)
	if err != nil {
		return err
	}
	email, err := h.renderer.RenderTest(to, settings.Footer())
	if err != nil {
		return fmt.Errorf("rendering test email: %w", err)
	}
	// The queue worker logs its own delivery failures, so without this the
	// admin test send is the one delivery path that fails silently: its error
	// reaches a single browser in the HTTP response and is then discarded,
	// leaving an operator nothing to investigate. The recipient is a
	// mail.ParseAddress-validated address, so it carries no control bytes.
	if err := h.sender.Send(ctx, *settings, *email); err != nil {
		slog.Error("notification: test send failed", "recipient", to, logKeyError, err)
		return err //nolint:wrapcheck // sender error already carries context
	}
	return nil
}
