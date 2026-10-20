package eprefusal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingAdder struct {
	mu    sync.Mutex
	added []Count
	err   error
}

func (a *recordingAdder) Add(_ context.Context, counts []Count) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.added = append(a.added, counts...)
	return nil
}

func (a *recordingAdder) total() map[string]int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]int64{}
	for _, c := range a.added {
		out[c.EndpointID+"/"+c.Reason+"/"+c.Hour.Format("15")] += c.Count
	}
	return out
}

// TestCounter_CountsByEndpointHourAndReason: refusals are counted in memory,
// one counter per endpoint, hour and reason, and added in one batch.
func TestCounter_CountsByEndpointHourAndReason(t *testing.T) {
	store := &recordingAdder{}
	c := NewCounter(store, time.Hour)
	now := time.Date(2026, 10, 19, 9, 59, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	for range 3 {
		c.Refused("e1", ReasonBotCheck)
	}
	c.Refused("e1", ReasonRateLimit)
	now = now.Add(2 * time.Minute) // the next hour
	c.Refused("e1", ReasonBotCheck)
	c.Refused("", ReasonBotCheck) // no endpoint, nothing counted

	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"e1/bot_check/09": 3, "e1/rate_limit/09": 1, "e1/bot_check/10": 1}
	got := store.total()
	if len(got) != len(want) {
		t.Fatalf("added %v; want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s = %d; want %d", k, got[k], n)
		}
	}
	if err := c.Flush(context.Background()); err != nil || len(store.added) != 3 {
		t.Errorf("a flush with nothing counted adds nothing: %v %d", err, len(store.added))
	}
}

// TestCounter_ARefusedBatchIsKept: counts the store would not take are added
// on the next flush, not lost.
func TestCounter_ARefusedBatchIsKept(t *testing.T) {
	store := &recordingAdder{err: errors.New("db down")}
	c := NewCounter(store, 0)
	c.Refused("e1", ReasonBodyLimit)
	if err := c.Flush(context.Background()); err == nil {
		t.Fatal("the store's refusal is reported")
	}
	c.Refused("e1", ReasonBodyLimit)
	store.err = nil
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, v := range store.total() {
		n += v
	}
	if n != 2 {
		t.Errorf("added %d; want both refusals", n)
	}
}

// TestCounter_StopAddsWhatIsStillCounted: a replica that stops loses no
// refusal it saw.
func TestCounter_StopAddsWhatIsStillCounted(t *testing.T) {
	store := &recordingAdder{}
	c := NewCounter(store, time.Hour)
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = c.Start(context.Background()) // a second start does nothing
	c.Refused("e1", ReasonOrigin)
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.added) != 1 || store.added[0].Reason != ReasonOrigin {
		t.Errorf("added %+v", store.added)
	}
	store.err = errors.New("db down")
	c.Refused("e1", ReasonOrigin)
	if err := c.Stop(context.Background()); err != nil {
		t.Errorf("a failed final flush is logged, not returned: %v", err)
	}
	var nilCounter *Counter
	nilCounter.Refused("e1", ReasonOrigin) // nil-safe
}
