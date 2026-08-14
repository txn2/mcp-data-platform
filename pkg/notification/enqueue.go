package notification

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
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
// preferences and reports whether a row was written. Events targeting nobody
// (empty recipient) or the actor themselves are dropped silently, as are
// events the recipient opted out of and events over the actor's rate limit;
// all of those return queued=false with a nil error. A nil Enqueuer (feature
// not wired, e.g. no database) drops everything.
//
// Callers that fan one event out across several categories must branch on
// queued rather than on the error: a recipient who was dropped here has been
// told nothing, so the caller may still owe them a different notification.
func (e *Enqueuer) Notify(ctx context.Context, recipient, category string, p Payload) (queued bool, err error) {
	if e == nil {
		return false, nil
	}
	if !e.allowActor(p.Actor, recipient) {
		return false, nil
	}
	return e.enqueue(ctx, recipient, category, p)
}

// NotifyFanout queues p for every recipient of an audience the actor did not
// choose -- the people a target is already shared with -- and returns the
// recipients a row was written for.
//
// It charges the actor's rate limit once for the whole fan-out rather than
// once per recipient: the size of this audience is a property of the item, not
// something the actor picked, so a comment on a widely-shared asset must not
// exhaust the budget that bounds the addresses they DO pick (shares and
// mentions). The recipient count is bounded instead by maxFanout, and a
// truncated fan-out is logged with both counts rather than silently trimmed.
func (e *Enqueuer) NotifyFanout(ctx context.Context, recipients []string, category string, p Payload) []string {
	if e == nil || len(recipients) == 0 {
		return nil
	}
	if !e.allowActor(p.Actor, firstOf(recipients)) {
		return nil
	}
	if len(recipients) > maxFanout {
		slog.Warn("notification: fan-out truncated", // #nosec G706 -- structured slog call; counts only
			"category", category, "recipients", len(recipients), "sent", maxFanout)
		recipients = recipients[:maxFanout]
	}
	var sent []string
	for _, recipient := range recipients {
		queued, err := e.enqueue(ctx, recipient, category, p)
		if err != nil {
			slog.Warn("notification: fan-out enqueue failed", // #nosec G706 -- structured slog call; error sanitized
				"error", logsan.SanitizeForLog(err.Error()))
			continue
		}
		if queued {
			sent = append(sent, recipient)
		}
	}
	return sent
}

// maxFanout bounds how many people one event may notify through NotifyFanout.
// It is far above a normal share list and exists so a single comment cannot
// become an unbounded mail amplifier; crossing it is logged.
const maxFanout = 200

// enqueue applies the recipient's preferences and writes the queue row,
// reporting whether one was written. It performs no rate limiting: that is the
// caller's choice of per-recipient (Notify) or per-event (NotifyFanout).
func (e *Enqueuer) enqueue(ctx context.Context, recipient, category string, p Payload) (bool, error) {
	// Normalize both sides of the self-check to the bare address: the
	// candidate came from whatever shape a store holds ("Display Name
	// <addr>" is accepted at rest), while the actor is the signed-in
	// caller's address. Comparing the raw strings let an owner recorded in
	// display form receive their own comment (#1100). The normalized form is
	// also what the row stores, so the preference lookup below and the queue
	// key agree on the person.
	recipient = NormalizeAddress(recipient)
	if recipient == "" || recipient == NormalizeAddress(p.Actor) {
		return false, nil
	}
	prefs, err := e.prefs.Get(ctx, recipient)
	if err != nil {
		// The recipient is request-supplied and these errors are logged by
		// the trigger sites, so strip control characters before embedding.
		return false, fmt.Errorf("reading notification prefs for %s: %w", logsan.SanitizeForLog(recipient), err)
	}
	if !wantsCategory(prefs, category) {
		return false, nil
	}

	n := Notification{Recipient: recipient, Category: category, Payload: p}
	if prefs.Mode == ModeDaily {
		n.Digest = true
		n.ScheduledFor = NextDigestTime(e.now().UTC(), e.digestHourUTC)
	}
	if err := e.queue.Enqueue(ctx, n); err != nil {
		return false, fmt.Errorf("enqueueing %s notification for %s: %w",
			category, logsan.SanitizeForLog(recipient), err)
	}
	return true, nil
}

// firstOf returns the first entry of a non-empty slice, used as the rate-limit
// fallback key when an event carries no actor.
func firstOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// allowActor applies the per-actor rate limit, logging drops. Actorless
// events fall back to the recipient as the limit key.
func (e *Enqueuer) allowActor(actor, recipient string) bool {
	if e.limiter == nil {
		return true
	}
	key := NormalizeAddress(actor)
	if key == "" {
		key = NormalizeAddress(recipient)
	}
	if e.limiter.Allow(key) {
		return true
	}
	slog.Warn("notification: per-actor rate limit exceeded; dropping", // #nosec G706 -- structured slog call; actor sanitized
		"actor", logsan.SanitizeForLog(key))
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
	case CategoryMention:
		return prefs.MentionsEnabled
	case CategoryReviewQueue, CategoryScriptRun, CategoryScriptReview:
		// Addressed by responsibility rather than by interest, so none has a
		// per-user category toggle: the recipients are named by the admin
		// settings (both review queues) or by owning and approving the
		// automation (script run), and Mode (checked above) is the recipient's
		// own opt-out. See CategoryReviewQueue, CategoryScriptRun, and
		// CategoryScriptReview.
		return true
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
