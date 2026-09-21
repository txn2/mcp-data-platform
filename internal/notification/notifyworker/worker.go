// Package notifyworker drains the notification queue: it claims due rows under
// a lease, renders them, delivers them over SMTP or a channel's own transport,
// and resolves each batch to sent, retried, or failed.
//
// It owns the delivery policy — which transports can deliver right now, the
// retry budget and its backoff, and the retention purge that bounds the table
// — and holds the rendering and transport layers behind the collaborators in
// Config.
package notifyworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/internal/notification/notifypost"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/internal/notification/notifysend"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// logKeyError is the structured-logging key for an error value.
const logKeyError = "error"

// Worker defaults.
const (
	// DefaultPollEvery is the fallback poll interval when LISTEN/NOTIFY does
	// not wake the worker.
	DefaultPollEvery = 30 * time.Second
	// DefaultLease bounds one delivery attempt; an expired lease returns the
	// row to claimable state for crash recovery.
	DefaultLease = 2 * time.Minute
	// DefaultMaxAttempts is the delivery attempt budget per row.
	DefaultMaxAttempts = 5
	// retryBackoffBase seeds the exponential retry backoff.
	retryBackoffBase = 30 * time.Second
	// maxBackoffShift caps the exponential backoff doubling (30s * 2^6 = 32m).
	maxBackoffShift = 6
	// DefaultResolvedRetention keeps sent/failed rows for operator
	// inspection before the purge removes them.
	DefaultResolvedRetention = 30 * 24 * time.Hour
	// DefaultPendingTTL bounds how long an undelivered row stays relevant.
	// Beyond it the event is stale (nobody wants a share email from last
	// month when SMTP is finally configured) and the purge drops it.
	DefaultPendingTTL = 7 * 24 * time.Hour
	// purgeEvery throttles the worker's table-retention pass.
	purgeEvery = time.Hour
)

// Config configures the send worker.
type Config struct {
	Queue    notification.QueueStore
	Settings smtp.SettingsStore
	Renderer *notifyrender.Renderer
	Sender   notifysend.Sender
	// Channels reads the destination a channel row names. nil disables
	// channel delivery: those rows stay pending, as email rows do on a
	// deployment with no mail server.
	Channels notification.ChannelStore
	// ChannelSenders delivers to the three HTTP channel kinds. nil disables
	// channel delivery with Channels.
	//
	// It is an interface rather than *notifypost.Senders so the worker's
	// own behavior -- which transport a row goes to, and how a failure
	// resolves -- is testable without standing up an upstream for each kind.
	ChannelSenders ChannelSender
	// PollEvery, Lease, and MaxAttempts default to the package constants
	// when zero.
	PollEvery   time.Duration
	Lease       time.Duration
	MaxAttempts int
}

// ChannelSender posts one document to one channel, dispatching on its kind.
// notifypost.Senders implements it.
type ChannelSender interface {
	Send(ctx context.Context, ch notification.Channel, doc notification.Document) error
}

// Worker drains the notification queue: it claims due rows, renders them, and
// delivers each over the transport its destination names. It follows the
// indexjobs worker shape (poll ticker + LISTEN/NOTIFY wakeup, lease-based
// claiming, retry with exponential backoff). A transport that cannot deliver
// right now — SMTP unconfigured, or no channel senders wired — is excluded
// from the claim, so its rows stay pending without burning delivery attempts
// while the other transport keeps draining.
type Worker struct {
	cfg      Config
	wakeup   chan struct{}
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
	// lastPurge throttles the retention pass to once per purgeEvery.
	// Only the single run goroutine touches it.
	lastPurge time.Time
}

// New creates a send worker, applying defaults for zero config values.
func New(cfg Config) *Worker {
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = DefaultPollEvery
	}
	if cfg.Lease <= 0 {
		cfg.Lease = DefaultLease
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	return &Worker{
		cfg:    cfg,
		wakeup: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
	}
}

// Notify wakes the worker without waiting for the next poll tick. Safe to
// call from any goroutine; a flurry of calls coalesces into one wakeup.
func (w *Worker) Notify() {
	select {
	case w.wakeup <- struct{}{}:
	default:
	}
}

// Start launches the worker loop. Idempotent.
func (w *Worker) Start(_ context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.run()
}

// Stop terminates the worker loop and waits for in-flight work. Idempotent.
// An abandoned claimed row is safe: its lease expires and it is reclaimed.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

// run is the poll/wakeup loop.
func (w *Worker) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.cfg.PollEvery)
	defer ticker.Stop()
	for {
		w.drain()
		select {
		case <-w.stopCh:
			return
		case <-w.wakeup:
		case <-ticker.C:
		}
	}
}

// drain processes due rows until none remain or the worker stops.
func (w *Worker) drain() {
	ctx := context.Background()
	// Retention runs before the deliverability gate so the table stays
	// bounded even on deployments that never configure SMTP.
	w.maybePurge(ctx)
	settings := w.deliverableSettings(ctx)
	filter := notification.TransportFilter{
		Email:   settings != nil,
		Channel: w.cfg.Channels != nil && w.cfg.ChannelSenders != nil,
	}
	if !filter.Deliverable() {
		return
	}
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}
		if !w.processNext(ctx, settings, filter) {
			return
		}
	}
}

// maybePurge runs the table-retention pass at most once per purgeEvery.
func (w *Worker) maybePurge(ctx context.Context) {
	if time.Since(w.lastPurge) < purgeEvery {
		return
	}
	w.lastPurge = time.Now()
	purged, err := w.cfg.Queue.PurgeOld(ctx, DefaultResolvedRetention, DefaultPendingTTL)
	if err != nil {
		slog.Warn("notification: retention purge failed", logKeyError, err)
		return
	}
	if purged > 0 {
		slog.Info("notification: retention purge", "rows", purged)
	}
}

// deliverableSettings returns SMTP settings ready for sending, or nil when
// SMTP is unconfigured or disabled (rows stay pending, no attempts burned).
func (w *Worker) deliverableSettings(ctx context.Context) *smtp.Settings {
	settings, err := w.cfg.Settings.Get(ctx)
	if errors.Is(err, smtp.ErrNotFound) {
		return nil
	}
	if err != nil {
		slog.Warn("notification: reading smtp settings failed", logKeyError, err)
		return nil
	}
	if !settings.Enabled || settings.Host == "" {
		return nil
	}
	return settings
}

// processNext claims and delivers one unit of work (one immediate row or one
// recipient's digest batch). It reports whether more work may remain.
func (w *Worker) processNext(ctx context.Context, settings *smtp.Settings, filter notification.TransportFilter) bool {
	batch, err := w.claimNext(ctx, filter)
	if errors.Is(err, notification.ErrNoWork) {
		return false
	}
	if err != nil {
		slog.Warn("notification: claim failed", logKeyError, err)
		return false
	}
	w.deliver(ctx, settings, batch)
	return true
}

// claimNext prefers immediate rows, then falls back to digest batches.
// notification.ErrNoWork wraps through so processNext can match it with errors.Is.
func (w *Worker) claimNext(ctx context.Context, filter notification.TransportFilter) ([]notification.Notification, error) {
	n, err := w.cfg.Queue.ClaimImmediate(ctx, w.cfg.Lease, filter)
	if err == nil {
		return []notification.Notification{*n}, nil
	}
	if !errors.Is(err, notification.ErrNoWork) {
		return nil, fmt.Errorf("claiming immediate notification: %w", err)
	}
	batch, err := w.cfg.Queue.ClaimDigest(ctx, w.cfg.Lease, filter)
	if err != nil {
		return nil, fmt.Errorf("claiming digest batch: %w", err)
	}
	return batch, nil
}

// deliver sends one claimed batch over the transport its destination names,
// then resolves its rows.
func (w *Worker) deliver(ctx context.Context, settings *smtp.Settings, batch []notification.Notification) {
	if len(batch) == 0 {
		return
	}
	var (
		terminal bool
		err      error
	)
	if name, addressed := notification.ChannelName(batch[0].Recipient); addressed {
		terminal, err = w.deliverToChannel(ctx, name, batch)
	} else {
		terminal, err = w.deliverByEmail(ctx, settings, batch)
	}
	if err != nil {
		w.resolve(ctx, batch, err, terminal)
		return
	}
	if err := w.cfg.Queue.MarkSent(ctx, ids(batch)); err != nil {
		slog.Error("notification: marking sent failed", logKeyError, err)
		return
	}
	slog.Info("notification: sent", "recipient", batch[0].Recipient, "count", len(batch))
}

// deliverByEmail renders the batch as one branded email and sends it over
// SMTP, reporting whether a failure is terminal. A render failure is
// deterministic, so retrying cannot fix it.
func (w *Worker) deliverByEmail(ctx context.Context, settings *smtp.Settings, batch []notification.Notification) (terminal bool, err error) {
	email, err := w.cfg.Renderer.Render(batch)
	if err != nil {
		return true, err //nolint:wrapcheck // the renderer's message is what the history records
	}
	if err := w.cfg.Sender.Send(ctx, *settings, *email); err != nil {
		return false, err //nolint:wrapcheck // the mail server's own words are the operator's answer
	}
	return false, nil
}

// deliverToChannel posts the batch to the destination it names, reporting the
// failure and whether it is terminal.
//
// A batch is one document per row. A digest batch for one channel is posted as
// one message per document rather than as a bulletin: composing the bulletin
// is the rendering stage's work, and until it exists a reader gets each
// document whole instead of a summary that drops what it could not fit.
func (w *Worker) deliverToChannel(ctx context.Context, name string, batch []notification.Notification) (terminal bool, err error) {
	ch, err := w.cfg.Channels.Get(ctx, name)
	if err != nil {
		// A row naming a channel the operator has since deleted can never be
		// delivered, so it fails rather than retrying five times first.
		if errors.Is(err, notifychannel.ErrChannelNotFound) {
			return true, fmt.Errorf("channel %q no longer exists", name)
		}
		return false, fmt.Errorf("reading channel %q: %w", name, err)
	}
	if !ch.Enabled {
		return true, fmt.Errorf("channel %q is disabled", name)
	}
	for _, n := range batch {
		if n.Payload.Document == nil {
			return true, fmt.Errorf("notification %d carries no document to post to channel %q", n.ID, name)
		}
		if err := w.cfg.ChannelSenders.Send(ctx, *ch, *n.Payload.Document); err != nil {
			return errors.Is(err, notifypost.ErrTerminal), err
		}
	}
	return false, nil
}

// resolve routes a failed batch to retry or permanent failure.
func (w *Worker) resolve(ctx context.Context, batch []notification.Notification, sendErr error, terminal bool) {
	attempts := maxAttempts(batch)
	if terminal || attempts >= w.cfg.MaxAttempts {
		slog.Error("notification: delivery failed permanently",
			"recipient", batch[0].Recipient, "attempts", attempts, logKeyError, sendErr)
		if err := w.cfg.Queue.Fail(ctx, ids(batch), sendErr.Error()); err != nil {
			slog.Error("notification: recording failure failed", logKeyError, err)
		}
		return
	}
	backoff := computeBackoff(attempts)
	slog.Warn("notification: delivery failed; will retry",
		"recipient", batch[0].Recipient, "attempts", attempts, "backoff", backoff, logKeyError, sendErr)
	if err := w.cfg.Queue.Retry(ctx, ids(batch), sendErr.Error(), backoff); err != nil {
		slog.Error("notification: recording retry failed", logKeyError, err)
	}
}

// ids collects the row IDs of a batch.
func ids(batch []notification.Notification) []int64 {
	out := make([]int64, len(batch))
	for i, n := range batch {
		out[i] = n.ID
	}
	return out
}

// maxAttempts returns the highest attempt count in a batch (digest rows can
// carry different counts when new rows join a retried batch).
func maxAttempts(batch []notification.Notification) int {
	most := 0
	for _, n := range batch {
		if n.Attempts > most {
			most = n.Attempts
		}
	}
	return most
}

// computeBackoff returns retryBackoffBase * 2^(attempts-1), capped.
func computeBackoff(attempts int) time.Duration {
	shift := min(max(attempts-1, 0), maxBackoffShift)
	return retryBackoffBase * (1 << shift)
}
