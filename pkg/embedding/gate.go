package embedding

import (
	"context"
	"errors"
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
// interactive embed behind it for minutes. The gate stops this replica adding
// background work while an interactive call waits, and with a Signal shared
// (Share) the background side also waits while another replica has an
// interactive embed in flight, which is what reaches the workers of every
// replica.
//
// Waiting between calls is not enough on a CPU embedder: one background call
// already at the server held it for minutes, and every search behind it gave
// up at its embedding timeout (#2839). So a background call at the server is
// preempted: when an interactive embed begins (on this replica, or on another
// as the shared signal reports it), the background call's request is
// canceled, which makes the server stop the inputs it has not reached
// (Ollama does, measured on 0.30.6: the search waits for the input in
// progress, not the rest of the batch), and the call is sent again once the
// interactive work is done. A background call is preempted only within
// maxYield of its first attempt; past it the call runs to the end, so a
// replica answering a steady stream of searches still makes indexing
// progress.
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

	// running holds every background call at the server, guarded by mu: its
	// cancel when it may still be preempted, and how long its largest input
	// is expected to hold the server (#2846).
	running map[*bgCall]struct{}
	// preempted counts the background calls preempted, for tests and logs.
	preempted int
	// rate is the seconds one byte of background input takes the server, as
	// measured over completed calls; zero until one is measured. Guarded by mu.
	rate float64

	// bg is what this replica last published of its background calls to a
	// signal that carries them (#2846).
	bg bgPublication
}

// bgCall is one background call at the server: its cancel (nil when it may
// not be preempted) and how long its largest input is expected to take.
type bgCall struct {
	cancel context.CancelCauseFunc
	input  time.Duration
}

// errPreempted is the cause a preempted background call's context carries.
var errPreempted = errors.New("embedding gate: background embed preempted by an interactive embed")

// NewGate returns a gate whose background side waits at most maxYield for
// interactive embeds to finish; a non-positive value means DefaultMaxYield.
func NewGate(maxYield time.Duration) *Gate {
	if maxYield <= 0 {
		maxYield = DefaultMaxYield
	}
	idle := make(chan struct{})
	close(idle)
	return &Gate{idle: idle, maxYield: maxYield, linger: DefaultLinger, running: map[*bgCall]struct{}{}}
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
	return &gated{inner: p, before: g.enter, after: g.leave, allowance: g.searchAllowance}
}

// Background wraps p so each call first yields to interactive embeds, and is
// preempted by one that begins while it is at the server. A nil p stays nil.
func (g *Gate) Background(p Provider) Provider {
	if p == nil {
		return nil
	}
	return &background{inner: p, gate: g}
}

// Preempted is how many background calls the gate has preempted.
func (g *Gate) Preempted() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.preempted
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
	for c := range g.running {
		if c.cancel != nil {
			c.cancel(errPreempted)
		}
	}
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
		slog.DebugContext(ctx, "embedding gate: shared signal not updated", "interactive", busy, "error", err)
		return
	}
	g.published = busy
}

// yield waits until no interactive embed is in flight on this replica or, with
// a shared signal, on any replica; or limit passes; or ctx ends. Only the
// last is an error: a background call that waited its limit goes ahead.
func (g *Gate) yield(ctx context.Context, limit time.Duration) error {
	if limit <= 0 {
		return nil
	}
	g.mu.Lock()
	idle := g.idle
	g.mu.Unlock()
	timer := time.NewTimer(limit)
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
	for g.sharedBusy(ctx) {
		if done, err := g.pollWait(ctx, expired); done {
			return err
		}
	}
	return nil
}

// pollWait waits one poll interval, reporting done when expired fired (nil)
// or ctx ended (its error) first.
func (g *Gate) pollWait(ctx context.Context, expired <-chan time.Time) (bool, error) {
	wait := time.NewTimer(g.poll)
	defer wait.Stop()
	select {
	case <-wait.C:
		return false, nil
	case <-expired:
		return true, nil
	case <-ctx.Done():
		return true, fmt.Errorf("embedding gate: %w", ctx.Err())
	}
}

// sharedBusy asks the shared signal, taking an unreadable one as idle.
func (g *Gate) sharedBusy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, signalTimeout)
	defer cancel()
	busy, err := g.signal.Busy(ctx)
	if err != nil {
		slog.DebugContext(ctx, "embedding gate: shared signal unreadable; not waiting", "error", err)
		return false
	}
	return busy
}

// preemptible runs one background call: it yields to interactive embeds,
// sends the call, and sends it again when an interactive embed preempted it,
// until maxYield has passed since it first yielded. Past that the call neither
// waits nor is preempted. Errors are the call's own, or ctx's while waiting.
func preemptible[T any](ctx context.Context, g *Gate, sz inputSizes, call func(context.Context) (T, error)) (T, error) {
	deadline := time.Now().Add(g.maxYield)
	for {
		var zero T
		if err := g.yield(ctx, time.Until(deadline)); err != nil {
			return zero, err
		}
		began := time.Now()
		callCtx, done := g.start(ctx, began.Before(deadline), sz)
		out, err := call(callCtx)
		preempted := errors.Is(context.Cause(callCtx), errPreempted)
		done()
		if err == nil && !preempted {
			g.observe(time.Since(began), sz.total)
		}
		if err != nil && preempted && ctx.Err() == nil {
			slog.DebugContext(ctx, "embedding gate: background embed preempted; sending it again after the interactive embeds")
			continue
		}
		return out, err
	}
}

// start registers a background call and returns the context it runs on and
// the func that ends it. Every call is registered with how long its largest
// input is expected to hold the server, which a search waits for (#2846). A
// preemptable call made while the gate is interactive (an interactive embed
// began since it yielded) is preempted at once; with a shared signal, it also
// asks it every poll while at the server and is preempted when another
// replica reports an interactive embed. A call past its yield limit is never
// cut.
func (g *Gate) start(ctx context.Context, preemptable bool, sz inputSizes) (callCtx context.Context, done func()) {
	c := &bgCall{}
	callCtx, cancel := context.WithCancelCause(ctx)
	g.mu.Lock()
	c.input = g.inputTimeLocked(sz.largest)
	if preemptable {
		c.cancel = cancel
		if g.active {
			cancel(errPreempted)
		}
	}
	g.running[c] = struct{}{}
	g.mu.Unlock()
	g.publishBackground(ctx)
	stopWatch := func() {}
	if preemptable && g.signal != nil {
		stopWatch = g.watchShared(callCtx, c)
	}
	return callCtx, func() {
		stopWatch()
		g.mu.Lock()
		delete(g.running, c)
		if errors.Is(context.Cause(callCtx), errPreempted) {
			g.preempted++
		}
		g.mu.Unlock()
		cancel(nil)
		g.publishBackground(ctx)
	}
}

// watchShared preempts c when the shared signal reports an interactive embed
// while c is at the server, asking every poll until the returned stop is
// called. It is one timer re-armed after each answer, not a loop: it lives as
// long as one call, and once stop returns no answer preempts c.
func (g *Gate) watchShared(ctx context.Context, c *bgCall) (stop func()) {
	var (
		mu      sync.Mutex
		stopped bool
		timer   *time.Timer
		ask     func()
	)
	ask = func() {
		busy := ctx.Err() == nil && g.sharedBusy(ctx)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case stopped || ctx.Err() != nil:
		case busy:
			c.cancel(errPreempted)
		default:
			timer = time.AfterFunc(g.poll, ask)
		}
	}
	mu.Lock()
	timer = time.AfterFunc(g.poll, ask)
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		stopped = true
		timer.Stop()
	}
}

// background is the background side of the gate: each call yields to
// interactive embeds and is preempted by one that begins while it runs.
type background struct {
	inner Provider
	gate  *Gate
}

// Embed embeds one text as a background call.
func (b *background) Embed(ctx context.Context, text string) ([]float32, error) {
	return preemptible(ctx, b.gate, sizesOf(text), func(ctx context.Context) ([]float32, error) {
		return b.inner.Embed(ctx, text) //nolint:wrapcheck // transparent wrapper; callers classify the provider's own error
	})
}

// EmbedBatch embeds texts as one background call.
func (b *background) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return preemptible(ctx, b.gate, sizesOf(texts...), func(ctx context.Context) ([][]float32, error) {
		return b.inner.EmbedBatch(ctx, texts) //nolint:wrapcheck // transparent wrapper; callers classify the provider's own error
	})
}

// Dimension reports the wrapped provider's dimension.
func (b *background) Dimension() int { return b.inner.Dimension() }

// Kind reports the wrapped provider's kind.
func (b *background) Kind() string { return b.inner.Kind() }

// Model reports the wrapped provider's model, "" when it names none.
func (b *background) Model() string { return ModelName(b.inner) }

// MaxInputBytes reports the wrapped provider's input budget.
func (b *background) MaxInputBytes() int { return MaxInputBytes(b.inner) }

// gated is a provider whose calls are bracketed by the gate. It forwards the
// optional model name and input budget, which the index worker and the
// knowledge-page chunker read off the provider they hold.
type gated struct {
	inner  Provider
	before func(context.Context) error
	after  func()
	// allowance is the gate's searchAllowance; nil outside a gate.
	allowance func(context.Context) time.Duration
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
var (
	_ Provider = (*gated)(nil)
	_ Provider = (*background)(nil)
)
