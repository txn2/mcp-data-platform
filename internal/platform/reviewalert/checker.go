package reviewalert

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// DefaultInterval is how often the queue is evaluated. The threshold is
// measured in days, so an hourly check is already far finer than the signal
// it watches; the cooldown, not the interval, decides how often mail goes out.
const DefaultInterval = time.Hour

// reviewQueueRoute is the portal deep link the alert points at: the Insights
// tab's review queue.
const reviewQueueRoute = "/knowledge#review"

// reviewQueueTitle labels the alert's target in the email.
const reviewQueueTitle = "Knowledge review queue"

// logKeyError is the structured-logging key for an error value.
const logKeyError = "error"

// Config carries the checker's dependencies. All are required; New returns
// nil when any is missing, which is how the composition root expresses "this
// deployment has no database" without a second flag.
type Config struct {
	// Settings holds the operator's threshold and recipients.
	Settings SettingsStore
	// State holds the re-alert marker.
	State StateStore
	// Insights is the knowledge insight store the pending rollup is read
	// from, through knowledge.PendingReviewOf's fast path.
	Insights knowledgekit.InsightStore
	// Enqueuer is the notification substrate's trigger-side entry point.
	Enqueuer *notification.Enqueuer
	// BaseURL is the portal's public base URL, for the alert's deep link.
	BaseURL string
	// Interval overrides DefaultInterval. Testing hook.
	Interval time.Duration
	// Now overrides time.Now. Testing hook.
	Now func() time.Time
}

// Checker evaluates the pending review queue on a timer and alerts the
// configured recipients when it crosses the threshold.
type Checker struct {
	cfg      Config
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New builds a Checker, or nil when a dependency is absent (no database, or
// notifications disabled). A nil Checker's methods are no-ops, so the caller
// brackets Start/Stop unconditionally.
func New(cfg Config) *Checker {
	if cfg.Settings == nil || cfg.State == nil || cfg.Insights == nil || cfg.Enqueuer == nil {
		return nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Checker{cfg: cfg, stopCh: make(chan struct{})}
}

// Start runs the check loop until ctx is canceled or Stop is called. The
// first check runs one interval in rather than at startup, keeping it clear
// of boot: the signal it watches is measured in days, so nothing is lost by
// waiting. What bounds repeat mail is the cooldown claim, which is in the
// database and so survives a restart. Nil-safe.
func (c *Checker) Start(ctx context.Context) {
	if c == nil {
		return
	}
	c.wg.Go(func() {
		ticker := time.NewTicker(c.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stopCh:
				return
			case <-ticker.C:
				if err := c.Check(ctx); err != nil {
					slog.Warn("review queue alert check failed", // #nosec G706 -- structured slog call; error sanitized
						logKeyError, logsan.SanitizeForLog(err.Error()))
				}
			}
		}
	})
}

// Stop ends the check loop and waits for an in-flight check. Nil-safe and
// idempotent.
func (c *Checker) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.wg.Wait()
}

// Check evaluates the queue once and enqueues the alert when it has crossed
// the threshold and the cooldown allows. It is the whole behavior of the
// package; Start only supplies the clock.
func (c *Checker) Check(ctx context.Context) error {
	settings, err := SettingsOf(ctx, c.cfg.Settings)
	if err != nil {
		return err
	}
	if !settings.Deliverable() {
		return nil
	}
	review, err := knowledgekit.PendingReviewOf(ctx, c.cfg.Insights)
	if err != nil {
		return err //nolint:wrapcheck // PendingReviewOf already names the operation
	}
	rollup := c.rollup(review)
	if !settings.Crossed(rollup.Pending, rollup.OldestAgeDays) {
		// Back under threshold: drop the marker so the next crossing is
		// reported as news rather than waiting out this one's cooldown.
		return c.cfg.State.Clear(ctx) //nolint:wrapcheck // store errors already name their operation
	}
	claimed, err := c.cfg.State.ClaimAlert(ctx, settings.Cooldown(), c.cfg.Now())
	if err != nil {
		return fmt.Errorf("review queue alert check: %w", err)
	}
	if !claimed {
		return nil
	}
	c.deliver(ctx, settings.Recipients, rollup)
	return nil
}

// rollup converts the store's pending-review summary into the shape the email
// carries.
func (c *Checker) rollup(review *knowledgekit.PendingReview) notification.ReviewQueue {
	q := notification.ReviewQueue{
		Pending:        review.TotalPending,
		StaleCount:     review.PendingOver30d,
		StaleAfterDays: knowledgekit.PendingStalenessThresholdDays,
	}
	if review.OldestPendingAt != nil {
		q.OldestAgeDays = knowledgekit.AgeDays(*review.OldestPendingAt, c.cfg.Now())
	}
	return q
}

// deliver enqueues the alert for every configured recipient. A failure for one
// address is logged and the rest still go out: the claim has already been
// stamped, so a caller that gave up here would suppress the whole cooldown
// window over one bad row.
func (c *Checker) deliver(ctx context.Context, recipients []string, q notification.ReviewQueue) {
	payload := notification.Payload{
		Kind:      notification.KindReviewQueue,
		ItemTitle: reviewQueueTitle,
		Link:      notification.PortalLink(c.cfg.BaseURL, reviewQueueRoute),
		Review:    &q,
	}
	queued := 0
	for _, recipient := range recipients {
		// No actor: the platform raised this, not a person. The enqueuer then
		// rate-limits per recipient, which is the right bucket for an alert
		// nobody addressed by hand.
		wrote, err := c.cfg.Enqueuer.Notify(ctx, recipient, notification.CategoryReviewQueue, payload)
		if err != nil {
			slog.Warn("review queue alert enqueue failed", // #nosec G706 -- structured slog call; error sanitized
				logKeyError, logsan.SanitizeForLog(err.Error()))
			continue
		}
		if wrote {
			queued++
		}
	}
	// queued is what was actually written, not what was attempted: a
	// recipient who opted out, or whose enqueue failed, must not read as
	// notified in the log an operator checks after a silent alert.
	slog.Info("review queue alert enqueued",
		"pending", q.Pending, "oldest_age_days", q.OldestAgeDays,
		"recipients", len(recipients), "queued", queued)
}
