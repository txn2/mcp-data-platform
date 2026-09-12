package connalert

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// DefaultSweepInterval is how often the open revocations are swept.
//
// Finer than the review-queue check's hour because the window it watches can be
// set as low as an hour, and the tick is what bounds how late an escalation
// lands inside it. The cost is one statement against a partial index that holds
// only unescalated revocations — on a deployment where nothing is broken, no
// rows at all.
const DefaultSweepInterval = time.Minute

// Escalator raises a revocation nobody has acted on with the addresses the
// operator named.
//
// It exists because the premise of the first alert is that its recipient may be
// unreachable. A connection is a tenant asset: the person who authorized it
// holds the credential, but everyone using it is stopped while it is revoked,
// and on a scheduled run nobody is there to notice.
type Escalator struct {
	alerter  *Alerter
	interval time.Duration
	now      func() time.Time
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewEscalator builds the sweep, or nil when a dependency is absent. A nil
// Escalator's methods are no-ops, so the caller brackets Start/Stop
// unconditionally.
func NewEscalator(cfg Config) *Escalator {
	alerter := NewAlerter(cfg)
	if alerter == nil {
		return nil
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return &Escalator{
		alerter:  alerter,
		interval: interval,
		now:      alerter.cfg.Now,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the sweep until ctx is canceled or Stop is called. The first sweep
// runs one interval in rather than at startup, keeping it clear of boot: what
// bounds a repeat escalation is the stamp in the database, which survives a
// restart. Nil-safe.
func (e *Escalator) Start(ctx context.Context) {
	if e == nil {
		return
	}
	e.wg.Go(func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-e.stopCh:
				return
			case <-ticker.C:
				if err := e.Sweep(ctx); err != nil {
					slog.Warn("connection revocation escalation sweep failed", // #nosec G706 -- structured slog call; error sanitized
						logKeyError, logsan.SanitizeForLog(err.Error()))
				}
			}
		}
	})
}

// Stop ends the sweep and waits for one in flight. Nil-safe and idempotent.
func (e *Escalator) Stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() { close(e.stopCh) })
	e.wg.Wait()
}

// Sweep escalates every revocation past the operator's window that nobody has
// acted on. It is the whole behavior of the type; Start only supplies the clock.
func (e *Escalator) Sweep(ctx context.Context) error {
	if e == nil {
		return nil
	}
	settings, err := SettingsOf(ctx, e.alerter.cfg.Settings)
	if err != nil {
		return err
	}
	recipients := settings.EscalatesTo()
	if !settings.Enabled || len(recipients) == 0 {
		// Nowhere to escalate. The rows stay unstamped, so naming a recipient
		// later picks up the revocations that are still open rather than
		// starting from the next one.
		return nil
	}
	window := settings.EscalateAfter()
	due, err := e.alerter.cfg.Alerts.ClaimEscalations(ctx, window, e.now())
	if err != nil {
		return fmt.Errorf("sweeping open connection revocations: %w", err)
	}
	esc := &escalation{afterHours: int(window / time.Hour)}
	for _, alert := range due {
		e.alerter.deliver(ctx, recipients, alert, esc)
	}
	return nil
}
