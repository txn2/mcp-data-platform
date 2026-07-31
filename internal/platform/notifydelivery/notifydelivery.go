// Package notifydelivery assembles the email-notification substrate into one
// startable handle: the settings, preference, and queue stores, the
// trigger-side enqueuer, the renderer and SMTP sender, and the send worker
// with its LISTEN/NOTIFY wakeup adapter.
//
// It is the only place that names every layer of the substrate at once. Each
// layer is a package of its own (pkg/notification for the domain,
// pkg/notification/smtp for the mail-server settings, internal/notification/*
// for persistence, rendering, transport and the worker) and knows only the
// layers below it; the composition happens here.
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

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyprefs"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyqueue"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/internal/notification/notifysend"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyworker"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
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
	Encryptor smtp.StringEncryptor
	// Branding is the deployment identity emails render with.
	Branding notifyrender.Branding
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
	settings smtp.SettingsStore
	prefs    notification.PrefsStore
	queue    notification.QueueStore
	enqueuer *notification.Enqueuer
	renderer *notifyrender.Renderer
	sender   notifysend.Sender
	worker   *notifyworker.Worker
	listener listenerControl
}

// New composes the substrate. Returns nil when cfg.DB is nil (no database:
// the feature is unavailable and every accessor degrades gracefully).
func New(cfg Config) (*Handle, error) {
	if cfg.DB == nil {
		return nil, nil //nolint:nilnil // nil handle = feature unavailable
	}
	renderer, err := notifyrender.NewRenderer(cfg.Branding)
	if err != nil {
		return nil, fmt.Errorf("building notification renderer: %w", err)
	}
	if cfg.UnsubscribeURL != nil {
		renderer.SetUnsubscribeURLFn(cfg.UnsubscribeURL)
	}
	h := &Handle{
		settings: smtp.NewPostgresStore(cfg.DB, cfg.Encryptor),
		prefs:    notifyprefs.NewPostgresStore(cfg.DB),
		queue:    notifyqueue.NewPostgresStore(cfg.DB),
		renderer: renderer,
		sender:   notifysend.NewSMTPSender(),
	}
	h.enqueuer = notification.NewEnqueuer(h.prefs, h.queue, cfg.DigestHourUTC)
	h.worker = notifyworker.New(notifyworker.Config{
		Queue:    h.queue,
		Settings: h.settings,
		Renderer: renderer,
		Sender:   h.sender,
	})
	if cfg.DSN != "" {
		h.listener = notifyqueue.NewListener(cfg.DSN, h.worker)
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
func (h *Handle) Settings() smtp.SettingsStore {
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
	email, err := h.renderer.RenderGuestLink(to, link)
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
func (h *Handle) smtpSettings(ctx context.Context) (*smtp.Settings, error) {
	settings, err := h.settings.Get(ctx)
	if errors.Is(err, smtp.ErrNotFound) {
		return nil, smtp.ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("loading smtp settings: %w", err)
	}
	if !settings.Enabled || settings.Host == "" {
		return nil, smtp.ErrNotConfigured
	}
	return settings, nil
}

// SendTest delivers a test email to the given recipient using the currently
// stored SMTP settings, so an admin can verify the configuration end to end.
//
// The admin API answers a failed test with fixed text that does not vary with
// the failure mode (#1072), so this log is the only place the underlying error
// survives. Every failure below is therefore recorded, with the host and port
// that produced it.
func (h *Handle) SendTest(ctx context.Context, to string) error {
	if h == nil {
		return errors.New("notifications unavailable: no database configured")
	}
	// Mirror the worker's deliverability gate: a disabled or hostless
	// configuration refuses the test instead of sending around the switch.
	// smtp.ErrNotConfigured is a configuration state the API reports verbatim,
	// not a failure to investigate, so it is returned unlogged.
	settings, err := h.smtpSettings(ctx)
	if err != nil {
		if !errors.Is(err, smtp.ErrNotConfigured) {
			slog.Error("notification: test send failed", "recipient", to, logKeyError, err)
		}
		return err
	}
	// The recipient is a mail.ParseAddress-validated address and the host is
	// sanitized, so neither can forge a log line.
	if err := h.deliverTest(ctx, *settings, to); err != nil {
		slog.Error("notification: test send failed",
			"recipient", to,
			"smtp_host", logsan.SanitizeForLog(settings.Host),
			"smtp_port", settings.Port,
			logKeyError, err)
		return err
	}
	return nil
}

// deliverTest renders and delivers the test email through settings.
func (h *Handle) deliverTest(ctx context.Context, settings smtp.Settings, to string) error {
	email, err := h.renderer.RenderTest(to)
	if err != nil {
		return fmt.Errorf("rendering test email: %w", err)
	}
	return h.sender.Send(ctx, settings, *email) //nolint:wrapcheck // sender error already carries context
}
