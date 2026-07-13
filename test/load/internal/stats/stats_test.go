package stats

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	// 1..10 ms.
	sorted := make([]time.Duration, 10)
	for i := range sorted {
		sorted[i] = time.Duration(i+1) * time.Millisecond
	}
	cases := []struct {
		p    float64
		want time.Duration
	}{
		{0.50, 5 * time.Millisecond},  // ceil(5.0)=5 -> index 4 -> 5ms
		{0.95, 10 * time.Millisecond}, // ceil(9.5)=10 -> 10ms
		{0.99, 10 * time.Millisecond}, // ceil(9.9)=10 -> 10ms
		{0.10, 1 * time.Millisecond},  // ceil(1.0)=1 -> 1ms
	}
	for _, tc := range cases {
		if got := percentile(sorted, tc.p); got != tc.want {
			t.Errorf("percentile(%.2f) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestPercentileEdgeCases(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile(nil) = %v, want 0", got)
	}
	one := []time.Duration{7 * time.Millisecond}
	if got := percentile(one, 0.99); got != 7*time.Millisecond {
		t.Errorf("percentile(single, 0.99) = %v, want 7ms", got)
	}
	if got := percentile(one, 0.0); got != 7*time.Millisecond {
		t.Errorf("percentile clamps rank to >=1, got %v", got)
	}
}

func TestSummarizeThroughputAndErrorRate(t *testing.T) {
	m := NewMultiRecorder()
	boom := errors.New("boom")
	// 4 samples of "search": 3 ok, 1 error, latencies 10,20,30,40 ms.
	m.Record("search", 10*time.Millisecond, nil)
	m.Record("search", 20*time.Millisecond, nil)
	m.Record("search", 30*time.Millisecond, nil)
	m.Record("search", 40*time.Millisecond, boom)
	// A second op so ordering is exercised.
	m.Record("trino_query", 5*time.Millisecond, nil)

	got := m.Summarize(2 * time.Second)
	if len(got) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(got))
	}
	// Sorted by name: search before trino_query.
	s := got[0]
	if s.Operation != "search" {
		t.Fatalf("first op = %q, want search", s.Operation)
	}
	if s.Count != 4 || s.Errors != 1 {
		t.Errorf("count=%d errors=%d, want 4/1", s.Count, s.Errors)
	}
	if s.ErrorRate != 0.25 {
		t.Errorf("error rate = %v, want 0.25", s.ErrorRate)
	}
	if s.ThroughputPerSec != 2.0 { // 4 samples / 2s
		t.Errorf("throughput = %v, want 2.0", s.ThroughputPerSec)
	}
	if s.MinMs != 10 || s.MaxMs != 40 || s.MeanMs != 25 {
		t.Errorf("min/mean/max = %v/%v/%v, want 10/25/40", s.MinMs, s.MeanMs, s.MaxMs)
	}
	if s.P50Ms != 20 { // ceil(0.5*4)=2 -> index1 -> 20ms
		t.Errorf("p50 = %v, want 20", s.P50Ms)
	}
}

func TestResetDiscardsWarmup(t *testing.T) {
	m := NewMultiRecorder()
	m.Record("op", time.Millisecond, nil)
	m.Reset()
	if got := m.Summarize(time.Second); len(got) != 0 {
		t.Fatalf("expected empty after reset, got %d ops", len(got))
	}
}

func TestRecordConcurrent(t *testing.T) {
	m := NewMultiRecorder()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				m.Record("op", time.Millisecond, nil)
			}
		})
	}
	wg.Wait()
	got := m.Summarize(time.Second)
	if got[0].Count != 800 {
		t.Fatalf("count = %d, want 800", got[0].Count)
	}
}
