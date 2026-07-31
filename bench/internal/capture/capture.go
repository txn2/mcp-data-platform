// Package capture holds the knowledge-capture instrumentation shared by the S5
// lifecycle suite and the cold-start suite (issue #1136). Capture caps every
// downstream metric — an insight that was never recorded can be neither recalled
// nor promoted — so both suites report it as a headline rate and must attribute
// every miss to a cause rather than leaving it a bare count.
//
// Both the attempt signal and the miss attribution are derived from the
// provider-agnostic transcript, so the in-process agent loop and the claude-cli
// path cannot diverge on what "the agent called capture" means, and the two
// suites cannot diverge on what a capture miss is.
package capture

import (
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- bootstrap-CI resampling for benchmark statistics; not security-sensitive, and a seedable PRNG is required for reproducible intervals.
	"math/rand"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/agent"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/stats"
)

// Attempted reports whether an episode actually executed a knowledge-capture
// call. A capture request the budget refused (emitted only after the tool-call
// budget was spent, so it never ran) does NOT count: it is a budget-starvation
// miss, not an attempt that failed to land. This keeps the attribution honest —
// an agent that only reaches for capture after burning its budget on discovery
// is starved, not a landing failure.
func Attempted(msgs []llm.Message) bool {
	captureIDs := map[string]bool{}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if IsTool(c.Name) {
				captureIDs[c.ID] = true
			}
		}
	}
	if len(captureIDs) == 0 {
		return false
	}
	for _, m := range msgs {
		for _, r := range m.ToolResults {
			// A capture call ran when its paired result is present and is not the
			// budget-refusal sentinel. A server-side capture error still counts as
			// executed (that is a landing failure, a different bucket).
			if captureIDs[r.CallID] && r.Text != agent.BudgetRefusalText {
				return true
			}
		}
	}
	return false
}

// IsTool reports whether a tool name is the knowledge-capture tool. The suffix
// match tolerates a renamed or namespaced capture tool without silently
// classifying every teach episode as a capture miss.
func IsTool(name string) bool {
	return name == "memory_capture" || strings.HasSuffix(name, "_capture")
}

// Cause is the attributed cause of one capture miss. Every graded miss carries
// exactly one, which is the point of the decomposition: a miss that "never
// attempted" points at the model or its steering, one that is budget-starved
// points at the harness budget, and one that attempted and failed points at the
// capture path itself. The three imply different fixes in different layers, so
// an unattributed miss cannot motivate any of them.
type Cause string

// Miss causes. CauseNone marks a graded outcome that is not a miss.
const (
	CauseNone Cause = ""
	// CauseAttemptedFailed: capture executed, but no linked insight landed.
	CauseAttemptedFailed Cause = "attempted_failed"
	// CauseBudgetStarved: capture never executed and the episode exhausted its
	// tool-call budget (the discovery-budget-exhaustion failure mode).
	CauseBudgetStarved Cause = "budget_starved"
	// CauseNeverAttempted: capture never executed with budget left over — the
	// agent had the calls available and did not try.
	CauseNeverAttempted Cause = "never_attempted"
	// CauseBudgetUnobservable: capture never executed and budget exhaustion is not
	// observable (the claude-cli path runs its own turn budget), so the miss is
	// attributed as far as the evidence allows and no further.
	CauseBudgetUnobservable Cause = "budget_unobservable"
	// CauseUnattributed: a miss with no attempt signal at all, which only arises
	// for results written before the attempt signal existed. It is counted
	// separately rather than folded into a real cause, so a legacy file cannot
	// silently inflate one.
	CauseUnattributed Cause = "unattributed"
)

// String renders the cause as the phrase used in terminal summaries.
func (c Cause) String() string {
	switch c {
	case CauseAttemptedFailed:
		return "attempted, insight did not land"
	case CauseBudgetStarved:
		return "never attempted, budget exhausted"
	case CauseNeverAttempted:
		return "never attempted, budget remained"
	case CauseBudgetUnobservable:
		return "never attempted, budget not observable"
	case CauseUnattributed:
		return "unattributed (no attempt signal recorded)"
	case CauseNone:
		return ""
	default:
		return string(c)
	}
}

// Classify attributes one graded capture outcome. captured is the graded
// outcome (nil when capture was never reached, e.g. a harness abort — such an
// outcome is excluded from the metrics entirely and classifies as CauseNone);
// attempted is whether the episode executed a capture call (nil only for
// results written before the signal existed); budgetExhausted is whether the
// episode hit its tool-call budget (nil when that is not observable).
func Classify(captured, attempted, budgetExhausted *bool) Cause {
	if captured == nil || *captured {
		return CauseNone
	}
	switch {
	case attempted == nil:
		return CauseUnattributed
	case *attempted:
		return CauseAttemptedFailed
	case budgetExhausted == nil:
		return CauseBudgetUnobservable
	case *budgetExhausted:
		return CauseBudgetStarved
	default:
		return CauseNeverAttempted
	}
}

// Misses counts graded capture misses by attributed cause. Total is the sum of
// the buckets, so a reader can confirm at a glance that every miss in the run
// was attributed.
type Misses struct {
	Total              int `json:"total"`
	AttemptedFailed    int `json:"attempted_failed"`
	BudgetStarved      int `json:"budget_starved"`
	NeverAttempted     int `json:"never_attempted"`
	BudgetUnobservable int `json:"budget_unobservable"`
	Unattributed       int `json:"unattributed,omitempty"`
}

// add folds one cause into the counts. CauseNone (not a miss) is ignored.
func (m *Misses) add(c Cause) {
	switch c {
	case CauseAttemptedFailed:
		m.AttemptedFailed++
	case CauseBudgetStarved:
		m.BudgetStarved++
	case CauseNeverAttempted:
		m.NeverAttempted++
	case CauseBudgetUnobservable:
		m.BudgetUnobservable++
	case CauseUnattributed:
		m.Unattributed++
	case CauseNone:
		return
	default:
		return
	}
	m.Total++
}

// counts pairs each cause with its bucket, in report order.
func (m Misses) counts() []struct {
	cause Cause
	n     int
} {
	return []struct {
		cause Cause
		n     int
	}{
		{CauseAttemptedFailed, m.AttemptedFailed},
		{CauseBudgetStarved, m.BudgetStarved},
		{CauseNeverAttempted, m.NeverAttempted},
		{CauseBudgetUnobservable, m.BudgetUnobservable},
		{CauseUnattributed, m.Unattributed},
	}
}

// Split is the capture decomposition both suites report alongside their capture
// rate: among graded capture outcomes, how many episodes executed a capture call
// (AttemptRate), how many of those landed an insight (GivenAttempted), and what
// every miss is attributed to (Misses). AttemptRate shares the capture rate's
// denominator, so the two read as a decomposition of the same population — with
// one exception it must not hide: an outcome recorded before the attempt signal
// existed is excluded from AttemptRate (and counted as an unattributed miss),
// which is the only way the two denominators can differ.
type Split struct {
	AttemptRate    stats.Rate `json:"attempt_rate"`
	GivenAttempted stats.Rate `json:"given_attempted"`
	Misses         Misses     `json:"misses"`
}

// Add folds one graded capture outcome into the split. An outcome that was never
// reached (captured == nil) is excluded from every denominator, mirroring how
// the suites exclude harness failures from their rates. A legacy outcome with no
// attempt signal (attempted == nil) is excluded from the attempt denominators —
// counting it as "not attempted" would fabricate evidence — and, when it is a
// miss, lands in the unattributed bucket.
func (s *Split) Add(captured, attempted, budgetExhausted *bool) {
	if captured == nil {
		return
	}
	s.Misses.add(Classify(captured, attempted, budgetExhausted))
	if attempted == nil {
		return
	}
	s.AttemptRate.Add(attempted)
	s.GivenAttempted.AddConditional(*attempted, *captured)
}

// FillCIs attaches bootstrap intervals to the split's rates, threading the
// caller's seeded RNG so the scorecard stays reproducible from one seed.
func (s *Split) FillCIs(rng *rand.Rand) {
	stats.FillCIs(rng, &s.AttemptRate, &s.GivenAttempted)
}

// Rows renders the two split rates as scorecard lines under a capture-rate row.
func (s Split) Rows() string {
	return s.AttemptRate.Row("  capture attempted") + s.GivenAttempted.Row("  landed given attempt")
}

// MissBlock renders the miss attribution, or "" when nothing missed. Every miss
// is listed under exactly one cause, so the counts sum to the total shown. An
// unattributed miss carries a warning rather than a silent bucket: for a file
// this harness wrote it would mean the attempt signal stopped being recorded,
// which is a wiring regression, not a measurement.
func (s Split) MissBlock() string {
	if s.Misses.Total == 0 {
		return ""
	}
	var parts []string
	for _, c := range s.Misses.counts() {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", c.cause, c.n))
		}
	}
	block := fmt.Sprintf("  capture misses (%d): %s\n", s.Misses.Total, strings.Join(parts, " | "))
	if s.Misses.Unattributed > 0 {
		block += fmt.Sprintf("  WARNING: %d capture miss(es) carry no attempt signal, so they are excluded from the attempted/landed split; expect this only for a results file written before the signal existed\n",
			s.Misses.Unattributed)
	}
	return block
}
