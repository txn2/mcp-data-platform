package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// Per-actor enqueue caps. Sharing is human-paced: the burst covers a
// collection shared with a large team in one action, while the sustained
// rate bounds the outbound-mail primitive an authenticated account
// represents (attacker-chosen recipient and message text).
const (
	// actorRatePerMinute is the sustained per-actor enqueue rate.
	actorRatePerMinute = 6
	// actorBurst is the per-actor burst allowance.
	actorBurst = 30
)

// Enqueuer is the trigger-side entry point: it consults the recipient's
// preferences and either drops the event, queues it for immediate delivery,
// or schedules it into the recipient's next daily digest window.
//
// Enqueue is a single cheap DB insert; callers on a request path must log a
// returned error and continue — a share or comment never fails because its
// notification could not be queued.
type Enqueuer struct {
	prefs         PrefsStore
	queue         QueueStore
	digestHourUTC int
	now           func() time.Time
	// limiter caps enqueues per actor so one authenticated account is not
	// an unbounded outbound-mail primitive. Per-process; multi-replica
	// deployments multiply the cap by replica count, which still bounds it.
	limiter *ratelimit.Limiter
}

// NewEnqueuer creates an Enqueuer. digestHourUTC is the hour of day (0-23,
// UTC) daily digests are scheduled for. Close releases the limiter's
// background goroutine.
func NewEnqueuer(prefs PrefsStore, queue QueueStore, digestHourUTC int) *Enqueuer {
	return &Enqueuer{
		prefs:         prefs,
		queue:         queue,
		digestHourUTC: digestHourUTC,
		now:           time.Now,
		limiter:       ratelimit.New(ratelimit.Config{RequestsPerMinute: actorRatePerMinute, BurstSize: actorBurst}),
	}
}

// Close stops the limiter's background eviction goroutine. Nil-safe.
func (e *Enqueuer) Close() {
	if e == nil || e.limiter == nil {
		return
	}
	e.limiter.Close()
}

// Notify queues one notification for recipient according to their
// preferences. Events targeting nobody (empty recipient) or the actor
// themselves are dropped silently, as are events the recipient opted out of.
// A nil Enqueuer (feature not wired, e.g. no database) drops everything.
func (e *Enqueuer) Notify(ctx context.Context, recipient, category string, p Payload) error {
	if e == nil {
		return nil
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	if recipient == "" || strings.EqualFold(recipient, p.Actor) {
		return nil
	}
	if !e.allowActor(p.Actor, recipient) {
		return nil
	}
	prefs, err := e.prefs.Get(ctx, recipient)
	if err != nil {
		return fmt.Errorf("reading notification prefs for %s: %w", recipient, err)
	}
	if !wantsCategory(prefs, category) {
		return nil
	}

	n := Notification{Recipient: recipient, Category: category, Payload: p}
	if prefs.Mode == ModeDaily {
		n.Digest = true
		n.ScheduledFor = NextDigestTime(e.now().UTC(), e.digestHourUTC)
	}
	if err := e.queue.Enqueue(ctx, n); err != nil {
		return fmt.Errorf("enqueueing %s notification for %s: %w", category, recipient, err)
	}
	return nil
}

// allowActor applies the per-actor rate limit, logging drops. Actorless
// events fall back to the recipient as the limit key.
func (e *Enqueuer) allowActor(actor, recipient string) bool {
	if e.limiter == nil {
		return true
	}
	key := strings.ToLower(strings.TrimSpace(actor))
	if key == "" {
		key = recipient
	}
	if e.limiter.Allow(key) {
		return true
	}
	slog.Warn("notification: per-actor rate limit exceeded; dropping", "actor", key)
	return false
}

// wantsCategory reports whether prefs accept a notification of category.
func wantsCategory(prefs Prefs, category string) bool {
	if prefs.Mode == ModeOff {
		return false
	}
	switch category {
	case CategoryShare:
		return prefs.SharesEnabled
	case CategoryComment:
		return prefs.CommentsEnabled
	default:
		return false
	}
}

// NextDigestTime returns the next occurrence of hourUTC:00 strictly after
// now. The result is in UTC.
func NextDigestTime(now time.Time, hourUTC int) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), hourUTC, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
