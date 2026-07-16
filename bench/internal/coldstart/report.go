// Package coldstart runs the cold-start knowledge-growth suite (issue #963) and
// scores it. This file defines the results model: a manifest pinning the run, a
// record per lesson (teach + promote outcome), and a learning curve — one
// checkpoint per point on the accumulated-knowledge axis, each carrying the
// fixed eval set's accuracy, a per-trap-class breakdown, and the delivery-side
// enrichment coverage. The curve is the deliverable: accuracy and coverage as a
// function of how much promoted knowledge the enrichment layer holds.
package coldstart

import (
	"encoding/json"
	"fmt"
	"os"
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
	// ClientVersion records the external client path (claude-cli: the
	// `claude --version` string), empty for in-process adapters.
	ClientVersion  string `json:"client_version,omitempty"`
	Model          string `json:"model"`
	Seed           int64  `json:"seed"`
	CurriculumID   string `json:"curriculum_id"`
	CurriculumHash string `json:"curriculum_hash"`
	EvalSuite      string `json:"eval_suite"`
	TaskSetHash    string `json:"task_set_hash"`
	// K is the number of fresh evaluator identities per checkpoint; each answers
	// the whole eval set, so a checkpoint's accuracy averages over K x eval-tasks.
	K int `json:"k"`
	// Settle is the promote-to-eval cache-settle window the run was paced with
	// (Duration string, e.g. "5m0s"; empty when disabled). Settle pacing affects
	// how much of a datahub-sink lesson's lift the next checkpoint can see, so a
	// kept result must record it to stay comparable with other runs.
	Settle string `json:"settle,omitempty"`
}

// EpisodeRecord is one teach session's telemetry (the lesson's capture episode).
type EpisodeRecord struct {
	Email               string           `json:"email"`
	SessionID           string           `json:"session_id,omitempty"`
	ToolCalls           int              `json:"tool_calls"`
	ToolErrors          int              `json:"tool_errors"`
	WallMS              int64            `json:"wall_ms"`
	InputTokens         int64            `json:"input_tokens"`
	OutputTokens        int64            `json:"output_tokens"`
	CacheReadTokens     int64            `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64            `json:"cache_creation_tokens,omitempty"`
	Audit               auditapi.Metrics `json:"audit"`
	Error               string           `json:"error,omitempty"`
}

// LessonRecord captures one lesson's teach-and-promote outcome. Captured and
// Promoted are pointers so a lesson never reached (an earlier harness abort)
// is distinguishable from one that failed the transition.
type LessonRecord struct {
	LessonID  string `json:"lesson_id"`
	Title     string `json:"title"`
	TrapClass string `json:"trap_class"`
	Sink      string `json:"sink"`
	InsightID string `json:"insight_id,omitempty"`

	Captured *bool `json:"captured,omitempty"` // insight recorded and entity-linked
	Promoted *bool `json:"promoted,omitempty"` // applied + changeset links the insight

	Episode EpisodeRecord `json:"episode"`
	Error   string        `json:"error,omitempty"` // harness failure in the teach/promote
}

// EvalAttempt is one evaluator answering one eval task at a checkpoint. Graded
// is false for a harness-level failure (connect, adapter, audit read-back),
// which is excluded from accuracy and reported separately, mirroring the S1-S3
// and S5 pipelines.
type EvalAttempt struct {
	TaskID      string   `json:"task_id"`
	TrapClasses []string `json:"trap_classes,omitempty"`
	Email       string   `json:"email"`
	SessionID   string   `json:"session_id,omitempty"`
	Repeat      int      `json:"repeat"`
	Graded      bool     `json:"graded"`
	Correct     bool     `json:"correct"`
	// MemoryWrites counts memory_capture/memory_manage calls the evaluator made,
	// derived from the transcript. Evaluators are instructed never to write
	// (self-taught knowledge could surface to later checkpoints and confound the
	// curve), so any non-zero value is a validity warning on the run.
	MemoryWrites        int              `json:"memory_writes,omitempty"`
	FinalAnswer         string           `json:"final_answer,omitempty"`
	WallMS              int64            `json:"wall_ms"`
	InputTokens         int64            `json:"input_tokens"`
	OutputTokens        int64            `json:"output_tokens"`
	CacheReadTokens     int64            `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64            `json:"cache_creation_tokens,omitempty"`
	Audit               auditapi.Metrics `json:"audit"`
	Error               string           `json:"error,omitempty"`
}

// ClassScore is one trap class's accuracy at a checkpoint.
type ClassScore struct {
	Correct  int     `json:"correct"`
	Graded   int     `json:"graded"`
	Accuracy float64 `json:"accuracy"`
}

// Checkpoint is one point on the learning curve: the eval set's outcome after a
// given number of lessons have been promoted. Index 0 is the empty baseline.
type Checkpoint struct {
	Index       int    `json:"index"`
	LessonID    string `json:"lesson_id,omitempty"`    // lesson promoted to reach this point
	LessonTitle string `json:"lesson_title,omitempty"` //
	TrapClass   string `json:"trap_class,omitempty"`   // that lesson's trap class

	// PromotedSoFar is the count of lessons successfully promoted at or before
	// this checkpoint — the accumulated-knowledge coordinate on the x-axis.
	PromotedSoFar int `json:"promoted_so_far"`

	EvalGraded  int     `json:"eval_graded"`
	EvalCorrect int     `json:"eval_correct"`
	Accuracy    float64 `json:"accuracy"`

	ByTrapClass map[string]ClassScore `json:"by_trap_class,omitempty"`

	AuditedCalls       int     `json:"audited_calls"`
	EnrichedCalls      int     `json:"enriched_calls"`
	EnrichmentCoverage float64 `json:"enrichment_coverage"`

	HarnessFailures int           `json:"harness_failures"`
	Attempts        []EvalAttempt `json:"attempts"`
}

// aggregate folds this checkpoint's attempts into its scores. Harness failures
// are excluded from the accuracy denominators and counted separately.
func (c *Checkpoint) aggregate() {
	c.EvalGraded, c.EvalCorrect, c.HarnessFailures = 0, 0, 0
	c.AuditedCalls, c.EnrichedCalls = 0, 0
	byClass := map[string]ClassScore{}
	for _, a := range c.Attempts {
		if !a.Graded {
			c.HarnessFailures++
			continue
		}
		c.EvalGraded++
		c.AuditedCalls += a.Audit.AuditedCalls
		c.EnrichedCalls += a.Audit.EnrichedCalls
		if a.Correct {
			c.EvalCorrect++
		}
		for _, class := range a.TrapClasses {
			s := byClass[class]
			s.Graded++
			if a.Correct {
				s.Correct++
			}
			byClass[class] = s
		}
	}
	c.Accuracy = ratio(c.EvalCorrect, c.EvalGraded)
	c.EnrichmentCoverage = ratio(c.EnrichedCalls, c.AuditedCalls)
	for class, s := range byClass {
		s.Accuracy = ratio(s.Correct, s.Graded)
		byClass[class] = s
	}
	if len(byClass) > 0 {
		c.ByTrapClass = byClass
	}
}

// Metrics is the cold-start scorecard: the curve's endpoints and totals.
type Metrics struct {
	Lessons         int `json:"lessons"`
	LessonsCaptured int `json:"lessons_captured"`
	LessonsPromoted int `json:"lessons_promoted"`
	Checkpoints     int `json:"checkpoints"`
	EvalTasks       int `json:"eval_tasks"`
	HarnessFailures int `json:"harness_failures"`
	// EvalMemoryWrites totals evaluator memory writes across every attempt. Any
	// non-zero value flags the curve's validity: an evaluator taught itself
	// something a later checkpoint's evaluators may have read.
	EvalMemoryWrites int `json:"eval_memory_writes,omitempty"`

	// Token totals across every episode and attempt, so a run self-reports its
	// cost basis. The cache split lets a cached run's cost be computed from
	// committed data (cache reads bill far below fresh input).
	TotalInputTokens         int64 `json:"total_input_tokens"`
	TotalOutputTokens        int64 `json:"total_output_tokens"`
	TotalCacheReadTokens     int64 `json:"total_cache_read_tokens"`
	TotalCacheCreationTokens int64 `json:"total_cache_creation_tokens"`

	// Curve endpoints: the empty baseline vs the fully-taught ceiling.
	BaselineAccuracy float64 `json:"baseline_accuracy"`
	FinalAccuracy    float64 `json:"final_accuracy"`
	AccuracyLift     float64 `json:"accuracy_lift"`
	BaselineCoverage float64 `json:"baseline_coverage"`
	FinalCoverage    float64 `json:"final_coverage"`
}

// Results is the full cold-start run output.
type Results struct {
	Manifest    Manifest       `json:"manifest"`
	Lessons     []LessonRecord `json:"lessons"`
	Checkpoints []Checkpoint   `json:"checkpoints"`
	Metrics     Metrics        `json:"metrics"`
}

// Aggregate computes the checkpoint scores and the top-level metrics.
func (res *Results) Aggregate() {
	m := Metrics{Lessons: len(res.Lessons), Checkpoints: len(res.Checkpoints)}
	for _, l := range res.Lessons {
		if boolTrue(l.Captured) {
			m.LessonsCaptured++
		}
		if boolTrue(l.Promoted) {
			m.LessonsPromoted++
		}
		if l.Error != "" {
			m.HarnessFailures++
		}
		m.TotalInputTokens += l.Episode.InputTokens
		m.TotalOutputTokens += l.Episode.OutputTokens
		m.TotalCacheReadTokens += l.Episode.CacheReadTokens
		m.TotalCacheCreationTokens += l.Episode.CacheCreationTokens
	}
	for i := range res.Checkpoints {
		res.Checkpoints[i].aggregate()
		m.HarnessFailures += res.Checkpoints[i].HarnessFailures
		for _, a := range res.Checkpoints[i].Attempts {
			m.EvalMemoryWrites += a.MemoryWrites
			m.TotalInputTokens += a.InputTokens
			m.TotalOutputTokens += a.OutputTokens
			m.TotalCacheReadTokens += a.CacheReadTokens
			m.TotalCacheCreationTokens += a.CacheCreationTokens
		}
	}
	if len(res.Checkpoints) > 0 {
		m.EvalTasks = distinctTasks(res.Checkpoints[0].Attempts)
		first, last := res.Checkpoints[0], res.Checkpoints[len(res.Checkpoints)-1]
		m.BaselineAccuracy, m.FinalAccuracy = first.Accuracy, last.Accuracy
		m.AccuracyLift = last.Accuracy - first.Accuracy
		m.BaselineCoverage, m.FinalCoverage = first.EnrichmentCoverage, last.EnrichmentCoverage
	}
	res.Metrics = m
}

// distinctTasks counts the unique task IDs in a checkpoint's attempts (the eval
// set size, independent of k).
func distinctTasks(attempts []EvalAttempt) int {
	seen := map[string]bool{}
	for _, a := range attempts {
		seen[a.TaskID] = true
	}
	return len(seen)
}

// ratio is num/den, or 0 for an empty denominator.
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// boolTrue reports whether a nil-able bool is set and true.
func boolTrue(b *bool) bool { return b != nil && *b }

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

// HumanSummary renders the learning curve for a terminal.
func (res *Results) HumanSummary() string {
	var b strings.Builder
	m := res.Manifest
	provider := m.LLMProvider
	if m.ClientVersion != "" {
		provider = fmt.Sprintf("%s via %s", m.LLMProvider, m.ClientVersion)
	}
	fmt.Fprintf(&b, "bench cold-start (%s): arm=%s model=%s (%s) k=%d\n", m.CurriculumID, m.Arm, m.Model, provider, m.K)
	fmt.Fprintf(&b, "  platform %s @ %s | commit %s | seed %d | curriculum %s | eval %s (%s)\n",
		m.PlatformVersion, m.Target, short(m.GitCommit), m.Seed, short(m.CurriculumHash), m.EvalSuite, short(m.TaskSetHash))
	fmt.Fprintf(&b, "  %s .. %s\n\n", m.StartedAt.Format(time.RFC3339), m.FinishedAt.Format(time.RFC3339))

	mt := res.Metrics
	fmt.Fprintf(&b, "lessons %d (captured %d, promoted %d)  eval tasks %d  checkpoints %d  harness failures %d\n",
		mt.Lessons, mt.LessonsCaptured, mt.LessonsPromoted, mt.EvalTasks, mt.Checkpoints, mt.HarnessFailures)
	if mt.EvalMemoryWrites > 0 {
		fmt.Fprintf(&b, "WARNING: evaluators performed %d memory write(s); an evaluator taught itself knowledge that later checkpoints may have read, so the curve's validity is suspect\n", mt.EvalMemoryWrites)
	}
	fmt.Fprintf(&b, "tokens: input %d  output %d  cache read %d  cache write %d (apply current model pricing for cost)\n\n",
		mt.TotalInputTokens, mt.TotalOutputTokens, mt.TotalCacheReadTokens, mt.TotalCacheCreationTokens)

	b.WriteString("learning curve (accuracy and enrichment coverage vs promoted knowledge):\n")
	fmt.Fprintf(&b, "  %-4s %-22s %-9s %-8s %-9s\n", "idx", "lesson promoted", "promoted", "accuracy", "coverage")
	for _, c := range res.Checkpoints {
		label := "(empty baseline)"
		if c.LessonID != "" {
			label = c.LessonID
		}
		fmt.Fprintf(&b, "  %-4d %-22s %-8d %6.1f%%  %6.1f%%\n",
			c.Index, truncate(label, 22), c.PromotedSoFar, c.Accuracy*100, c.EnrichmentCoverage*100)
	}
	fmt.Fprintf(&b, "\naccuracy %.1f%% -> %.1f%% (lift %+.1f pts)   coverage %.1f%% -> %.1f%%\n",
		mt.BaselineAccuracy*100, mt.FinalAccuracy*100, mt.AccuracyLift*100, mt.BaselineCoverage*100, mt.FinalCoverage*100)

	res.writeTrapClassCurve(&b)
	res.writeLessonFailures(&b)
	return b.String()
}

// writeTrapClassCurve renders the baseline vs final accuracy for each trap class
// so a reader sees which lesson unlocked which class.
func (res *Results) writeTrapClassCurve(b *strings.Builder) {
	if len(res.Checkpoints) < 2 {
		return
	}
	first, last := res.Checkpoints[0], res.Checkpoints[len(res.Checkpoints)-1]
	classes := make([]string, 0, len(last.ByTrapClass))
	for class := range last.ByTrapClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	if len(classes) == 0 {
		return
	}
	b.WriteString("\nper-trap-class accuracy (baseline -> final):\n")
	for _, class := range classes {
		fmt.Fprintf(b, "  %-18s %5.1f%% -> %5.1f%%\n", class, first.ByTrapClass[class].Accuracy*100, last.ByTrapClass[class].Accuracy*100)
	}
}

// writeLessonFailures lists lessons whose teach or promote did not complete.
func (res *Results) writeLessonFailures(b *strings.Builder) {
	var lines []string
	for _, l := range res.Lessons {
		switch {
		case l.Error != "":
			lines = append(lines, fmt.Sprintf("  %s: %s", l.LessonID, l.Error))
		case !boolTrue(l.Captured):
			lines = append(lines, fmt.Sprintf("  %s: not captured", l.LessonID))
		case !boolTrue(l.Promoted):
			lines = append(lines, fmt.Sprintf("  %s: captured but not promoted", l.LessonID))
		}
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\nlesson gaps (knowledge not delivered, so its class stays flat):\n")
	b.WriteString(strings.Join(lines, "\n") + "\n")
}

// short truncates a hash for display.
func short(s string) string {
	if s == "" {
		return "unknown"
	}
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// truncate caps a label to n runes for the fixed-width curve table.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
