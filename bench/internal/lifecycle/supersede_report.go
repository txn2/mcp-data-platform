package lifecycle

import (
	"encoding/json"
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- bootstrap-CI resampling for benchmark statistics; not security-sensitive, and a seedable PRNG is required for reproducible intervals.
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/stats"
)

// SupersedeResults is the isolated supersede sub-benchmark output (issue #964).
// It reuses ProtocolRun as the per-attempt record — each attempt runs the same
// teach/capture and supersede stages the S5 suite uses — but scores only the
// supersede-relevant outcomes and adds a per-protocol stability breakdown, so
// the 0%-vs-42.9% duplicate-rate instability the phase-4 data exposed is visible
// per protocol rather than hidden in one blended range.
type SupersedeResults struct {
	Manifest Manifest         `json:"manifest"`
	Runs     []ProtocolRun    `json:"runs"`
	Metrics  SupersedeMetrics `json:"metrics"`
}

// SupersedeMetrics is the focused supersede scorecard. SupersedeRate and
// DuplicateRate are complements over the same denominator (captured attempts
// whose supersede ran); both are reported because a reader scans for either the
// success or the failure framing.
type SupersedeMetrics struct {
	Protocols       int `json:"protocols"`
	Attempts        int `json:"attempts"`
	HarnessFailures int `json:"harness_failures"`

	TotalInputTokens         int64 `json:"total_input_tokens"`
	TotalOutputTokens        int64 `json:"total_output_tokens"`
	TotalCacheReadTokens     int64 `json:"total_cache_read_tokens"`
	TotalCacheCreationTokens int64 `json:"total_cache_creation_tokens"`

	CaptureRate       Rate `json:"capture_rate"`
	SupersedeRate     Rate `json:"supersede_rate"`     // among captured attempts, the original was superseded (higher is better)
	DuplicateRate     Rate `json:"duplicate_rate"`     // among captured attempts, a duplicate was left (lower is better)
	UpdateCorrectness Rate `json:"update_correctness"` // post-correction recall flipped to the new value

	PassK       Rate                    `json:"pass_k"`       // protocols cleanly superseding on all k attempts
	PerProtocol []SupersedeProtocolStat `json:"per_protocol"` // per-protocol stability across the k attempts
}

// SupersedeProtocolStat is one protocol's outcome counts across its k attempts.
// Superseded+Duplicated over a captured attempt count below k exposes exactly
// the instability the sub-benchmark targets (e.g. 2/3 superseded, 1/3 duplicated
// on identical inputs).
type SupersedeProtocolStat struct {
	ProtocolID string `json:"protocol_id"`
	Title      string `json:"title"`
	Attempts   int    `json:"attempts"`
	Captured   int    `json:"captured"`
	Superseded int    `json:"superseded"`
	Duplicated int    `json:"duplicated"`
}

// Aggregate computes the supersede metrics from the runs.
func (res *SupersedeResults) Aggregate() {
	m := SupersedeMetrics{}
	byProtocol := map[string]*SupersedeProtocolStat{}
	var order []string
	for i := range res.Runs {
		r := res.Runs[i]
		stat, ok := byProtocol[r.ProtocolID]
		if !ok {
			order = append(order, r.ProtocolID)
			stat = &SupersedeProtocolStat{ProtocolID: r.ProtocolID, Title: r.Title}
			byProtocol[r.ProtocolID] = stat
		}
		for _, e := range r.Episodes {
			m.TotalInputTokens += e.InputTokens
			m.TotalOutputTokens += e.OutputTokens
			m.TotalCacheReadTokens += e.CacheReadTokens
			m.TotalCacheCreationTokens += e.CacheCreationTokens
		}
		if r.Error != "" {
			m.HarnessFailures++
			continue
		}
		m.Attempts++
		foldSupersedeStat(stat, r)
		m.CaptureRate.add(r.Captured)
		m.DuplicateRate.add(r.Duplicated)
		m.UpdateCorrectness.add(r.UpdateCorrect)
	}
	// SupersedeRate is the complement of DuplicateRate over the same denominator
	// (captured attempts whose supersede ran), derived once so the two can never
	// drift out of the complement relationship the report advertises.
	m.SupersedeRate = m.DuplicateRate.complement()
	m.Protocols = len(order)
	m.PassK = supersedePassKRate(order, res.Runs, res.Manifest.K)
	for _, id := range order {
		m.PerProtocol = append(m.PerProtocol, *byProtocol[id])
	}
	m.fillCIs(stats.NewRNG())
	res.Metrics = m
}

// fillCIs attaches a bootstrap confidence interval to every supersede rate,
// threading one seeded RNG in a fixed order so the sub-benchmark is reproducible
// (issue #965). SupersedeRate is the exact point-complement of DuplicateRate
// (set in Aggregate), so rather than bootstrapping it independently — which
// would drift its interval out of the advertised complement relationship by a
// resampling-quantile step — its interval is the reflection of DuplicateRate's:
// [1 - dupHigh, 1 - dupLow]. This mirrors how the point rate is derived, so the
// two intervals stay exact complements. When nothing was captured (empty
// denominator) DuplicateRate has no interval, so SupersedeRate keeps its
// zero-width default rather than reflecting into a spurious [1, 1].
func (m *SupersedeMetrics) fillCIs(rng *rand.Rand) {
	for _, r := range []*Rate{
		&m.CaptureRate, &m.DuplicateRate, &m.UpdateCorrectness, &m.PassK,
	} {
		r.fillCI(rng)
	}
	if m.DuplicateRate.Den > 0 {
		m.SupersedeRate.CILow = 1 - m.DuplicateRate.CIHigh
		m.SupersedeRate.CIHigh = 1 - m.DuplicateRate.CILow
	}
}

// foldSupersedeStat accumulates one graded attempt into its protocol's stability
// counts.
func foldSupersedeStat(stat *SupersedeProtocolStat, r ProtocolRun) {
	stat.Attempts++
	if boolTrue(r.Captured) {
		stat.Captured++
	}
	if r.Duplicated != nil {
		if *r.Duplicated {
			stat.Duplicated++
		} else {
			stat.Superseded++
		}
	}
}

// supersedePassKRate is the fraction of protocols whose every one of the k
// attempts captured, superseded cleanly (no duplicate), and flipped recall.
func supersedePassKRate(order []string, runs []ProtocolRun, k int) Rate {
	byProtocol := map[string][]ProtocolRun{}
	for _, r := range runs {
		byProtocol[r.ProtocolID] = append(byProtocol[r.ProtocolID], r)
	}
	r := Rate{Den: len(order)}
	for _, id := range order {
		attempts := byProtocol[id]
		if len(attempts) != k {
			continue
		}
		all := true
		for _, a := range attempts {
			if !supersedePassed(a) {
				all = false
				break
			}
		}
		if all {
			r.Num++
		}
	}
	if r.Den > 0 {
		r.Rate = float64(r.Num) / float64(r.Den)
	}
	return r
}

// supersedePassed reports whether one attempt cleanly superseded: captured the
// fact, left no duplicate, and flipped the post-correction recall.
func supersedePassed(r ProtocolRun) bool {
	return r.Error == "" && boolTrue(r.Captured) && !boolTrue(r.Duplicated) && boolTrue(r.UpdateCorrect)
}

// WriteJSON persists the supersede results.
func (res *SupersedeResults) WriteJSON(path string) error {
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal supersede results: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write supersede results: %w", err)
	}
	return nil
}

// LoadSupersedeJSON reads results written by SupersedeResults.WriteJSON.
func LoadSupersedeJSON(path string) (*SupersedeResults, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied results path
	if err != nil {
		return nil, fmt.Errorf("read supersede results: %w", err)
	}
	var res SupersedeResults
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse supersede results: %w", err)
	}
	return &res, nil
}

// HumanSummary renders the supersede sub-benchmark for a terminal.
func (res *SupersedeResults) HumanSummary() string {
	var b strings.Builder
	m := res.Manifest
	provider := m.LLMProvider
	if m.ClientVersion != "" {
		provider = fmt.Sprintf("%s via %s", m.LLMProvider, m.ClientVersion)
	}
	fmt.Fprintf(&b, "bench supersede (S5 isolated): arm=%s model=%s (%s) k=%d\n", m.Arm, m.Model, provider, m.K)
	fmt.Fprintf(&b, "  platform %s @ %s | commit %s | seed %d | protocols %s\n",
		m.PlatformVersion, m.Target, short(m.GitCommit), m.Seed, short(m.ProtocolSetHash))
	fmt.Fprintf(&b, "  %s .. %s\n\n", m.StartedAt.Format(time.RFC3339), m.FinishedAt.Format(time.RFC3339))
	mt := res.Metrics
	fmt.Fprintf(&b, "protocols %d  attempts %d  harness failures %d\n", mt.Protocols, mt.Attempts, mt.HarnessFailures)
	fmt.Fprintf(&b, "tokens: input %d  output %d  cache read %d  cache write %d (apply current model pricing for cost)\n\n",
		mt.TotalInputTokens, mt.TotalOutputTokens, mt.TotalCacheReadTokens, mt.TotalCacheCreationTokens)
	writeMetric(&b, "capture rate", mt.CaptureRate)
	writeMetric(&b, "supersede rate", mt.SupersedeRate)
	writeMetric(&b, "duplicate rate", mt.DuplicateRate)
	writeMetric(&b, "update correctness", mt.UpdateCorrectness)
	writeMetric(&b, "pass^k (protocols)", mt.PassK)
	if len(mt.PerProtocol) > 0 {
		b.WriteString("\nper-protocol stability (superseded/duplicated of captured attempts):\n")
		for _, s := range mt.PerProtocol {
			fmt.Fprintf(&b, "  %-24s cap %d/%d  superseded %d  duplicated %d\n",
				s.ProtocolID, s.Captured, s.Attempts, s.Superseded, s.Duplicated)
		}
	}
	return b.String()
}
