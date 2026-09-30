package embedding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingProvider holds each call until release is closed, reporting on
// entered when a call reaches it.
type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func newBlocking() *blockingProvider {
	return &blockingProvider{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (b *blockingProvider) Embed(ctx context.Context, _ string) ([]float32, error) {
	b.calls.Add(1)
	b.entered <- struct{}{}
	select {
	case <-b.release:
		return []float32{1}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("held: %w", ctx.Err())
	}
}

func (b *blockingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	v, err := b.Embed(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = v
	}
	return out, nil
}
func (*blockingProvider) Dimension() int { return 1 }
func (*blockingProvider) Kind() string   { return "test" }

// countingProvider answers at once and counts its calls.
type countingProvider struct{ calls atomic.Int64 }

func (c *countingProvider) Embed(context.Context, string) ([]float32, error) {
	c.calls.Add(1)
	return []float32{1}, nil
}

func (c *countingProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	c.calls.Add(1)
	return make([][]float32, len(texts)), nil
}
func (*countingProvider) Dimension() int { return 1 }
func (*countingProvider) Kind() string   { return "test" }

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// TestGate_BackgroundWaitsWhileInteractiveIsInFlight is #1988's rule: with an
// interactive embed at the server, a background embed does not reach it. The
// canceled context proves the call was waiting rather than timing a sleep: a
// call that did not wait would have reached the provider.
func TestGate_BackgroundWaitsWhileInteractiveIsInFlight(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	interactive := newBlocking()
	background := &countingProvider{}
	fg, bg := g.Interactive(interactive), g.Background(background)

	done := make(chan error, 1)
	go func() {
		_, err := fg.Embed(context.Background(), "a search")
		done <- err
	}()
	<-interactive.entered

	if _, err := bg.Embed(canceled(), "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("background Embed returned %v while interactive was in flight; want it to have waited", err)
	}
	if _, err := bg.EmbedBatch(canceled(), []string{"x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("background EmbedBatch returned %v while interactive was in flight; want it to have waited", err)
	}
	if n := background.calls.Load(); n != 0 {
		t.Fatalf("background reached the provider %d times while interactive was in flight", n)
	}

	close(interactive.release)
	if err := <-done; err != nil {
		t.Fatalf("interactive: %v", err)
	}
	// Idle again: a background call goes straight through, even on a context
	// that is already done, because it has nothing to wait for.
	if _, err := bg.Embed(canceled(), "x"); err != nil {
		t.Fatalf("background after interactive finished: %v", err)
	}
	if _, err := bg.EmbedBatch(context.Background(), []string{"x", "y"}); err != nil {
		t.Fatalf("background batch: %v", err)
	}
	if n := background.calls.Load(); n != 2 {
		t.Errorf("background calls = %d, want 2", n)
	}
}

// TestGate_WaitsForEveryInteractiveCall: the gate opens when the LAST
// interactive call ends, not the first.
func TestGate_WaitsForEveryInteractiveCall(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	first, second := newBlocking(), newBlocking()
	var wg sync.WaitGroup
	for _, p := range []*blockingProvider{first, second} {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			_, _ = g.Interactive(p).EmbedBatch(context.Background(), []string{"q"})
		}(p)
	}
	<-first.entered
	<-second.entered
	close(first.release)

	bg := g.Background(&countingProvider{})
	// The second interactive call is still in flight, so this waits (and its
	// context gives up) until the second is released.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bg.Embed(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("background went ahead with an interactive call in flight: %v", err)
	}
	close(second.release)
	wg.Wait()
	if _, err := bg.Embed(context.Background(), "x"); err != nil {
		t.Fatalf("background after both finished: %v", err)
	}
}

// TestGate_BackgroundGoesAheadAfterMaxYield keeps a replica with steady
// interactive traffic indexing.
func TestGate_BackgroundGoesAheadAfterMaxYield(t *testing.T) {
	g := NewGate(time.Millisecond)
	interactive := newBlocking()
	defer close(interactive.release)
	go func() { _, _ = g.Interactive(interactive).Embed(context.Background(), "q") }()
	<-interactive.entered

	background := &countingProvider{}
	if _, err := g.Background(background).Embed(context.Background(), "x"); err != nil {
		t.Fatalf("background after max yield: %v", err)
	}
	if background.calls.Load() != 1 {
		t.Error("background did not go ahead after waiting its limit")
	}
}

// TestGate_WrapsTransparently: a wrapped provider reports what the wrapped one
// does, including the optional model name and input budget the index worker
// and the page chunker read, and nil stays nil.
func TestGate_WrapsTransparently(t *testing.T) {
	g := NewGate(0)
	if g.maxYield != DefaultMaxYield {
		t.Errorf("maxYield = %v, want the default", g.maxYield)
	}
	inner := NewOllamaProvider(OllamaConfig{Model: "nomic-embed-text", MaxInputBytes: 4000})
	for name, p := range map[string]Provider{"interactive": g.Interactive(inner), "background": g.Background(inner)} {
		if ModelName(p) != "nomic-embed-text" || MaxInputBytes(p) != 4000 ||
			p.Kind() != KindOllama || p.Dimension() != inner.Dimension() || !IsConfigured(p) {
			t.Errorf("%s: model %q bytes %d kind %q dim %d", name, ModelName(p), MaxInputBytes(p), p.Kind(), p.Dimension())
		}
	}
	noop := g.Interactive(NewNoopProvider(4))
	if IsConfigured(noop) || ModelName(noop) != "" || MaxInputBytes(noop) != DefaultMaxInputBytes {
		t.Error("a wrapped noop must still read as unconfigured, unnamed and uncapped")
	}
	if g.Interactive(nil) != nil || g.Background(nil) != nil {
		t.Error("wrapping nil must stay nil")
	}
}

// fakeSignal is a shared signal whose other replicas are scripted: others is
// whether another replica has an interactive embed in flight, and ours is what
// this replica last published.
type fakeSignal struct {
	mu       sync.Mutex
	others   bool
	ours     bool
	marks    int
	clears   int
	busyErr  error
	markErr  error
	asked    int
	onAsk    func(asked int)
	sequence []string
}

func (f *fakeSignal) Mark(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.ours, f.marks = true, f.marks+1
	f.sequence = append(f.sequence, "mark")
	return nil
}

func (f *fakeSignal) Clear(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ours, f.clears = false, f.clears+1
	f.sequence = append(f.sequence, "clear")
	return nil
}

func (f *fakeSignal) Busy(context.Context) (bool, error) {
	f.mu.Lock()
	f.asked++
	asked, onAsk := f.asked, f.onAsk
	f.mu.Unlock()
	if onAsk != nil {
		onAsk(asked)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.others || f.ours, f.busyErr
}

// TestGate_PublishesInteractiveEmbedsToTheOtherReplicas: the first interactive
// call marks the shared signal, the last one to end clears it, and calls in
// between publish nothing.
func TestGate_PublishesInteractiveEmbedsToTheOtherReplicas(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	sig := &fakeSignal{}
	g.Share(sig, time.Millisecond)
	first, second := newBlocking(), newBlocking()
	var wg sync.WaitGroup
	for _, p := range []*blockingProvider{first, second} {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			_, _ = g.Interactive(p).Embed(context.Background(), "q")
		}(p)
	}
	<-first.entered
	<-second.entered
	sig.mu.Lock()
	if !sig.ours || sig.marks != 1 {
		t.Errorf("two interactive calls in flight: published %v after %d marks, want one mark", sig.ours, sig.marks)
	}
	sig.mu.Unlock()
	close(first.release)
	close(second.release)
	wg.Wait()
	if sig.ours || sig.clears != 1 || strings.Join(sig.sequence, ",") != "mark,clear" {
		t.Errorf("after both ended: published %v, sequence %v; want one mark then one clear", sig.ours, sig.sequence)
	}
}

// TestGate_BackgroundWaitsForAnotherReplica is the deployment-wide half of
// #1988: with nothing in flight here, a background call still waits while
// another replica reports an interactive embed, and goes ahead once it stops.
func TestGate_BackgroundWaitsForAnotherReplica(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	sig := &fakeSignal{others: true}
	// The other replica's embed ends on the third time this one asks.
	sig.onAsk = func(asked int) {
		if asked == 3 {
			sig.mu.Lock()
			sig.others = false
			sig.mu.Unlock()
		}
	}
	g.Share(sig, time.Millisecond)
	background := &countingProvider{}
	if _, err := g.Background(background).Embed(context.Background(), "x"); err != nil {
		t.Fatalf("background: %v", err)
	}
	if sig.asked != 3 || background.calls.Load() != 1 {
		t.Errorf("asked %d times and called %d; want to wait until the third answer", sig.asked, background.calls.Load())
	}

	// While another replica is busy, a canceled context proves the call waited.
	sig.mu.Lock()
	sig.others, sig.onAsk = true, nil
	sig.mu.Unlock()
	if _, err := g.Background(background).Embed(canceled(), "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("background went ahead while another replica was busy: %v", err)
	}
}

// TestGate_SharedWaitIsBoundedAndNeverBlocksWork: the shared wait ends at
// maxYield, an unreadable signal is taken as idle, and a failed mark does not
// fail the interactive embed.
func TestGate_SharedWaitIsBoundedAndNeverBlocksWork(t *testing.T) {
	g := NewGate(time.Millisecond)
	g.Share(&fakeSignal{others: true}, time.Hour)
	if _, err := g.Background(&countingProvider{}).Embed(context.Background(), "x"); err != nil {
		t.Fatalf("background after max yield: %v", err)
	}

	unreadable := NewGate(time.Hour)
	unreadable.linger = 0
	unreadable.Share(&fakeSignal{others: true, busyErr: errors.New("db down")}, 0)
	if unreadable.poll != DefaultSharedPoll {
		t.Errorf("poll = %v, want the default", unreadable.poll)
	}
	if _, err := unreadable.Background(&countingProvider{}).Embed(context.Background(), "x"); err != nil {
		t.Fatalf("background with an unreadable signal: %v", err)
	}

	failing := NewGate(time.Hour)
	failing.linger = 0
	g.linger = 0
	sig := &fakeSignal{markErr: errors.New("db down")}
	failing.Share(sig, time.Millisecond)
	if _, err := failing.Interactive(&countingProvider{}).Embed(context.Background(), "q"); err != nil {
		t.Fatalf("an interactive embed failed with the signal: %v", err)
	}
	if sig.clears != 0 {
		t.Errorf("a mark that never landed was cleared %d times", sig.clears)
	}
}

// TestGate_ABurstOfInteractiveEmbedsReadsAsOne is the gap #1988's acceptance
// run found: a search embeds once per source, one after another, and a
// background call waiting between two of them must not go to the embedding
// server in the gap. The gate stays interactive for the linger after the last
// call, and a call that begins within it keeps it so.
func TestGate_ABurstOfInteractiveEmbedsReadsAsOne(t *testing.T) {
	g := NewGate(time.Hour)
	if g.linger != DefaultLinger {
		t.Fatalf("linger = %v, want the default", g.linger)
	}
	g.linger = time.Hour
	sig := &fakeSignal{}
	g.Share(sig, time.Millisecond)
	fg, bg := g.Interactive(&countingProvider{}), g.Background(&countingProvider{})

	for range 3 {
		if _, err := fg.Embed(context.Background(), "one source's embed"); err != nil {
			t.Fatal(err)
		}
		// Between two embeds of the burst, a background call still waits.
		if _, err := bg.Embed(canceled(), "x"); !errors.Is(err, context.Canceled) {
			t.Fatalf("background went ahead in the gap between two interactive embeds: %v", err)
		}
	}
	if sig.marks != 1 || sig.clears != 0 {
		t.Errorf("a burst published %d marks and %d clears, want one mark and no clear yet", sig.marks, sig.clears)
	}

	// The linger runs out with nothing new begun: the gate settles.
	g.mu.Lock()
	timer := g.settling
	g.mu.Unlock()
	if timer == nil {
		t.Fatal("no linger pending after the burst")
	}
	g.settle()
	if _, err := bg.Embed(context.Background(), "x"); err != nil {
		t.Fatalf("background after the linger: %v", err)
	}
	if sig.clears != 1 || sig.ours {
		t.Errorf("after the linger: %d clears, published %v; want the gate cleared", sig.clears, sig.ours)
	}
	// A settle that finds a call in flight, or nothing to settle, changes nothing.
	g.settle()
	if sig.clears != 1 {
		t.Errorf("a second settle published again")
	}
}

// TestGate_ACallWithinTheLingerKeepsTheGateInteractive: a timer that fires
// while a new interactive call is in flight does not settle the gate.
func TestGate_ACallWithinTheLingerKeepsTheGateInteractive(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = time.Hour
	held := newBlocking()
	if _, err := g.Interactive(&countingProvider{}).Embed(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.Interactive(held).Embed(context.Background(), "q")
	}()
	<-held.entered
	g.settle() // the old linger's timer, firing late
	if _, err := g.Background(&countingProvider{}).Embed(canceled(), "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("the gate settled with an interactive call in flight: %v", err)
	}
	close(held.release)
	<-done
}
