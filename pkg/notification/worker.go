package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
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

// WorkerConfig configures the send worker.
type WorkerConfig struct {
	Queue    QueueStore
	Settings SettingsStore
	Renderer *Renderer
	Sender   Sender
	// PollEvery, Lease, and MaxAttempts default to the package constants
	// when zero.
	PollEvery   time.Duration
	Lease       time.Duration
	MaxAttempts int
}

// Worker drains the notification queue: it claims due rows, renders branded
// emails, and delivers them over SMTP. It follows the indexjobs worker shape
// (poll ticker + LISTEN/NOTIFY wakeup, lease-based claiming, retry with
// exponential backoff). When SMTP is unconfigured or disabled the worker
// leaves rows pending without burning delivery attempts.
type Worker struct {
	cfg      WorkerConfig
	wakeup   chan struct{}
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
	// lastPurge throttles the retention pass to once per purgeEvery.
	// Only the single run goroutine touches it.
	lastPurge time.Time
}

// NewWorker creates a send worker, applying defaults for zero config values.
func NewWorker(cfg WorkerConfig) *Worker {
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
	if settings == nil {
		return
	}
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}
		if !w.processNext(ctx, settings) {
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
func (w *Worker) deliverableSettings(ctx context.Context) *SMTPSettings {
	settings, err := w.cfg.Settings.GetSMTP(ctx)
	if errors.Is(err, ErrNotFound) {
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
func (w *Worker) processNext(ctx context.Context, settings *SMTPSettings) bool {
	batch, err := w.claimNext(ctx)
	if errors.Is(err, ErrNoWork) {
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
// ErrNoWork wraps through so processNext can match it with errors.Is.
func (w *Worker) claimNext(ctx context.Context) ([]Notification, error) {
	n, err := w.cfg.Queue.ClaimImmediate(ctx, w.cfg.Lease)
	if err == nil {
		return []Notification{*n}, nil
	}
	if !errors.Is(err, ErrNoWork) {
		return nil, fmt.Errorf("claiming immediate notification: %w", err)
	}
	batch, err := w.cfg.Queue.ClaimDigest(ctx, w.cfg.Lease)
	if err != nil {
		return nil, fmt.Errorf("claiming digest batch: %w", err)
	}
	return batch, nil
}

// deliver renders and sends one claimed batch, then resolves its rows.
func (w *Worker) deliver(ctx context.Context, settings *SMTPSettings, batch []Notification) {
	if len(batch) == 0 {
		return
	}
	email, err := w.cfg.Renderer.Render(batch)
	if err != nil {
		// A render failure is deterministic; retrying cannot fix it.
		w.resolve(ctx, batch, err, true)
		return
	}
	if err := w.cfg.Sender.Send(ctx, *settings, *email); err != nil {
		w.resolve(ctx, batch, err, false)
		return
	}
	if err := w.cfg.Queue.MarkSent(ctx, ids(batch)); err != nil {
		slog.Error("notification: marking sent failed", logKeyError, err)
		return
	}
	slog.Info("notification: sent", "recipient", batch[0].Recipient, "count", len(batch))
}

// resolve routes a failed batch to retry or permanent failure.
func (w *Worker) resolve(ctx context.Context, batch []Notification, sendErr error, terminal bool) {
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
func ids(batch []Notification) []int64 {
	out := make([]int64, len(batch))
	for i, n := range batch {
		out[i] = n.ID
	}
	return out
}

// maxAttempts returns the highest attempt count in a batch (digest rows can
// carry different counts when new rows join a retried batch).
func maxAttempts(batch []Notification) int {
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
