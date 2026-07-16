// Package lifecycle runs the S5 memory-insight-knowledge lifecycle protocols
// (issue #944) and scores them. This file defines the results model: a manifest
// pinning the run, one record per protocol attempt with every stage outcome, and
// the aggregate lifecycle metrics (capture rate, personal recall, transfer rate,
// update correctness, duplicate rate, abstention rate).
package lifecycle

import (
	"encoding/json"
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- bootstrap-CI resampling for benchmark statistics; not security-sensitive, and a seedable PRNG is required for reproducible intervals.
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/stats"
)

// Stage names, one per lifecycle episode (issue #930 S5). update_recall is the
// post-correction recall that must flip to the new value.
const (
	StageTeach        = "teach"
	StageRecall       = "recall"
	StagePromote      = "promote"
	StageTransfer     = "transfer"
	StageUpdate       = "update"
	StageUpdateRecall = "update_recall"
	StageAbstain      = "abstain"
)

// Manifest pins the run so results are attributable and reproducible.
type Manifest struct {
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	GitCommit       string    `json:"git_commit"`
	PlatformVersion string    `json:"platform_version"`
	Target          string    `json:"target"`
	Arm             string    `json:"arm"`
	LLMProvider     string    `json:"llm_provider"`
	// ClientVersion records the external client path (claude-cli: the
	// `claude --version` string), empty for in-process adapters. See the S1-S3
	// report.Manifest for the comparability rationale.
	ClientVersion   string `json:"client_version,omitempty"`
	Model           string `json:"model"`
	Seed            int64  `json:"seed"`
	ProtocolSetHash string `json:"protocol_set_hash"`
	K               int    `json:"k"`
}

// EpisodeRecord captures one episode's execution for the transcript and audit
// trail. Each lifecycle stage is one fresh MCP session.
type EpisodeRecord struct {
	Stage        string `json:"stage"`
	Identity     string `json:"identity"` // "teacher" or "learner"
	Email        string `json:"email"`
	SessionID    string `json:"session_id,omitempty"`
	ToolCalls    int    `json:"tool_calls"`
	ToolErrors   int    `json:"tool_errors"`
	SearchCalled bool   `json:"search_called"`
	// CaptureAttempted is true when the episode actually executed a
	// knowledge-capture call (a budget-refused capture request does not count). On
	// a teach episode it distinguishes a capture miss caused by never reaching an
	// executed capture (budget starvation) from one where capture ran but the
	// insight did not land (issue #964 capture-budget gap).
	CaptureAttempted bool `json:"capture_attempted,omitempty"`
	// BudgetExhausted is true when the episode hit its tool-call budget. It is
	// meaningful only on the in-process loop path, which owns the tool-call
	// budget; the claude-cli path runs its own turn budget and leaves it false
	// (the run-level TeachBudgetExhausted is left nil there, so a claude-cli
	// capture miss is excluded from the budget-starvation rate rather than
	// miscounted as not-starved).
	BudgetExhausted bool `json:"budget_exhausted,omitempty"`
	// FactSurfaced, when set, reports whether the promoted fact appeared in a tool
	// result this episode saw. Set only for episodes that pass a surface target
	// (the transfer stage); nil otherwise.
	FactSurfaced *bool  `json:"fact_surfaced,omitempty"`
	FinalAnswer  string `json:"final_answer,omitempty"`
	WallMS       int64  `json:"wall_ms"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	// Cache token split from llm.Usage, so a cached run's cost is computed from
	// committed per-episode data (cache reads bill far below fresh input) rather
	// than estimated from the input/output totals alone. Zero (omitted) for
	// adapters that do not report cache usage.
	CacheReadTokens     int64            `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64            `json:"cache_creation_tokens,omitempty"`
	Audit               auditapi.Metrics `json:"audit"`
	Error               string           `json:"error,omitempty"`
}

// ProtocolRun records one protocol attempt (1..k). Each stage outcome is a
// pointer: nil means the stage was not applicable (the protocol omits it) or
// never reached (an earlier harness failure aborted the run, recorded in Error).
// A nil outcome is excluded from its metric's denominator, mirroring how the
// task pipeline excludes harness failures from accuracy.
type ProtocolRun struct {
	ProtocolID string `json:"protocol_id"`
	Title      string `json:"title"`
	Sink       string `json:"sink"`
	Attempt    int    `json:"attempt"`

	InsightID string `json:"insight_id,omitempty"`

	Captured         *bool `json:"captured,omitempty"`          // insight recorded and entity-linked
	RecallCorrect    *bool `json:"recall_correct,omitempty"`    // personal recall answer correct
	RecallSurfaced   *bool `json:"recall_surfaced,omitempty"`   // search surfaced the memory unprompted
	Promoted         *bool `json:"promoted,omitempty"`          // applied + changeset links the insight (nil for update protocols)
	TransferCorrect  *bool `json:"transfer_correct,omitempty"`  // cross-identity recall correct
	TransferSurfaced *bool `json:"transfer_surfaced,omitempty"` // promoted fact appeared in a tool result the learner saw
	UpdateCorrect    *bool `json:"update_correct,omitempty"`    // recall flipped to the corrected value
	// UpdateCaptured reports whether the update episode actually executed a
	// correction capture call. When false the platform never received the
	// correction, so its supersede gate never ran: Duplicated stays nil (the
	// attempt is a capture miss, not a duplicate) and the run cannot pass.
	UpdateCaptured *bool `json:"update_captured,omitempty"`
	Duplicated     *bool `json:"duplicated,omitempty"`      // supersede left more than one live insight (nil when the correction capture never executed)
	AbstainCorrect *bool `json:"abstain_correct,omitempty"` // abstained on a never-taught fact

	// Capture-budget diagnosis (issue #964), read from the teach episode. Nil
	// when the teach episode never ran (harness abort before teach).
	CaptureAttempted *bool `json:"capture_attempted,omitempty"` // teach episode executed a capture call
	// TeachBudgetExhausted is nil when budget exhaustion is not observable (the
	// claude-cli path runs its own turn budget), which excludes the run from the
	// budget-starvation rate.
	TeachBudgetExhausted *bool `json:"teach_budget_exhausted,omitempty"` // teach episode hit its tool-call budget (loop path only)

	Episodes []EpisodeRecord `json:"episodes"`
	Error    string          `json:"error,omitempty"` // harness failure that aborted the run
}

// Passed reports whether every applicable stage of this run succeeded. A
// harness-failed run never passes. Teach and recall are always required; the
// remaining stages are optional (a protocol either promotes+transfers OR
// supersedes, never both), so an omitted stage (nil outcome) counts as passed.
// Update passes only when the recall flipped AND no duplicate was left.
func (r ProtocolRun) Passed() bool {
	if r.Error != "" {
		return false
	}
	return boolTrue(r.Captured) && boolTrue(r.RecallCorrect) &&
		optPass(r.Promoted) && optPass(r.TransferCorrect) &&
		r.updatePassed() && optPass(r.AbstainCorrect)
}

// updatePassed reports whether the supersede stage passed, or was not run. It
// requires an executed correction capture (when observed), a flipped recall,
// and no duplicate. A missed correction capture fails the stage even if the
// recall answer happens to be right: the lifecycle never received the
// correction. A nil UpdateCaptured (results from before the field existed)
// falls back to the recall-and-duplicate check alone.
func (r ProtocolRun) updatePassed() bool {
	if r.UpdateCorrect == nil {
		return true
	}
	if r.UpdateCaptured != nil && !*r.UpdateCaptured {
		return false
	}
	return *r.UpdateCorrect && !boolTrue(r.Duplicated)
}

// boolTrue reports whether a nil-able bool is set and true.
func boolTrue(b *bool) bool { return b != nil && *b }

// optPass treats an absent (nil) optional outcome as passed, so a protocol that
// omits a stage is not penalized for it.
func optPass(b *bool) bool { return b == nil || *b }

// Rate is one metric's numerator, denominator, and ratio. Denominator counts
// only the runs where the outcome was applicable and reached.
type Rate struct {
	Num  int     `json:"num"`
	Den  int     `json:"den"`
	Rate float64 `json:"rate"`
	// CILow/CIHigh are the 95% percentile-bootstrap confidence interval on the
	// rate (issue #965), resampled from num/den with a fixed seed so the interval
	// is reproducible. Both zero when the denominator is empty — the metric was
	// not exercised, so it carries no interval. The bootstrap treats each
	// applicable outcome as an independent draw (like the S1-S3 report); it does
	// not model protocol-level correlation across the k replicates, so a narrow
	// interval on a small, few-protocol denominator still warrants caution.
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}

// fillCI attaches a bootstrap confidence interval to the rate from its num/den.
// The caller threads one seeded RNG across a scorecard's rates so the whole
// report is reproducible from a single seed (issue #965).
func (r *Rate) fillCI(rng *rand.Rand) {
	r.CILow, r.CIHigh = stats.ProportionCI(r.Num, r.Den, rng)
}

// add folds one applicable outcome into the rate.
func (r *Rate) add(v *bool) {
	if v == nil {
		return
	}
	r.Den++
	if *v {
		r.Num++
	}
	if r.Den > 0 {
		r.Rate = float64(r.Num) / float64(r.Den)
	}
}

// addConditional folds one outcome into a conditional rate: the run counts
// toward the denominator only when it belongs to the conditioning subset (e.g.
// "among transfer attempts where the fact surfaced"). It backs the transfer-gap
// and capture-budget decompositions, whose denominators are subsets of the
// attempts rather than a single nil-able outcome.
func (r *Rate) addConditional(applicable, value bool) {
	if !applicable {
		return
	}
	r.Den++
	if value {
		r.Num++
	}
	if r.Den > 0 {
		r.Rate = float64(r.Num) / float64(r.Den)
	}
}

// complement returns the rate of the opposite outcome over the same
// denominator, so a pair like supersede/duplicate is stored once and derived
// once rather than accumulated twice (which could silently drift apart).
func (r Rate) complement() Rate {
	c := Rate{Num: r.Den - r.Num, Den: r.Den}
	if c.Den > 0 {
		c.Rate = float64(c.Num) / float64(c.Den)
	}
	return c
}

// captureMissed reports whether capture was applicable to a run and failed.
func captureMissed(captured *bool) bool { return captured != nil && !*captured }

// captureBudgetObservable reports whether a run belongs in the
// CaptureBudgetStarved denominator: capture must have missed AND the teach
// episode's budget exhaustion must be observable (TeachBudgetExhausted set — the
// loop path). A claude-cli run leaves TeachBudgetExhausted nil, so it is excluded
// rather than miscounted as not-starved.
func captureBudgetObservable(r ProtocolRun) bool {
	return captureMissed(r.Captured) && r.TeachBudgetExhausted != nil
}

// budgetStarved reports whether a run's teach episode exhausted its tool-call
// budget without ever calling capture (the discovery-budget-exhaustion failure
// mode).
func budgetStarved(r ProtocolRun) bool {
	return boolTrue(r.TeachBudgetExhausted) && !boolTrue(r.CaptureAttempted)
}

// Metrics is the S5 lifecycle scorecard (issue #944).
type Metrics struct {
	Protocols       int `json:"protocols"`
	Attempts        int `json:"attempts"`         // graded protocol runs (harness failures excluded)
	HarnessFailures int `json:"harness_failures"` // runs aborted by a harness error

	// Token totals across every episode of every run (including harness-failed
	// runs — a failed episode still spent tokens), so a run self-reports its cost
	// basis rather than needing cost reverse-engineered from transcripts. The
	// cache split lets a cached run's cost be computed from committed data:
	// cache reads bill at roughly a tenth of fresh input.
	TotalInputTokens         int64 `json:"total_input_tokens"`
	TotalOutputTokens        int64 `json:"total_output_tokens"`
	TotalCacheReadTokens     int64 `json:"total_cache_read_tokens"`
	TotalCacheCreationTokens int64 `json:"total_cache_creation_tokens"`

	CaptureRate       Rate `json:"capture_rate"`
	PersonalRecall    Rate `json:"personal_recall"`
	UnpromptedSurface Rate `json:"unprompted_surface"` // among captured runs, search surfaced the memory
	TransferRate      Rate `json:"transfer_rate"`
	UpdateCorrectness Rate `json:"update_correctness"`
	// UpdateCaptureRate is, among update stages that ran, the fraction whose
	// correction capture actually executed. Its misses are excluded from
	// DuplicateRate (no correction reached the platform, so the supersede gate
	// never ran) and reported here instead of inflating the duplicate count.
	UpdateCaptureRate Rate `json:"update_capture_rate"`
	DuplicateRate     Rate `json:"duplicate_rate"` // fraction of executed supersedes that duplicated (lower is better)
	AbstentionRate    Rate `json:"abstention_rate"`

	// Transfer-gap decomposition (issue #964). TransferSurfaced is the fraction
	// of transfer attempts where the promoted fact reached the learner in a tool
	// result; TransferUsedGivenSurfaced is, among those, the fraction answered
	// correctly. A low TransferSurfaced points at delivery (enrichment/search);
	// a low TransferUsedGivenSurfaced points at reasoning (the agent had it and
	// ignored it).
	TransferSurfaced          Rate `json:"transfer_surfaced"`
	TransferUsedGivenSurfaced Rate `json:"transfer_used_given_surfaced"`

	// CaptureBudgetStarved is, among capture misses whose budget exhaustion is
	// observable (the loop path), the fraction where the teach episode exhausted
	// its tool-call budget without executing a capture call — the
	// discovery-budget-exhaustion failure mode (issue #964). claude-cli misses are
	// excluded (their budget exhaustion is not observable), not counted as
	// not-starved.
	CaptureBudgetStarved Rate `json:"capture_budget_starved"`

	PassK Rate `json:"pass_k"` // protocols passing all k full lifecycles / protocols
}

// Results is the full lifecycle run output.
type Results struct {
	Manifest Manifest      `json:"manifest"`
	Runs     []ProtocolRun `json:"runs"`
	Metrics  Metrics       `json:"metrics"`
}

// Aggregate computes the lifecycle metrics from the runs.
func (res *Results) Aggregate() {
	m := Metrics{}
	byProtocol := map[string][]ProtocolRun{}
	var order []string
	for _, r := range res.Runs {
		if _, ok := byProtocol[r.ProtocolID]; !ok {
			order = append(order, r.ProtocolID)
		}
		byProtocol[r.ProtocolID] = append(byProtocol[r.ProtocolID], r)
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
		m.CaptureRate.add(r.Captured)
		m.PersonalRecall.add(r.RecallCorrect)
		m.UnpromptedSurface.add(r.RecallSurfaced)
		m.TransferRate.add(r.TransferCorrect)
		m.TransferSurfaced.add(r.TransferSurfaced)
		m.TransferUsedGivenSurfaced.addConditional(boolTrue(r.TransferSurfaced), boolTrue(r.TransferCorrect))
		m.UpdateCorrectness.add(r.UpdateCorrect)
		m.UpdateCaptureRate.add(r.UpdateCaptured)
		m.DuplicateRate.add(r.Duplicated)
		m.AbstentionRate.add(r.AbstainCorrect)
		m.CaptureBudgetStarved.addConditional(captureBudgetObservable(r), budgetStarved(r))
	}
	m.Protocols = len(order)
	m.PassK = passKRate(order, byProtocol, res.Manifest.K)
	m.fillCIs(stats.NewRNG())
	res.Metrics = m
}

// fillCIs attaches a bootstrap confidence interval to every rate on the
// scorecard, threading one RNG in a fixed order so the whole report is
// reproducible from a single seed (issue #965). The #964 diagnostic
// decompositions carry intervals too — a reader weighing "surfaced but not used"
// against noise needs the same uncertainty signal the headline rates carry.
func (m *Metrics) fillCIs(rng *rand.Rand) {
	for _, r := range []*Rate{
		&m.CaptureRate, &m.PersonalRecall, &m.UnpromptedSurface,
		&m.TransferRate, &m.TransferSurfaced, &m.TransferUsedGivenSurfaced,
		&m.UpdateCorrectness, &m.UpdateCaptureRate, &m.DuplicateRate, &m.AbstentionRate,
		&m.CaptureBudgetStarved, &m.PassK,
	} {
		r.fillCI(rng)
	}
}

// passKRate computes the fraction of protocols whose every one of the k attempts
// passed the full applicable lifecycle (tau-bench pass^k applied to protocols).
func passKRate(order []string, byProtocol map[string][]ProtocolRun, k int) Rate {
	r := Rate{Den: len(order)}
	for _, id := range order {
		runs := byProtocol[id]
		if len(runs) != k {
			continue // an aborted/short protocol cannot claim pass^k
		}
		all := true
		for _, run := range runs {
			if !run.Passed() {
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

// WriteJSON persists the results.
func (res *Results) WriteJSON(path string) error {
	raw, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	return nil
}

// LoadJSON reads results written by WriteJSON (for -summarize).
func LoadJSON(path string) (*Results, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied results path
	if err != nil {
		return nil, fmt.Errorf("read results: %w", err)
	}
	var res Results
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse results: %w", err)
	}
	return &res, nil
}

// HumanSummary renders the lifecycle run for a terminal.
func (res *Results) HumanSummary() string {
	var b strings.Builder
	m := res.Manifest
	provider := m.LLMProvider
	if m.ClientVersion != "" {
		provider = fmt.Sprintf("%s via %s", m.LLMProvider, m.ClientVersion)
	}
	fmt.Fprintf(&b, "bench lifecycle (S5): arm=%s model=%s (%s) k=%d\n", m.Arm, m.Model, provider, m.K)
	fmt.Fprintf(&b, "  platform %s @ %s | commit %s | seed %d | protocols %s\n",
		m.PlatformVersion, m.Target, short(m.GitCommit), m.Seed, short(m.ProtocolSetHash))
	fmt.Fprintf(&b, "  %s .. %s\n\n", m.StartedAt.Format(time.RFC3339), m.FinishedAt.Format(time.RFC3339))
	mt := res.Metrics
	fmt.Fprintf(&b, "protocols %d  attempts %d  harness failures %d\n", mt.Protocols, mt.Attempts, mt.HarnessFailures)
	fmt.Fprintf(&b, "tokens: input %d  output %d  cache read %d  cache write %d (apply current model pricing for cost)\n\n",
		mt.TotalInputTokens, mt.TotalOutputTokens, mt.TotalCacheReadTokens, mt.TotalCacheCreationTokens)
	writeMetric(&b, "capture rate", mt.CaptureRate)
	writeMetric(&b, "personal recall", mt.PersonalRecall)
	writeMetric(&b, "unprompted surface", mt.UnpromptedSurface)
	writeMetric(&b, "transfer rate", mt.TransferRate)
	writeMetric(&b, "  transfer surfaced", mt.TransferSurfaced)
	writeMetric(&b, "  used given surfaced", mt.TransferUsedGivenSurfaced)
	writeMetric(&b, "update correctness", mt.UpdateCorrectness)
	writeMetric(&b, "  update capture rate", mt.UpdateCaptureRate)
	writeMetric(&b, "duplicate rate", mt.DuplicateRate)
	writeMetric(&b, "abstention rate", mt.AbstentionRate)
	writeMetric(&b, "capture budget-starved", mt.CaptureBudgetStarved)
	writeMetric(&b, "pass^k (protocols)", mt.PassK)
	if failures := res.harnessFailures(); len(failures) > 0 {
		b.WriteString("\nharness failures (excluded from metrics):\n")
		for _, f := range failures {
			fmt.Fprintf(&b, "  %s attempt %d: %s\n", f.ProtocolID, f.Attempt, f.Error)
		}
	}
	return b.String()
}

// writeMetric renders one rate row. The 95% CI bracket is shown only when the
// interval has width (CILow != CIHigh). A zero-width interval carries no
// uncertainty and is omitted: it arises for an unexercised metric (empty
// denominator), for an all-failure rate whose bootstrap collapses to a point at
// zero AND for an all-success rate that collapses to a point at one (both ends,
// not just zero), and for a pre-#965 results file whose stored metrics carry no
// interval (the fields default to zero) — all of which would otherwise print a
// meaningless [x.x-x.x] bracket the count already conveys.
func writeMetric(b *strings.Builder, label string, r Rate) {
	if r.CILow == r.CIHigh {
		fmt.Fprintf(b, "  %-22s %5.1f%%  (%d/%d)\n", label, r.Rate*100, r.Num, r.Den)
		return
	}
	fmt.Fprintf(b, "  %-22s %5.1f%%  95%% CI [%.1f-%.1f]  (%d/%d)\n",
		label, r.Rate*100, r.CILow*100, r.CIHigh*100, r.Num, r.Den)
}

// harnessFailures lists runs that errored rather than completing.
func (res *Results) harnessFailures() []ProtocolRun {
	var out []ProtocolRun
	for _, r := range res.Runs {
		if r.Error != "" {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ProtocolID < out[j].ProtocolID })
	return out
}

// short truncates a hash for display.
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "unknown"
	}
	return s
}
