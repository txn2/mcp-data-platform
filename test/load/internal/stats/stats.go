// Package stats aggregates per-operation latency samples into throughput and
// percentile summaries for the load harness. It is concurrency-safe: many
// worker goroutines record into one MultiRecorder while a phase runs.
package stats

import (
	"maps"
	"slices"
	"sort"
	"sync"
	"time"
)

// MultiRecorder collects latency samples partitioned by operation name (for
// example "search" and "trino_query"). A single tool-call iteration may record
// several operations.
type MultiRecorder struct {
	mu  sync.Mutex
	ops map[string]*opSamples
}

type opSamples struct {
	durations []time.Duration
	errors    int
	reasons   map[string]int
}

// MaxErrorReasons bounds the distinct failure texts one operation keeps. An
// error that carries a varying value (an id, a duration) would otherwise grow
// the map with every call; past the bound, further new texts are counted under
// OtherErrorReason so the counts still sum to the operation's errors.
const MaxErrorReasons = 20

// OtherErrorReason is the key failures past MaxErrorReasons are counted under.
const OtherErrorReason = "(other)"

// NewMultiRecorder returns an empty recorder.
func NewMultiRecorder() *MultiRecorder {
	return &MultiRecorder{ops: make(map[string]*opSamples)}
}

// Record adds one latency sample for op. A non-nil err marks the sample as a
// failed call; its latency is still recorded so error-path latency is visible.
func (m *MultiRecorder) Record(op string, d time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.ops[op]
	if s == nil {
		s = &opSamples{}
		m.ops[op] = s
	}
	s.durations = append(s.durations, d)
	if err != nil {
		s.errors++
		s.countReason(err.Error())
	}
}

// countReason counts one failure under its text, within MaxErrorReasons.
func (s *opSamples) countReason(text string) {
	if s.reasons == nil {
		s.reasons = make(map[string]int)
	}
	if _, seen := s.reasons[text]; !seen && len(s.reasons) >= MaxErrorReasons {
		text = OtherErrorReason
	}
	s.reasons[text]++
}

// Reset drops all recorded samples. Used to discard the warmup phase before the
// measured window begins.
func (m *MultiRecorder) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops = make(map[string]*opSamples)
}

// OpSummary is the aggregated result for one operation over a measured window.
type OpSummary struct {
	Operation        string  `json:"operation"`
	Count            int     `json:"count"`
	Errors           int     `json:"errors"`
	ErrorRate        float64 `json:"error_rate"`
	ThroughputPerSec float64 `json:"throughput_per_sec"`
	MinMs            float64 `json:"min_ms"`
	MeanMs           float64 `json:"mean_ms"`
	P50Ms            float64 `json:"p50_ms"`
	P95Ms            float64 `json:"p95_ms"`
	P99Ms            float64 `json:"p99_ms"`
	MaxMs            float64 `json:"max_ms"`
	// ErrorReasons counts the operation's failures by what each said: the HTTP
	// status a scenario reports as "status 403", or the error's text (#2729).
	// Absent when nothing failed.
	ErrorReasons map[string]int `json:"error_reasons,omitempty"`
}

// Summarize aggregates every operation over a measured window of length wall.
// Throughput is count/wall-seconds. Percentiles use the nearest-rank method on
// the sorted sample set. Returns operations sorted by name for stable output.
func (m *MultiRecorder) Summarize(wall time.Duration) []OpSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.ops))
	for name := range m.ops {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]OpSummary, 0, len(names))
	for _, name := range names {
		out = append(out, summarizeOp(name, m.ops[name], wall))
	}
	return out
}

func summarizeOp(name string, s *opSamples, wall time.Duration) OpSummary {
	sum := OpSummary{Operation: name, Count: len(s.durations), Errors: s.errors}
	if len(s.reasons) > 0 {
		sum.ErrorReasons = maps.Clone(s.reasons)
	}
	if sum.Count == 0 {
		return sum
	}
	if wall > 0 {
		sum.ThroughputPerSec = float64(sum.Count) / wall.Seconds()
	}
	sum.ErrorRate = float64(s.errors) / float64(sum.Count)

	sorted := make([]time.Duration, len(s.durations))
	copy(sorted, s.durations)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	sum.MinMs = ms(sorted[0])
	sum.MaxMs = ms(sorted[len(sorted)-1])
	sum.MeanMs = ms(total / time.Duration(len(sorted)))
	sum.P50Ms = ms(percentile(sorted, 0.50))
	sum.P95Ms = ms(percentile(sorted, 0.95))
	sum.P99Ms = ms(percentile(sorted, 0.99))
	return sum
}

// percentile returns the p-quantile (0<p<=1) of a non-empty ascending slice
// using the nearest-rank method: rank = ceil(p*N), clamped to [1,N].
func percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := max(1, min(int(float64(n)*p+0.9999999), n))
	return sorted[rank-1]
}

// ms converts a duration to fractional milliseconds.
func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
