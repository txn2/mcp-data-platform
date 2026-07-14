// Package lifecycle runs the S5 memory-insight-knowledge lifecycle protocols
// (issue #944) and scores them. This file defines the results model: a manifest
// pinning the run, one record per protocol attempt with every stage outcome, and
// the aggregate lifecycle metrics (capture rate, personal recall, transfer rate,
// update correctness, duplicate rate, abstention rate).
package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
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
	Stage        string           `json:"stage"`
	Identity     string           `json:"identity"` // "teacher" or "learner"
	Email        string           `json:"email"`
	SessionID    string           `json:"session_id,omitempty"`
	ToolCalls    int              `json:"tool_calls"`
	ToolErrors   int              `json:"tool_errors"`
	SearchCalled bool             `json:"search_called"`
	FinalAnswer  string           `json:"final_answer,omitempty"`
	WallMS       int64            `json:"wall_ms"`
	InputTokens  int64            `json:"input_tokens"`
	OutputTokens int64            `json:"output_tokens"`
	Audit        auditapi.Metrics `json:"audit"`
	Error        string           `json:"error,omitempty"`
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

	Captured        *bool `json:"captured,omitempty"`         // insight recorded and entity-linked
	RecallCorrect   *bool `json:"recall_correct,omitempty"`   // personal recall answer correct
	RecallSurfaced  *bool `json:"recall_surfaced,omitempty"`  // search surfaced the memory unprompted
	Promoted        *bool `json:"promoted,omitempty"`         // applied + changeset links the insight (nil for update protocols)
	TransferCorrect *bool `json:"transfer_correct,omitempty"` // cross-identity recall correct
	UpdateCorrect   *bool `json:"update_correct,omitempty"`   // recall flipped to the corrected value
	Duplicated      *bool `json:"duplicated,omitempty"`       // supersede left more than one live insight
	AbstainCorrect  *bool `json:"abstain_correct,omitempty"`  // abstained on a never-taught fact

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
// requires both a flipped recall and no duplicate.
func (r ProtocolRun) updatePassed() bool {
	if r.UpdateCorrect == nil {
		return true
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

// Metrics is the S5 lifecycle scorecard (issue #944).
type Metrics struct {
	Protocols       int `json:"protocols"`
	Attempts        int `json:"attempts"`         // graded protocol runs (harness failures excluded)
	HarnessFailures int `json:"harness_failures"` // runs aborted by a harness error

	// Token totals across every episode of every run (including harness-failed
	// runs — a failed episode still spent tokens), so a run self-reports its cost
	// basis rather than needing cost reverse-engineered from transcripts.
	TotalInputTokens  int64 `json:"total_input_tokens"`
	TotalOutputTokens int64 `json:"total_output_tokens"`

	CaptureRate       Rate `json:"capture_rate"`
	PersonalRecall    Rate `json:"personal_recall"`
	UnpromptedSurface Rate `json:"unprompted_surface"` // among captured runs, search surfaced the memory
	TransferRate      Rate `json:"transfer_rate"`
	UpdateCorrectness Rate `json:"update_correctness"`
	DuplicateRate     Rate `json:"duplicate_rate"` // fraction of supersedes that duplicated (lower is better)
	AbstentionRate    Rate `json:"abstention_rate"`

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
		m.UpdateCorrectness.add(r.UpdateCorrect)
		m.DuplicateRate.add(r.Duplicated)
		m.AbstentionRate.add(r.AbstainCorrect)
	}
	m.Protocols = len(order)
	m.PassK = passKRate(order, byProtocol, res.Manifest.K)
	res.Metrics = m
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
	fmt.Fprintf(&b, "tokens: input %d  output %d (apply current model pricing for cost)\n\n", mt.TotalInputTokens, mt.TotalOutputTokens)
	writeMetric(&b, "capture rate", mt.CaptureRate)
	writeMetric(&b, "personal recall", mt.PersonalRecall)
	writeMetric(&b, "unprompted surface", mt.UnpromptedSurface)
	writeMetric(&b, "transfer rate", mt.TransferRate)
	writeMetric(&b, "update correctness", mt.UpdateCorrectness)
	writeMetric(&b, "duplicate rate", mt.DuplicateRate)
	writeMetric(&b, "abstention rate", mt.AbstentionRate)
	writeMetric(&b, "pass^k (protocols)", mt.PassK)
	if failures := res.harnessFailures(); len(failures) > 0 {
		b.WriteString("\nharness failures (excluded from metrics):\n")
		for _, f := range failures {
			fmt.Fprintf(&b, "  %s attempt %d: %s\n", f.ProtocolID, f.Attempt, f.Error)
		}
	}
	return b.String()
}

// writeMetric renders one rate row.
func writeMetric(b *strings.Builder, label string, r Rate) {
	fmt.Fprintf(b, "  %-20s %5.1f%%  (%d/%d)\n", label, r.Rate*100, r.Num, r.Den)
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
