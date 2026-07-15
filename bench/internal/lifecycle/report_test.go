package lifecycle

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPassed(t *testing.T) {
	full := ProtocolRun{
		Captured: new(true), RecallCorrect: new(true), Promoted: new(true),
		TransferCorrect: new(true), UpdateCorrect: new(true), Duplicated: new(false), AbstainCorrect: new(true),
	}
	if !full.Passed() {
		t.Error("complete success should pass")
	}
	if (ProtocolRun{Error: "boom", Captured: new(true)}).Passed() {
		t.Error("harness failure never passes")
	}
	if (ProtocolRun{Captured: new(false)}).Passed() {
		t.Error("no capture fails")
	}
	dup := full
	dup.Duplicated = new(true)
	if dup.Passed() {
		t.Error("a duplicate must fail the run")
	}
	// A protocol without optional stages passes on the required three.
	minimal := ProtocolRun{Captured: new(true), RecallCorrect: new(true), Promoted: new(true)}
	if !minimal.Passed() {
		t.Error("minimal lifecycle should pass")
	}
}

func TestAggregate(t *testing.T) {
	res := &Results{
		Manifest: Manifest{K: 2},
		Runs: []ProtocolRun{
			// lc-a: both attempts fully pass.
			fullRun("lc-a", 1), fullRun("lc-a", 2),
			// lc-b: attempt 1 passes, attempt 2 misses recall -> protocol not pass^k.
			fullRun("lc-b", 1), missRecall("lc-b", 2),
			// lc-c: one attempt is a harness failure.
			{ProtocolID: "lc-c", Attempt: 1, Error: "connect failed"},
			fullRun("lc-c", 2),
		},
	}
	res.Aggregate()
	m := res.Metrics
	if m.Protocols != 3 {
		t.Fatalf("protocols = %d, want 3", m.Protocols)
	}
	if m.HarnessFailures != 1 {
		t.Fatalf("harness failures = %d, want 1", m.HarnessFailures)
	}
	if m.Attempts != 5 {
		t.Fatalf("attempts = %d, want 5", m.Attempts)
	}
	// capture: all 5 graded runs captured.
	if m.CaptureRate.Num != 5 || m.CaptureRate.Den != 5 {
		t.Fatalf("capture = %+v", m.CaptureRate)
	}
	// recall: 4 of 5 correct (lc-b attempt 2 missed).
	if m.PersonalRecall.Num != 4 || m.PersonalRecall.Den != 5 {
		t.Fatalf("recall = %+v", m.PersonalRecall)
	}
	// pass^k: only lc-a passes both attempts; lc-b fails attempt 2; lc-c has
	// only one graded run (!= k) so cannot claim pass^k.
	if m.PassK.Num != 1 || m.PassK.Den != 3 {
		t.Fatalf("passK = %+v", m.PassK)
	}
}

func fullRun(id string, attempt int) ProtocolRun {
	return ProtocolRun{
		ProtocolID: id, Attempt: attempt,
		Captured: new(true), RecallCorrect: new(true), RecallSurfaced: new(true), Promoted: new(true),
		TransferCorrect: new(true), UpdateCorrect: new(true), Duplicated: new(false), AbstainCorrect: new(true),
	}
}

func missRecall(id string, attempt int) ProtocolRun {
	r := fullRun(id, attempt)
	r.RecallCorrect = new(false)
	return r
}

func TestAggregateSumsTokens(t *testing.T) {
	res := &Results{
		Manifest: Manifest{K: 1},
		Runs: []ProtocolRun{
			{ProtocolID: "lc-a", Captured: new(true), RecallCorrect: new(true), Episodes: []EpisodeRecord{
				{Stage: StageTeach, InputTokens: 1000, OutputTokens: 50, CacheReadTokens: 800, CacheCreationTokens: 200},
				{Stage: StageRecall, InputTokens: 2000, OutputTokens: 80, CacheReadTokens: 1500, CacheCreationTokens: 100},
			}},
			// A harness-failed run still spent tokens and must count toward the total.
			{ProtocolID: "lc-b", Error: "boom", Episodes: []EpisodeRecord{
				{Stage: StageTeach, InputTokens: 500, OutputTokens: 20, CacheReadTokens: 300, CacheCreationTokens: 50},
			}},
		},
	}
	res.Aggregate()
	if res.Metrics.TotalInputTokens != 3500 || res.Metrics.TotalOutputTokens != 150 {
		t.Fatalf("token totals = in %d out %d, want 3500/150", res.Metrics.TotalInputTokens, res.Metrics.TotalOutputTokens)
	}
	if res.Metrics.TotalCacheReadTokens != 2600 || res.Metrics.TotalCacheCreationTokens != 350 {
		t.Fatalf("cache token totals = read %d write %d, want 2600/350",
			res.Metrics.TotalCacheReadTokens, res.Metrics.TotalCacheCreationTokens)
	}
	if !strings.Contains(res.HumanSummary(), "input 3500") {
		t.Errorf("summary missing token line:\n%s", res.HumanSummary())
	}
	if !strings.Contains(res.HumanSummary(), "cache read 2600") {
		t.Errorf("summary missing cache token line:\n%s", res.HumanSummary())
	}
}

func TestWriteAndLoadJSON(t *testing.T) {
	res := &Results{
		Manifest: Manifest{Arm: "a3", K: 1, StartedAt: time.Unix(1, 0).UTC(), FinishedAt: time.Unix(2, 0).UTC()},
		Runs:     []ProtocolRun{fullRun("lc-a", 1)},
	}
	res.Aggregate()
	path := filepath.Join(t.TempDir(), "res.json")
	if err := res.WriteJSON(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadJSON(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Manifest.Arm != "a3" || len(got.Runs) != 1 || got.Metrics.CaptureRate.Rate != 1 {
		t.Fatalf("roundtrip mismatch: %+v", got.Metrics)
	}
}

func TestAggregateFillsConfidenceIntervals(t *testing.T) {
	// A mix of correct and incorrect recalls across many attempts produces a
	// non-degenerate CI that brackets the point rate.
	res := &Results{Manifest: Manifest{K: 1}}
	for i := range 40 {
		r := ProtocolRun{ProtocolID: fmt.Sprintf("lc-%02d", i), Captured: new(true)}
		r.RecallCorrect = new(i%4 != 0) // 30/40 correct -> recall 0.75
		res.Runs = append(res.Runs, r)
	}
	res.Aggregate()
	pr := res.Metrics.PersonalRecall
	if pr.Rate != 0.75 {
		t.Fatalf("recall rate = %v, want 0.75", pr.Rate)
	}
	if !(pr.CILow < pr.Rate && pr.Rate < pr.CIHigh) {
		t.Fatalf("recall CI [%v, %v] does not bracket %v", pr.CILow, pr.CIHigh, pr.Rate)
	}
	if pr.CILow < 0 || pr.CIHigh > 1 {
		t.Fatalf("recall CI [%v, %v] escapes [0, 1]", pr.CILow, pr.CIHigh)
	}
	// An unexercised metric (empty denominator) carries no interval.
	if tu := res.Metrics.TransferUsedGivenSurfaced; tu.Den != 0 || tu.CILow != 0 || tu.CIHigh != 0 {
		t.Fatalf("unexercised metric should have zero-width CI, got %+v", tu)
	}
}

func TestAggregateConfidenceIntervalsReproducible(t *testing.T) {
	build := func() Metrics {
		res := &Results{Manifest: Manifest{K: 1}}
		for i := range 20 {
			res.Runs = append(res.Runs, ProtocolRun{
				ProtocolID: fmt.Sprintf("lc-%02d", i), Captured: new(true), RecallCorrect: new(i%3 != 0),
			})
		}
		res.Aggregate()
		return res.Metrics
	}
	a, b := build(), build()
	if a.PersonalRecall.CILow != b.PersonalRecall.CILow || a.PersonalRecall.CIHigh != b.PersonalRecall.CIHigh {
		t.Fatalf("CI not reproducible: %+v vs %+v", a.PersonalRecall, b.PersonalRecall)
	}
}

func TestHumanSummaryShowsConfidenceInterval(t *testing.T) {
	res := &Results{Manifest: Manifest{Arm: "a3", Model: "scripted", K: 1}}
	for i := range 10 {
		res.Runs = append(res.Runs, ProtocolRun{
			ProtocolID: fmt.Sprintf("lc-%02d", i), Captured: new(true), RecallCorrect: new(i%2 == 0),
		})
	}
	res.Aggregate()
	if out := res.HumanSummary(); !strings.Contains(out, "95% CI") {
		t.Errorf("summary missing CI bracket:\n%s", out)
	}
}

// TestHumanSummaryOmitsDegenerateInterval guards both ends of the writeMetric
// guard: an all-success (100%) and an all-failure (0%) rate each collapse to a
// zero-width bootstrap interval, which must be omitted rather than printed as a
// meaningless [100.0-100.0] or [0.0-0.0] (issue #965 review finding).
func TestHumanSummaryOmitsDegenerateInterval(t *testing.T) {
	res := &Results{Manifest: Manifest{Arm: "a3", Model: "scripted", K: 1}}
	for i := range 8 {
		// Capture always true (100%), abstain always false (0%): both degenerate.
		res.Runs = append(res.Runs, ProtocolRun{
			ProtocolID: fmt.Sprintf("lc-%02d", i), Captured: new(true), RecallCorrect: new(true), AbstainCorrect: new(false),
		})
	}
	res.Aggregate()
	out := res.HumanSummary()
	// Every exercised metric here is degenerate (100% or 0%), so no bracket at
	// all — and never a meaningless [100.0-100.0] or [0.0-0.0].
	if strings.Contains(out, "100.0-100.0") || strings.Contains(out, "0.0-0.0") || strings.Contains(out, "95% CI") {
		t.Errorf("degenerate CI bracket should be omitted:\n%s", out)
	}
	if !strings.Contains(out, "capture rate") || !strings.Contains(out, "100.0%") {
		t.Errorf("capture line missing:\n%s", out)
	}
}

func TestHumanSummaryIncludesMetricsAndFailures(t *testing.T) {
	res := &Results{
		Manifest: Manifest{Arm: "a3", Model: "scripted", K: 1},
		Runs: []ProtocolRun{
			fullRun("lc-a", 1),
			{ProtocolID: "lc-b", Attempt: 1, Error: "connect failed"},
		},
	}
	res.Aggregate()
	out := res.HumanSummary()
	for _, want := range []string{"capture rate", "pass^k", "harness failures", "lc-b", "connect failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}
