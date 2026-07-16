package coldstart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
)

// sampleResults builds a two-lesson curve: a baseline where nothing is
// answerable and a final checkpoint where the units class is unlocked.
func sampleResults() *Results {
	audited := auditapi.Metrics{AuditedCalls: 2, EnrichedCalls: 0}
	enriched := auditapi.Metrics{AuditedCalls: 2, EnrichedCalls: 2}
	return &Results{
		Manifest: Manifest{CurriculumID: "cs-traps", Arm: "a3", EvalSuite: "s3", K: 1},
		Lessons: []LessonRecord{
			{LessonID: "cs-units", TrapClass: "units_cents", Sink: "datahub",
				Captured: new(true), Promoted: new(true),
				Episode: EpisodeRecord{InputTokens: 100, OutputTokens: 20}},
			{LessonID: "cs-net", TrapClass: "net_revenue", Sink: "knowledge_page",
				Captured: new(true), Promoted: new(false),
				Episode: EpisodeRecord{InputTokens: 100, OutputTokens: 20}},
		},
		Checkpoints: []Checkpoint{
			{Index: 0, PromotedSoFar: 0, Attempts: []EvalAttempt{
				{TaskID: "s3-units-a", TrapClasses: []string{"units_cents"}, Graded: true, Correct: false, Audit: audited, InputTokens: 50, OutputTokens: 10},
				{TaskID: "s3-net-a", TrapClasses: []string{"net_revenue"}, Graded: true, Correct: false, Audit: audited},
			}},
			{Index: 1, LessonID: "cs-units", TrapClass: "units_cents", PromotedSoFar: 1, Attempts: []EvalAttempt{
				{TaskID: "s3-units-a", TrapClasses: []string{"units_cents"}, Graded: true, Correct: true, Audit: enriched},
				{TaskID: "s3-net-a", TrapClasses: []string{"net_revenue"}, Graded: true, Correct: false, Audit: enriched},
				{TaskID: "s3-x", Graded: false, Error: "connect: boom"}, // harness failure excluded
			}},
		},
	}
}

func TestAggregateCurveAndMetrics(t *testing.T) {
	res := sampleResults()
	res.Aggregate()

	base, final := res.Checkpoints[0], res.Checkpoints[1]
	if base.Accuracy != 0 {
		t.Errorf("baseline accuracy = %v, want 0", base.Accuracy)
	}
	if final.EvalGraded != 2 || final.EvalCorrect != 1 || final.Accuracy != 0.5 {
		t.Errorf("final checkpoint = graded %d correct %d acc %v, want 2/1/0.5", final.EvalGraded, final.EvalCorrect, final.Accuracy)
	}
	if final.HarnessFailures != 1 {
		t.Errorf("final harness failures = %d, want 1", final.HarnessFailures)
	}
	// Coverage: baseline 0/4 enriched, final 4/4.
	if base.EnrichmentCoverage != 0 {
		t.Errorf("baseline coverage = %v, want 0", base.EnrichmentCoverage)
	}
	if final.EnrichmentCoverage != 1 {
		t.Errorf("final coverage = %v, want 1", final.EnrichmentCoverage)
	}
	// Per-trap-class: units flips 0 -> 1.0, net stays 0.
	if got := final.ByTrapClass["units_cents"].Accuracy; got != 1 {
		t.Errorf("final units_cents accuracy = %v, want 1", got)
	}
	if got := final.ByTrapClass["net_revenue"].Accuracy; got != 0 {
		t.Errorf("final net_revenue accuracy = %v, want 0", got)
	}

	m := res.Metrics
	if m.Lessons != 2 || m.LessonsCaptured != 2 || m.LessonsPromoted != 1 {
		t.Errorf("lesson metrics = %d/%d/%d, want 2/2/1", m.Lessons, m.LessonsCaptured, m.LessonsPromoted)
	}
	if m.EvalTasks != 2 {
		t.Errorf("eval tasks = %d, want 2", m.EvalTasks)
	}
	if m.BaselineAccuracy != 0 || m.FinalAccuracy != 0.5 || m.AccuracyLift != 0.5 {
		t.Errorf("curve endpoints = %v/%v lift %v, want 0/0.5/0.5", m.BaselineAccuracy, m.FinalAccuracy, m.AccuracyLift)
	}
	// Token totals: lesson inputs 100+100 plus the one baseline attempt input 50
	// = 250; outputs 20+20 plus attempt 10 = 50.
	if m.TotalInputTokens != 250 || m.TotalOutputTokens != 50 {
		t.Errorf("token totals = in %d out %d, want 250/50", m.TotalInputTokens, m.TotalOutputTokens)
	}
	if m.HarnessFailures != 1 {
		t.Errorf("total harness failures = %d, want 1", m.HarnessFailures)
	}
}

func TestWriteAndLoadRoundTrip(t *testing.T) {
	res := sampleResults()
	res.Aggregate()
	path := filepath.Join(t.TempDir(), "cold.json")
	if err := res.WriteJSON(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadJSON(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Metrics.FinalAccuracy != res.Metrics.FinalAccuracy || len(got.Checkpoints) != len(res.Checkpoints) {
		t.Errorf("round-trip mismatch: %+v", got.Metrics)
	}
	// Every token total must serialize under a snake_case key so downstream cost
	// readers find them (a dropped json tag round-trips fine but breaks consumers).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total_input_tokens", "total_output_tokens", "total_cache_read_tokens", "total_cache_creation_tokens"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("results JSON missing snake_case key %q", key)
		}
	}
}

func TestHumanSummaryRendersCurve(t *testing.T) {
	res := sampleResults()
	res.Aggregate()
	out := res.HumanSummary()
	for _, want := range []string{"learning curve", "(empty baseline)", "cs-units", "per-trap-class", "units_cents", "captured but not promoted"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n%s", want, out)
		}
	}
	// No evaluator wrote a memory, so no validity warning appears.
	if strings.Contains(out, "memory write") {
		t.Errorf("summary warns about memory writes on a clean run\n%s", out)
	}
}

// TestEvalMemoryWritesWarn proves an evaluator memory write is totaled in
// Metrics and surfaces as a validity warning in the human summary: the
// no-self-teach rule is prompt-level, so the report must carry the audit-side
// signal when it is violated.
func TestEvalMemoryWritesWarn(t *testing.T) {
	res := sampleResults()
	res.Checkpoints[1].Attempts[0].MemoryWrites = 2
	res.Aggregate()
	if res.Metrics.EvalMemoryWrites != 2 {
		t.Errorf("EvalMemoryWrites = %d, want 2", res.Metrics.EvalMemoryWrites)
	}
	if !strings.Contains(res.HumanSummary(), "WARNING: evaluators performed 2 memory write(s)") {
		t.Errorf("summary missing the memory-write validity warning\n%s", res.HumanSummary())
	}
}

func TestAggregateEmptyIsSafe(t *testing.T) {
	res := &Results{}
	res.Aggregate() // must not panic on no lessons/checkpoints
	if res.Metrics.Checkpoints != 0 || res.Metrics.FinalAccuracy != 0 {
		t.Errorf("empty results should aggregate to zero, got %+v", res.Metrics)
	}
}
