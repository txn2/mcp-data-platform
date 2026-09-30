package embedding

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// DefaultMaxYield bounds how long a background embed waits for interactive
// embeds to finish before it goes ahead anyway, so a replica answering a
// steady stream of searches still makes indexing progress.
const DefaultMaxYield = 30 * time.Second

// DefaultLinger is how long the gate stays interactive after the last
// interactive embed ends. One search embeds its intent once per source it
// federates, one after another; without it each gap between two of them read
// as idle, and a waiting background embed went to the embedding server in the
// middle of the search.
const DefaultLinger = time.Second

// DefaultSharedPoll is how often a waiting background embed asks the shared
// signal whether another replica still has an interactive embed in flight.
const DefaultSharedPoll = 250 * time.Millisecond

// signalTimeout bounds one exchange with the shared signal. It is a database
// round trip; a slow one must not hold a search up, and a missed mark only
// lets one background embed through.
const signalTimeout = time.Second

// Signal carries "an interactive embed is in flight" between the replicas of
// one deployment, which share one embedding server (#1988). Mark says this
// replica has one, Clear that it has none, and Busy whether any replica,
// this one included, has one now. A replica that stops without clearing its
// mark is not waited on forever: an implementation lets a mark lapse.
type Signal interface {
	Mark(ctx context.Context) error
	Clear(ctx context.Context) error
	Busy(ctx context.Context) (bool, error)
}

// Gate puts the embeds a person is waiting on ahead of the ones nobody is
// (#1988). A search, a discovery ranking or a knowledge-page dedup probe goes
// through the Interactive side; the index worker goes through the Background
// side, and before each call waits until no interactive embed is in flight on
// this replica.
//
// Both kinds share one embedding server, and a CPU-bound one serves them in
// arrival order: a backlog of index jobs from a bulk write put every
// interactive embed behind it for minutes. The gate cannot reach a call
// already at the server, or a call from another replica; what it does is stop
// this replica adding background work while an interactive call waits. With
// a Signal shared (Share), the background side also waits while another
// replica has an interactive embed in flight, which is what reaches the
// workers of every replica.
//
// A Gate is safe for concurrent use. The zero value is not usable; call
// NewGate.
type Gate struct {
	mu   sync.Mutex
	busy int
	// active is whether the gate is interactive: an interactive embed is in
	// flight, or one ended less than linger ago. idle is closed while it is
	// not, and replaced by an open channel when it becomes so.
	active bool
	idle   chan struct{}
	// settling is the timer that ends the linger; nil while none is pending.
	settling *time.Timer
	linger   time.Duration
	// maxYield is how long a background call waits before going ahead.
	maxYield time.Duration

	// signal, when set, is the deployment-wide half; poll is how often a
	// waiting background call asks it again.
	signal Signal
	poll   time.Duration
	// pubMu serializes publishing to signal, and published is what was last
	// published, so the shared state follows the local one in order.
	pubMu     sync.Mutex
	published bool
}

// NewGate returns a gate whose background side waits at most maxYield for
// interactive embeds to finish; a non-positive value means DefaultMaxYield.
func NewGate(maxYield time.Duration) *Gate {
	if maxYield <= 0 {
		maxYield = DefaultMaxYield
	}
	idle := make(chan struct{})
	close(idle)
	return &Gate{idle: idle, maxYield: maxYield, linger: DefaultLinger}
}

// Share makes the gate deployment-wide: this replica publishes its
// interactive embeds to sig, and its background side also waits while sig
// reports one anywhere, asking again every poll (DefaultSharedPoll when not
// positive). Call it before the gate is used.
func (g *Gate) Share(sig Signal, poll time.Duration) {
	if poll <= 0 {
		poll = DefaultSharedPoll
	}
	g.signal, g.poll = sig, poll
}

// Interactive wraps p so its calls count as interactive. A nil p stays nil.
func (g *Gate) Interactive(p Provider) Provider {
	if p == nil {
		return nil
	}
	return &gated{inner: p, before: g.enter, after: g.leave}
}

// Background wraps p so each call first yields to interactive embeds. A nil p
// stays nil.
func (g *Gate) Background(p Provider) Provider {
	if p == nil {
		return nil
	}
	return &gated{inner: p, before: g.yield, after: func() {}}
}

func (g *Gate) enter(ctx context.Context) error {
	g.mu.Lock()
	if g.settling != nil {
		g.settling.Stop()
		g.settling = nil
	}
	if !g.active {
		g.active = true
		g.idle = make(chan struct{})
	}
	g.busy++
	g.mu.Unlock()
	g.publish(ctx)
	return nil
}

// leave ends one interactive call. The last one to end starts the linger,
// and the gate settles when it runs out with nothing new begun.
func (g *Gate) leave() {
	g.mu.Lock()
	g.busy--
	if g.busy > 0 {
		g.mu.Unlock()
		return
	}
	if g.linger > 0 {
		g.settling = time.AfterFunc(g.linger, g.settle)
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()
	g.settle()
}

// settle makes the gate idle unless an interactive call began since the last
// one ended, and publishes it.
func (g *Gate) settle() {
	g.mu.Lock()
	if g.busy > 0 || !g.active {
		g.mu.Unlock()
		return
	}
	g.active = false
	g.settling = nil
	close(g.idle)
	g.mu.Unlock()
	g.publish(context.Background())
}

// publish brings the shared signal up to date with whether this replica is
// interactive. Serialized, and reading the count under the
// publish lock, so a clear published for one call never lands after the mark
// of the next. A failure is not the caller's: the embed goes ahead, and the
// shared state is at worst one transition behind.
func (g *Gate) publish(ctx context.Context) {
	if g.signal == nil {
		return
	}
	g.pubMu.Lock()
	defer g.pubMu.Unlock()
	g.mu.Lock()
	busy := g.active
	g.mu.Unlock()
	if busy == g.published {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signalTimeout)
	defer cancel()
	var err error
	if busy {
		err = g.signal.Mark(ctx)
	} else {
		err = g.signal.Clear(ctx)
	}
	if err != nil {
		slog.Debug("embedding gate: shared signal not updated", "interactive", busy, "error", err)
		return
	}
	g.published = busy
}

// yield waits until no interactive embed is in flight on this replica or, with
// a shared signal, on any replica; or maxYield passes; or ctx ends. Only the
// last is an error: a background call that waited its limit goes ahead.
func (g *Gate) yield(ctx context.Context) error {
	g.mu.Lock()
	idle := g.idle
	g.mu.Unlock()
	timer := time.NewTimer(g.maxYield)
	defer timer.Stop()
	select {
	case <-idle:
	default:
		select {
		case <-idle:
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("embedding gate: %w", ctx.Err())
		}
	}
	return g.yieldShared(ctx, timer.C)
}

// yieldShared waits while the shared signal reports an interactive embed on
// any replica, until expired fires or ctx ends. An unreadable signal is taken
// as idle: the gate orders work, and it never stops it.
func (g *Gate) yieldShared(ctx context.Context, expired <-chan time.Time) error {
	if g.signal == nil {
		return nil
	}
	for {
		if !g.sharedBusy(ctx) {
			return nil
		}
		wait := time.NewTimer(g.poll)
		select {
		case <-wait.C:
		case <-expired:
			wait.Stop()
			return nil
		case <-ctx.Done():
			wait.Stop()
			return fmt.Errorf("embedding gate: %w", ctx.Err())
		}
	}
}

// sharedBusy asks the shared signal, taking an unreadable one as idle.
func (g *Gate) sharedBusy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, signalTimeout)
	defer cancel()
	busy, err := g.signal.Busy(ctx)
	if err != nil {
		slog.Debug("embedding gate: shared signal unreadable; not waiting", "error", err)
		return false
	}
	return busy
}

// gated is a provider whose calls are bracketed by the gate. It forwards the
// optional model name and input budget, which the index worker and the
// knowledge-page chunker read off the provider they hold.
type gated struct {
	inner  Provider
	before func(context.Context) error
	after  func()
}

// Embed embeds one text once the gate lets it through. The provider's error is
// returned as the provider's: callers classify it (a timeout, ErrInputTooLarge).
func (g *gated) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := g.before(ctx); err != nil {
		return nil, err
	}
	defer g.after()
	return g.inner.Embed(ctx, text) //nolint:wrapcheck // transparent wrapper; callers classify the provider's own error
}

// EmbedBatch embeds texts once the gate lets them through, returning the
// provider's error as the provider's.
func (g *gated) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if err := g.before(ctx); err != nil {
		return nil, err
	}
	defer g.after()
	return g.inner.EmbedBatch(ctx, texts) //nolint:wrapcheck // transparent wrapper; callers classify the provider's own error
}

// Dimension reports the wrapped provider's dimension.
func (g *gated) Dimension() int { return g.inner.Dimension() }

// Kind reports the wrapped provider's kind, so a wrapped noop still reads as
// unconfigured.
func (g *gated) Kind() string { return g.inner.Kind() }

// Model reports the wrapped provider's model, "" when it names none.
func (g *gated) Model() string { return ModelName(g.inner) }

// MaxInputBytes reports the wrapped provider's input budget.
func (g *gated) MaxInputBytes() int { return MaxInputBytes(g.inner) }

// Verify interface compliance.
var _ Provider = (*gated)(nil)
