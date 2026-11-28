package embedding

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackgroundSignal is a shared signal that also carries background marks:
// ours is this replica's, others what the other replicas published.
type fakeBackgroundSignal struct {
	fakeSignal
	bmu     sync.Mutex
	ours    time.Duration
	others  time.Duration
	marked  []time.Duration
	cleared int
	readErr error
	markErr error
}

func (f *fakeBackgroundSignal) MarkBackground(_ context.Context, input time.Duration) error {
	f.bmu.Lock()
	defer f.bmu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.ours = input
	f.marked = append(f.marked, input)
	return nil
}

func (f *fakeBackgroundSignal) ClearBackground(context.Context) error {
	f.bmu.Lock()
	defer f.bmu.Unlock()
	f.ours = 0
	f.cleared++
	return nil
}

func (f *fakeBackgroundSignal) BackgroundInput(context.Context) (time.Duration, error) {
	f.bmu.Lock()
	defer f.bmu.Unlock()
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.ours + f.others, nil
}

func (f *fakeBackgroundSignal) state() (ours time.Duration, marks, clears int) {
	f.bmu.Lock()
	defer f.bmu.Unlock()
	return f.ours, len(f.marked), f.cleared
}

// TestSearchAllowance_ABackgroundInputAtTheServerIsWaitedFor is #2846: with a
// background call at the server, a search's embed is allowed the time its
// largest input is expected to take at the measured rate, with the margin;
// with none, nothing.
func TestSearchAllowance_ABackgroundInputAtTheServerIsWaitedFor(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	held := newBlocking()
	bg, fg := g.Background(held), g.Interactive(&countingProvider{})
	if d := SearchAllowance(context.Background(), fg); d != 0 {
		t.Fatalf("with nothing at the server the allowance is %s, want 0", d)
	}
	// A measured 2s for 4,000 bytes is half a millisecond a byte.
	g.observe(2*time.Second, 4000)

	done := make(chan error, 1)
	go func() {
		_, err := bg.EmbedBatch(context.Background(), []string{strings.Repeat("a", 6000), "short"})
		done <- err
	}()
	<-held.entered
	want := time.Duration(6000 * 0.0005 * allowanceMargin * float64(time.Second))
	if d := SearchAllowance(context.Background(), fg); d != want {
		t.Errorf("allowance with a 6,000-byte input at the server = %s, want %s", d, want)
	}
	close(held.release)
	if err := <-done; err != nil {
		t.Fatalf("background: %v", err)
	}
	if d := SearchAllowance(context.Background(), fg); d != 0 {
		t.Errorf("after the call the allowance is %s, want 0", d)
	}
}

// TestSearchAllowance_BeforeAnythingIsMeasuredItIsTheCap: a replica that has
// measured no background call allows the most, not nothing.
func TestSearchAllowance_BeforeAnythingIsMeasuredItIsTheCap(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	held := newBlocking()
	bg, fg := g.Background(held), g.Interactive(&countingProvider{})
	done := make(chan error, 1)
	go func() {
		_, err := bg.Embed(context.Background(), "x")
		done <- err
	}()
	<-held.entered
	if d := SearchAllowance(context.Background(), fg); d != MaxSearchAllowance {
		t.Errorf("allowance before any measurement = %s, want %s", d, MaxSearchAllowance)
	}
	close(held.release)
	<-done
}

// TestSearchAllowance_IsCapped: a measured rate that would allow more than
// MaxSearchAllowance allows MaxSearchAllowance.
func TestSearchAllowance_IsCapped(t *testing.T) {
	g := NewGate(time.Hour)
	g.observe(time.Minute, 1000)
	g.mu.Lock()
	d := g.inputTimeLocked(6000)
	g.mu.Unlock()
	if d != MaxSearchAllowance {
		t.Errorf("input time = %s, want the cap %s", d, MaxSearchAllowance)
	}
}

// TestGate_ObserveMovesTowardEachMeasurementAndIgnoresSmallCalls: a call
// below rateFloorBytes, or that took nothing, leaves the rate; a later
// measurement moves it halfway.
func TestGate_ObserveMovesTowardEachMeasurementAndIgnoresSmallCalls(t *testing.T) {
	g := NewGate(time.Hour)
	g.observe(time.Second, rateFloorBytes-1)
	g.observe(0, 5000)
	if g.rate != 0 {
		t.Fatalf("rate after ignored calls = %v, want 0", g.rate)
	}
	g.observe(time.Second, 1000)
	g.observe(3*time.Second, 1000)
	if want := 0.002; g.rate < want-1e-12 || g.rate > want+1e-12 {
		t.Errorf("rate = %v, want %v", g.rate, want)
	}
}

// TestGate_OnlyCompletedCallsAreMeasured: a call sent again after its
// preemption and completed is measured; a call the server refused is not.
func TestGate_OnlyCompletedCallsAreMeasured(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 0
	slow := newPreemptedOnce()
	bg, fg := g.Background(slow), g.Interactive(&countingProvider{})
	done := make(chan error, 1)
	go func() {
		_, err := bg.Embed(context.Background(), strings.Repeat("a", 2000))
		done <- err
	}()
	<-slow.entered
	if _, err := fg.Embed(context.Background(), "q"); err != nil {
		t.Fatalf("interactive: %v", err)
	}
	<-slow.cut
	if err := <-done; err != nil {
		t.Fatalf("background: %v", err)
	}
	g.mu.Lock()
	measured := g.rate > 0
	g.mu.Unlock()
	if !measured {
		t.Errorf("the call sent again after its preemption completed and was not measured")
	}

	g2 := NewGate(time.Hour)
	_, _ = g2.Background(failingProvider{}).Embed(context.Background(), strings.Repeat("a", 2000))
	if g2.rate != 0 {
		t.Errorf("a failed call set the rate to %v", g2.rate)
	}
}

// TestSearchAllowance_AnotherReplicasInputIsWaitedFor: with the signal
// carrying background marks, a search on this replica is allowed the time the
// other replicas' inputs at the server take, though this one has none.
func TestSearchAllowance_AnotherReplicasInputIsWaitedFor(t *testing.T) {
	g := NewGate(time.Hour)
	sig := &fakeBackgroundSignal{others: 7 * time.Second}
	g.Share(sig, time.Millisecond)
	fg := g.Interactive(&countingProvider{})
	if d := SearchAllowance(context.Background(), fg); d != 7*time.Second {
		t.Errorf("allowance = %s, want the other replica's 7s", d)
	}
	sig.others = time.Hour
	if d := SearchAllowance(context.Background(), fg); d != MaxSearchAllowance {
		t.Errorf("allowance = %s, want the cap", d)
	}
	sig.readErr = errors.New("database gone")
	if d := SearchAllowance(context.Background(), fg); d != 0 {
		t.Errorf("with the marks unreadable and nothing local the allowance is %s, want 0", d)
	}
}

// TestGate_PublishesItsBackgroundInputAndClearsItAfterTheLinger: a background
// call marks its expected input for the other replicas; a run of calls keeps
// one mark; the mark is cleared once the last call ended and the linger ran
// out.
func TestGate_PublishesItsBackgroundInputAndClearsItAfterTheLinger(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = 20 * time.Millisecond
	sig := &fakeBackgroundSignal{}
	g.Share(sig, time.Hour)
	g.observe(time.Second, 2000)
	bg := g.Background(&countingProvider{})
	// Inputs under rateFloorBytes leave the measured rate where it is.
	for range 3 {
		if _, err := bg.Embed(context.Background(), strings.Repeat("a", 900)); err != nil {
			t.Fatalf("background: %v", err)
		}
	}
	if _, marks, clears := sig.state(); marks != 1 || clears != 0 {
		t.Errorf("after a run of three calls: %d marks, %d clears; want 1 and 0", marks, clears)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ours, _, clears := sig.state()
		if clears == 1 && ours == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mark was not cleared after the linger: ours %s, %d clears", ours, clears)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestGate_AChangedInputIsMarkedAgainAndAFailedMarkIsRetried: the mark follows
// a call whose input differs by more than remarkChange, and a mark that was
// not written is written on the next call.
func TestGate_AChangedInputIsMarkedAgainAndAFailedMarkIsRetried(t *testing.T) {
	g := NewGate(time.Hour)
	g.linger = time.Hour
	sig := &fakeBackgroundSignal{markErr: errors.New("down")}
	g.Share(sig, time.Hour)
	g.observe(time.Second, 2000)
	bg := g.Background(&countingProvider{})
	// Inputs under rateFloorBytes leave the measured rate where it is; the
	// last call's mark is written before it is measured.
	_, _ = bg.Embed(context.Background(), strings.Repeat("a", 900))
	if _, marks, _ := sig.state(); marks != 0 {
		t.Fatalf("a failed mark was recorded")
	}
	sig.bmu.Lock()
	sig.markErr = nil
	sig.bmu.Unlock()
	_, _ = bg.Embed(context.Background(), strings.Repeat("a", 900))
	_, _ = bg.Embed(context.Background(), strings.Repeat("a", 950))
	_, _ = bg.Embed(context.Background(), strings.Repeat("a", 2700))
	if _, marks, _ := sig.state(); marks != 2 {
		t.Errorf("marks = %d, want 2 (the retried mark, then the tripled input)", marks)
	}
}

// TestNeedsRemark covers when a published mark is written again.
func TestNeedsRemark(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		published time.Duration
		at        time.Time
		input     time.Duration
		want      bool
	}{
		{"none published", 0, time.Time{}, time.Second, true},
		{"same", time.Second, now, time.Second, false},
		{"slightly longer", time.Second, now, 1200 * time.Millisecond, false},
		{"much longer", time.Second, now, 2 * time.Second, true},
		{"much shorter", time.Second, now, 500 * time.Millisecond, true},
		{"half its hold old", time.Second, now.Add(-DefaultBackgroundHold), time.Second, true},
	}
	for _, tc := range cases {
		if got := needsRemark(tc.published, tc.at, tc.input, now); got != tc.want {
			t.Errorf("%s: needsRemark = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSearchAllowance_OutsideAGateIsZero: a provider that is not the
// interactive side of a gate allows nothing.
func TestSearchAllowance_OutsideAGateIsZero(t *testing.T) {
	if d := SearchAllowance(context.Background(), &countingProvider{}); d != 0 {
		t.Errorf("allowance = %s, want 0", d)
	}
	if d := (&gated{}).SearchAllowance(context.Background()); d != 0 {
		t.Errorf("a gated provider with no gate allows %s", d)
	}
	g := NewGate(time.Hour)
	fg, ok := g.Interactive(&countingProvider{}).(*gated)
	if !ok {
		t.Fatal("the interactive side is not a gated provider")
	}
	if d := fg.SearchAllowance(context.Background()); d != 0 {
		t.Errorf("an idle gate allows %s", d)
	}
}

// failingProvider refuses every call.
type failingProvider struct{}

func (failingProvider) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("refused")
}

func (failingProvider) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("refused")
}
func (failingProvider) Dimension() int { return 1 }
func (failingProvider) Kind() string   { return "test" }
