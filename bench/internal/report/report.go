// Package report defines the benchmark results model: a manifest pinning
// everything that shaped the run, per-attempt records, and per-task/per-suite
// aggregates (accuracy, pass^k, tool-call and wall-clock distributions).
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
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
	Model           string    `json:"model"`
	Seed            int64     `json:"seed"`
	TaskSetHash     string    `json:"task_set_hash"`
	K               int       `json:"k"`
	Suite           string    `json:"suite,omitempty"` // filter, "" = all
}

// Attempt is one task execution.
type Attempt struct {
	TaskID          string           `json:"task_id"`
	Suite           string           `json:"suite"`
	Attempt         int              `json:"attempt"` // 1..k
	SessionID       string           `json:"session_id"`
	Correct         bool             `json:"correct"`
	FinalAnswer     string           `json:"final_answer"`
	GotValue        *float64         `json:"got_value,omitempty"`
	MatchedAlias    string           `json:"matched_alias,omitempty"`
	ToolCalls       int              `json:"tool_calls"`
	ToolErrors      int              `json:"tool_errors"`
	BudgetExhausted bool             `json:"budget_exhausted"`
	WallMS          int64            `json:"wall_ms"`
	InputTokens     int64            `json:"input_tokens"`
	OutputTokens    int64            `json:"output_tokens"`
	Audit           auditapi.Metrics `json:"audit"`
	TranscriptPath  string           `json:"transcript_path,omitempty"`
	Error           string           `json:"error,omitempty"` // harness/adapter failure, not a wrong answer
}

// TaskSummary aggregates one task's k attempts. Harness-failed attempts
// (Attempt.Error set) are excluded from grading counts and reported in
// HarnessFailures: an infrastructure failure is not evidence about the
// platform's quality. PassK requires all of the manifest's k attempts graded
// AND correct, so a task with harness failures can never claim pass^k.
type TaskSummary struct {
	TaskID          string  `json:"task_id"`
	Suite           string  `json:"suite"`
	Graded          int     `json:"graded"`
	Correct         int     `json:"correct"`
	HarnessFailures int     `json:"harness_failures,omitempty"`
	PassRate        float64 `json:"pass_rate"` // correct / graded (0 when nothing graded)
	PassK           bool    `json:"pass_k"`    // all k attempts graded and correct (tau-bench pass^k)
}

// SuiteSummary aggregates one suite under the run's arm over GRADED attempts;
// harness failures are counted separately and never fold into accuracy.
type SuiteSummary struct {
	Suite                 string  `json:"suite"`
	Tasks                 int     `json:"tasks"`
	Graded                int     `json:"graded"`
	HarnessFailures       int     `json:"harness_failures,omitempty"`
	Accuracy              float64 `json:"accuracy"`    // correct graded attempts / graded attempts
	PassKRate             float64 `json:"pass_k_rate"` // tasks with all k graded and correct / tasks
	MedianToolCalls       float64 `json:"median_tool_calls"`
	P90ToolCalls          float64 `json:"p90_tool_calls"`
	MedianWallMS          float64 `json:"median_wall_ms"`
	ToolErrors            int     `json:"tool_errors"`
	EnrichmentTokensDedup int     `json:"enrichment_tokens_dedup"`
}

// Results is the full run output.
type Results struct {
	Manifest Manifest       `json:"manifest"`
	Attempts []Attempt      `json:"attempts"`
	Tasks    []TaskSummary  `json:"tasks"`
	Suites   []SuiteSummary `json:"suites"`
}

// Aggregate builds the task and suite summaries from the attempts.
func (r *Results) Aggregate() {
	r.Tasks = taskSummaries(r.Attempts, r.Manifest.K)
	r.Suites = suiteSummaries(r.Attempts, r.Tasks)
}

// taskSummaries folds attempts per task; k is the manifest's repeat count.
func taskSummaries(attempts []Attempt, k int) []TaskSummary {
	byTask := map[string]*TaskSummary{}
	var order []string
	for _, a := range attempts {
		s, ok := byTask[a.TaskID]
		if !ok {
			s = &TaskSummary{TaskID: a.TaskID, Suite: a.Suite}
			byTask[a.TaskID] = s
			order = append(order, a.TaskID)
		}
		if a.Error != "" {
			s.HarnessFailures++
			continue
		}
		s.Graded++
		if a.Correct {
			s.Correct++
		}
	}
	out := make([]TaskSummary, 0, len(order))
	for _, id := range order {
		s := byTask[id]
		if s.Graded > 0 {
			s.PassRate = float64(s.Correct) / float64(s.Graded)
		}
		s.PassK = s.Graded == k && s.Correct == k
		out = append(out, *s)
	}
	return out
}

// suiteSummaries folds attempts per suite.
func suiteSummaries(attempts []Attempt, tasks []TaskSummary) []SuiteSummary {
	bySuite := map[string][]Attempt{}
	var order []string
	for _, a := range attempts {
		if _, ok := bySuite[a.Suite]; !ok {
			order = append(order, a.Suite)
		}
		bySuite[a.Suite] = append(bySuite[a.Suite], a)
	}
	sort.Strings(order)
	out := make([]SuiteSummary, 0, len(order))
	for _, suite := range order {
		out = append(out, summarizeSuite(suite, bySuite[suite], tasks))
	}
	return out
}

// summarizeSuite computes one suite's aggregate row over graded attempts.
func summarizeSuite(suite string, attempts []Attempt, tasks []TaskSummary) SuiteSummary {
	s := SuiteSummary{Suite: suite}
	toolCalls := make([]float64, 0, len(attempts))
	wall := make([]float64, 0, len(attempts))
	correct := 0
	for _, a := range attempts {
		if a.Error != "" {
			s.HarnessFailures++
			continue
		}
		s.Graded++
		if a.Correct {
			correct++
		}
		toolCalls = append(toolCalls, float64(a.ToolCalls))
		wall = append(wall, float64(a.WallMS))
		s.ToolErrors += a.ToolErrors
		s.EnrichmentTokensDedup += a.Audit.EnrichmentTokensDedup
	}
	if s.Graded > 0 {
		s.Accuracy = float64(correct) / float64(s.Graded)
	}
	s.MedianToolCalls = percentile(toolCalls, 0.5)
	s.P90ToolCalls = percentile(toolCalls, 0.9)
	s.MedianWallMS = percentile(wall, 0.5)
	passK := 0
	for _, t := range tasks {
		if t.Suite != suite {
			continue
		}
		s.Tasks++
		if t.PassK {
			passK++
		}
	}
	if s.Tasks > 0 {
		s.PassKRate = float64(passK) / float64(s.Tasks)
	}
	return s
}

// percentile computes the nearest-rank percentile of values.
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

// WriteJSON persists the results.
func (r *Results) WriteJSON(path string) error {
	raw, err := json.MarshalIndent(r, "", "  ")
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
	var r Results
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse results: %w", err)
	}
	return &r, nil
}

// HumanSummary renders the run for a terminal.
func (r *Results) HumanSummary() string {
	var b strings.Builder
	m := r.Manifest
	fmt.Fprintf(&b, "bench run: arm=%s model=%s (%s) k=%d\n", m.Arm, m.Model, m.LLMProvider, m.K)
	fmt.Fprintf(&b, "  platform %s @ %s | commit %s | seed %d | tasks %s\n",
		m.PlatformVersion, m.Target, short(m.GitCommit), m.Seed, short(m.TaskSetHash))
	fmt.Fprintf(&b, "  %s .. %s\n\n", m.StartedAt.Format(time.RFC3339), m.FinishedAt.Format(time.RFC3339))
	b.WriteString("suite   tasks  graded  accuracy  pass^k  med calls  p90 calls  med wall ms  tool errs  enrich tok  harness fails\n")
	for _, s := range r.Suites {
		fmt.Fprintf(&b, "%-7s %5d  %6d  %7.1f%%  %5.1f%%  %9.1f  %9.1f  %11.0f  %9d  %10d  %13d\n",
			s.Suite, s.Tasks, s.Graded, s.Accuracy*100, s.PassKRate*100,
			s.MedianToolCalls, s.P90ToolCalls, s.MedianWallMS, s.ToolErrors, s.EnrichmentTokensDedup, s.HarnessFailures)
	}
	b.WriteString("\ntask                            graded  correct  pass^k\n")
	for _, t := range r.Tasks {
		fmt.Fprintf(&b, "%-30s %6d  %7d  %v\n", t.TaskID, t.Graded, t.Correct, t.PassK)
	}
	if failures := r.harnessFailures(); len(failures) > 0 {
		b.WriteString("\nharness failures (excluded from grading):\n")
		for _, f := range failures {
			fmt.Fprintf(&b, "  %s attempt %d: %s\n", f.TaskID, f.Attempt, f.Error)
		}
	}
	return b.String()
}

// harnessFailures lists attempts that errored rather than being graded.
func (r *Results) harnessFailures() []Attempt {
	var out []Attempt
	for _, a := range r.Attempts {
		if a.Error != "" {
			out = append(out, a)
		}
	}
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
