package coldstart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/capture"
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

// TestAuditReadFailuresWarn proves a lost audit read-back (on a lesson episode
// or an eval attempt) is totaled in Metrics and surfaces as a coverage-integrity
// warning in the human summary: the attempt contributes zero to enrichment
// coverage through signal loss, which must never read as "nothing was enriched".
func TestAuditReadFailuresWarn(t *testing.T) {
	res := sampleResults()
	res.Lessons[0].Episode.AuditReadError = "audit rows below minimum"
	res.Checkpoints[1].Attempts[0].AuditReadError = "audit rows below minimum"
	res.Aggregate()
	if res.Metrics.AuditReadFailures != 2 {
		t.Errorf("AuditReadFailures = %d, want 2", res.Metrics.AuditReadFailures)
	}
	if !strings.Contains(res.HumanSummary(), "WARNING: 2 episode(s) lost their audit read-back") {
		t.Errorf("summary missing the audit-read-back coverage warning\n%s", res.HumanSummary())
	}
}

// TestCaptureRateIsHeadlineAndAttributed is the issue #1136 acceptance
// criterion for the cold-start suite: capture is reported as a rate with a
// confidence interval and an attempted/landed split, and every miss is
// attributed to exactly one cause — on the run's summary and on the lesson that
// missed. A capture miss keeps its lesson off the curve for good, so a bare
// "captured 5 of 6" cannot say which layer to fix.
func TestCaptureRateIsHeadlineAndAttributed(t *testing.T) {
	res := sampleResults()
	res.Lessons = append(res.Lessons,
		// never reached capture, budget spent on discovery
		LessonRecord{LessonID: "cs-starved", Captured: new(false), CaptureAttempted: new(false), BudgetExhausted: new(true)},
		// capture ran; no linked insight landed
		LessonRecord{LessonID: "cs-misfiled", Captured: new(false), CaptureAttempted: new(true), BudgetExhausted: new(false)},
		// never called capture with budget to spare
		LessonRecord{LessonID: "cs-untried", Captured: new(false), CaptureAttempted: new(false), BudgetExhausted: new(false)},
		// claude-cli path: budget exhaustion is not observable
		LessonRecord{LessonID: "cs-cli", Captured: new(false), CaptureAttempted: new(false)},
	)
	// The first two lessons predate the attempt signal in this fixture; they
	// captured, so they carry no miss and only widen the rate's denominator.
	res.Aggregate()
	m := res.Metrics

	if m.CaptureRate.Num != 2 || m.CaptureRate.Den != 6 {
		t.Fatalf("capture rate = %d/%d, want 2/6", m.CaptureRate.Num, m.CaptureRate.Den)
	}
	if m.CaptureRate.CILow == m.CaptureRate.CIHigh {
		t.Errorf("capture rate CI = [%v, %v], want a non-degenerate interval on a mixed run",
			m.CaptureRate.CILow, m.CaptureRate.CIHigh)
	}
	if m.Capture.AttemptRate.Num != 1 || m.Capture.AttemptRate.Den != 4 {
		t.Errorf("capture attempted = %d/%d, want 1/4 (the two lessons with no attempt signal are excluded)",
			m.Capture.AttemptRate.Num, m.Capture.AttemptRate.Den)
	}
	if m.Capture.GivenAttempted.Num != 0 || m.Capture.GivenAttempted.Den != 1 {
		t.Errorf("landed given attempt = %d/%d, want 0/1", m.Capture.GivenAttempted.Num, m.Capture.GivenAttempted.Den)
	}
	want := capture.Misses{Total: 4, AttemptedFailed: 1, BudgetStarved: 1, NeverAttempted: 1, BudgetUnobservable: 1}
	if m.Capture.Misses != want {
		t.Errorf("capture misses = %+v, want %+v", m.Capture.Misses, want)
	}
	// LessonsCaptured stays the plain count it always was, consistent with the rate.
	if m.LessonsCaptured != m.CaptureRate.Num {
		t.Errorf("lessons captured = %d, disagrees with the capture rate numerator %d", m.LessonsCaptured, m.CaptureRate.Num)
	}

	out := res.HumanSummary()
	for _, w := range []string{
		"capture rate", "capture attempted", "landed given attempt", "capture misses (4)",
		"cs-starved: not captured (" + capture.CauseBudgetStarved.String() + ")",
		"cs-misfiled: not captured (" + capture.CauseAttemptedFailed.String() + ")",
		"cs-cli: not captured (" + capture.CauseBudgetUnobservable.String() + ")",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("summary missing %q\n%s", w, out)
		}
	}
}

// TestCaptureMissWithoutAttemptSignal covers a results file written before the
// attempt signal existed: the miss is reported as unattributed rather than
// silently counted as one of the real causes.
func TestCaptureMissWithoutAttemptSignal(t *testing.T) {
	res := &Results{Lessons: []LessonRecord{{LessonID: "cs-legacy", Captured: new(false)}}}
	res.Aggregate()
	if got := res.Metrics.Capture.Misses; got.Total != 1 || got.Unattributed != 1 {
		t.Errorf("misses = %+v, want 1 unattributed", got)
	}
	if res.Metrics.Capture.AttemptRate.Den != 0 {
		t.Errorf("attempt denominator = %d, want 0 — a record with no signal must not count as not-attempted",
			res.Metrics.Capture.AttemptRate.Den)
	}
	if !strings.Contains(res.HumanSummary(), "cs-legacy: not captured ("+capture.CauseUnattributed.String()+")") {
		t.Errorf("summary did not mark the legacy miss unattributed\n%s", res.HumanSummary())
	}
	if !strings.Contains(res.HumanSummary(), "WARNING: 1 capture miss(es) carry no attempt signal") {
		t.Errorf("summary did not warn about the unattributed miss\n%s", res.HumanSummary())
	}
	// A lesson whose capture outcome was never graded carries no cause at all,
	// and must not be rendered with an empty parenthetical.
	ungraded := &Results{Lessons: []LessonRecord{{LessonID: "cs-ungraded"}}}
	ungraded.Aggregate()
	if !strings.Contains(ungraded.HumanSummary(), "cs-ungraded: not captured\n") {
		t.Errorf("ungraded lesson should render bare\n%s", ungraded.HumanSummary())
	}
}

func TestAggregateEmptyIsSafe(t *testing.T) {
	res := &Results{}
	res.Aggregate() // must not panic on no lessons/checkpoints
	if res.Metrics.Checkpoints != 0 || res.Metrics.FinalAccuracy != 0 {
		t.Errorf("empty results should aggregate to zero, got %+v", res.Metrics)
	}
}
