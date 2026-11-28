package embedding

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// MaxSearchAllowance bounds how much longer than its own timeout a search's
// intent embed waits for the background input already at the embedding
// server (#2846).
const MaxSearchAllowance = 20 * time.Second

// DefaultBackgroundHold is how long a replica's background mark holds without
// being refreshed or cleared, so a replica that stops mid-embed is not waited
// on past it.
const DefaultBackgroundHold = time.Minute

// allowanceMargin covers the spread between one input's measured cost and the
// next one's: inputs of one size do not take one time.
const allowanceMargin = 1.5

// rateFloorBytes is the smallest call that sets the measured rate: below it a
// call's time is mostly the request's own overhead, not its input.
const rateFloorBytes = 1000

// rateWeight is the weight a new measurement carries against the rate so far.
const rateWeight = 0.5

// remarkChange is the relative change in this replica's expected input time
// past which its published mark is written again.
const remarkChange = 0.25

// BackgroundSignal is the deployment-wide half of the search allowance
// (#2846). A Signal that also implements it carries, besides the interactive
// embeds, how long the background input each replica has at the embedding
// server is expected to take: MarkBackground says this replica has one that
// takes about input, ClearBackground that it has none, and BackgroundInput how
// long the server needs to finish what every replica has at it now. A mark a
// stopped replica leaves lapses.
type BackgroundSignal interface {
	MarkBackground(ctx context.Context, input time.Duration) error
	ClearBackground(ctx context.Context) error
	BackgroundInput(ctx context.Context) (time.Duration, error)
}

// SearchAllowance is how much longer than its own timeout a search's embed
// through p waits, because background work is at the embedding server (#2846).
//
// A CPU embedding server serves one input at a time and does not stop one it
// has begun: a background call the gate preempts still finishes the input in
// progress, so a search waits for it. A chunk near the input budget takes
// seconds on a CPU embedder, as long as a search's whole embedding timeout or
// longer, and a search that gave up then was ranked by words alone while the
// embedder was about to answer it. The allowance is the expected time of the
// background input at the server (measured by this replica's worker, or
// published by another replica's), at most MaxSearchAllowance, and zero when
// no background work is at the server or p is not the interactive side of a
// gate.
func SearchAllowance(ctx context.Context, p Provider) time.Duration {
	if g, ok := p.(*gated); ok && g.allowance != nil {
		return g.allowance(ctx)
	}
	return 0
}

// SearchAllowance reports the gate's allowance for a search's embed.
func (g *gated) SearchAllowance(ctx context.Context) time.Duration {
	if g.allowance == nil {
		return 0
	}
	return g.allowance(ctx)
}

// inputSizes is what a background call carries: its largest input and all of
// them, in bytes.
type inputSizes struct{ largest, total int }

// sizesOf measures texts.
func sizesOf(texts ...string) inputSizes {
	var sz inputSizes
	for _, t := range texts {
		sz.total += len(t)
		sz.largest = max(sz.largest, len(t))
	}
	return sz
}

// bgPublication is what this replica last published of its background calls:
// the expected input time and when, and the pending clear. Guarded by mu.
type bgPublication struct {
	mu       sync.Mutex
	input    time.Duration
	at       time.Time
	clearing *time.Timer
}

// observe records a completed background call that took took for total bytes,
// moving the measured rate toward it. A call too small to say anything about
// its input's cost is ignored.
func (g *Gate) observe(took time.Duration, total int) {
	if total < rateFloorBytes || took <= 0 {
		return
	}
	sample := took.Seconds() / float64(total)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rate == 0 {
		g.rate = sample
		return
	}
	g.rate = rateWeight*sample + (1-rateWeight)*g.rate
}

// inputTimeLocked is how long an input of n bytes is expected to hold the
// server: the measured rate with a margin, or MaxSearchAllowance before
// anything is measured. Called with mu held.
func (g *Gate) inputTimeLocked(n int) time.Duration {
	if g.rate == 0 {
		return MaxSearchAllowance
	}
	d := time.Duration(float64(n) * g.rate * allowanceMargin * float64(time.Second))
	return min(d, MaxSearchAllowance)
}

// localInput is the longest expected input among this replica's background
// calls at the server, zero when there are none.
func (g *Gate) localInput() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	var d time.Duration
	for c := range g.running {
		d = max(d, c.input)
	}
	return d
}

// backgroundSignal is the shared signal when it carries background marks.
func (g *Gate) backgroundSignal() (BackgroundSignal, bool) {
	if g.signal == nil {
		return nil, false
	}
	bs, ok := g.signal.(BackgroundSignal)
	return bs, ok
}

// searchAllowance is SearchAllowance for this gate: the longer of this
// replica's own background input at the server and what every replica
// published, at most MaxSearchAllowance. An unreadable signal leaves the
// local figure.
func (g *Gate) searchAllowance(ctx context.Context) time.Duration {
	d := g.localInput()
	if bs, ok := g.backgroundSignal(); ok {
		sctx, cancel := context.WithTimeout(ctx, signalTimeout)
		shared, err := bs.BackgroundInput(sctx)
		cancel()
		if err != nil {
			slog.DebugContext(ctx, "embedding gate: background marks unreadable; allowing for this replica's only", "error", err)
		} else {
			d = max(d, shared)
		}
	}
	return min(d, MaxSearchAllowance)
}

// publishBackground brings this replica's background mark up to date with
// the calls it has at the server. A mark is written when there was none, when
// the expected input changed by more than remarkChange, or when it is half
// its hold old; with none left, the clear waits the gate's linger, so the
// next call of a run of jobs keeps the mark instead of clearing and writing
// it again. A failure is not the call's: the embed goes ahead.
func (g *Gate) publishBackground(ctx context.Context) {
	bs, ok := g.backgroundSignal()
	if !ok {
		return
	}
	input := g.localInput()
	p := &g.bg
	p.mu.Lock()
	defer p.mu.Unlock()
	if input == 0 {
		if p.input > 0 && p.clearing == nil {
			p.clearing = time.AfterFunc(g.linger, func() { g.clearBackground(bs) })
		}
		return
	}
	if p.clearing != nil {
		p.clearing.Stop()
		p.clearing = nil
	}
	if !needsRemark(p.input, p.at, input, time.Now()) {
		return
	}
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signalTimeout)
	defer cancel()
	if err := bs.MarkBackground(mctx, input); err != nil {
		slog.DebugContext(ctx, "embedding gate: background mark not written", "error", err)
		return
	}
	p.input, p.at = input, time.Now()
}

// needsRemark reports whether a mark of published written at at no longer
// says input.
func needsRemark(published time.Duration, at time.Time, input time.Duration, now time.Time) bool {
	if published == 0 || now.Sub(at) > DefaultBackgroundHold/2 {
		return true
	}
	change := float64(input-published) / float64(published)
	return change > remarkChange || change < -remarkChange
}

// clearBackground removes this replica's mark once the linger after its last
// background call ran out with no call begun since.
func (g *Gate) clearBackground(bs BackgroundSignal) {
	p := &g.bg
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearing = nil
	if p.input == 0 || g.localInput() > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), signalTimeout)
	defer cancel()
	if err := bs.ClearBackground(ctx); err != nil {
		slog.DebugContext(ctx, "embedding gate: background mark not cleared", "error", err)
		return
	}
	p.input = 0
}
